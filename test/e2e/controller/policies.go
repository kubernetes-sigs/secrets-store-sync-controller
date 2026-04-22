package controller

import (
	"context"
	"strings"
	"testing"
	"time"

	authenticationv1 "k8s.io/api/authentication/v1"
	authnv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	corev1applyconfig "k8s.io/client-go/applyconfigurations/core/v1"
	metav1applyconfig "k8s.io/client-go/applyconfigurations/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/utils/ptr"

	e2elib "sigs.k8s.io/secrets-store-sync-controller/test/e2e/library"
)

const (
	createUpdateErrMsg = "secrets \"{NAME}\" is forbidden: ValidatingAdmissionPolicy 'secrets-store-sync-controller-create-update-policy' with binding " +
		"'secrets-store-sync-controller-create-update-policy-binding' denied request: secrets-store-sync-controller has failed to {ACTION} secret " +
		"with Opaque type in the {NAMESPACE} namespace. The controller can only create or update secrets in the allowed types list with a single secretsync owner."

	oldObjectUpdateErrMsg = "secrets \"{NAME}\" is forbidden: ValidatingAdmissionPolicy 'secrets-store-sync-controller-update-check-oldobject-policy' with binding " +
		"'secrets-store-sync-controller-update-check-oldobject-policy-binding' denied request: secrets-store-sync-controller has failed to UPDATE old secret in the " +
		"{NAMESPACE} namespace. The controller can only update secrets with a single secrets-store-sync-controller owner."

	updateLabelErrMsg = "secrets \"{NAME}\" is forbidden: ValidatingAdmissionPolicy 'secrets-store-sync-controller-update-label-policy' with binding " +
		"'secrets-store-sync-controller-update-label-policy-binding' denied request: secrets-store-sync-controller has failed to UPDATE secret with " +
		"Opaque type in the {NAMESPACE} namespace because it does not have the correct label. Delete the secret and force the controller to recreate it with the correct label."

	saTokenRequestErrMsg = "serviceaccounts \"{NAME}\" is forbidden: ValidatingAdmissionPolicy 'secrets-store-sync-controller-validate-token-policy' with binding " +
		"'secrets-store-sync-controller-validate-token-policy-binding' denied request: Unauthorized ServiceAccount token request in namespace {NAMESPACE}: " +
		"the request must set expirationSeconds=600 and pick a single audience from [secrets-store-sync-controller]"
)

