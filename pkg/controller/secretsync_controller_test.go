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
	"slices"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	fakeclient "k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"

	secretsstorecsiv1 "sigs.k8s.io/secrets-store-csi-driver/apis/v1"
	csilisters "sigs.k8s.io/secrets-store-csi-driver/pkg/client/listers/apis/v1"
	providerfake "sigs.k8s.io/secrets-store-csi-driver/provider/fake"
	"sigs.k8s.io/secrets-store-csi-driver/provider/v1alpha1"

	secretsyncv1alpha1 "sigs.k8s.io/secrets-store-sync-controller/api/secretsync/v1alpha1"
	ssfake "sigs.k8s.io/secrets-store-sync-controller/client/clientset/versioned/fake"
	secretsynclister "sigs.k8s.io/secrets-store-sync-controller/client/listers/secretsync/v1alpha1"
	"sigs.k8s.io/secrets-store-sync-controller/pkg/provider"
	"sigs.k8s.io/secrets-store-sync-controller/pkg/token"
)

type testSecretSyncReconciler struct {
	fakeProviderServer   *providerfake.MockCSIProviderServer
	secretSyncReconciler *SecretSyncReconciler
	actions              []recordedAction
}

type recordedAction struct {
	verb        string
	resource    string
	subresource string
	objectName  string
}

func newRecordedAction(verb, resource, subresource, objectName string) recordedAction {
	return recordedAction{
		verb:        verb,
		resource:    resource,
		subresource: subresource,
		objectName:  objectName,
	}
}

