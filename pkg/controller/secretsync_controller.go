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
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/pbkdf2"
	"golang.org/x/time/rate"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"

	secretsstorecsiv1 "sigs.k8s.io/secrets-store-csi-driver/apis/v1"
	csiinformers "sigs.k8s.io/secrets-store-csi-driver/pkg/client/informers/externalversions/apis/v1"
	csilisters "sigs.k8s.io/secrets-store-csi-driver/pkg/client/listers/apis/v1"
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

	ssLister                  sslisters.SecretSyncLister
	ssSynced                  cache.InformerSynced
	secretProviderClassLister csilisters.SecretProviderClassLister
	secretProviderClassSynced cache.InformerSynced

	audiences       []string
	tokenCache      *token.Manager
	providerClients AllClientBuilder

	workqueue workqueue.TypedRateLimitingInterface[cache.ObjectName]
	// rate limiter for the resynced items, prevents workqueue flooding with just resync
	// items. Chose BucketRateLimiter, we'll handle item error backoff with the workqueue default
	// rate limiter.
	resyncRateLimiter *workqueue.TypedBucketRateLimiter[cache.ObjectName]
}

func NewSecretSyncReconciler(
	kubeClient kubernetes.Interface,
	secretSyncClient ssclients.SecretSyncV1alpha1Interface,
	secretSyncInformer ssinformers.SecretSyncInformer,
	providerClients AllClientBuilder,
	secretProviderClassInformer csiinformers.SecretProviderClassInformer,
	saTokenAudiences []string,
) (*SecretSyncReconciler, error) {
	c := &SecretSyncReconciler{
		clients:  kubeClient,
		ssClient: secretSyncClient,

		ssLister: secretSyncInformer.Lister(),
		ssSynced: secretSyncInformer.Informer().HasSynced,

		secretProviderClassLister: secretProviderClassInformer.Lister(),
		secretProviderClassSynced: secretProviderClassInformer.Informer().HasSynced,

		tokenCache: token.NewManager(kubeClient),
		audiences:  saTokenAudiences,

		providerClients: providerClients,

		workqueue: workqueue.NewTypedRateLimitingQueueWithConfig(
			workqueue.DefaultTypedControllerRateLimiter[cache.ObjectName](),
			workqueue.TypedRateLimitingQueueConfig[cache.ObjectName]{
				Name: controllerName,
			},
		),
		resyncRateLimiter: &workqueue.TypedBucketRateLimiter[cache.ObjectName]{Limiter: rate.NewLimiter(rate.Limit(10), 100)},
	}

	if _, err := secretSyncInformer.Informer().AddEventHandler(
		cache.ResourceEventHandlerFuncs{
			AddFunc: c.enqueue,
			// we're currently using the informer to react to resyncs - rather than
			// reacting to just status changes by comparing obj Generation, we must
			// always add the object to the queue
			UpdateFunc: func(oldObj, newObj any) {
				ssOldObj := oldObj.(*secretsyncv1alpha1.SecretSync)
				ssNewObj := newObj.(*secretsyncv1alpha1.SecretSync)

				if ssOldObj.Generation != ssNewObj.Generation {
					c.enqueue(newObj)
				} else {
					c.enqueueAfter(newObj, c.resyncRateLimiter.When(cache.MetaObjectToName(ssNewObj)))
				}
			},
		},
	); err != nil {
		return nil, err
	}

	return c, nil
}

func (r *SecretSyncReconciler) enqueueAfter(obj any, after time.Duration) {
	enqueue(func(o cache.ObjectName) { r.workqueue.AddAfter(o, after) }, obj)
}

func (r *SecretSyncReconciler) enqueue(obj any) {
	enqueue(r.workqueue.AddRateLimited, obj)
}

func enqueue(addToQueue func(o cache.ObjectName), obj any) {
	objRef, err := cache.ObjectToName(obj)
	if err != nil {
		utilruntime.HandleError(err)
		return
	}
	addToQueue(objRef)
}

