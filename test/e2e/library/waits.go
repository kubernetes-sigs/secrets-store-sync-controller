package library

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"

	secretsyncv1alpha1 "sigs.k8s.io/secrets-store-sync-controller/api/secretsync/v1alpha1"
	secretsyncclient "sigs.k8s.io/secrets-store-sync-controller/client/clientset/versioned"
)

const pollingInterval = 2 * time.Second

// WaitForSecretSyncCondition waits until the secretsync object at ssNamespace/ssName contains the condition from `cond`.
//
// The matching of the condition is based on (Type,Reason,Status,MesssagePrefix) from the `cond` argument, where MessagePrefix is the cond.Message.
// Prefix matching of the condition Message makes it possible to match conditons when e.g. namespace that varies is a part of the sought condition.Message.
func WaitForSecretSyncCondition(ctx context.Context, t *testing.T, ssClient secretsyncclient.Interface, ssNamespace, ssName string, cond *metav1.Condition, timeout time.Duration) error {
	const waitContinuesFmt = "continuing to wait for SecretSync condition because: %v"

	var lastWaitReason string
	return wait.PollUntilContextTimeout(ctx, pollingInterval, timeout, true, func(waitCtx context.Context) (bool, error) {
		ss, err := ssClient.SecretSyncV1alpha1().SecretSyncs(ssNamespace).Get(waitCtx, ssName, metav1.GetOptions{})
		if err != nil {
			if lastWaitReason != err.Error() {
				lastWaitReason = err.Error()
				t.Logf(waitContinuesFmt, lastWaitReason)
			}
			return false, nil
		}

		foundCond := meta.FindStatusCondition(ss.Status.Conditions, cond.Type)
		if foundCond == nil {
			reason := fmt.Sprintf("condition of type %q not yet found in %v", cond.Type, ss.Status.Conditions)
			if lastWaitReason != reason {
				lastWaitReason = reason
				t.Logf(waitContinuesFmt, lastWaitReason)
			}
			return false, nil
		}

		if foundCond.Reason != cond.Reason ||
			foundCond.Status != cond.Status ||
			!strings.HasPrefix(foundCond.Message, cond.Message) {

			reason := fmt.Sprintf("condition of type %q was found but expected values don't match %v != %v", cond.Type, cond, foundCond)
			if lastWaitReason != reason {
				lastWaitReason = reason
				t.Logf(waitContinuesFmt, lastWaitReason)
			}
			return false, nil
		}

		return true, nil
	})

}

func WaitForSecret(ctx context.Context, t *testing.T, secretClient corev1client.SecretsGetter, secret *corev1.Secret, timeout time.Duration) error {
	const waitContinuesFmt = "continuing to wait for Secret condition because: %v"

	var lastWaitReason string
	return wait.PollUntilContextTimeout(ctx, pollingInterval, timeout, true, func(waitCtx context.Context) (bool, error) {
		s, err := secretClient.Secrets(secret.Namespace).Get(waitCtx, secret.Name, metav1.GetOptions{})
		if err != nil {
			if lastWaitReason != err.Error() {
				lastWaitReason = err.Error()
				t.Logf(waitContinuesFmt, lastWaitReason)
			}
			return false, nil
		}

		if labelDiff := cmp.Diff(secret.Labels, s.Labels); len(labelDiff) != 0 {
			return false, fmt.Errorf("labels differ: %s", labelDiff)
		}
		if annotationDiff := cmp.Diff(secret.Annotations, s.Annotations); len(annotationDiff) != 0 {
			return false, fmt.Errorf("annotations differ: %s", annotationDiff)
		}
		if ownersDiff := cmp.Diff(secret.OwnerReferences, s.OwnerReferences); len(ownersDiff) != 0 {
			return false, fmt.Errorf("ownerReferences differ: %s", ownersDiff)
		}
		if dataDiff := cmp.Diff(secret.Data, s.Data); len(dataDiff) != 0 {
			return false, fmt.Errorf("secret.data differ: %s", dataDiff)
		}
		return true, nil
	})
}

func WaitForSecretForSecretSync(ctx context.Context, t *testing.T,
	secretClient corev1client.SecretsGetter,
	secretSync *secretsyncv1alpha1.SecretSync,
	expectedSecretData map[string][]byte,
	timeout time.Duration,
) error {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretSync.Name,
			Namespace: secretSync.Namespace,
			Labels:    map[string]string{"secrets-store.sync.x-k8s.io": ""},
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: "secret-sync.x-k8s.io/v1alpha1",
				Kind:       "SecretSync",
				Name:       secretSync.Name,
				UID:        secretSync.UID,
			}},
		},
		Data: expectedSecretData,
	}

	return WaitForSecret(ctx, t, secretClient, secret, timeout)
}
