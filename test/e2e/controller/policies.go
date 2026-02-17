package controller

import (
	"context"
	"strings"
	"testing"
	"time"

	authnv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/utils/ptr"

	secretsyncv1alpha1 "sigs.k8s.io/secrets-store-sync-controller/api/secretsync/v1alpha1"
	secretsyncv1alphaclient "sigs.k8s.io/secrets-store-sync-controller/client/clientset/versioned/typed/secretsync/v1alpha1"

	e2elib "sigs.k8s.io/secrets-store-sync-controller/test/e2e/library"
	"sigs.k8s.io/secrets-store-sync-controller/test/e2e/testdata"
)

func TestCreateUpdateSecretsTypes(t *testing.T, f *e2elib.Framework) {
	controllerKubeClients := kubernetes.NewForConfigOrDie(getControllerClientConfig(t, f))

	for _, tc := range []struct {
		name        string
		secret      *corev1.Secret
		expectedErr *string
	}{
		{
			name:   "owner references - none exist",
			secret: newSecretBuilder("no-owner-refs").Done(),
			expectedErr: ptr.To(
				"secrets \"no-owner-refs\" is forbidden: ValidatingAdmissionPolicy 'secrets-store-sync-controller-create-update-policy' with binding " +
					"'secrets-store-sync-controller-create-update-policy-binding' denied request: expression 'variables.allowedSecretTypes == true && " +
					"variables.hasOneSecretSyncOwner == true' resulted in error: composited variable \"hasOneSecretSyncOwner\" fails to evaluate: no such key: ownerReferences",
			),
		},
		{
			name: "owner references - wrong kind",
			secret: newSecretBuilder("ownererefs-wrong-kind").
				withOwnerRefs([]metav1.OwnerReference{{
					APIVersion: "rbac.authorization.k8s.io/v1",
					Kind:       "User",
					Name:       "ownererefs-wrong-kind",
					UID:        "someuid",
				}}).
				Done(),
			expectedErr: ptr.To(
				"secrets \"ownererefs-wrong-kind\" is forbidden: ValidatingAdmissionPolicy 'secrets-store-sync-controller-create-update-policy' with binding " +
					"'secrets-store-sync-controller-create-update-policy-binding' denied request: secrets-store-sync-controller has failed to CREATE secret " +
					"with Opaque type in the {NAMESPACE_NAME_HERE} namespace. The controller can only create or update secrets in the allowed types list with a single secretsync owner.",
			),
		},
		{
			name: "owner references - two refs",
			secret: newSecretBuilder("two-owner-refs").
				withOwnerRefs([]metav1.OwnerReference{{
					APIVersion: "secret-sync.x-k8s.io/v1",
					Kind:       "SecretSync",
					Name:       "two-owner-refs",
					UID:        "someuid",
				}, {
					APIVersion: "secret-sync.x-k8s.io/v1alpha",
					Kind:       "SecretSync",
					Name:       "two-owner-refs",
					UID:        "someuid",
				}}).
				Done(),
			expectedErr: ptr.To(
				"secrets \"two-owner-refs\" is forbidden: ValidatingAdmissionPolicy 'secrets-store-sync-controller-create-update-policy' with binding " +
					"'secrets-store-sync-controller-create-update-policy-binding' denied request: secrets-store-sync-controller has failed to CREATE secret " +
					"with Opaque type in the {NAMESPACE_NAME_HERE} namespace. The controller can only create or update secrets in the allowed types list with a single secretsync owner.",
			),
		},
		{
			name: "create a ServiceAccount secret",
			secret: newSecretBuilder("sa-type-secret").
				withOwnerRefs([]metav1.OwnerReference{{
					APIVersion: "secret-sync.x-k8s.io/v1",
					Kind:       "SecretSync",
					Name:       "sa-type-secret",
					UID:        "someuid",
				}}).
				withType(corev1.SecretTypeServiceAccountToken).
				withAnnotations(map[string]string{corev1.ServiceAccountNameKey: "default"}).
				Done(),
			expectedErr: ptr.To("secrets \"sa-type-secret\" is forbidden: ValidatingAdmissionPolicy 'secrets-store-sync-controller-create-update-policy' " +
				"with binding 'secrets-store-sync-controller-create-update-policy-binding' denied request: secrets-store-sync-controller has failed to " +
				"CREATE secret with kubernetes.io/service-account-token type in the {NAMESPACE_NAME_HERE} namespace. The controller is " +
				"not allowed to create or update secrets with this type."),
		},
		{
			name: "create a secret with a type that is not explicitly allowed",
			secret: newSecretBuilder("type-not-allowed").
				withOwnerRefs([]metav1.OwnerReference{{
					APIVersion: "secret-sync.x-k8s.io/v1",
					Kind:       "SecretSync",
					Name:       "type-not-allowed",
					UID:        "someuid",
				}}).
				withType("very.random/custom-type").
				Done(),
			expectedErr: ptr.To("secrets \"type-not-allowed\" is forbidden: ValidatingAdmissionPolicy 'secrets-store-sync-controller-create-update-policy' with " +
				"binding 'secrets-store-sync-controller-create-update-policy-binding' denied request: secrets-store-sync-controller has failed to CREATE secret" +
				" with very.random/custom-type type in the {NAMESPACE_NAME_HERE} namespace. The controller can only create or update secrets in the allowed types" +
				" list with a single secretsync owner."),
		},
	} {
		f.RunTest(t, tc.name, func(t *testing.T, testCfg *e2elib.TestConfig) {
			_, gotErr := controllerKubeClients.CoreV1().Secrets(testCfg.Namespace).Create(testCfg.Context, tc.secret, metav1.CreateOptions{})
			if (tc.expectedErr != nil) != (gotErr != nil) {
				t.Fatalf("expectedErr: %v; got: %v", tc.expectedErr, gotErr)
			}

			if tc.expectedErr != nil {
				expectedError := strings.Replace(*tc.expectedErr, "{NAMESPACE_NAME_HERE}", testCfg.Namespace, 1)
				if gotErr.Error() != expectedError {
					t.Fatalf("expectedErr: %v; got: %v", expectedError, gotErr)
				}
			}
		})
	}
}

