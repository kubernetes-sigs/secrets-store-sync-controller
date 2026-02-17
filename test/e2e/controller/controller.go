package controller

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"

	sscsiv1 "sigs.k8s.io/secrets-store-csi-driver/apis/v1"

	secretsyncv1alpha1 "sigs.k8s.io/secrets-store-sync-controller/api/secretsync/v1alpha1"

	e2elib "sigs.k8s.io/secrets-store-sync-controller/test/e2e/library"
	"sigs.k8s.io/secrets-store-sync-controller/test/e2e/testdata"
)

const syncWaitTimeout = 1000 * time.Second

func TestSecretSyncsFailures(t *testing.T, f *e2elib.Framework) {
	for _, tc := range []struct {
		name                           string
		secretSyncName                 string
		secretSyncInDifferentNamespace bool
		expectedCondition              *metav1.Condition
	}{
		{
			name:           "invalid secret type in secret definition",
			secretSyncName: "api_credential",
			expectedCondition: &metav1.Condition{
				Type:   "SecretCreated",
				Status: metav1.ConditionFalse,
				Reason: "ControllerPatchError",
				Message: "failed to patch secret \"my-custom-api-secret\": secrets \"my-custom-api-secret\" is forbidden: ValidatingAdmissionPolicy 'secrets-store-sync-controller-create-update-policy'" +
					" with binding 'secrets-store-sync-controller-create-update-policy-binding' denied request: secrets-store-sync-controller has failed to CREATE secret with example.com/api-credentials type",
			},
		},
		{
			name:           "service account token type in secret definition",
			secretSyncName: "service_account_token",
			expectedCondition: &metav1.Condition{
				Type:   "SecretCreated",
				Status: metav1.ConditionFalse,
				Reason: "ControllerPatchError",
				Message: "failed to patch secret \"sse2eserviceaccountsecret\": secrets \"sse2eserviceaccountsecret\" is forbidden: ValidatingAdmissionPolicy 'secrets-store-sync-controller-create-update-policy'" +
					" with binding 'secrets-store-sync-controller-create-update-policy-binding' denied request: secrets-store-sync-controller has failed to CREATE secret with kubernetes.io/service-account-token type",
			},
		},
		{
			name:           "invalid annotation key in secret definition",
			secretSyncName: "invalid_annotation_key",
			expectedCondition: &metav1.Condition{
				Type:   "SecretCreated",
				Status: metav1.ConditionFalse,
				Reason: "ControllerPatchError",
				Message: "failed to patch secret \"sse2einvalidannotationssecret\": Secret \"sse2einvalidannotationssecret\" is invalid: metadata.annotations: Invalid value: \"my.annotation/with_invalid_characters!\":" +
					" name part must consist of alphanumeric characters, '-', '_' or '.', and must start and end with an alphanumeric character (e.g. 'MyName',  or 'my.name',  or '123-abc', regex used for validation is '([A-Za-z0-9][-A-Za-z0-9_.]*)?[A-Za-z0-9]')",
			},
		},
		{
			name:           "invalid label key in secret definition",
			secretSyncName: "invalid_label_key",
			expectedCondition: &metav1.Condition{
				Type:   "SecretCreated",
				Status: metav1.ConditionFalse,
				Reason: "ControllerPatchError",
				Message: "failed to patch secret \"sse2einvalidlabelsecret\": Secret \"sse2einvalidlabelsecret\" is invalid: metadata.labels: Invalid value: \"invalid/key_with_invalid_characters!\":" +
					" name part must consist of alphanumeric characters, '-', '_' or '.', and must start and end with an alphanumeric character (e.g. 'MyName',  or 'my.name',  or '123-abc', regex used for validation is '([A-Za-z0-9][-A-Za-z0-9_.]*)?[A-Za-z0-9]')",
			},
		},
		{
			name:                           "secretproviderclass in a different namespace",
			secretSyncName:                 "valid",
			secretSyncInDifferentNamespace: true,
			expectedCondition: &metav1.Condition{
				Type:    "SecretCreated",
				Status:  metav1.ConditionFalse,
				Reason:  "SecretProviderClassMisconfigured",
				Message: "failed to get SecretProviderClass \"e2e-providerspc\": secretproviderclasses.secrets-store.csi.x-k8s.io \"e2e-providerspc\" not found",
			},
		},
	} {
		f.RunTest(t, tc.name, func(t *testing.T, testCfg *e2elib.TestConfig) {
			testCtx := testCfg.Context

			createE2ESecretProviderClass(testCtx, t, testCfg.Clients.SecretProviderClasses(testCfg.Namespace))

			secretSyncObj := testdata.GetSecretSyncOrDie(tc.secretSyncName)

			ssNamespace := testCfg.Namespace
			if tc.secretSyncInDifferentNamespace {
				ns, err := e2elib.CreateNS(testCtx, testCfg.Clients.KubeClients().CoreV1().Namespaces(), "secretsync-different-ns-")
				if err != nil {
					t.Fatalf("failed to create separate ns for the SecretSync: %v", err)
				}
				t.Cleanup(func() {
					_ = testCfg.Clients.KubeClients().CoreV1().Namespaces().Delete(testCtx, ns.Name, metav1.DeleteOptions{})
				})
				ssNamespace = ns.Name
			}

			if _, err := testCfg.Clients.SSClient().SecretSyncV1alpha1().SecretSyncs(ssNamespace).Create(testCtx, secretSyncObj, metav1.CreateOptions{}); err != nil {
				t.Fatalf("failed to create SecretSync: %v", err)
			}

			if err := e2elib.WaitForSecretSyncCondition(testCtx, t, testCfg.Clients.SSClient(), ssNamespace, secretSyncObj.Name, tc.expectedCondition, syncWaitTimeout); err != nil {
				t.Fatalf("waiting for condition failed: %v", err)
			}
		})
	}
}

