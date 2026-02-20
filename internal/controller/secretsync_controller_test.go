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
	"os"
	"path/filepath"
	"reflect"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
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
	"sigs.k8s.io/secrets-store-sync-controller/pkg/provider"
)

type testCondition struct {
	Type   string
	Status corev1.ConditionStatus
	Reason string
}

type testSecretSyncReconciler struct {
	fakeProviderServer   *providerfake.MockCSIProviderServer
	secretSyncReconciler *SecretSyncReconciler
}

func TestReconcile(t *testing.T) {
	tests := []struct {
		name                         string
		secretProviderClassToProcess *secretsstorecsiv1.SecretProviderClass
		secretSyncToProcess          *secretsyncv1alpha1.SecretSync
		secret                       *corev1.Secret
		expectedErrorString          string
		expectedCondition            *testCondition
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
			expectedCondition: &testCondition{
				Type:   "Create",
				Status: corev1.ConditionTrue,
				Reason: "CreateSucceeded",
			},
		},
		{
			name: "creates secret successfully with sync interval",
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
					SyncInterval:            "5m",
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
					"bar": []byte("v1"),
				},
				Type: corev1.SecretTypeOpaque,
			},
			expectedCondition: &testCondition{
				Type:   "Create",
				Status: corev1.ConditionTrue,
				Reason: "CreateSucceeded",
			},
		},
		{
			name: "fails with invalid sync interval",
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
					SyncInterval:            "30s",
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
					"bar": []byte("v1"),
				},
				Type: corev1.SecretTypeOpaque,
			},
			expectedErrorString: "sync interval 30s is less than minimum allowed 1m0s",
			expectedCondition: &testCondition{
				Type:   "Create",
				Status: corev1.ConditionFalse,
				Reason: "UserInputValidationFailed",
			},
		},
		{
			name: "SecretSync not found",
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
			secretSyncToProcess: &secretsyncv1alpha1.SecretSync{},
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "sse2esecret",
					Namespace: "default",
				},
				Data: map[string][]byte{
					"foo": []byte("bar"),
				},
			},
			expectedErrorString: `secretsyncs.secret-sync.x-k8s.io "sse2esecret" not found`,
		},
		{
			name: "use of reserved label returns validation error",
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
						Labels: map[string]string{
							"secrets-store.sync.x-k8s.io": "test",
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
			expectedErrorString: "label secrets-store.sync.x-k8s.io is reserved for use by the Secrets Store Sync Controller",
			expectedCondition: &testCondition{
				Type:   "Create",
				Status: corev1.ConditionFalse,
				Reason: "InvalidClusterSecretLabelError",
			},
		},
		{
			name: "use of reserved annotation returns validation error",
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
						Annotations: map[string]string{
							"secrets-store.sync.x-k8s.io": "test",
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
			expectedErrorString: "annotation secrets-store.sync.x-k8s.io is reserved for use by the Secrets Store Sync Controller",
			expectedCondition: &testCondition{
				Type:   "Create",
				Status: corev1.ConditionFalse,
				Reason: "InvalidClusterSecretAnnotationError",
			},
		},
		{
			name:                         "SecretProviderClass not found",
			secretProviderClassToProcess: &secretsstorecsiv1.SecretProviderClass{},
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
			expectedErrorString: `secretproviderclasses.secrets-store.csi.x-k8s.io "test-spc" not found`,
			expectedCondition: &testCondition{
				Type:   "Create",
				Status: corev1.ConditionFalse,
				Reason: "ControllerSPCError",
			},
		},
		{
			name: "failed to get provider client",
			secretProviderClassToProcess: &secretsstorecsiv1.SecretProviderClass{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-spc",
					Namespace: "default",
				},
				Spec: secretsstorecsiv1.SecretProviderClassSpec{
					Provider: "invalid-fake-provider",
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
			expectedErrorString: `provider not found: provider "invalid-fake-provider"`,
			expectedCondition: &testCondition{
				Type:   "Create",
				Status: corev1.ConditionFalse,
				Reason: "ControllerSPCError",
			},
		},
		{
			name: "invalid SecretObjectData returns validation error",
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
								TargetKey:  "",
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
			expectedErrorString: "target key in secretObject.data is empty",
			expectedCondition: &testCondition{
				Type:   "Create",
				Status: corev1.ConditionFalse,
				Reason: "UserInputValidationFailed",
			},
		},
	}

	scheme := setupScheme(t)

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			testSecretSyncReconciler := newSecretSyncReconciler(t, scheme, test.secretProviderClassToProcess, test.secretSyncToProcess, test.secret)

			// Mock request to simulate Reconcile being called
			req := ctrl.Request{
				NamespacedName: types.NamespacedName{
					Name:      "sse2esecret",
					Namespace: "default",
				},
			}

			_, err := testSecretSyncReconciler.secretSyncReconciler.Reconcile(context.Background(), req)
			if len(test.expectedErrorString) > 0 {
				if err == nil || err.Error() != test.expectedErrorString {
					t.Fatalf("expected error %q, got %q", test.expectedErrorString, err)
				}
			} else if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			// validate status condition
			condition := getSecretSyncStatusCondition(t, testSecretSyncReconciler.secretSyncReconciler, req)
			if !reflect.DeepEqual(condition, test.expectedCondition) {
				t.Fatalf("expected condition %v, got %v", test.expectedCondition, condition)
			}
		})
	}
}