func TestDeleteSecrets(t *testing.T, f *e2elib.Framework) {
	controllerKubeClients := kubernetes.NewForConfigOrDie(getControllerClientConfig(t, f))

	for _, tc := range []struct {
		name        string
		action      func(cfg *e2elib.TestConfig, client kubernetes.Interface) error
		expectedErr *string
	}{
		{
			name: "delete a secret",
			action: func(cfg *e2elib.TestConfig, client kubernetes.Interface) error {
				return client.CoreV1().Secrets(cfg.Namespace).
					Delete(cfg.Context, "some-name", metav1.DeleteOptions{})
			},
			expectedErr: ptr.To("secrets \"some-name\" is forbidden: User \"system:serviceaccount:secrets-store-sync-controller-system:secrets-store-sync-controller\"" +
				" cannot delete resource \"secrets\" in API group \"\" in the namespace \"{NAMESPACE_NAME_HERE}\""),
		},
		{
			name: "delete a secret with explicit RBAC privileges",
			action: func(cfg *e2elib.TestConfig, client kubernetes.Interface) error {
				adminRBACClient := cfg.Clients.KubeClients().RbacV1()
				_, err := adminRBACClient.RoleBindings(cfg.Namespace).Create(cfg.Context,
					&rbacv1.RoleBinding{
						ObjectMeta: metav1.ObjectMeta{
							GenerateName: "sync-controller-edit",
						},
						RoleRef: rbacv1.RoleRef{
							APIGroup: rbacv1.GroupName,
							Kind:     "ClusterRole",
							Name:     "edit",
						},
						Subjects: []rbacv1.Subject{{
							Kind:      "ServiceAccount",
							Name:      "secrets-store-sync-controller",
							Namespace: "secrets-store-sync-controller-system",
						}},
					},
					metav1.CreateOptions{},
				)
				if err != nil {
					t.Fatalf("failed to grant delete privileges to the sync controller: %v", err)
				}

				_, err = cfg.Clients.KubeClients().CoreV1().Secrets(cfg.Namespace).Create(cfg.Context,
					newSecretBuilder("some-name").Done(),
					metav1.CreateOptions{},
				)
				if err != nil {
					t.Fatalf("failed to create test secret: %v", err)
				}

				return client.CoreV1().Secrets(cfg.Namespace).
					Delete(cfg.Context, "some-name", metav1.DeleteOptions{})
			},
			expectedErr: ptr.To("secrets \"some-name\" is forbidden: ValidatingAdmissionPolicy 'secrets-store-sync-controller-delete-policy' with binding" +
				" 'secrets-store-sync-controller-delete-policy-binding' denied request: secrets-store-sync-controller has failed to DELETE secrets in " +
				"the {NAMESPACE_NAME_HERE} namespace. The controller is not allowed to delete secrets.",
			),
		},
	} {
		f.RunTest(t, tc.name, func(t *testing.T, testCfg *e2elib.TestConfig) {
			gotErr := tc.action(testCfg, controllerKubeClients)
			if (tc.expectedErr != nil) != (gotErr != nil) {
				t.Fatalf("expectedErr: %v; got: %v", tc.expectedErr, gotErr)
			}

			if tc.expectedErr != nil {
				expectedError := strings.Replace(*tc.expectedErr, "{NAMESPACE_NAME_HERE}", testCfg.Namespace, 1)
				if gotErr.Error() != expectedError {
					t.Fatalf("expectedErr: %v; got: %v", expectedError, gotErr)
				}
			}
		})
	}
}

