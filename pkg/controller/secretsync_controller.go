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
	"crypto/hmac"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/pbkdf2"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"

	secretsstorecsiv1 "sigs.k8s.io/secrets-store-csi-driver/apis/v1"
	"sigs.k8s.io/secrets-store-csi-driver/provider/v1alpha1"

	secretsyncv1alpha1 "sigs.k8s.io/secrets-store-sync-controller/api/secretsync/v1alpha1"
	ssclients "sigs.k8s.io/secrets-store-sync-controller/client/clientset/versioned/typed/secretsync/v1alpha1"
	ssinformers "sigs.k8s.io/secrets-store-sync-controller/client/informers/externalversions/secretsync/v1alpha1"
	sslisters "sigs.k8s.io/secrets-store-sync-controller/client/listers/secretsync/v1alpha1"
	"sigs.k8s.io/secrets-store-sync-controller/pkg/provider"
	"sigs.k8s.io/secrets-store-sync-controller/pkg/token"
	"sigs.k8s.io/secrets-store-sync-controller/pkg/util/secretutil"
)

const (
	// csiPodName is the name of the pod that the mount is created for
	csiPodName = "csi.storage.k8s.io/pod.name"

	// csiPodNamespace is the namespace of the pod that the mount is created for
	csiPodNamespace = "csi.storage.k8s.io/pod.namespace"

	// csiPodUID is the UID of the pod that the mount is created for
	csiPodUID = "csi.storage.k8s.io/pod.uid"

	// csiPodServiceAccountName is the name of the pod service account that the mount is created for
	csiPodServiceAccountName = "csi.storage.k8s.io/serviceAccount.name"

	// Label applied by the controller to the secret object
	controllerLabelKey = "secrets-store.sync.x-k8s.io"

	// Annotation applied by the controller to the secret object
	controllerAnnotationKey = "secrets-store.sync.x-k8s.io"

	// secretSyncControllerFieldManager is the field manager used by the secrets store sync controller
	secretSyncControllerFieldManager = "secrets-store-sync-controller"

	// Environment variables set using downward API to pass as params to the controller
	// Used to maintain the same logic as the Secrets Store CSI driver
	syncControllerPodName = "SYNC_CONTROLLER_POD_NAME"
	syncControllerPodUID  = "SYNC_CONTROLLER_POD_UID"

	controllerName = "secret-sync-controller"
)

type AllClientBuilder interface {
	Get(ctx context.Context, provider string) (v1alpha1.CSIDriverProviderClient, error)
}

// SecretSyncReconciler reconciles a SecretSync object
type SecretSyncReconciler struct {
	clients  kubernetes.Interface
	ssClient ssclients.SecretSyncV1alpha1Interface

	ssInformer                ssinformers.SecretSyncInformer
	ssLister                  sslisters.SecretSyncLister
	ssSynced                  func() bool
	secretProviderClassLister cache.GenericLister
	secretProviderClassSynced func() bool

	audiences       []string
	tokenCache      *token.Manager
	providerClients AllClientBuilder

	workqueue workqueue.TypedRateLimitingInterface[cache.ObjectName]
}