func TestConditionsOnHashChange(t *testing.T) {
	secretProviderClassToProcess := &secretsstorecsiv1.SecretProviderClass{
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
	}
	secretSyncToProcess := &secretsyncv1alpha1.SecretSync{
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
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "sse2esecret",
			Namespace: "default",
		},
		Data: map[string][]byte{
			"foo": []byte("bar"),
		},
	}

	scheme := setupScheme(t)
	testSecretSyncReconciler := newSecretSyncReconciler(t, scheme, secretProviderClassToProcess, secretSyncToProcess, secret)

	// Mock request to simulate Reconcile being called
	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name:      "sse2esecret",
			Namespace: "default",
		},
	}

	_, err := testSecretSyncReconciler.secretSyncReconciler.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// simulate update with no secret value change
	_, err = testSecretSyncReconciler.secretSyncReconciler.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expectedCondition := &testCondition{
		Type:   "Update",
		Status: corev1.ConditionTrue,
		Reason: "UpdateNoValueChangeSucceeded",
	}
	condition := getSecretSyncStatusCondition(t, testSecretSyncReconciler.secretSyncReconciler, req)
	if !reflect.DeepEqual(condition, expectedCondition) {
		t.Fatalf("expected condition %v, got %v", expectedCondition, condition)
	}

	// simulate update with secret value change
	testSecretSyncReconciler.fakeProviderServer.SetFiles([]*v1alpha1.File{
		{
			Path:     "foo",
			Mode:     0644,
			Contents: []byte("bar"),
		},
	})
	_, err = testSecretSyncReconciler.secretSyncReconciler.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expectedConditionAfterSecretChange := &testCondition{
		Type:   "Update",
		Status: corev1.ConditionTrue,
		Reason: "UpdateValueChangeOrForceUpdateSucceeded",
	}
	conditionAfterSecretChange := getSecretSyncStatusCondition(t, testSecretSyncReconciler.secretSyncReconciler, req)
	if !reflect.DeepEqual(conditionAfterSecretChange, expectedConditionAfterSecretChange) {
		t.Fatalf("expected condition %v, got %v", expectedConditionAfterSecretChange, conditionAfterSecretChange)
	}
}

func getSecretSyncStatusCondition(t *testing.T, ssc *SecretSyncReconciler, req ctrl.Request) *testCondition {
	t.Helper()

	secretSync := getSecretSyncObject(t, ssc, req)
	if len(secretSync.Status.Conditions) == 0 {
		return nil
	}

	latestCondition := secretSync.Status.Conditions[len(secretSync.Status.Conditions)-1]

	return &testCondition{
		Type:   latestCondition.Type,
		Status: corev1.ConditionStatus(latestCondition.Status),
		Reason: latestCondition.Reason,
	}
}

func getSecretSyncObject(t *testing.T, ssc *SecretSyncReconciler, req ctrl.Request) *secretsyncv1alpha1.SecretSync {
	t.Helper()

	secretSync := &secretsyncv1alpha1.SecretSync{}
	err := ssc.Get(context.Background(), req.NamespacedName, secretSync)
	if err != nil && !apierrors.IsNotFound(err) {
		t.Fatalf("error getting secret sync: %v", err)
	}

	return secretSync
}

func newSecretSyncReconciler(
	t *testing.T,
	scheme *runtime.Scheme,
	spc *secretsstorecsiv1.SecretProviderClass,
	secretSync *secretsyncv1alpha1.SecretSync,
	testSecret *corev1.Secret,
) *testSecretSyncReconciler {
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

	// Create a ReconcileSecretSync object with the scheme and fake client
	kubeClient := fakeclient.NewClientset(testSecret)
	ssc := &SecretSyncReconciler{
		Client:          ctrlClient,
		Clientset:       kubeClient,
		Scheme:          scheme,
		TokenClient:     k8s.NewTokenClient(kubeClient),
		ProviderClients: providerClients,
	}

	return &testSecretSyncReconciler{
		fakeProviderServer:   server,
		secretSyncReconciler: ssc,
	}
}

func setupScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
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