func (r *SecretSyncReconciler) Run(ctx context.Context, workers int) error {
	defer utilruntime.HandleCrash()
	defer r.workqueue.ShutDown()
	logger := klog.LoggerWithName(klog.FromContext(ctx), controllerName)
	ctx = klog.NewContext(ctx, logger)

	// Start the informer factories to begin populating the informer caches
	logger.Info("Starting controller")

	// Wait for the caches to be synced before starting workers
	logger.Info("Waiting for informer caches to sync")

	if ok := cache.WaitForCacheSync(ctx.Done(), r.ssSynced, r.secretProviderClassSynced); !ok {
		return fmt.Errorf("failed to wait for caches to sync")
	}

	var wg sync.WaitGroup
	defer func() {
		logger.Info("Shutting down workers")
		r.workqueue.ShutDown()
		wg.Wait()
	}()

	logger.Info("Starting workers", "count", workers)
	// Launch two workers to process Foo resources
	for range workers {
		wg.Go(func() { wait.UntilWithContext(ctx, r.runWorker, time.Second) })
	}

	logger.Info("Started workers")
	<-ctx.Done()

	return nil
}

func (r *SecretSyncReconciler) runWorker(ctx context.Context) {
	for r.processNextWorkItem(ctx) {
	}
}

func (r *SecretSyncReconciler) processNextWorkItem(ctx context.Context) bool {
	objRef, shutdown := r.workqueue.Get()

	if shutdown {
		return false
	}
	defer r.workqueue.Done(objRef)

	logger := klog.FromContext(ctx).WithValues("namespace", objRef.Namespace, "name", objRef.Name)
	ctx = klog.NewContext(ctx, logger)

	err := r.sync(ctx, objRef)
	if err == nil {
		r.workqueue.Forget(objRef)
		// technically unnecessary, bucket rate limiters don't have item-specific rate-limiting, so this is a noop
		r.resyncRateLimiter.Forget(objRef)
		logger.V(4).Info("Successfully synced")
		return true
	}

	// There was an error handling the object, log it and requeue with backoff
	utilruntime.HandleErrorWithContext(ctx, err, "Error syncing; requeuing for later retry")
	r.workqueue.AddRateLimited(objRef)
	return true
}

