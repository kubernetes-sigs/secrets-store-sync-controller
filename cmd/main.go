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
	"k8s.io/apiserver/pkg/server"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	basemetricsreg "k8s.io/component-base/metrics/legacyregistry"
	"k8s.io/klog/v2"

	ssclients "sigs.k8s.io/secrets-store-sync-controller/client/clientset/versioned"
	ssinformers "sigs.k8s.io/secrets-store-sync-controller/client/informers/externalversions"
	"sigs.k8s.io/secrets-store-sync-controller/pkg/controller"
	"sigs.k8s.io/secrets-store-sync-controller/pkg/provider"
)

var (
	kubeconfig            = flag.String("kubeconfig", "", "Location of the master configuration file to run from.")
	tokenRequestAudiences = flag.String("token-request-audience", "", "Audience for the token request, comma separated.")
	metricsAddr           = flag.String("metrics-bind-address", ":8085", "The address the metric endpoint binds to.")
	providerVolumePath    = flag.String("provider-volume", "/provider", "Volume path for provider.")
	maxCallRecvMsgSize    = flag.Int("max-call-recv-msg-size", 1024*1024*4, "maximum size in bytes of gRPC response from plugins")
)

const defaultResyncPeriod = 1 * time.Hour

func runMain() error {
	klog.InitFlags(flag.CommandLine)
	flag.Parse()

	shutdownCtx, cancel := context.WithCancel(context.Background())
	shutdownHandler := server.SetupSignalHandler()
	go func() {
		defer cancel()
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

	httpOKHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})
	controllerMux := http.NewServeMux()
	controllerMux.Handle("/metrics", basemetricsreg.Handler())
	controllerMux.Handle("/healthz", httpOKHandler)
	controllerMux.Handle("/readyz", httpOKHandler)

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

	ssInformers.Start(shutdownCtx.Done())
	dynamicInformers.Start(shutdownCtx.Done())
	go syncController.Run(shutdownCtx, 1)

	if err := startControllerServer(shutdownCtx, *metricsAddr, controllerMux); err != nil {
		return fmt.Errorf("failed to start a server for the controller: %w", err)
	}

	<-shutdownCtx.Done() // FIXME: it might be a bit too early to quit here as the controllers will just be exiting - setup a waitgroup for controllers?
	return nil
}

func startControllerServer(ctx context.Context, listenAddress string, handler http.Handler) error {
	var listenConfig net.ListenConfig
	listener, err := listenConfig.Listen(ctx, "tcp", *metricsAddr)
	if err != nil {
		logger := klog.FromContext(ctx)
		logger.Error(err, "failed to start a listener", "listenAddress", listenAddress)
		klog.FlushAndExit(klog.ExitFlushTimeout, 1)
	}
	go http.Serve(listener, handler)
	return nil
}

func main() {
	if err := runMain(); err != nil {
		os.Exit(1)
	}

	os.Exit(0)
}
