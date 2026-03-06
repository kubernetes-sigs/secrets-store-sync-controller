/*
Copyright 2024 The Kubernetes Authors.

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

package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"google.golang.org/grpc"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apiserver/pkg/server"
	"k8s.io/apiserver/pkg/server/healthz"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/kubernetes"
	clientgokubescheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	kleaderelection "k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/record"
	basemetricsreg "k8s.io/component-base/metrics/legacyregistry"
	controllerhealthz "k8s.io/controller-manager/pkg/healthz"
	"k8s.io/klog/v2"

	sscsiv1 "sigs.k8s.io/secrets-store-csi-driver/apis/v1"

	ssv1alpha1 "sigs.k8s.io/secrets-store-sync-controller/api/secretsync/v1alpha1"
	ssclients "sigs.k8s.io/secrets-store-sync-controller/client/clientset/versioned"
	ssinformers "sigs.k8s.io/secrets-store-sync-controller/client/informers/externalversions"
	"sigs.k8s.io/secrets-store-sync-controller/pkg/controller"
	"sigs.k8s.io/secrets-store-sync-controller/pkg/leaderelection"
	"sigs.k8s.io/secrets-store-sync-controller/pkg/provider"
)

var (
	kubeconfig              = flag.String("kubeconfig", "", "Location of the master configuration file to run from.")
	tokenRequestAudiences   = flag.String("token-request-audience", "", "Audience for the token request, comma separated.")
	controllerServingAddr   = flag.String("controller-server-address", ":8081", "The address where the controller serves its /healthz, /readyz and /metrics endpoints.")
	enableLeaderElection    = flag.Bool("leader-elect", false, "Enable leader election for controller manager. This will ensure there is only one active controller manager.")
	leaderElectionNamespace = flag.String("leader-election-namespace", "kube-system", "Namespace for leader election. Defaults to \"kube-system\".")
	providerVolumePath      = flag.String("provider-volume", "/provider", "Volume path for provider.")
	maxCallRecvMsgSize      = flag.Int("max-call-recv-msg-size", 1024*1024*4, "maximum size in bytes of gRPC response from plugins")
)

const defaultResyncPeriod = 1 * time.Hour

func runMain() error {
	klog.InitFlags(flag.CommandLine)
	flag.Parse()

	gracefulShutdownCtx, gracefulCancel := context.WithCancel(context.Background())
	shutdownCtx, cancel := context.WithCancel(context.Background())
	shutdownHandler := server.SetupSignalHandler()
	go func() {
		defer func() {
			defer gracefulCancel()
			cancel()
			time.Sleep(10 * time.Second) // graceful period
		}()
		<-shutdownHandler
		klog.Infof("Received SIGTERM or SIGINT signal, shutting down controller.")
	}()

	logger := klog.FromContext(shutdownCtx)

	cfg, err := clientcmd.BuildConfigFromFlags("", *kubeconfig)
	if err != nil {
		logger.Error(err, "Error building kubeconfig")
		klog.FlushAndExit(klog.ExitFlushTimeout, 1)
	}
	cfg.UserAgent = "secret-store-sync-controller"

	protoCfg := rest.CopyConfig(cfg)
	protoCfg.AcceptContentTypes = "application/vnd.kubernetes.protobuf,application/json"
	protoCfg.ContentType = "application/vnd.kubernetes.protobuf"

	// token request client
	kubeClient := kubernetes.NewForConfigOrDie(protoCfg)
	dynamicClient := dynamic.NewForConfigOrDie(cfg)
	secretSyncs := ssclients.NewForConfigOrDie(cfg)

	ssInformers := ssinformers.NewSharedInformerFactory(secretSyncs, defaultResyncPeriod)
	dynamicInformers := dynamicinformer.NewDynamicSharedInformerFactory(dynamicClient, defaultResyncPeriod)

	providerClients := provider.NewPluginClientBuilder(
		[]string{*providerVolumePath},
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(*maxCallRecvMsgSize),
		),
	)
	defer providerClients.Cleanup()

	audiences := strings.Split(*tokenRequestAudiences, ",")
	if len(*tokenRequestAudiences) == 0 {
		audiences = []string{}
	}

	healthzChecker := controllerhealthz.NewMutableHealthzHandler()
	healthzChecker.AddHealthChecker(
		healthz.NewInformerSyncHealthz(ssInformers),
		NewDynamicInformerSyncHealthz(dynamicInformers),
	)

	var electionChecker *kleaderelection.HealthzAdaptor
	if *enableLeaderElection {
		electionChecker = kleaderelection.NewLeaderHealthzAdaptor(time.Second * 20)
		healthzChecker.AddHealthChecker(electionChecker)
	}

	controllerMux := http.NewServeMux()
	controllerMux.Handle("/metrics", basemetricsreg.Handler())
	controllerMux.Handle("/healthz", healthzChecker)

	if err := startControllerServer(shutdownCtx, *controllerServingAddr, controllerMux); err != nil {
		return fmt.Errorf("failed to start a server for the controller: %w", err)
	}

	syncController, err := controller.NewSecretSyncReconciler(
		shutdownCtx,
		kubeClient,
		dynamicInformers,
		secretSyncs.SecretSyncV1alpha1(),
		ssInformers.SecretSync().V1alpha1().SecretSyncs(),
		providerClients,
		audiences,
	)
	if err != nil {
		return err
	}

	runAll := func(ctx context.Context) {
		ssInformers.Start(ctx.Done())
		dynamicInformers.Start(ctx.Done())
		go func() {
			if err := syncController.Run(ctx, 1); err != nil {
				klog.Error(err, "failed to run the controller")
				klog.FlushAndExit(klog.ExitFlushTimeout, 1)
			}
		}()
	}

	projectScheme := runtime.NewScheme()
	for schemeName, schemeAdder := range map[string]func(*runtime.Scheme) error{
		"client-go":                            clientgokubescheme.AddToScheme,
		ssv1alpha1.SchemeGroupVersion.String(): ssv1alpha1.AddToScheme,
		sscsiv1.SchemeGroupVersion.String():    sscsiv1.AddToScheme,
	} {
		if err := schemeAdder(projectScheme); err != nil {
			// the scheme is currently only used in the event recorder, make the error
			// to register an API informational
			klog.Error(err, "failed to register scheme", "schemeName", schemeName)
		}
	}

	eventBroadcaster := record.NewBroadcaster(record.WithContext(gracefulShutdownCtx))
	eventRecorder := eventBroadcaster.NewRecorder(
		projectScheme,
		v1.EventSource{Component: "secret-store-sync-controller"},
	).WithLogger(logger)

	if *enableLeaderElection {
		leaderelection.LeaderElectAndRun(shutdownCtx, cfg, *leaderElectionNamespace, electionChecker, runAll, eventRecorder)
	} else {
		runAll(shutdownCtx)
	}

	<-gracefulShutdownCtx.Done()
	return nil
}

func startControllerServer(ctx context.Context, listenAddress string, handler http.Handler) error {
	var listenConfig net.ListenConfig
	listener, err := listenConfig.Listen(ctx, "tcp", *controllerServingAddr)
	if err != nil {
		logger := klog.FromContext(ctx)
		logger.Error(err, "failed to start a listener", "listenAddress", listenAddress)
		klog.FlushAndExit(klog.ExitFlushTimeout, 1)
	}

	controllerServer := &http.Server{
		Addr:           listener.Addr().String(),
		Handler:        handler,
		MaxHeaderBytes: 1 << 20,

		IdleTimeout:       90 * time.Second, // matches http.DefaultTransport keep-alive timeout
		ReadHeaderTimeout: 32 * time.Second, // just shy of requestTimeoutUpperBound
	}

	_, _, err = server.RunServer(controllerServer, listener, 5*time.Second, ctx.Done())
	return err
}

func main() {
	if err := runMain(); err != nil {
		os.Exit(1)
	}

	os.Exit(0)
}

type cacheSyncWaiter interface {
	WaitForCacheSync(stopCh <-chan struct{}) map[schema.GroupVersionResource]bool
}

type informerSync struct {
	cacheSyncWaiter cacheSyncWaiter
}

// NewDynamicInformerSyncHealthz returns a new HealthChecker that will pass only if all informers in the given cacheSyncWaiter sync.
func NewDynamicInformerSyncHealthz(cacheSyncWaiter cacheSyncWaiter) healthz.HealthChecker {
	return &informerSync{
		cacheSyncWaiter: cacheSyncWaiter,
	}
}

func (*informerSync) Name() string {
	return "dynamic-informer-check"
}

func (i *informerSync) Check(_ *http.Request) error {
	stopCh := make(chan struct{})
	// Close stopCh to force checking if informers are synced now.
	close(stopCh)

	informersByStarted := make(map[bool][]string)
	for informerType, started := range i.cacheSyncWaiter.WaitForCacheSync(stopCh) {
		informersByStarted[started] = append(informersByStarted[started], informerType.String())
	}

	if notStarted := informersByStarted[false]; len(notStarted) > 0 {
		return fmt.Errorf("%d informers not started yet: %v", len(notStarted), notStarted)
	}
	return nil
}