func (r *SecretSyncReconciler) sync(ctx context.Context, objRef cache.ObjectName) error {
	logger := klog.FromContext(ctx)
	logger.V(4).Info("reconciling SecretSync object")

	var err error
	var ss *secretsyncv1alpha1.SecretSync
	if ss, err = r.ssLister.SecretSyncs(objRef.Namespace).Get(objRef.Name); apierrors.IsNotFound(err) {
		logger.Info("SecretSync not found, ignoring it")
		return nil
	} else if err != nil {
		logger.Error(err, "unable to fetch SecretSync")
		return err
	}

	if len(ss.Status.Conditions) == 0 {
		ss = ss.DeepCopy()
		ss, err = r.initConditions(ctx, ss)
		if err != nil {
			logger.Error(err, "failed to initialize SecretSync object conditions")
			return err
		}
	}

	secretName := ss.Name
	secretObj := ss.Spec.SecretObject

	reason, err := r.validateLabelsAnnotations(secretObj)
	if err != nil {
		if statusUpdateErr := r.updateStatusCondition(ctx, ss, metav1.ConditionFalse, reason, err.Error()); statusUpdateErr != nil {
			logger.Error(statusUpdateErr, "failed to update SecretSync status")
		}
		return err
	}

	spc, err := r.secretProviderClassLister.SecretProviderClasses(objRef.Namespace).Get(ss.Spec.SecretProviderClassName)
	if err != nil { // FIXME: handle not found? -> would have to be able to react to SPC changes
		if statusUpdateErr := r.updateStatusCondition(ctx, ss, metav1.ConditionFalse, ConditionReasonControllerSpcError, fmt.Sprintf("failed to get SecretProviderClass %q: %v", ss.Spec.SecretProviderClassName, err)); statusUpdateErr != nil {
			logger.Error(statusUpdateErr, "failed to update SecretSync status")
		}
		return err
	}

	datamap, reason, err := r.fetchSecretsFromProvider(ctx, logger, spc, ss)
	if err != nil {
		if statusUpdateErr := r.updateStatusCondition(ctx, ss, metav1.ConditionFalse, reason, fmt.Sprintf("fetching secrets from the provider failed: %v", err)); statusUpdateErr != nil {
			logger.Error(statusUpdateErr, "failed to update SecretSync status")
		}
		return err
	}

	// Compute the hash of the secret
	syncHash, err := computeCurrentStateHash(datamap, spc, ss)
	if err != nil {
		logger.Error(err, "failed to compute state hash") // TODO: could this leak secrets?
		if statusUpdateErr := r.updateStatusCondition(ctx, ss, metav1.ConditionFalse, ConditionReasonControllerSyncError, "failed to compute state hash"); statusUpdateErr != nil {
			logger.Error(statusUpdateErr, "failed to update SecretSync status")
		}
		return err
	}

	// Check if the hash has changed.
	hashChanged := syncHash != ss.Status.SyncHash
	if !hashChanged {
		return nil
	}

	ssCopy := ss.DeepCopy()
	// Attempt to create or update the secret.
	if err = r.serverSidePatchSecret(ctx, ssCopy, datamap); err != nil {
		logger.Error(err, "failed to patch secret", "secretName", secretName)
		if statusUpdateErr := r.updateStatusCondition(ctx, ssCopy, metav1.ConditionFalse, ConditionReasonControllerPatchError, fmt.Sprintf("failed to patch secret %q: %v", ssCopy.Name, err)); statusUpdateErr != nil {
			logger.Error(statusUpdateErr, "failed to update SecretSync status")
		}
		return err
	}

	// Update status fields.
	ssCopy.Status.LastSuccessfulSyncTime = &metav1.Time{Time: time.Now()}
	ssCopy.Status.SyncHash = syncHash
	if err := r.updateStatusCondition(ctx, ssCopy, metav1.ConditionTrue, ConditionReasonSecretUpToDate, ConditionMessageUpdateSuccessful); err != nil {
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
	if datamap, err = secretutil.BuildKubeSecretData(secretObj.Data, secretType, files); err != nil {
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
		logger.Error(fmt.Errorf("%T", err), "failed to marshal parameters")
		return nil, ConditionReasonControllerSyncError, fmt.Errorf("failed to marshal parameters: %T", err)
	}

	return paramsJSON, "", nil
}

// serverSidePatchSecret performs a server-side patch on a Kubernetes Secret.
// It updates the specified secret with the provided data, labels, and annotations.
func (r *SecretSyncReconciler) serverSidePatchSecret(ctx context.Context, ssCopy *secretsyncv1alpha1.SecretSync, datamap map[string][]byte) (err error) {
	controllerLabels := ssCopy.Spec.SecretObject.Labels
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
			Name:        ssCopy.Name,
			Namespace:   ssCopy.Namespace,
			Labels:      controllerLabels,
			Annotations: ssCopy.Spec.SecretObject.Annotations,
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: secretsyncv1alpha1.SchemeGroupVersion.String(),
					Kind:       "SecretSync",
					Name:       ssCopy.Name,
					UID:        ssCopy.UID,
				},
			},
		},
		Data: datamap,
		Type: corev1.SecretType(ssCopy.Spec.SecretObject.Type),
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
		return "", fmt.Errorf("failed to marshal data: %T", err)
	}
	// secretBytesLenPrefixed does a length prefix on the secretBytes given it's a
	// user-input base for the hashing below
	secretBytesLenPrefixed := append([]byte(strconv.Itoa(len(secretBytes))+":"), secretBytes...)

	// changes to any of the below inputs (and the above secret data) mean the state
	// changed and we should attempt a new sync
	toHash := strings.Join(
		[]string{
			// SecretProviderClass bits
			string(spc.UID), // SPC UID in case the SPC object got recreated
			strconv.FormatInt(spc.ObjectMeta.Generation, 10), // SPC generation in case the current SPC object's spec changed (ignore status changes)
			// SecretSync bits
			string(ss.UID), // SS UID in case the SS got recreated
			strconv.FormatInt(ss.ObjectMeta.Generation, 10), // SS generation in case the SS spec changed (ignore status changes)
			// ForceSynchronization is the only user input in this group and must therefore always come last
			ss.Spec.ForceSynchronization, // changes to this field should always cause a new attempt to sync the Secret
		},
		"|",
	)

	salt := []byte(string(ss.UID))
	// we need to use key derivation here rather than hashing directly in case the
	// secretBytes had low enthropy -> the rest of the hash input are discoverable
	// and we could leak the secret otherwise.
	dk := pbkdf2.Key(append(secretBytesLenPrefixed, []byte(toHash)...), salt, 100_000, 32, sha512.New)

	return "v1:" + hex.EncodeToString(dk), nil
}
