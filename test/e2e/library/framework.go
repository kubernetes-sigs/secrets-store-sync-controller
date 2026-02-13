package library

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	secretscsiclientv1 "sigs.k8s.io/secrets-store-csi-driver/pkg/client/clientset/versioned/typed/apis/v1"

	secretsyncclient "sigs.k8s.io/secrets-store-sync-controller/client/clientset/versioned"
)

type Framework struct {
	clients *testClientSet
}

type TestConfig struct {
	Context   context.Context
	Namespace string
	Clients   TestClientSet
}

func NewFramework(t *testing.T) *Framework {
	clientConfig, err := NewClientConfig()
	if err != nil {
		t.Fatalf("failed to get kube client config: %v", err)
	}

	testClientConfig := rest.CopyConfig(clientConfig)
	testClientConfig.UserAgent = "secret-store-sync-controller-e2e-tests"

	clients := NewTestClientSet(testClientConfig)

	return &Framework{
		clients: clients,
	}
}

type TestClientSet interface {
	KubeClients() kubernetes.Interface
	Dynamic() dynamic.Interface
	SSClient() secretsyncclient.Interface
	// we'll only ever be working with the SecretProviderClasses, use just this specific getter
	SecretProviderClasses() secretscsiclientv1.SecretProviderClassesGetter
}

type testClientSet struct {
	kubeClients      kubernetes.Interface
	dynamic          dynamic.Interface
	ssClient         secretsyncclient.Interface
	secretsCSIClient secretscsiclientv1.SecretProviderClassesGetter
}

func NewTestClientSet(cfg *rest.Config) *testClientSet {
	return &testClientSet{
		kubeClients:      kubernetes.NewForConfigOrDie(cfg),
		dynamic:          dynamic.NewForConfigOrDie(cfg),
		ssClient:         secretsyncclient.NewForConfigOrDie(cfg),
		secretsCSIClient: secretscsiclientv1.NewForConfigOrDie(cfg),
	}
}

func (s *testClientSet) KubeClients() kubernetes.Interface    { return s.kubeClients }
func (s *testClientSet) Dynamic() dynamic.Interface           { return s.dynamic }
func (s *testClientSet) SSClient() secretsyncclient.Interface { return s.ssClient }
func (s *testClientSet) SecretProviderClasses() secretscsiclientv1.SecretProviderClassesGetter {
	return s.secretsCSIClient
}

func (f *Framework) RunTest(t *testing.T, name string, runner func(t *testing.T, testCfg *TestConfig)) {
	t.Run(name, func(t *testing.T) {
		testCtx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
		defer cancel()

		ns, err := f.clients.KubeClients().CoreV1().Namespaces().Create(testCtx, &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "secret-store-sync-e2e-",
				Labels: map[string]string{
					"e2e.test": "secrets-store-sync-controller",
				},
				Annotations: map[string]string{"e2e.test/name": name},
			},
		}, metav1.CreateOptions{})

		if err != nil {
			t.Fatalf("failed to prepare namespace for a test: %v", err)
		}

		t.Cleanup(func() {
			if !t.Failed() {
				if err := f.clients.KubeClients().CoreV1().Namespaces().Delete(context.Background(), ns.Name, metav1.DeleteOptions{}); err != nil {
					t.Logf("failed to clean up namespace %q: %v", ns.Name, err)
				}
				return
			}
		})

		testCfg := &TestConfig{
			Context:   testCtx,
			Namespace: ns.Name,
			Clients:   f.clients,
		}

		runner(t, testCfg)
	})
}

func (f *Framework) CreateNS(ctx context.Context, nsNamePrefix string) (*corev1.Namespace, error) {
	return f.clients.kubeClients.CoreV1().Namespaces().Create(ctx,
		&corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: nsNamePrefix,
				Labels: map[string]string{
					"e2e.test": "secrets-store-sync-controller",
				},
			},
		},
		metav1.CreateOptions{},
	)
}