func TestReconcile(t *testing.T) {
	tests := []struct {
		name                         string
		secretProviderClassToProcess *secretsstorecsiv1.SecretProviderClass
		secretSyncToProcess          *secretsyncv1alpha1.SecretSync
		secret                       *corev1.Secret
		expectedErrorString          string
		expectedConditions           []metav1.Condition
		expectedActions              []recordedAction
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
			expectedConditions: []metav1.Condition{
				{
					Type:    "SecretUpdated",
					Status:  metav1.ConditionTrue,
					Reason:  ConditionReasonSecretUpToDate,
					Message: "Secret contains last observed values.",
				},
			},
			expectedActions: []recordedAction{
				newRecordedAction("update", "secretsyncs", "status", "sse2esecret"),
				newRecordedAction("patch", "secrets", "", "sse2esecret"),
				newRecordedAction("update", "secretsyncs", "status", "sse2esecret"),
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
			secretSyncToProcess: nil,
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "sse2esecret",
					Namespace: "default",
				},
				Data: map[string][]byte{
					"foo": []byte("bar"),
				},
			},
			expectedActions: []recordedAction{},
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
			expectedConditions: []metav1.Condition{
				{
					Type:    "SecretUpdated",
					Status:  metav1.ConditionFalse,
					Reason:  "InvalidClusterSecretLabelError",
					Message: "label secrets-store.sync.x-k8s.io is reserved for use by the Secrets Store Sync Controller",
				},
			},
			expectedActions: []recordedAction{
				newRecordedAction("update", "secretsyncs", "status", "sse2esecret"),
				newRecordedAction("update", "secretsyncs", "status", "sse2esecret"),
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
			expectedConditions: []metav1.Condition{
				{
					Type:    "SecretUpdated",
					Status:  metav1.ConditionFalse,
					Reason:  "InvalidClusterSecretAnnotationError",
					Message: "annotation secrets-store.sync.x-k8s.io is reserved for use by the Secrets Store Sync Controller",
				},
			},
			expectedActions: []recordedAction{
				newRecordedAction("update", "secretsyncs", "status", "sse2esecret"),
				newRecordedAction("update", "secretsyncs", "status", "sse2esecret"),
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
			expectedErrorString: `secretproviderclass.secrets-store.csi.x-k8s.io "test-spc" not found`,
			expectedConditions: []metav1.Condition{
				{
					Type:    "SecretUpdated",
					Status:  metav1.ConditionFalse,
					Reason:  "SecretProviderClassMisconfigured",
					Message: `failed to get SecretProviderClass "test-spc": secretproviderclass.secrets-store.csi.x-k8s.io "test-spc" not found`,
				},
			},
			expectedActions: []recordedAction{
				newRecordedAction("update", "secretsyncs", "status", "sse2esecret"),
				newRecordedAction("update", "secretsyncs", "status", "sse2esecret"),
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
			expectedConditions: []metav1.Condition{
				{
					Type:    "SecretUpdated",
					Status:  metav1.ConditionFalse,
					Reason:  "SecretProviderClassMisconfigured",
					Message: `fetching secrets from the provider failed: provider not found: provider "invalid-fake-provider"`,
				},
			},
			expectedActions: []recordedAction{
				newRecordedAction("update", "secretsyncs", "status", "sse2esecret"),
				newRecordedAction("update", "secretsyncs", "status", "sse2esecret"),
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
			expectedConditions: []metav1.Condition{
				{
					Type:    "SecretUpdated",
					Status:  metav1.ConditionFalse,
					Reason:  "RemoteSecretStoreFetchFailed",
					Message: "fetching secrets from the provider failed: target key in secretObject.data is empty",
				},
			},
			expectedActions: []recordedAction{
				newRecordedAction("update", "secretsyncs", "status", "sse2esecret"),
				newRecordedAction("update", "secretsyncs", "status", "sse2esecret"),
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			testCtx, cancel := context.WithCancel(klog.NewContext(context.Background(), klog.NewKlogr()))
			defer cancel()

			testSecretSyncReconciler := newSecretSyncReconciler(t, test.secretProviderClassToProcess, test.secretSyncToProcess, test.secret)

			// Mock request to simulate Reconcile being called
			objRef := cache.ObjectName{
				Name:      "sse2esecret",
				Namespace: "default",
			}

			err := testSecretSyncReconciler.secretSyncReconciler.sync(testCtx, objRef)
			if len(test.expectedErrorString) > 0 {
				if err == nil || err.Error() != test.expectedErrorString {
					t.Fatalf("expected error %q, got %q", test.expectedErrorString, err)
				}
			} else if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			actions := testSecretSyncReconciler.consumeRecordedActions()
			if !slices.Equal(actions, test.expectedActions) {
				t.Fatalf("unexpected client actions: expected %v, got %v", test.expectedActions, actions)
			}

			// validate status condition
			ss := getSecretSyncObject(t, testSecretSyncReconciler.secretSyncReconciler, objRef)
			if gotConditions := ss.Status.Conditions; !compareConditionsWithoutTransitionTime(gotConditions, test.expectedConditions) {
				t.Fatalf("expected conditions %v, got %v", test.expectedConditions, gotConditions)
			}
		})
	}
}

func TestConditionsOnHashChange(t *testing.T) {
	testCtx, cancel := context.WithCancel(klog.NewContext(context.Background(), klog.NewKlogr()))
	defer cancel()

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
		Status: secretsyncv1alpha1.SecretSyncStatus{
			SyncHash:               "just-a-hash",
			LastSuccessfulSyncTime: &metav1.Time{Time: time.Now()},
			Conditions: []metav1.Condition{
				{
					Type:    ConditionTypeUpdate,
					Status:  metav1.ConditionFalse,
					Reason:  ConditionReasonControllerPatchError,
					Message: "Error",
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

	testSecretSyncReconciler := newSecretSyncReconciler(t, secretProviderClassToProcess, secretSyncToProcess, secret)

	objRef := cache.ObjectName{
		Name:      "sse2esecret",
		Namespace: "default",
	}
	err := testSecretSyncReconciler.secretSyncReconciler.sync(testCtx, objRef)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	actions := testSecretSyncReconciler.consumeRecordedActions()
	if !slices.Equal(actions, []recordedAction{
		newRecordedAction("patch", "secrets", "", "sse2esecret"),
		newRecordedAction("update", "secretsyncs", "status", "sse2esecret"),
	}) {
		t.Fatalf("unexpected actions on first sync: got %v", actions)
	}

	// simulate update with no secret value change
	err = testSecretSyncReconciler.secretSyncReconciler.sync(testCtx, objRef)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	actions = testSecretSyncReconciler.consumeRecordedActions()
	if !slices.Equal(actions, []recordedAction{
		newRecordedAction("patch", "secrets", "", "sse2esecret"),
		newRecordedAction("update", "secretsyncs", "status", "sse2esecret"),
	}) {
		t.Fatalf("unexpected actions on second sync: got %v", actions)
	}
	expectedConditions := []metav1.Condition{
		{
			Type:    "SecretUpdated",
			Status:  "True",
			Reason:  "SecretUpToDate",
			Message: "Secret contains last observed values.",
		},
	}
	ss := getSecretSyncObject(t, testSecretSyncReconciler.secretSyncReconciler, objRef)
	oldHash := ss.Status.SyncHash
	oldUpdateTime := ss.Status.LastSuccessfulSyncTime
	if gotConditions := ss.Status.Conditions; !compareConditionsWithoutTransitionTime(gotConditions, expectedConditions) {
		t.Fatalf("expected condition %v, got %v", expectedConditions, gotConditions)
	}

	// simulate update with secret value change
	testSecretSyncReconciler.fakeProviderServer.SetFiles([]*v1alpha1.File{
		{
			Path:     "foo",
			Mode:     0644,
			Contents: []byte("bar"),
		},
	})

	// Sleep so that we can observe LastTransitionTime change in LastSuccessfulSyncTime
	time.Sleep(1 * time.Second)
	err = testSecretSyncReconciler.secretSyncReconciler.sync(testCtx, objRef)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	actions = testSecretSyncReconciler.consumeRecordedActions()
	if !slices.Equal(actions, []recordedAction{
		newRecordedAction("get", "secretsyncs", "", "sse2esecret"),
		newRecordedAction("patch", "secrets", "", "sse2esecret"),
		newRecordedAction("update", "secretsyncs", "status", "sse2esecret"),
	}) {
		t.Fatalf("unexpected actions on third sync: got %v", actions)
	}
	expectedConditionAsfterSecretChange := []metav1.Condition{
		{
			Type:    "SecretUpdated",
			Status:  metav1.ConditionTrue,
			Reason:  "SecretUpToDate",
			Message: "Secret contains last observed values.",
		},
	}
	ssChanged := getSecretSyncObject(t, testSecretSyncReconciler.secretSyncReconciler, objRef)
	if gotConditions := ssChanged.Status.Conditions; !compareConditionsWithoutTransitionTime(gotConditions, expectedConditionAsfterSecretChange) {
		t.Fatalf("expected condition %v, got %v", expectedConditionAsfterSecretChange, gotConditions)
	}

	if newHash := ssChanged.Status.SyncHash; newHash == oldHash {
		t.Error("expected SyncHashes to change on provider secrets change")
	}

	newUpdateTime := ssChanged.Status.LastSuccessfulSyncTime
	if !oldUpdateTime.Before(newUpdateTime) {
		t.Errorf("expected old update condition LastTransitionTime (%v) to be before new update condition LastTransitionTime (%v)", oldUpdateTime, newUpdateTime)
	}
}

func getSecretSyncObject(t *testing.T, ssc *SecretSyncReconciler, objRef cache.ObjectName) *secretsyncv1alpha1.SecretSync {
	t.Helper()

	secretSync, err := ssc.ssClient.SecretSyncs(objRef.Namespace).Get(context.TODO(), objRef.Name, metav1.GetOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		t.Fatalf("error getting secret sync: %v", err)
	}

	return secretSync
}

func newSecretSyncReconciler(
	t *testing.T,
	spc *secretsstorecsiv1.SecretProviderClass,
	secretSync *secretsyncv1alpha1.SecretSync,
	testSecret *corev1.Secret,
) *testSecretSyncReconciler {
	t.Helper()

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

	secretSyncs := []runtime.Object{}
	if secretSync != nil {
		secretSyncs = append(secretSyncs, secretSync)
	}

	// Create a fake client to mock API calls
	fakeClient := fakeclient.NewClientset(testSecret)
	fakeSecretSyncs := ssfake.NewSimpleClientset(secretSyncs...)

	ssCache := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
	if secretSync != nil {
		if err := ssCache.Add(secretSync); err != nil {
			t.Fatalf("unable to add secretsync object to the cache: %v", err)
		}
	}

	spcCache := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
	if spc != nil {
		if err := spcCache.Add(spc); err != nil {
			t.Fatalf("unable to add secretproviderclass object to cache: %v", err)
		}
	}

	ssc := &SecretSyncReconciler{
		clients:  fakeClient,
		ssClient: fakeSecretSyncs.SecretSyncV1alpha1(),

		ssLister:                  secretsynclister.NewSecretSyncLister(ssCache),
		ssSynced:                  func() bool { return true },
		secretProviderClassLister: csilisters.NewSecretProviderClassLister(spcCache),
		secretProviderClassSynced: func() bool { return true },

		tokenCache:      token.NewManager(fakeClient),
		providerClients: provider.NewPluginClientBuilder([]string{socketPath}),
	}

	testReconciler := &testSecretSyncReconciler{
		fakeProviderServer:   server,
		secretSyncReconciler: ssc,
	}

	fakeClient.PrependReactor("*", "*", func(action k8stesting.Action) (bool, runtime.Object, error) {
		testReconciler.actions = append(testReconciler.actions, recordedAction{
			verb:        action.GetVerb(),
			resource:    action.GetResource().Resource,
			subresource: action.GetSubresource(),
			objectName:  getObjectName(action),
		})
		return false, nil, nil
	})

	fakeSecretSyncs.PrependReactor("*", "*", func(action k8stesting.Action) (bool, runtime.Object, error) {
		testReconciler.actions = append(testReconciler.actions, recordedAction{
			verb:        action.GetVerb(),
			resource:    action.GetResource().Resource,
			subresource: action.GetSubresource(),
			objectName:  getObjectName(action),
		})
		return false, nil, nil
	})

	return testReconciler
}

func (r *testSecretSyncReconciler) consumeRecordedActions() []recordedAction {
	actions := append([]recordedAction(nil), r.actions...)
	r.actions = nil

	return actions
}

func getObjectName(action k8stesting.Action) string {
	switch a := action.(type) {
	case k8stesting.PatchAction:
		return a.GetName()
	case k8stesting.DeleteAction:
		return a.GetName()
	case k8stesting.GetAction:
		return a.GetName()
	case k8stesting.CreateAction:
		return objectNameFromRuntimeObject(a.GetObject())
	default:
		return ""
	}
}

func objectNameFromRuntimeObject(obj runtime.Object) string {
	accessor, err := meta.Accessor(obj)
	if err != nil {
		return ""
	}

	return accessor.GetName()
}

func compareConditionsWithoutTransitionTime(a, b []metav1.Condition) bool {
	if len(a) != len(b) {
		return false
	}

	for i := range a {
		condA := a[i].DeepCopy()
		condB := b[i].DeepCopy()

		condA.LastTransitionTime = condB.LastTransitionTime
		if !reflect.DeepEqual(condA, condB) {
			return false
		}
	}

	return true
}
