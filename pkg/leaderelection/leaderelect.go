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

package leaderelection

import (
	"context"
	"errors"
	"os"
	"time"

	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog/v2"
)

func LeaderElectAndRun(
	ctx context.Context,
	clientConfig *rest.Config,
	leaderElectionNamespace string,
	electionChecker *leaderelection.HealthzAdaptor,
	runner func(context.Context) error,
	eventRecorder record.EventRecorder,
) error {
	logger := klog.FromContext(ctx)
	const (
		leaderElectionResourceName = "secrets-store-sync-controller-lease"
		// recommended values from k8s.io/component-base/config/v1alpha1
		recommendedLeaseDuration = 15 * time.Second
		recommendedRenewDeadline = 10 * time.Second
		recommendedRetryPeriod   = 2 * time.Second
	)

	lockID, err := os.Hostname()
	if err != nil {
		klog.FlushAndExit(klog.ExitFlushTimeout, 1)
	}
	// add a uniquifier so that two processes on the same host don't accidentally both become active
	lockID = lockID + "_" + string(uuid.NewUUID())

	rl, err := resourcelock.NewFromKubeconfig(resourcelock.LeasesResourceLock,
		leaderElectionNamespace,
		leaderElectionResourceName,
		resourcelock.ResourceLockConfig{
			Identity:      lockID,
			EventRecorder: eventRecorder,
		},
		clientConfig,
		recommendedRenewDeadline)
	if err != nil {
		logger.Error(err, "Error creating lock")
		return err
	}

	leContext, leaderCancel := context.WithCancel(ctx)
	// FIXME: currently, it looks like leaderelection would not stop if the controller
	// failed to .Run(). See https://github.com/kubernetes/kubernetes/blob/23fa182f91653a5fdbf7b8da8fc2290907c5b466/cmd/kube-controller-manager/app/controllermanager.go#L382-L383
	// for details how to run this properly (will likely need to cancel the shutdown context for leader election?)
	var runErr error
	leaderelection.RunOrDie(leContext, leaderelection.LeaderElectionConfig{
		Lock:          rl,
		LeaseDuration: recommendedLeaseDuration,
		RenewDeadline: recommendedRenewDeadline,
		RetryPeriod:   recommendedRetryPeriod,
		Callbacks: leaderelection.LeaderCallbacks{
			OnStartedLeading: func(ctx context.Context) {
				defer leaderCancel()
				if err := runner(ctx); err != nil && !errors.Is(err, context.Canceled) {
					runErr = err
				}
			},
			OnStoppedLeading: func() {
				logger.Info("leader election lost")
			},
		},
		WatchDog: electionChecker,
		Name:     leaderElectionResourceName,
	})

	return runErr
}