func TestCreateUpdateSecretsTypes_Create(t *testing.T, f *e2elib.Framework) {
	controllerKubeClients := kubernetes.NewForConfigOrDie(getControllerClientConfig(t, f))

	tests := []struct {
		name            string
		secretModifiers func(sb *secretBuilder) *corev1.Secret
		expectedErr     *string
	}{
		{
			name:            "owner references - none exist",
			secretModifiers: func(sb *secretBuilder) *corev1.Secret { return sb.Done() },
			expectedErr:     new(createUpdateErrMsg),
		},
		{
			name: "owner references - wrong kind",
			secretModifiers: func(sb *secretBuilder) *corev1.Secret {
				return sb.withOwnerRefs([]metav1.OwnerReference{{
					APIVersion: "rbac.authorization.k8s.io/v1",
					Kind:       "User",
					Name:       "ownererefs-wrong-kind",
					UID:        "someuid",
				}},
				).Done()
			},
			expectedErr: new(createUpdateErrMsg),
		},
		{
			name: "owner references - two refs",
			secretModifiers: func(sb *secretBuilder) *corev1.Secret {
				return sb.withOwnerRefs([]metav1.OwnerReference{{
					APIVersion: "secret-sync.x-k8s.io/v1",
					Kind:       "SecretSync",
					Name:       "two-owner-refs",
					UID:        "someuid",
				}, {
					APIVersion: "secret-sync.x-k8s.io/v1alpha",
					Kind:       "SecretSync",
					Name:       "two-owner-refs",
					UID:        "someuid",
				}},
				).Done()
			},
			expectedErr: new(createUpdateErrMsg),
		},
		{
			name: "create a ServiceAccount secret",
			secretModifiers: func(sb *secretBuilder) *corev1.Secret {
				return sb.withOwnerRefs([]metav1.OwnerReference{{
					APIVersion: "secret-sync.x-k8s.io/v1",
					Kind:       "SecretSync",
					Name:       "sa-type-secret",
					UID:        "someuid",
				}}).
					withType(corev1.SecretTypeServiceAccountToken).
					withAnnotations(map[string]string{corev1.ServiceAccountNameKey: "default"}).
					Done()
			},
			expectedErr: new("secrets \"{NAME}\" is forbidden: ValidatingAdmissionPolicy 'secrets-store-sync-controller-create-update-policy' " +
				"with binding 'secrets-store-sync-controller-create-update-policy-binding' denied request: secrets-store-sync-controller has failed to " +
				"CREATE secret with kubernetes.io/service-account-token type in the {NAMESPACE} namespace. The controller is " +
				"not allowed to create or update secrets with this type."),
		},
		{
			name: "create a secret with a type that is not explicitly allowed",
			secretModifiers: func(sb *secretBuilder) *corev1.Secret {
				return sb.withOwnerRefs([]metav1.OwnerReference{{
					APIVersion: "secret-sync.x-k8s.io/v1",
					Kind:       "SecretSync",
					Name:       "type-not-allowed",
					UID:        "someuid",
				}}).
					withType("very.random/custom-type").
					Done()
			},
			expectedErr: new("secrets \"{NAME}\" is forbidden: ValidatingAdmissionPolicy 'secrets-store-sync-controller-create-update-policy' with " +
				"binding 'secrets-store-sync-controller-create-update-policy-binding' denied request: secrets-store-sync-controller has failed to CREATE secret" +
				" with very.random/custom-type type in the {NAMESPACE} namespace. The controller can only create or update secrets in the allowed types" +
				" list with a single secretsync owner."),
		},
	}

	for _, tc := range tests {
		f.RunTest(t, "CREATE "+tc.name, func(t *testing.T, testCfg *e2elib.TestConfig) {
			testSecret := tc.secretModifiers(newSecretBuilder("test-secret"))

			_, gotErr := controllerKubeClients.CoreV1().Secrets(testCfg.Namespace).Create(testCfg.Context, testSecret, metav1.CreateOptions{})
			if (tc.expectedErr != nil) != (gotErr != nil) {
				t.Fatalf("expectedErr: %v; got: %v", tc.expectedErr, gotErr)
			}

			if tc.expectedErr != nil {
				expectedError := replaceInAuthorizationMessages(*tc.expectedErr, testCfg.Namespace, testSecret.Name, "CREATE")
				if gotErr.Error() != expectedError {
					t.Fatalf("expectedErr: %v; got: %v", expectedError, gotErr)
				}
			}
		})
	}
}