func NewSecretSyncReconciler(
	ctx context.Context,
	kubeClient kubernetes.Interface,
	dynamicInformers dynamicinformer.DynamicSharedInformerFactory,
	secretSyncClient ssclients.SecretSyncV1alpha1Interface,
	secretSyncInformer ssinformers.SecretSyncInformer,
	providerClients AllClientBuilder,
	saTokenAudiences []string,
) (*SecretSyncReconciler, error) {
	logger := klog.FromContext(ctx)

	spcInformer := dynamicInformers.ForResource(secretsstorecsiv1.SchemeGroupVersion.WithResource("secretproviderclasses"))

	c := &SecretSyncReconciler{
		clients:  kubeClient,
		ssClient: secretSyncClient,

		ssInformer: secretSyncInformer,
		ssLister:   secretSyncInformer.Lister(),
		ssSynced:   secretSyncInformer.Informer().HasSynced,

		secretProviderClassLister: spcInformer.Lister(),
		secretProviderClassSynced: spcInformer.Informer().HasSynced,

		tokenCache: token.NewManager(kubeClient),
		audiences:  saTokenAudiences,

		providerClients: providerClients,

		workqueue: workqueue.NewTypedRateLimitingQueueWithConfig(
			workqueue.DefaultTypedControllerRateLimiter[cache.ObjectName](),
			workqueue.TypedRateLimitingQueueConfig[cache.ObjectName]{
				Name: controllerName,
			},
		),
	}

	logger.Info("Setting up event handlers")
	// TODO: should we handle SPC changes?
	if _, err := c.ssInformer.Informer().AddEventHandler(
		cache.ResourceEventHandlerFuncs{
			AddFunc: c.enqueue,
			UpdateFunc: func(oldObj, newObj any) {
				ssOldObj := oldObj.(*secretsyncv1alpha1.SecretSync)
				ssNewObj := newObj.(*secretsyncv1alpha1.SecretSync)

				if ssOldObj.Generation != ssNewObj.Generation {
					c.enqueue(ssNewObj)
				}
			},
		},
	); err != nil {
		return nil, err
	}

	return c, nil
}

func (r *SecretSyncReconciler) enqueue(obj any) {
	objRef, err := cache.ObjectToName(obj)
	if err != nil {
		utilruntime.HandleError(err)
		return
	}
	r.workqueue.Add(objRef)
}

func (r *SecretSyncReconciler) Run(ctx context.Context, workers int) error {
	defer utilruntime.HandleCrash()
	defer r.workqueue.ShutDown()
	logger := klog.FromContext(ctx)

	// Start the informer factories to begin populating the informer caches
	logger.Info("Starting Foo controller")

	// Wait for the caches to be synced before starting workers
	logger.Info("Waiting for informer caches to sync")

	if ok := cache.WaitForCacheSync(ctx.Done(), r.ssSynced, r.secretProviderClassSynced); !ok {
		return fmt.Errorf("failed to wait for caches to sync")
	}

	logger.Info("Starting workers", "count", workers)
	// Launch two workers to process Foo resources
	for i := 0; i < workers; i++ {
		go wait.UntilWithContext(ctx, r.runWorker, time.Second)
	}

	logger.Info("Started workers")
	<-ctx.Done()
	logger.Info("Shutting down workers")

	return nil
}

func (r *SecretSyncReconciler) runWorker(ctx context.Context) {
	for r.processNextWorkItem(ctx) {
	}
}

func (r *SecretSyncReconciler) processNextWorkItem(ctx context.Context) bool {
	objRef, shutdown := r.workqueue.Get()
	logger := klog.FromContext(ctx)

	if shutdown {
		return false
	}
	defer r.workqueue.Done(objRef)

	err := r.sync(ctx, objRef)
	if err == nil {
		r.workqueue.Forget(objRef)
		logger.Info("Successfully synced", "objectName", objRef)
		return true
	}

	// There was an error handling the object, log it and requeue with backoff
	utilruntime.HandleErrorWithContext(ctx, err, "Error syncing; requeuing for later retry", "objectReference", objRef)
	r.workqueue.AddRateLimited(objRef)
	return true
}

