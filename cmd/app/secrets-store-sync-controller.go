/*
Copyright The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package app

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apiserver/pkg/server"
	"k8s.io/apiserver/pkg/server/healthz"
	"k8s.io/client-go/kubernetes"
	clientgokubescheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/clientcmd"
	kleaderelection "k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/record"
	k8sapiflag "k8s.io/component-base/cli/flag"
	"k8s.io/component-base/cli/globalflag"
	"k8s.io/component-base/logs"
	logsapi "k8s.io/component-base/logs/api/v1"
	basemetricsreg "k8s.io/component-base/metrics/legacyregistry"
	"k8s.io/component-base/version/verflag"
	"k8s.io/klog/v2"

	sscsiv1 "sigs.k8s.io/secrets-store-csi-driver/apis/v1"
	csiclients "sigs.k8s.io/secrets-store-csi-driver/pkg/client/clientset/versioned"
	csiinformers "sigs.k8s.io/secrets-store-csi-driver/pkg/client/informers/externalversions"

	ssv1alpha1 "sigs.k8s.io/secrets-store-sync-controller/api/secretsync/v1alpha1"
	ssclients "sigs.k8s.io/secrets-store-sync-controller/client/clientset/versioned"
	ssinformers "sigs.k8s.io/secrets-store-sync-controller/client/informers/externalversions"
	"sigs.k8s.io/secrets-store-sync-controller/pkg/controller"
	"sigs.k8s.io/secrets-store-sync-controller/pkg/leaderelection"
	"sigs.k8s.io/secrets-store-sync-controller/pkg/provider"
)

const defaultResyncPeriod = 0 // we don't really need to do resyncs

var scheme = runtime.NewScheme()

func init() {
	utilruntime.Must(clientgokubescheme.AddToScheme(scheme))
	utilruntime.Must(ssv1alpha1.AddToScheme(scheme))
	utilruntime.Must(sscsiv1.AddToScheme(scheme))
}

func NewSecretsStoreSyncControllerCommand() *cobra.Command {
	controllerOpts := NewControllerOptions()
	loggingConfig := logsapi.NewLoggingConfiguration()

	cmd := &cobra.Command{
		Use: "secrets-store-sync-controller",
		Long: `Runs a Kubernetes controller that syncs secrets from external secrets store to a Kubernetes secret.
This can be useful for syncing secrets across multiple namespaces and making sure that the secrets
are available when the secrets store goes offline.`,
		Args: cobra.NoArgs,
		PersistentPreRunE: func(_ *cobra.Command, _ []string) error {
			// apply the extra logging flags
			if err := logsapi.ValidateAndApply(loggingConfig, nil); err != nil {
				return fmt.Errorf("failed to init logs: %w", err)
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			verflag.PrintAndExitIfRequested()

			fs := cmd.Flags()
			k8sapiflag.PrintFlags(fs)

			return runMain(cmd.Context(), controllerOpts)
		},
	}

	fs := cmd.Flags()
	controllerOpts.AddFlags(fs)
	verflag.AddFlags(fs)
	logsapi.AddFlags(loggingConfig, fs)
	globalflag.AddGlobalFlags(fs, cmd.Name(), logs.SkipLoggingConfigurationFlags())

	return cmd
}

func runMain(ctx context.Context, opts *ControllerOptions) error {
	var wg sync.WaitGroup
	defer wg.Wait()

	shutdownCtx, shutdownCancel := context.WithCancel(ctx)
	// make sure shutdownCancel() is always called before wg.Wait() to avoid deadlock
	// as the goroutines in the wg are likely to be waiting for the context to be canceled.
	defer shutdownCancel()
	shutdownHandler := server.SetupSignalHandler()

	logger := klog.FromContext(shutdownCtx)
	wg.Go(func() {
		defer func() {
			shutdownCancel()
		}()
		select {
		case <-shutdownHandler:
			logger.Info("Received SIGTERM or SIGINT signal, shutting down controller.")
		case <-shutdownCtx.Done():
			logger.Info("Context canceled, shutting down controller.")
		}
	})

	cfg, err := clientcmd.BuildConfigFromFlags("", opts.KubeConfigPath)
	if err != nil {
		logger.Error(err, "Error building kubeconfig")
		klog.FlushAndExit(klog.ExitFlushTimeout, 1)
	}
	cfg.UserAgent = "secret-store-sync-controller"

	kubeClient := kubernetes.NewForConfigOrDie(cfg)
	secretSyncs := ssclients.NewForConfigOrDie(cfg)
	secretProviderClasses := csiclients.NewForConfigOrDie(cfg)

	// Supply rotation poll interval to SecretSync informer, the attach the controller's
	// Add handler to it to react to all listed SecretSyncs at the rotation period.
	ssInformers := ssinformers.NewSharedInformerFactory(secretSyncs, opts.RotationPollInterval)
	spcInformers := csiinformers.NewSharedInformerFactory(secretProviderClasses, defaultResyncPeriod)

	providerClients := provider.NewPluginClientBuilder(
		[]string{opts.ProviderVolumePath},
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(opts.GRPCResponseMaxSize),
		),
	)
	defer providerClients.Cleanup()

	if len(opts.TokenRequestAudiences) == 0 {
		opts.TokenRequestAudiences = []string{} // TODO: opts completion?
	}

	electionChecker := kleaderelection.NewLeaderHealthzAdaptor(time.Second * 20)

	healthCheckers := []healthz.HealthChecker{
		healthz.NamedCheck("informers-secretsync", healthz.NewInformerSyncHealthz(ssInformers).Check),
		healthz.NamedCheck("informers-secretproviderclass", healthz.NewInformerSyncHealthz(spcInformers).Check),
		electionChecker,
	}

	controllerMux := http.NewServeMux()
	controllerMux.Handle("/metrics", basemetricsreg.Handler())
	healthz.InstallHandler(controllerMux, healthCheckers...)

	// we need to start the server outside leader election as it needs to independently serve
	// metrics and healthz
	serverStopped, err := startControllerServer(shutdownCtx, opts.ServingAddress, controllerMux)
	if err != nil {
		return fmt.Errorf("failed to start a server for the controller: %w", err)
	}
	wg.Go(func() {
		defer shutdownCancel() // shutdown the controller in case the server exits early
		<-serverStopped
		logger.Info("the controller server has stopped")
	})

	syncController, err := controller.NewSecretSyncReconciler(
		kubeClient,
		secretSyncs.SecretSyncV1alpha1(),
		ssInformers.SecretSync().V1alpha1().SecretSyncs(),
		providerClients,
		spcInformers.Secretsstore().V1().SecretProviderClasses(),
		opts.TokenRequestAudiences,
	)
	if err != nil {
		return err
	}

	runAll := func(ctx context.Context) error {
		var wg errgroup.Group
		ssInformers.Start(ctx.Done())
		defer ssInformers.Shutdown()
		spcInformers.Start(ctx.Done())
		// defer spcInformers.Shutdown() // missing until https://github.com/kubernetes-sigs/secrets-store-csi-driver/pull/2046/ merges
		wg.Go(func() error {
			if err := syncController.Run(ctx, 1); err != nil {
				klog.Error(err, "failed to run the controller")
				return err
			}
			return nil
		})
		return wg.Wait()
	}

	eventBroadcaster := record.NewBroadcaster(record.WithContext(shutdownCtx))
	defer eventBroadcaster.Shutdown()

	eventRecorder := eventBroadcaster.NewRecorder(
		scheme,
		corev1.EventSource{Component: "secret-store-sync-controller"},
	).WithLogger(logger)

	// 1. Controller is wired to the leader election context and waits for it to be canceled,
	//    which can also occur in case we receive terminating signals or the controller server
	//    stops.
	// 2. Informer shutdowns in runAll() should be capable of blocking until all informers are stopped
	//    when the controller.Run() method finishes -> there should be no goroutine leak for
	//    the controller and the informers.
	// 3. Controller server shutdown occurs when the shutdownContext is canceled - we
	//    need to make sure that always happens before we start waiting for the server
	//    to stop -> make sure a defer with shutdownCancel() always follows the defer()
	//    with workgroup.Wait() for a workgroup that contains the goroutine that waits
	//    for the server to stop.
	return leaderelection.LeaderElectAndRun(shutdownCtx, cfg, opts.LeaderElectionNamespace, electionChecker, runAll, eventRecorder)
}

func startControllerServer(ctx context.Context, listenAddress string, handler http.Handler) (stopped <-chan struct{}, err error) {
	var listenConfig net.ListenConfig
	listener, err := listenConfig.Listen(ctx, "tcp", listenAddress)
	if err != nil {
		return nil, fmt.Errorf("failed to listen at %s: %w", listenAddress, err)
	}

	controllerServer := &http.Server{
		Addr:           listener.Addr().String(),
		Handler:        handler,
		MaxHeaderBytes: 1 << 20,

		IdleTimeout:       90 * time.Second, // matches http.DefaultTransport keep-alive timeout
		ReadHeaderTimeout: 32 * time.Second, // just shy of requestTimeoutUpperBound
	}

	_, listenerStoppedCh, err := server.RunServer(controllerServer, listener, 5*time.Second, ctx.Done())
	return listenerStoppedCh, err
}