func TestCreateUpdateSecretsTypes_Patch(t *testing.T, f *e2elib.Framework) {
	controllerKubeClients := kubernetes.NewForConfigOrDie(getControllerClientConfig(t, f))

	tests := []struct {
		name                     string
		secretApplyConfigChanges func(*corev1applyconfig.SecretApplyConfiguration) *corev1applyconfig.SecretApplyConfiguration
		expectedErr              *string
	}{
		{
			name: "owner references - remove all",
			secretApplyConfigChanges: func(ac *corev1applyconfig.SecretApplyConfiguration) *corev1applyconfig.SecretApplyConfiguration {
				ac.OwnerReferences = nil
				return ac
			},
			expectedErr: new(createUpdateErrMsg),
		},
		{
			name: "owner references - wrong kind",
			secretApplyConfigChanges: func(ac *corev1applyconfig.SecretApplyConfiguration) *corev1applyconfig.SecretApplyConfiguration {
				ac.OwnerReferences = []metav1applyconfig.OwnerReferenceApplyConfiguration{{
					APIVersion: new("rbac.authorization.k8s.io/v1"),
					Kind:       new("User"),
					Name:       new("ownererefs-wrong-kind"),
					UID:        new(types.UID("someuid")),
				}}
				return ac
			},
			expectedErr: new(createUpdateErrMsg),
		},
		{
			name: "owner references - two refs",
			secretApplyConfigChanges: func(ac *corev1applyconfig.SecretApplyConfiguration) *corev1applyconfig.SecretApplyConfiguration {
				ac.OwnerReferences = []metav1applyconfig.OwnerReferenceApplyConfiguration{{
					APIVersion: new("secret-sync.x-k8s.io/v1"),
					Kind:       new("SecretSync"),
					Name:       new("two-owner-refs"),
					UID:        new(types.UID("someuid")),
				}, {
					APIVersion: new("secret-sync.x-k8s.io/v1alpha"),
					Kind:       new("SecretSync"),
					Name:       new("two-owner-refs"),
					UID:        new(types.UID("someuid2")),
				}}
				return ac
			},
			expectedErr: new(createUpdateErrMsg),
		},
		{
			name: "owner references - patch results in two refs",
			secretApplyConfigChanges: func(ac *corev1applyconfig.SecretApplyConfiguration) *corev1applyconfig.SecretApplyConfiguration {
				ac.OwnerReferences = []metav1applyconfig.OwnerReferenceApplyConfiguration{{
					APIVersion: new("secret-sync.x-k8s.io/v1"),
					Kind:       new("SecretSync"),
					Name:       new("two-owner-refs"),
					UID:        new(types.UID("test-secret")),
				}}
				return ac
			},
			expectedErr: new(createUpdateErrMsg),
		},
	}

	for _, tc := range tests {
		f.RunTest(t, "PATCH "+tc.name, func(t *testing.T, testCfg *e2elib.TestConfig) {
			origSecret, err := f.Clients().KubeClients().CoreV1().Secrets(testCfg.Namespace).Create(testCfg.Context, newValidSecretBuilder("test-secret").withOwnerRefs(nil).Done(), metav1.CreateOptions{})
			if err != nil {
				t.Fatalf("failed to create secret: %v", err)
			}

			// prep step to get the managedFields right
			secretApply, err := corev1applyconfig.ExtractSecret(origSecret, "secrets-store-sync-controller")
			if err != nil {
				t.Fatalf("failed to extract applyconfig from Secret: %v", err)
			}
			secretApply = secretApply.WithOwnerReferences(&metav1applyconfig.OwnerReferenceApplyConfiguration{
				APIVersion: new("secret-sync.x-k8s.io/v1"),
				Kind:       new("SecretSync"),
				Name:       new("test-secret"),
				UID:        new(types.UID("someuid")),
			})

			preparedSecret, err := f.Clients().KubeClients().CoreV1().Secrets(testCfg.Namespace).Apply(testCfg.Context, secretApply, metav1.ApplyOptions{FieldManager: "secrets-store-sync-controller"})
			if err != nil {
				t.Fatalf("failed to prepare managedFields for next update: %v", err)
			}

			testSecretApply, err := corev1applyconfig.ExtractSecret(preparedSecret, "secrets-store-sync-controller")
			if err != nil {
				t.Fatalf("failed to extract applyconfig from Secret: %v", err)
			}

			if tc.secretApplyConfigChanges != nil {
				testSecretApply = tc.secretApplyConfigChanges(testSecretApply)
			}

			_, gotErr := controllerKubeClients.CoreV1().Secrets(testCfg.Namespace).Apply(testCfg.Context, testSecretApply, metav1.ApplyOptions{FieldManager: "secrets-store-sync-controller"})
			if (tc.expectedErr != nil) != (gotErr != nil) {
				t.Fatalf("expectedErr: %v; got: %v", tc.expectedErr, gotErr)
			}

			if tc.expectedErr != nil {
				expectedError := replaceInAuthorizationMessages(*tc.expectedErr, testCfg.Namespace, origSecret.Name, "UPDATE")
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
			expectedErr: new("secrets \"some-name\" is forbidden: User \"system:serviceaccount:secrets-store-sync-controller-system:secrets-store-sync-controller\"" +
				" cannot delete resource \"secrets\" in API group \"\" in the namespace \"{NAMESPACE}\""),
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
			expectedErr: new("secrets \"some-name\" is forbidden: ValidatingAdmissionPolicy 'secrets-store-sync-controller-delete-policy' with binding" +
				" 'secrets-store-sync-controller-delete-policy-binding' denied request: secrets-store-sync-controller has failed to DELETE secrets in " +
				"the {NAMESPACE} namespace. The controller is not allowed to delete secrets.",
			),
		},
	} {
		f.RunTest(t, tc.name, func(t *testing.T, testCfg *e2elib.TestConfig) {
			gotErr := tc.action(testCfg, controllerKubeClients)
			if (tc.expectedErr != nil) != (gotErr != nil) {
				t.Fatalf("expectedErr: %v; got: %v", tc.expectedErr, gotErr)
			}

			if tc.expectedErr != nil {
				expectedError := replaceInAuthorizationMessages(*tc.expectedErr, testCfg.Namespace, "some-name", "DELETE")
				if gotErr.Error() != expectedError {
					t.Fatalf("expectedErr: %v; got: %v", expectedError, gotErr)
				}
			}
		})
	}
}

func TestUpdateOwnersCheckOldObject(t *testing.T, f *e2elib.Framework) {
	controllerKubeClients := kubernetes.NewForConfigOrDie(getControllerClientConfig(t, f))

	tests := []struct {
		name                     string
		secretModifiers          func(*corev1applyconfig.SecretApplyConfiguration) *corev1applyconfig.SecretApplyConfiguration
		secretApplyConfigChanges func(*corev1applyconfig.SecretApplyConfiguration) *corev1applyconfig.SecretApplyConfiguration
		expectedErr              *string
	}{
		{
			name: "owner references - delete all",
			secretApplyConfigChanges: func(ac *corev1applyconfig.SecretApplyConfiguration) *corev1applyconfig.SecretApplyConfiguration {
				ac.OwnerReferences = nil
				return ac
			},
			expectedErr: new(oldObjectUpdateErrMsg),
		},
		{
			name: "owner references - wrong kind",
			secretApplyConfigChanges: func(ac *corev1applyconfig.SecretApplyConfiguration) *corev1applyconfig.SecretApplyConfiguration {
				ac.OwnerReferences = []metav1applyconfig.OwnerReferenceApplyConfiguration{{
					APIVersion: new("rbac.authorization.k8s.io/v1"),
					Kind:       new("User"),
					Name:       new("ownererefs-wrong-kind"),
					UID:        new(types.UID("someuid")),
				}}
				return ac
			},
			expectedErr: new(oldObjectUpdateErrMsg),
		},
		{
			name: "owner references - two refs",
			secretApplyConfigChanges: func(ac *corev1applyconfig.SecretApplyConfiguration) *corev1applyconfig.SecretApplyConfiguration {
				ac.OwnerReferences = []metav1applyconfig.OwnerReferenceApplyConfiguration{{
					APIVersion: new("secret-sync.x-k8s.io/v1"),
					Kind:       new("SecretSync"),
					Name:       new("two-owner-refs"),
					UID:        new(types.UID("someuid")),
				}, {
					APIVersion: new("secret-sync.x-k8s.io/v1alpha"),
					Kind:       new("SecretSync"),
					Name:       new("two-owner-refs"),
					UID:        new(types.UID("someuid2")),
				}}
				return ac
			},
			expectedErr: new(oldObjectUpdateErrMsg),
		},
		{
			name: "owner references - everything works",
			secretApplyConfigChanges: func(ac *corev1applyconfig.SecretApplyConfiguration) *corev1applyconfig.SecretApplyConfiguration {
				return ac.WithOwnerReferences(&metav1applyconfig.OwnerReferenceApplyConfiguration{
					APIVersion: new("secret-sync.x-k8s.io/v1"),
					Kind:       new("SecretSync"),
					Name:       new("test-secret"),
					UID:        new(types.UID("test-secret")),
				})
			},
		},
	}

	for _, tc := range tests {
		f.RunTest(t, tc.name, func(t *testing.T, testCfg *e2elib.TestConfig) {
			origSecret, err := f.Clients().KubeClients().CoreV1().Secrets(testCfg.Namespace).Create(testCfg.Context, newValidSecretBuilder("test-secret").withOwnerRefs(nil).Done(), metav1.CreateOptions{})
			if err != nil {
				t.Fatalf("failed to create secret: %v", err)
			}

			// prep step to get the managedFields right
			secretApply, err := corev1applyconfig.ExtractSecret(origSecret, "secrets-store-sync-controller")
			if err != nil {
				t.Fatalf("failed to extract applyconfig from Secret: %v", err)
			}

			secretApply = tc.secretApplyConfigChanges(secretApply)

			preparedSecret, err := f.Clients().KubeClients().CoreV1().Secrets(testCfg.Namespace).Apply(testCfg.Context, secretApply, metav1.ApplyOptions{FieldManager: "secrets-store-sync-controller"})
			if err != nil {
				t.Fatalf("failed to prepare managedFields for next update: %v", err)
			}

			// the test itself follows
			testSecretApply, err := corev1applyconfig.ExtractSecret(preparedSecret, "secrets-store-sync-controller")
			if err != nil {
				t.Fatalf("failed to extract applyconfig from Secret: %v", err)
			}

			testSecretApply.OwnerReferences = []metav1applyconfig.OwnerReferenceApplyConfiguration{{
				APIVersion: new("secret-sync.x-k8s.io/v1"),
				Kind:       new("SecretSync"),
				Name:       new(origSecret.Name),
				UID:        new(types.UID("some-uid")),
			}}

			_, gotErr := controllerKubeClients.CoreV1().Secrets(testCfg.Namespace).Apply(testCfg.Context, testSecretApply, metav1.ApplyOptions{FieldManager: "secrets-store-sync-controller"})
			if (tc.expectedErr != nil) != (gotErr != nil) {
				t.Fatalf("expectedErr: %v; got: %v", tc.expectedErr, gotErr)
			}

			if tc.expectedErr != nil {
				expectedError := replaceInAuthorizationMessages(*tc.expectedErr, testCfg.Namespace, origSecret.Name, "")
				if gotErr.Error() != expectedError {
					t.Fatalf("expectedErr: %v; got: %v", expectedError, gotErr)
				}
			}
		})
	}
}

func TestUpdateSecretsBasedOnLabel(t *testing.T, f *e2elib.Framework) {
	controllerKubeClients := kubernetes.NewForConfigOrDie(getControllerClientConfig(t, f))

	tests := []struct {
		name                     string
		secretModifiers          func(*secretBuilder) *corev1.Secret
		secretApplyConfigChanges func(*corev1applyconfig.SecretApplyConfiguration) *corev1applyconfig.SecretApplyConfiguration
		expectedErr              *string
	}{
		{
			name: "old secret has no labels",
			secretModifiers: func(sb *secretBuilder) *corev1.Secret {
				return sb.withLabels(nil).Done()
			},
			expectedErr: new(updateLabelErrMsg),
		},
		{
			name: "old secret has wrong label key",
			secretModifiers: func(sb *secretBuilder) *corev1.Secret {
				return sb.withLabels(map[string]string{"wrong-key": ""}).Done()
			},
			expectedErr: new(updateLabelErrMsg),
		},
		{
			name: "old secret has wrong label value",
			secretModifiers: func(sb *secretBuilder) *corev1.Secret {
				return sb.withLabels(map[string]string{"secrets-store.sync.x-k8s.io": "wrong-value"}).Done()
			},
			expectedErr: new(updateLabelErrMsg),
		},
		{
			name: "no labels in old secret",
			secretModifiers: func(sb *secretBuilder) *corev1.Secret {
				return sb.withLabels(map[string]string{}).Done()
			},
			expectedErr: new(updateLabelErrMsg),
		},
		{
			name: "remove controller label key",
			secretModifiers: func(sb *secretBuilder) *corev1.Secret {
				return sb.withLabels(map[string]string{"secrets-store.sync.x-k8s.io": ""}).Done()
			},
			secretApplyConfigChanges: func(ac *corev1applyconfig.SecretApplyConfiguration) *corev1applyconfig.SecretApplyConfiguration {
				ac.Labels = map[string]string{"some-other-key": "value"}
				return ac
			},
		},
		{
			name: "everything works - correct labels on orig",
			secretModifiers: func(sb *secretBuilder) *corev1.Secret {
				return sb.Done()
			},
		},
	}

	for _, tc := range tests {
		f.RunTest(t, tc.name, func(t *testing.T, testCfg *e2elib.TestConfig) {
			origSecret, err := f.Clients().KubeClients().CoreV1().Secrets(testCfg.Namespace).Create(testCfg.Context, tc.secretModifiers(newValidSecretBuilder("test-secret")), metav1.CreateOptions{})
			if err != nil {
				t.Fatalf("failed to create secret: %v", err)
			}

			secretApply, err := corev1applyconfig.ExtractSecret(origSecret, "secrets-store-sync-controller")
			if err != nil {
				t.Fatalf("failed to extract applyconfig from Secret: %v", err)
			}

			if tc.secretApplyConfigChanges != nil {
				secretApply = tc.secretApplyConfigChanges(secretApply)
			}

			_, gotErr := controllerKubeClients.CoreV1().Secrets(testCfg.Namespace).Apply(testCfg.Context, secretApply, metav1.ApplyOptions{FieldManager: "secrets-store-sync-controller"})
			if (tc.expectedErr != nil) != (gotErr != nil) {
				t.Fatalf("expectedErr: %v; got: %v", tc.expectedErr, gotErr)
			}

			if tc.expectedErr != nil {
				expectedError := replaceInAuthorizationMessages(*tc.expectedErr, testCfg.Namespace, origSecret.Name, "")
				if gotErr.Error() != expectedError {
					t.Fatalf("expectedErr: %v; got: %v", expectedError, gotErr)
				}
			}
		})
	}
}

func TestValidateTokenConfig(t *testing.T, f *e2elib.Framework) {
	controllerKubeClients := kubernetes.NewForConfigOrDie(getControllerClientConfig(t, f))

	testSAName := "test-sa"

	tests := []struct {
		name           string
		saTokenRequest *authenticationv1.TokenRequest
		expectedErr    *string
	}{
		{
			name: "everything correct",
			saTokenRequest: &authnv1.TokenRequest{
				Spec: authnv1.TokenRequestSpec{
					ExpirationSeconds: new(int64(600)),
					Audiences:         []string{"secrets-store-sync-controller"},
				},
			},
		},
		{
			name: "invalid expiration",
			saTokenRequest: &authnv1.TokenRequest{
				Spec: authnv1.TokenRequestSpec{
					ExpirationSeconds: new(int64(3600)),
					Audiences:         []string{"secrets-store-sync-controller"},
				},
			},
			expectedErr: new(saTokenRequestErrMsg),
		},
		{
			name: "no audiences requested",
			saTokenRequest: &authnv1.TokenRequest{
				Spec: authnv1.TokenRequestSpec{
					ExpirationSeconds: new(int64(600)),
				},
			},
			expectedErr: new(saTokenRequestErrMsg),
		},
		{
			name: "audience incorrect",
			saTokenRequest: &authnv1.TokenRequest{
				Spec: authnv1.TokenRequestSpec{
					ExpirationSeconds: new(int64(600)),
					Audiences:         []string{"incorrect"},
				},
			},
			expectedErr: new(saTokenRequestErrMsg),
		},
		{
			name: "audiences incorrect",
			saTokenRequest: &authnv1.TokenRequest{
				Spec: authnv1.TokenRequestSpec{
					ExpirationSeconds: new(int64(600)),
					Audiences:         []string{"incorrect", "second-one-the-same", "third-just-as-well"},
				},
			},
			expectedErr: new(saTokenRequestErrMsg),
		},
		{
			name: "too many audiences, one correct",
			saTokenRequest: &authnv1.TokenRequest{
				Spec: authnv1.TokenRequestSpec{
					ExpirationSeconds: new(int64(600)),
					Audiences:         []string{"second-one-the-same", "secrets-store-sync-controller", "third-just-as-well"},
				},
			},
			expectedErr: new(saTokenRequestErrMsg),
		},
	}

	for _, tc := range tests {
		f.RunTest(t, tc.name, func(t *testing.T, testCfg *e2elib.TestConfig) {
			if _, err := testCfg.Clients.KubeClients().CoreV1().
				ServiceAccounts(testCfg.Namespace).
				Create(
					testCfg.Context,
					&corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: testSAName}},
					metav1.CreateOptions{},
				); err != nil {
				t.Fatalf("failed to create a ServiceAccount: %v", err)
			}

			_, gotErr := controllerKubeClients.CoreV1().ServiceAccounts(testCfg.Namespace).
				CreateToken(
					testCfg.Context, testSAName, tc.saTokenRequest, metav1.CreateOptions{},
				)
			if (tc.expectedErr != nil) != (gotErr != nil) {
				t.Fatalf("expectedErr: %v; got: %v", tc.expectedErr, gotErr)
			}

			if tc.expectedErr != nil {
				expectedError := replaceInAuthorizationMessages(*tc.expectedErr, testCfg.Namespace, testSAName, "")
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

func newValidSecretBuilder(secretName string) *secretBuilder {
	return &secretBuilder{s: validSecret(secretName)}
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

func (b *secretBuilder) withLabels(l map[string]string) *secretBuilder {
	b.s.Labels = l
	return b
}

func (b *secretBuilder) withAnnotations(a map[string]string) *secretBuilder {
	b.s.Annotations = a
	return b
}

func validSecret(name string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
			Labels: map[string]string{
				"secrets-store.sync.x-k8s.io": "",
			},
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: "secret-sync.x-k8s.io/v1",
				Kind:       "SecretSync",
				Name:       name,
				UID:        "someuid",
			}},
		},
		Type: corev1.SecretTypeOpaque,
	}
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

func replaceInAuthorizationMessages(msg, namespace, name, action string) string {
	replacer := strings.NewReplacer("{NAMESPACE}", namespace, "{NAME}", name, "{ACTION}", action)

	return replacer.Replace(msg)
}
