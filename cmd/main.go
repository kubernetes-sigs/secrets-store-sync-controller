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
	"flag"
	"os"
	"strings"

	"google.golang.org/grpc"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/metrics/server"
	secretsstorecsiv1 "sigs.k8s.io/secrets-store-csi-driver/apis/v1"

	secretsyncv1alpha1 "sigs.k8s.io/secrets-store-sync-controller/api/v1alpha1"
	"sigs.k8s.io/secrets-store-sync-controller/internal/controller"
	"sigs.k8s.io/secrets-store-sync-controller/pkg/k8s"
	"sigs.k8s.io/secrets-store-sync-controller/pkg/metrics"
	"sigs.k8s.io/secrets-store-sync-controller/pkg/provider"
	"sigs.k8s.io/secrets-store-sync-controller/pkg/version"
	//+kubebuilder:scaffold:imports
)

var (
	scheme                = runtime.NewScheme()
	setupLog              = ctrl.Log.WithName("setup")
	metricsAddr           = flag.String("metrics-bind-address", ":8085", "The address the metric endpoint binds to.")
	enableLeaderElection  = flag.Bool("leader-elect", false, "Enable leader election for controller manager. "+"Enabling this will ensure there is only one active controller manager.")
	probeAddr             = flag.String("health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	tokenRequestAudiences = flag.String("token-request-audience", "", "Audience for the token request, comma separated.")
	providerVolumePath    = flag.String("provider-volume", "/provider", "Volume path for provider.")
	maxCallRecvMsgSize    = flag.Int("max-call-recv-msg-size", 1024*1024*4, "maximum size in bytes of gRPC response from plugins")
	versionInfo           = flag.Bool("version", false, "Print the version and exit")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	utilruntime.Must(secretsyncv1alpha1.AddToScheme(scheme))

	utilruntime.Must(secretsstorecsiv1.AddToScheme(scheme))
	//+kubebuilder:scaffold:scheme
}

func runMain() error {
	opts := zap.Options{
		Development: true,
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	if *versionInfo {
		versionErr := version.PrintVersion()
		if versionErr != nil {
			setupLog.Error(versionErr, "failed to print version")
			return versionErr
		}
		return nil
	}
	err := metrics.InitMetricsExporter()
	if err != nil {
		setupLog.Error(err, "failed to initialize metrics exporter")
		os.Exit(1)
	}

	controllerConfig := ctrl.GetConfigOrDie()
	controllerConfig.UserAgent = version.GetUserAgent("secrets-store-sync-controller")
	mgr, err := ctrl.NewManager(controllerConfig, ctrl.Options{
		Scheme: scheme,
		Metrics: server.Options{
			BindAddress: *metricsAddr,
		},
		HealthProbeBindAddress: *probeAddr,
		LeaderElection:         *enableLeaderElection,
		LeaderElectionID:       "29f1d54e.secret-sync.x-k8s.io",
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		return err
	}

	// token request client
	kubeClient := kubernetes.NewForConfigOrDie(ctrl.GetConfigOrDie())
	tokenClient := k8s.NewTokenClient(kubeClient)

	providerClients := provider.NewPluginClientBuilder(
		[]string{*providerVolumePath},
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(*maxCallRecvMsgSize),
		),
	)
	sr, err := controller.NewStatsReporter()
	if err != nil {
		setupLog.Error(err, "failed to initialize stats reporter")
		os.Exit(1)
	}

	defer providerClients.Cleanup()

	audiences := strings.Split(*tokenRequestAudiences, ",")
	if len(*tokenRequestAudiences) == 0 {
		audiences = []string{}
	}

	if err = (&controller.SecretSyncReconciler{
		Clientset:       kubeClient,
		Client:          mgr.GetClient(),
		Scheme:          mgr.GetScheme(),
		TokenClient:     tokenClient,
		ProviderClients: providerClients,
		Audiences:       audiences,
		EventRecorder:   record.NewBroadcaster().NewRecorder(scheme, corev1.EventSource{Component: "secret-sync-controller"}),
		MetricReporter:  sr,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "SecretSync")
		return err
	}
	//+kubebuilder:scaffold:builder

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		return err
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		return err
	}

	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		return err
	}

	return nil
}

func main() {
	if err := runMain(); err != nil {
		os.Exit(1)
	}

	os.Exit(0)
}