func (r *SecretSyncReconciler) sync(ctx context.Context, objRef cache.ObjectName) error {
	logger := klog.FromContext(ctx)
	logger.Info("Reconciling SecretSync", "namespace", objRef.Namespace, "name", objRef.Name)

	var err error
	var ss *secretsyncv1alpha1.SecretSync
	if ss, err = r.ssLister.SecretSyncs(objRef.Namespace).Get(objRef.Name); err != nil {
		logger.Error(err, "unable to fetch SecretSync")
		return err
	}

	// if the secret sync hash is empty, it means the secret does not exist, so the condition type is create
	// otherwise, the condition type is update
	conditionType := ConditionTypeUpdate
	if len(ss.Status.SyncHash) == 0 {
		conditionType = ConditionTypeCreate
	}

	ssCopy := ss.DeepCopy()
	if len(ss.Status.Conditions) < 2 {
		if err := r.initConditions(ctx, ssCopy); err != nil {
			logger.Error(err, "failed to initialize SecretSync object conditions", "namespace", ss.Namespace, "name", ss.Name)
			return err
		}
	}

	secretName := strings.TrimSpace(ssCopy.Name)
	secretObj := ssCopy.Spec.SecretObject

	reason, err := r.validateLabelsAnnotations(secretObj)
	if err != nil {
		r.updateStatusConditions(ctx, ssCopy, conditionType, metav1.ConditionFalse, reason, err.Error(), true)
		return err
	}

	spc, err := getSecretProviderClassFromCache(logger, r.secretProviderClassLister, objRef.Namespace, ss.Spec.SecretProviderClassName)
	if err != nil {
		r.updateStatusConditions(ctx, ssCopy, conditionType, metav1.ConditionFalse, ConditionReasonControllerSpcError, fmt.Sprintf("failed to get SecretProviderClass %q: %v", ss.Spec.SecretProviderClassName, err), true)
		return err
	}

	datamap, reason, err := r.fetchSecretsFromProvider(ctx, logger, spc, ss)
	if err != nil {
		r.updateStatusConditions(ctx, ssCopy, conditionType, metav1.ConditionFalse, reason, fmt.Sprintf("fetching secrets from the provider failed: %v", err), true)
		return err
	}

	// Compute the hash of the secret
	syncHash, err := computeCurrentStateHash(datamap, spc, ss)
	if err != nil {
		logger.Error(err, "failed to compute state hash", "secretName", secretName) // TODO: could this leak secrets?
		r.updateStatusConditions(ctx, ssCopy, conditionType, metav1.ConditionFalse, ConditionReasonControllerSyncError, "failed to compute state hash", true)
		return err
	}

	// Check if the hash has changed.
	hashChanged := syncHash != ss.Status.SyncHash

	// Check if a secret create or update failed and if the controller should re-try the operation
	var failedCondition *metav1.Condition
	for _, ssCondition := range ss.Status.Conditions {
		if slices.Contains(FailedConditionsTriggeringRetry, ssCondition.Reason) {
			failedCondition = &ssCondition
			break
		}
	}

	if failedCondition == nil && !hashChanged {
		return nil
	}

	if conditionType == ConditionTypeCreate {
		r.updateStatusConditions(ctx, ssCopy, conditionType, metav1.ConditionTrue, ConditionReasonCreateSuccessful, ConditionMessageCreateSuccessful, false)
		r.updateStatusConditions(ctx, ssCopy, ConditionTypeUpdate, metav1.ConditionTrue, ConditionReasonSecretUpToDate, ConditionMessageUpdateSuccessful, false)
	} else if hashChanged {
		r.updateStatusConditions(ctx, ssCopy, conditionType, metav1.ConditionTrue, ConditionReasonSecretUpToDate, ConditionMessageUpdateSuccessful, false)
	}

	// Save current state for potential rollback.
	prevSecretHash := ssCopy.Status.SyncHash
	prevTime := ssCopy.Status.LastSuccessfulSyncTime

	// Update status fields.
	ssCopy.Status.LastSuccessfulSyncTime = &metav1.Time{Time: time.Now()}
	ssCopy.Status.SyncHash = syncHash

	// Attempt to create or update the secret.
	if err = r.serverSidePatchSecret(ctx, ssCopy, datamap); err != nil {
		logger.Error(err, "failed to patch secret", "secretName", secretName)

		// Rollback to the previous hash and the previous last successful sync time.
		ssCopy.Status.SyncHash = prevSecretHash
		ssCopy.Status.LastSuccessfulSyncTime = prevTime

		r.updateStatusConditions(ctx, ssCopy, conditionType, metav1.ConditionFalse, ConditionReasonControllerPatchError, fmt.Sprintf("failed to patch secret %q: %v", ssCopy.Name, err), true)
		return err
	}

	// Update the status.
	_, err = r.ssClient.SecretSyncs(objRef.Namespace).UpdateStatus(ctx, ssCopy, metav1.UpdateOptions{})
	if err != nil {
		return err
	}

	logger.V(4).Info("Done... updated status", "syncHash", syncHash, "lastSuccessfulSyncTime", ssCopy.Status.LastSuccessfulSyncTime)
	return nil
}

