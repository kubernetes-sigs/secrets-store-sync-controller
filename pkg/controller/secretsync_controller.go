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

	"github.com/go-logr/logr"
	"golang.org/x/crypto/pbkdf2"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	secretsstorecsiv1 "sigs.k8s.io/secrets-store-csi-driver/apis/v1"
	"sigs.k8s.io/secrets-store-csi-driver/provider/v1alpha1"
	secretsyncv1alpha1 "sigs.k8s.io/secrets-store-sync-controller/api/v1alpha1"
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
)

type AllClientBuilder interface {
	Get(ctx context.Context, provider string) (v1alpha1.CSIDriverProviderClient, error)
}

// SecretSyncReconciler reconciles a SecretSync object
type SecretSyncReconciler struct {
	client          client.Client
	audiences       []string
	clientset       kubernetes.Interface
	scheme          *runtime.Scheme
	tokenCache      *token.Manager
	providerClients AllClientBuilder
	eventRecorder   record.EventRecorder
}

func NewSecretSyncReconciler(
	controlleRuntimeClient client.Client,
	scheme *runtime.Scheme,
	kubeClient kubernetes.Interface,
	providerClients AllClientBuilder,
	saTokenAudiences []string,
) *SecretSyncReconciler {
	return &SecretSyncReconciler{
		client: controlleRuntimeClient,
		scheme: scheme,

		clientset:  kubeClient,
		tokenCache: token.NewManager(kubeClient),
		audiences:  saTokenAudiences,

		providerClients: providerClients,

		eventRecorder: record.NewBroadcaster().NewRecorder(scheme, corev1.EventSource{Component: "secret-sync-controller"}),
	}
}

//+kubebuilder:rbac:groups=secret-sync.x-k8s.io,resources=secretsyncs,verbs=get;list;watch
//+kubebuilder:rbac:groups=secret-sync.x-k8s.io,resources=secretsyncs/status,verbs=get;update;patch
//+kubebuilder:rbac:groups="",resources=secrets,verbs=create;patch
//+kubebuilder:rbac:groups="",resources="serviceaccounts/token",verbs=create
//+kubebuilder:rbac:groups="events.k8s.io",resources=events,verbs=create;patch
//+kubebuilder:rbac:groups=secrets-store.csi.x-k8s.io,resources=secretproviderclasses,verbs=get;list;watch

