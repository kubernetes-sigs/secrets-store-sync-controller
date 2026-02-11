package leaderelection

import (
	"context"
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
	runner func(context.Context),
	eventRecorder record.EventRecorder,
) {
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
		klog.FlushAndExit(klog.ExitFlushTimeout, 1)
	}

	leaderelection.RunOrDie(ctx, leaderelection.LeaderElectionConfig{
		Lock:          rl,
		LeaseDuration: recommendedLeaseDuration,
		RenewDeadline: recommendedRenewDeadline,
		RetryPeriod:   recommendedRetryPeriod,
		Callbacks: leaderelection.LeaderCallbacks{
			OnStartedLeading: runner,
			OnStoppedLeading: func() {
				logger.Info("leader election lost")
				klog.FlushAndExit(klog.ExitFlushTimeout, 1)
			},
		},
		WatchDog: electionChecker,
		Name:     leaderElectionResourceName,
	})
}