func (r *SecretSyncReconciler) validateLabelsAnnotations(
	secretObj secretsyncv1alpha1.SecretObject,
) (string, error) {
	if val, ok := secretObj.Labels[controllerLabelKey]; ok && len(val) > 0 {
		return ConditionReasonFailedInvalidLabelError, fmt.Errorf("label %s is reserved for use by the Secrets Store Sync Controller", controllerLabelKey)
	}

	if _, ok := secretObj.Annotations[controllerAnnotationKey]; ok {
		return ConditionReasonFailedInvalidAnnotationError, fmt.Errorf("annotation %s is reserved for use by the Secrets Store Sync Controller", controllerAnnotationKey)
	}

	return "", nil
}

func (r *SecretSyncReconciler) fetchSecretsFromProvider(
	ctx context.Context,
	logger klog.Logger,
	spc *secretsstorecsiv1.SecretProviderClass,
	ss *secretsyncv1alpha1.SecretSync,
) (map[string][]byte, string, error) {
	providerName := string(spc.Spec.Provider)
	providerClient, err := r.providerClients.Get(ctx, providerName)
	if err != nil {
		logger.Error(err, "failed to get provider client", "provider", providerName)
		return nil, ConditionReasonControllerSpcError, err
	}

	paramsJSON, reason, err := r.prepareCSIProviderParams(logger, spc, ss.Namespace, ss.Spec.ServiceAccountName)
	if err != nil {
		return nil, reason, err
	}

	secretRefData := make(map[string]string)
	var secretsJSON []byte
	secretsJSON, err = json.Marshal(secretRefData)
	if err != nil {
		logger.Error(err, "failed to marshal secret")
		return nil, ConditionReasonControllerSyncError, err
	}

	oldObjectVersions := make(map[string]string)
	_, files, err := provider.MountContent(ctx, providerClient, string(paramsJSON), string(secretsJSON), oldObjectVersions)
	if err != nil {
		logger.Error(err, "failed to get secrets from provider", "provider", providerName)
		return nil, ConditionReasonFailedProviderError, err
	}

	secretObj := ss.Spec.SecretObject
	secretType := corev1.SecretType(secretObj.Type)
	var datamap map[string][]byte
	if datamap, err = secretutil.GetSecretData(secretObj.Data, secretType, files); err != nil {
		logger.Error(err, "failed to get secret data", "secretName", ss.Name)
		return nil, ConditionReasonRemoteSecretStoreFetchFailed, err
	}

	return datamap, "", nil
}

// prepareCSIProviderPerams prepares the parameters that would normally be sent to
// the provider by the CSI driver.
// This function will attempt to fetch SA token unless it is cached.
//
// Returns JSON-serialized parameters, condition reason in case of an error, and the error itself.
func (r *SecretSyncReconciler) prepareCSIProviderParams(
	logger klog.Logger,
	spc *secretsstorecsiv1.SecretProviderClass,
	namespace,
	saName string,
) ([]byte, string, error) {
	// get the service account token
	serviceAccountTokenAttrs, err := token.SecretProviderServiceAccountTokenAttrs(r.tokenCache, namespace, saName, r.audiences)
	if err != nil {
		logger.Error(err, "failed to get service account token", "name", saName)

		return nil, ConditionReasonControllerSyncError, err
	}

	// this is to mimic the parameters sent from CSI driver to the provider
	parameters := maps.Clone(spc.Spec.Parameters)

	parameters[csiPodName] = os.Getenv(syncControllerPodName)
	parameters[csiPodUID] = os.Getenv(syncControllerPodUID)
	parameters[csiPodNamespace] = namespace
	parameters[csiPodServiceAccountName] = saName

	maps.Copy(parameters, serviceAccountTokenAttrs)

	paramsJSON, err := json.Marshal(parameters)
	if err != nil {
		logger.Error(err, "failed to marshal parameters")
		return nil, ConditionReasonControllerSyncError, err
	}

	return paramsJSON, "", nil
}