func TestSecretSyncSuccess(t *testing.T, f *e2elib.Framework) {
	f.RunTest(t, "valid configuration creates a secret", func(t *testing.T, testCfg *e2elib.TestConfig) {
		testCtx := testCfg.Context

		createE2ESecretProviderClass(testCtx, t, testCfg.Clients.SecretProviderClasses(testCfg.Namespace))

		secretSyncObj := testdata.GetSecretSyncOrDie("valid")
		var err error
		if secretSyncObj, err = testCfg.Clients.SSClient().SecretSyncV1alpha1().SecretSyncs(testCfg.Namespace).Create(testCtx, secretSyncObj, metav1.CreateOptions{}); err != nil {
			t.Fatalf("failed to create SecretSync: %v", err)
		}

		if err := e2elib.WaitForSecretForSecretSync(testCtx, t,
			testCfg.Clients.KubeClients().CoreV1(),
			secretSyncObj,
			map[string][]byte{"bar": []byte("secret")},
			syncWaitTimeout,
		); err != nil {
			t.Fatalf("waiting for secret failed: %v", err)
		}
	})

}

func TestControllerResync(t *testing.T, f *e2elib.Framework) {
	for _, tc := range []struct {
		name              string
		modifySecretSync  func(*secretsyncv1alpha1.SecretSync)
		modifySecret      func(*corev1.Secret)
		expectedCondition *metav1.Condition
		expectedSecret    *corev1.Secret
	}{
		{
			name: "new annotation is invalid",
			modifySecretSync: func(ss *secretsyncv1alpha1.SecretSync) {
				if ss.Spec.SecretObject.Annotations == nil {
					ss.Spec.SecretObject.Annotations = map[string]string{}
				}
				ss.Spec.SecretObject.Annotations["!whoopsie-doodle"] = "oh-noes"
			},
			expectedCondition: &metav1.Condition{
				Type:    "SecretUpdated",
				Status:  metav1.ConditionFalse,
				Reason:  "ControllerPatchError",
				Message: "failed to patch secret \"sse2esecret\": Secret \"sse2esecret\" is invalid: metadata.annotations: Invalid value: \"!whoopsie-doodle\": name part must consist of alphanumeric characters",
			},
		},
		{
			name: "forced secret sync",
			modifySecret: func(s *corev1.Secret) {
				s.Data["bar"] = []byte("this should go")
				s.Data["baz"] = []byte("this should stay")
			},
			modifySecretSync: func(ss *secretsyncv1alpha1.SecretSync) {
				ss.Spec.ForceSynchronization = "UpdateNow"
			},
			expectedSecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "sse2esecret",
					Labels: map[string]string{"secrets-store.sync.x-k8s.io": ""},
					OwnerReferences: []metav1.OwnerReference{{
						APIVersion: "secret-sync.x-k8s.io/v1alpha1",
						Kind:       "SecretSync",
						Name:       "sse2esecret",
						UID:        "<REPLACE_IN_THE_TEST>",
					}},
				},
				Data: map[string][]byte{
					"bar": []byte("secret"),
					"baz": []byte("this should stay"),
				},
			},
			expectedCondition: &metav1.Condition{
				Type:    "SecretUpdated",
				Status:  metav1.ConditionTrue,
				Reason:  "SecretUpToDate",
				Message: "Secret contains last observed values.",
			},
		},
		{
			name: "secret fields get successfully overriden by the controller",
			modifySecret: func(s *corev1.Secret) {
				if s.ObjectMeta.Annotations == nil {
					s.ObjectMeta.Annotations = map[string]string{}
				}
				s.ObjectMeta.Annotations["secrets-store.sync.x-k8s.io"] = "non-empty string"
				s.ObjectMeta.Labels["secrets-store.sync.x-k8s.io"] = "non-empty-string"
			},
			expectedSecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "sse2esecret",
					Labels: map[string]string{"secrets-store.sync.x-k8s.io": ""},
					OwnerReferences: []metav1.OwnerReference{{
						APIVersion: "secret-sync.x-k8s.io/v1alpha1",
						Kind:       "SecretSync",
						Name:       "sse2esecret",
						UID:        "<REPLACE_IN_THE_TEST>",
					}},
				},
				Data: map[string][]byte{
					"bar": []byte("secret"),
					"baz": []byte("this should stay"),
				},
			},
			modifySecretSync: func(ss *secretsyncv1alpha1.SecretSync) {
				ss.Spec.ForceSynchronization = "FailTheSecret"
			},
			expectedCondition: &metav1.Condition{
				Type:    "SecretUpdated",
				Status:  metav1.ConditionTrue,
				Reason:  "SecretUpToDate",
				Message: "Secret contains last observed values.",
			},
		},
		{
			name: "new provider does not exist",
			modifySecretSync: func(ss *secretsyncv1alpha1.SecretSync) {
				ss.Spec.SecretProviderClassName = "not-very-random-but-will-do"
			},
			expectedCondition: &metav1.Condition{
				Type:    "SecretUpdated",
				Status:  metav1.ConditionFalse,
				Reason:  "SecretProviderClassMisconfigured",
				Message: "failed to get SecretProviderClass \"not-very-random-but-will-do\": secretproviderclasses.secrets-store.csi.x-k8s.io \"not-very-random-but-will-do\" not found",
			},
		},
	} {
		f.RunTest(t, tc.name, func(t *testing.T, testCfg *e2elib.TestConfig) {
			testCtx := testCfg.Context

			createE2ESecretProviderClass(testCtx, t, testCfg.Clients.SecretProviderClasses(testCfg.Namespace))

			initSS := testdata.GetSecretSyncOrDie("valid")
			initSS, err := testCfg.Clients.SSClient().SecretSyncV1alpha1().SecretSyncs(testCfg.Namespace).Create(testCtx, initSS, metav1.CreateOptions{})
			if err != nil {
				t.Fatalf("failed to create initial SecretSync: %v", err)
			}

			if err := e2elib.WaitForSecretForSecretSync(testCtx, t,
				testCfg.Clients.KubeClients().CoreV1(),
				initSS,
				map[string][]byte{"bar": []byte("secret")},
				syncWaitTimeout,
			); err != nil {
				t.Fatalf("waiting for secret failed: %v", err)
			}

			if tc.modifySecret != nil {
				s, err := testCfg.Clients.KubeClients().CoreV1().Secrets(testCfg.Namespace).Get(testCtx, initSS.Name, metav1.GetOptions{})
				if err != nil {
					t.Fatalf("failed to retrieve the secret to be modified: %v", err)
				}
				tc.modifySecret(s)
				_, err = testCfg.Clients.KubeClients().CoreV1().Secrets(testCfg.Namespace).Update(testCtx, s, metav1.UpdateOptions{})
				if err != nil {
					t.Fatalf("failed to update secret: %v", err)
				}
			}

			if tc.modifySecretSync != nil {
				ss, err := testCfg.Clients.SSClient().SecretSyncV1alpha1().SecretSyncs(testCfg.Namespace).Get(testCtx, initSS.Name, metav1.GetOptions{})
				if err != nil {
					t.Fatalf("failed to retrieve the secret to be modified: %v", err)
				}
				tc.modifySecretSync(ss)
				_, err = testCfg.Clients.SSClient().SecretSyncV1alpha1().SecretSyncs(testCfg.Namespace).Update(testCtx, ss, metav1.UpdateOptions{})
				if err != nil {
					t.Fatalf("failed to update SecretSync: %v", err)
				}
			}

			if err := e2elib.WaitForSecretSyncCondition(testCtx, t, testCfg.Clients.SSClient(), initSS.Namespace, initSS.Name, tc.expectedCondition, syncWaitTimeout); err != nil {
				t.Fatalf("waiting for condition failed: %v", err)
			}

			if tc.expectedSecret != nil {
				secret := tc.expectedSecret.DeepCopy()
				secret.OwnerReferences[0].UID = initSS.UID
				secret.Namespace = testCfg.Namespace

				e2elib.WaitForSecret(testCtx, t, testCfg.Clients.KubeClients().CoreV1(), secret, syncWaitTimeout)
			}

		})
	}
}

func createE2ESecretProviderClass(ctx context.Context, t *testing.T, dynamicClient dynamic.ResourceInterface) {
	scpGVK := sscsiv1.SchemeGroupVersion.WithKind("SecretProviderClass")

	provider := testdata.GetE2ESecretProviderClassOrDie()
	if _, err := dynamicClient.Create(ctx, e2elib.ToUnstructuredOrDie(provider, scpGVK), metav1.CreateOptions{}); err != nil {
		t.Fatalf("failed to create SecretProviderClass: %v", err)
	}
}
