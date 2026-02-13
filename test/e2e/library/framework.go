package library

import (
	"context"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	secretsstorecsiv1 "sigs.k8s.io/secrets-store-csi-driver/apis/v1"

	secretsyncclient "sigs.k8s.io/secrets-store-sync-controller/client/clientset/versioned"
)

type Framework struct {
	t       *testing.T
	clients *testClientSet
}

type TestConfig struct {
	Context   context.Context
	Namespace string
	Clients   TestClientSet
}

func NewFramework(ctx context.Context, t *testing.T, clientConfig *rest.Config) *Framework {
	testClientConfig := rest.CopyConfig(clientConfig)
	testClientConfig.UserAgent = "secret-store-sync-controller-e2e-tests"

	clients := NewTestClientSet(testClientConfig)

	return &Framework{
		t:       t,
		clients: clients,
	}
}

type TestClientSet interface {
	KubeClients() kubernetes.Interface
	Dynamic() dynamic.Interface
	SSClient() secretsyncclient.Interface
	SecretProviderClasses(namespace string) dynamic.ResourceInterface
}

type testClientSet struct {
	kubeClients kubernetes.Interface
	dynamic     dynamic.Interface
	ssClient    secretsyncclient.Interface
}

func NewTestClientSet(cfg *rest.Config) *testClientSet {
	return &testClientSet{
		kubeClients: kubernetes.NewForConfigOrDie(cfg),
		dynamic:     dynamic.NewForConfigOrDie(cfg),
		ssClient:    secretsyncclient.NewForConfigOrDie(cfg),
	}
}

func (s *testClientSet) KubeClients() kubernetes.Interface    { return s.kubeClients }
func (s *testClientSet) Dynamic() dynamic.Interface           { return s.dynamic }
func (s *testClientSet) SSClient() secretsyncclient.Interface { return s.ssClient }

func (s *testClientSet) SecretProviderClasses(namespace string) dynamic.ResourceInterface {
	return s.dynamic.Resource(secretsstorecsiv1.SchemeGroupVersion.WithResource("secretproviderclasses")).Namespace(namespace)
}

func (f *Framework) RunTest(name string, runner func(t *testing.T, testCfg *TestConfig)) {
	f.t.Run(name, func(t *testing.T) {
		testCtx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()

		ns, err := f.clients.KubeClients().CoreV1().Namespaces().Create(testCtx, &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: strings.ReplaceAll(strings.ToLower(name), " ", "-") + "-",
			},
		}, metav1.CreateOptions{})

		if err != nil {
			t.Fatalf("failed to prepare namespace for a test: %v", err)
		}

		t.Cleanup(func() {
			if err := f.clients.KubeClients().CoreV1().Namespaces().Delete(context.Background(), ns.Name, metav1.DeleteOptions{}); err != nil {
				t.Logf("failed to clean up namespace %q: %v", ns.Name, err)
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