func (r *SecretSyncReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("Reconciling SecretSync", "namespace", req.NamespacedName.String())

	// get the secret sync object
	ss := &secretsyncv1alpha1.SecretSync{}
	if err := r.client.Get(ctx, req.NamespacedName, ss); err != nil {
		logger.Error(err, "unable to fetch SecretSync")
		return ctrl.Result{}, err
	}

	// if the secret sync hash is empty, it means the secret does not exist, so the condition type is create
	// otherwise, the condition type is update
	conditionType := ConditionTypeUpdate
	if len(ss.Status.SyncHash) == 0 {
		conditionType = ConditionTypeCreate
	}

	if len(ss.Status.Conditions) < 2 {
		if err := r.initConditions(ctx, ss); err != nil {
			logger.Error(err, "failed to initialize SecretSync object conditions", "namespace", ss.Namespace, "name", ss.Name)
			return ctrl.Result{}, err
		}
	}

	secretName := strings.TrimSpace(ss.Name)
	secretObj := ss.Spec.SecretObject

	reason, err := r.validateLabelsAnnotations(secretObj)
	if err != nil {
		r.updateStatusConditions(ctx, ss, conditionType, metav1.ConditionFalse, reason, err.Error(), true)
		return ctrl.Result{}, err
	}

	// get the secret provider class object
	spc := &secretsstorecsiv1.SecretProviderClass{}
	if err := r.client.Get(ctx, client.ObjectKey{Name: ss.Spec.SecretProviderClassName, Namespace: req.Namespace}, spc); err != nil {
		logger.Error(err, "failed to get SecretProviderClass", "name", ss.Spec.SecretProviderClassName)
		r.updateStatusConditions(ctx, ss, conditionType, metav1.ConditionFalse, ConditionReasonControllerSpcError, fmt.Sprintf("failed to get SecretProviderClass %q: %v", ss.Spec.SecretProviderClassName, err), true)
		return ctrl.Result{}, err
	}

	datamap, reason, err := r.fetchSecretsFromProvider(ctx, logger, spc, ss)
	if err != nil {
		r.updateStatusConditions(ctx, ss, conditionType, metav1.ConditionFalse, reason, fmt.Sprintf("fetching secrets from the provider failed: %v", err), true)
		return ctrl.Result{}, err
	}

	// Compute the hash of the secret
	syncHash, err := computeCurrentStateHash(datamap, spc, ss)
	if err != nil {
		logger.Error(err, "failed to compute state hash", "secretName", secretName) // TODO: could this leak secrets?
		r.updateStatusConditions(ctx, ss, conditionType, metav1.ConditionFalse, ConditionReasonControllerSyncError, "failed to compute state hash", true)
		return ctrl.Result{}, err
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
		return ctrl.Result{}, nil
	}

	if conditionType == ConditionTypeCreate {
		r.updateStatusConditions(ctx, ss, conditionType, metav1.ConditionTrue, ConditionReasonCreateSuccessful, ConditionMessageCreateSuccessful, false)
		r.updateStatusConditions(ctx, ss, ConditionTypeUpdate, metav1.ConditionTrue, ConditionReasonSecretUpToDate, ConditionMessageUpdateSuccessful, false)
	} else if hashChanged {
		r.updateStatusConditions(ctx, ss, conditionType, metav1.ConditionTrue, ConditionReasonSecretUpToDate, ConditionMessageUpdateSuccessful, false)
	}

	// Save current state for potential rollback.
	prevSecretHash := ss.Status.SyncHash
	prevTime := ss.Status.LastSuccessfulSyncTime

	// Update status fields.
	ss.Status.LastSuccessfulSyncTime = &metav1.Time{Time: time.Now()}
	ss.Status.SyncHash = syncHash

	// Attempt to create or update the secret.
	if err = r.serverSidePatchSecret(ctx, ss, datamap); err != nil {
		logger.Error(err, "failed to patch secret", "secretName", secretName)

		// Rollback to the previous hash and the previous last successful sync time.
		ss.Status.SyncHash = prevSecretHash
		ss.Status.LastSuccessfulSyncTime = prevTime

		r.updateStatusConditions(ctx, ss, conditionType, metav1.ConditionFalse, ConditionReasonControllerPatchError, fmt.Sprintf("failed to patch secret %q: %v", ss.Name, err), true)
		return ctrl.Result{}, err
	}

	// Update the status.
	err = r.client.Status().Update(ctx, ss)
	if err != nil {
		return ctrl.Result{}, err
	}

	logger.V(4).Info("Done... updated status", "syncHash", syncHash, "lastSuccessfulSyncTime", ss.Status.LastSuccessfulSyncTime)
	return ctrl.Result{}, nil
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
	logger logr.Logger,
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
	logger logr.Logger,
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
					APIVersion: ss.APIVersion,
					Kind:       ss.Kind,
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
	_, err = r.clientset.CoreV1().Secrets(secretPatchData.Namespace).Patch(ctx, secretPatchData.Name, types.ApplyPatchType, patchData, metav1.PatchOptions{FieldManager: secretSyncControllerFieldManager})
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

// processIfSecretChanged checks if the secret sync object has changed.
func (r *SecretSyncReconciler) processIfSecretChanged(oldObj, newObj client.Object) bool {
	ssOldObj := oldObj.(*secretsyncv1alpha1.SecretSync)
	ssNewObj := newObj.(*secretsyncv1alpha1.SecretSync)

	return ssOldObj.Generation != ssNewObj.Generation
}

// We need to trigger the reconcile function when the secret sync object is created or updated, however
// we don't need to trigger the reconcile function when the status of the secret sync object is updated.
func (r *SecretSyncReconciler) shouldReconcilePredicate() predicate.Funcs {
	return predicate.Funcs{
		CreateFunc: func(_ event.CreateEvent) bool {
			return true
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			return r.processIfSecretChanged(e.ObjectOld, e.ObjectNew)
		},
		DeleteFunc: func(_ event.DeleteEvent) bool {
			return false
		},
		GenericFunc: func(_ event.GenericEvent) bool {
			return true
		},
	}
}

// SetupWithManager sets up the controller with the Manager.
func (r *SecretSyncReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&secretsyncv1alpha1.SecretSync{}).
		WithEventFilter(r.shouldReconcilePredicate()).
		Complete(r)
}