type secretBuilder struct {
	s *corev1.Secret
}

func newSecretBuilder(secretName string) *secretBuilder {
	return &secretBuilder{
		s: &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name: secretName,
			},
			Type: corev1.SecretTypeOpaque,
		},
	}
}

func (b *secretBuilder) Done() *corev1.Secret {
	return b.s
}

func (b *secretBuilder) withOwnerRefs(or []metav1.OwnerReference) *secretBuilder {
	b.s.OwnerReferences = or
	return b
}

func (b *secretBuilder) withType(t corev1.SecretType) *secretBuilder {
	b.s.Type = t
	return b
}

func (b *secretBuilder) withAnnotations(a map[string]string) *secretBuilder {
	b.s.Annotations = a
	return b
}

func createValidSecretSync(ctx context.Context, t *testing.T, client secretsyncv1alphaclient.SecretSyncInterface) *secretsyncv1alpha1.SecretSync {
	t.Helper()

	secretSync := testdata.GetSecretSyncOrDie("valid")

	secretSync, err := client.Create(ctx, secretSync, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("failed to create SecretSync: %v", err)
	}
	return secretSync
}

func getControllerClientConfig(t *testing.T, f *e2elib.Framework) *rest.Config {
	prepareCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	tokenReq, err := f.Clients().KubeClients().CoreV1().
		ServiceAccounts("secrets-store-sync-controller-system").
		CreateToken(prepareCtx,
			"secrets-store-sync-controller",
			&authnv1.TokenRequest{
				Spec: authnv1.TokenRequestSpec{
					ExpirationSeconds: ptr.To[int64](3600),
				},
			},
			metav1.CreateOptions{},
		)
	if err != nil {
		t.Fatalf("failed to retrieve token for the controller: %v", err)
	}

	saToken := tokenReq.Status.Token
	if len(saToken) == 0 {
		t.Fatalf("the SA token for the controller was empty")
	}

	controllerClientConfig := rest.AnonymousClientConfig(f.ClientConfig())
	controllerClientConfig.BearerToken = saToken

	return controllerClientConfig
}