// serverSidePatchSecret performs a server-side patch on a Kubernetes Secret.
// It updates the specified secret with the provided data, labels, and annotations.
func (r *SecretSyncReconciler) serverSidePatchSecret(ctx context.Context, ss *secretsyncv1alpha1.SecretSync, datamap map[string][]byte) (err error) {
	controllerLabels := maps.Clone(ss.Spec.SecretObject.Labels)
	if controllerLabels == nil {
		controllerLabels = make(map[string]string, 1)
	}
	controllerLabels[controllerLabelKey] = ""

	// Construct the patch for updating the Secret.
	secretPatchData := corev1.Secret{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Secret",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:        ss.Name,
			Namespace:   ss.Namespace,
			Labels:      controllerLabels,
			Annotations: ss.Spec.SecretObject.Annotations,
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: secretsyncv1alpha1.SchemeGroupVersion.String(),
					Kind:       "SecretSync",
					Name:       ss.Name,
					UID:        ss.UID,
				},
			},
		},
		Data: datamap,
		Type: corev1.SecretType(ss.Spec.SecretObject.Type),
	}

	patchData, err := json.Marshal(secretPatchData)
	if err != nil {
		return err
	}

	// Perform the server-side patch on the Secret.
	_, err = r.clients.CoreV1().Secrets(secretPatchData.Namespace).Patch(ctx, secretPatchData.Name, types.ApplyPatchType, patchData, metav1.PatchOptions{FieldManager: secretSyncControllerFieldManager})
	if err != nil {
		return err
	}

	return nil
}

// computeSecretDataObjectHash computes the HMAC hash of the provided secret data
// using the SS UID as the key.
func computeCurrentStateHash(secretData map[string][]byte, spc *secretsstorecsiv1.SecretProviderClass, ss *secretsyncv1alpha1.SecretSync) (string, error) {
	// Serialize the secret data, parts of the spc and the ss data.
	secretBytes, err := json.Marshal(secretData)
	if err != nil {
		return "", err
	}

	toHash := strings.Join(
		[]string{
			// SecretProviderClass bits
			string(spc.UID),
			strconv.FormatInt(spc.ObjectMeta.Generation, 10),
			// SecretSync bits
			string(ss.UID),
			strconv.FormatInt(ss.ObjectMeta.Generation, 10),
			ss.Spec.ForceSynchronization,
		},
		"|",
	)

	salt := []byte(string(ss.UID))
	dk := pbkdf2.Key(append(secretBytes, []byte(toHash)...), salt, 100_000, 32, sha512.New)

	// Create a new HMAC instance with SHA-56 as the hash type and the pbkdf2 key.
	hmac := hmac.New(sha512.New, dk)

	_, err = hmac.Write(dk)
	if err != nil {
		return "", err
	}

	// Get the final HMAC hash in hexadecimal format.
	dataHmac := hmac.Sum(nil)
	hmacHex := hex.EncodeToString(dataHmac)

	return hmacHex, nil
}

func getSecretProviderClassFromCache(logger klog.Logger, lister cache.GenericLister, namespace, name string) (*secretsstorecsiv1.SecretProviderClass, error) {
	var spcObject runtime.Object
	var err error
	if spcObject, err = lister.ByNamespace(namespace).Get(name); err != nil {
		return nil, err
	}

	spcUnstructured, ok := spcObject.(*unstructured.Unstructured)
	if !ok {
		err := fmt.Errorf("%T is not *unstructured.Unstructured", spcObject)
		logger.Error(err, "type-assertion failed")
		return nil, err
	}

	spc := &secretsstorecsiv1.SecretProviderClass{}
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(spcUnstructured.Object, spc); err != nil {
		logger.Error(err, "failed to convert unstructured data to SecretProviderClass object", "name", name, "namespace", namespace)
		return nil, err
	}
	return spc, nil
}
