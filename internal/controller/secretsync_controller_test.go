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

package controller

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	fakeclient "k8s.io/client-go/kubernetes/fake"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	secretsstorecsiv1 "sigs.k8s.io/secrets-store-csi-driver/apis/v1"
	providerfake "sigs.k8s.io/secrets-store-csi-driver/provider/fake"
	"sigs.k8s.io/secrets-store-csi-driver/provider/v1alpha1"
	secretsyncv1alpha1 "sigs.k8s.io/secrets-store-sync-controller/api/v1alpha1"
	"sigs.k8s.io/secrets-store-sync-controller/pkg/k8s"
	"sigs.k8s.io/secrets-store-sync-controller/pkg/metrics"
	"sigs.k8s.io/secrets-store-sync-controller/pkg/provider"
)

func TestReconcile(t *testing.T) {
	tests := []struct {
		name                         string
		secretProviderClassToProcess *secretsstorecsiv1.SecretProviderClass
		secretSyncToProcess          *secretsyncv1alpha1.SecretSync
		secret                       *corev1.Secret
		err                          error
	}{
		{
			name: "creates secret successfully",
			secretProviderClassToProcess: &secretsstorecsiv1.SecretProviderClass{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-spc",
					Namespace: "default",
				},
				Spec: secretsstorecsiv1.SecretProviderClassSpec{
					Provider: "fake-provider",
					Parameters: map[string]string{
						"foo": "v1",
					},
				},
			},
			secretSyncToProcess: &secretsyncv1alpha1.SecretSync{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "sse2esecret",
					Namespace: "default",
				},
				Spec: secretsyncv1alpha1.SecretSyncSpec{
					ServiceAccountName:      "default",
					SecretProviderClassName: "test-spc",
					SecretObject: secretsyncv1alpha1.SecretObject{
						Type: "Opaque",
						Data: []secretsyncv1alpha1.SecretObjectData{
							{
								SourcePath: "foo",
								TargetKey:  "bar",
							},
						},
					},
				},
			},
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "sse2esecret",
					Namespace: "default",
				},
				Data: map[string][]byte{
					"foo": []byte("bar"),
				},
			},
			err: nil,
		},
	}

	scheme := setupScheme(t)

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ssc := newSecretSyncReconciler(t, scheme, test.secretProviderClassToProcess, test.secretSyncToProcess, test.secret)

			// Mock request to simulate Reconcile being called
			req := ctrl.Request{
				NamespacedName: types.NamespacedName{
					Name:      "sse2esecret",
					Namespace: "default",
				},
			}

			_, err := ssc.Reconcile(context.Background(), req)
			if !errors.Is(err, test.err) {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func newSecretSyncReconciler(
	t *testing.T,
	scheme *runtime.Scheme,
	spc *secretsstorecsiv1.SecretProviderClass,
	secretSync *secretsyncv1alpha1.SecretSync,
	testSecret *corev1.Secret,
) *SecretSyncReconciler {
	t.Helper()

	initObjects := []client.Object{
		testSecret,
		spc,
		secretSync,
	}

	// Create a fake client to mock API calls
	ctrlClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(initObjects...).WithStatusSubresource(secretSync).Build()

	// Create a mock provider named "fake-provider".
	// t.TempDir() creates a temporary directory which might have long path. sever.Start() fails with long path.
	// So, create a temporary directory with shorter path.
	socketPath, _ := os.MkdirTemp("/tmp", "e2e-secret-sync-controller-test-")
	t.Cleanup(func() {
		err := os.RemoveAll(socketPath)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	server, err := providerfake.NewMocKCSIProviderServer(filepath.Join(socketPath, "fake-provider.sock"))
	if err != nil {
		t.Fatalf("unexpected mock provider failure: %v", err)
	}

	server.SetObjects(map[string]string{"secret/object1": "v1"})
	server.SetFiles([]*v1alpha1.File{
		{
			Path:     "foo",
			Mode:     0644,
			Contents: []byte("foo"),
		},
	})

	if err := server.Start(); err != nil {
		t.Fatalf("unexpected mock provider start failure: %v", err)
	}
	t.Cleanup(server.Stop)

	providerClients := provider.NewPluginClientBuilder([]string{socketPath})

	err = metrics.InitMetricsExporter()
	if err != nil {
		t.Fatalf("failed to initialize metrics exporter")
	}

	sr, err := NewStatsReporter()
	if err != nil {
		t.Fatalf("failed to initialize stats reporter")
	}

	// Create a ReconcileSecretSync object with the scheme and fake client
	kubeClient := fakeclient.NewSimpleClientset(testSecret)
	ssc := &SecretSyncReconciler{
		Client:          ctrlClient,
		Clientset:       kubeClient,
		Scheme:          scheme,
		TokenClient:     k8s.NewTokenClient(kubeClient),
		ProviderClients: providerClients,
		MetricReporter:  sr,
	}

	return ssc
}

func setupScheme(t *testing.T) *runtime.Scheme {
	scheme := runtime.NewScheme()

	if err := secretsstorecsiv1.AddToScheme(scheme); err != nil {
		t.Fatalf("Unable to add SecretProviderClass to scheme: %v", err)
	}

	if err := secretsyncv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("Unable to add SecretSync to scheme: %v", err)
	}

	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatalf("Unable to add ClientGo scheme: %v", err)
	}

	return scheme
}
