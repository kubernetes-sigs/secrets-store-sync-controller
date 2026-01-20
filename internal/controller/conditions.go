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

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/log"

	secretsyncv1alpha1 "sigs.k8s.io/secrets-store-sync-controller/api/v1alpha1"
)

const (
	ConditionTypeCreate = "SecretCreated"
	ConditionTypeUpdate = "SecretUpdated"

	ConditionReasonFailedProviderError          = "ProviderError"
	ConditionReasonFailedInvalidLabelError      = "InvalidClusterSecretLabelError"
	ConditionReasonFailedInvalidAnnotationError = "InvalidClusterSecretAnnotationError"
	ConditionReasonControllerSyncError          = "ControllerSyncError"
	ConditionReasonControllerPatchError         = "ControllerPatchError"
	ConditionReasonControllerSpcError           = "SecretProviderClassMisconfigured"
	ConditionReasonRemoteSecretStoreFetchFailed = "RemoteSecretStoreFetchFailed"

	ConditionReasonSyncStarting         = "SyncStarting"
	ConditionReasonNoUpdateAttemptedYet = "NoUpdatesAttemptedYet"

	ConditionReasonSecretUpToDate   = "SecretUpToDate"
	ConditionReasonCreateSuccessful = "CreateSuccessful"

	ConditionMessageCreateSuccessful = "Secret created successfully."
	ConditionMessageUpdateSuccessful = "Secret contains last observed values."
)

var FailedConditionsTriggeringRetry = []string{ // FIXME: should be a set
	ConditionReasonControllerSpcError,
	ConditionReasonFailedInvalidAnnotationError,
	ConditionReasonFailedInvalidLabelError,
	ConditionReasonFailedProviderError,
	ConditionReasonFailedInvalidAnnotationError,
	ConditionReasonFailedProviderError,
	ConditionReasonRemoteSecretStoreFetchFailed,
	ConditionReasonControllerPatchError,
	ConditionReasonControllerSyncError,
}

var SuccessfulConditionsTriggeringRetry = []string{
	ConditionReasonCreateSuccessful,
	ConditionReasonSecretUpToDate}

var AllowedStringsToDisplayConditionErrorMessage = []string{
	"validatingadmissionpolicy",
}

func (r *SecretSyncReconciler) updateStatusConditions(ctx context.Context, ss *secretsyncv1alpha1.SecretSync, conditionType string, conditionStatus metav1.ConditionStatus, conditionReason, conditionMessage string, shouldUpdateStatus bool) {
	logger := log.FromContext(ctx)

	if ss.Status.Conditions == nil {
		ss.Status.Conditions = []metav1.Condition{}
	}

	condition := metav1.Condition{
		Type:    conditionType,
		Status:  conditionStatus,
		Reason:  conditionReason,
		Message: conditionMessage,
	}

	logger.V(10).Info("Adding new condition", "newConditionType", conditionType, "conditionReason", conditionReason)
	meta.SetStatusCondition(&ss.Status.Conditions, condition)

	if !shouldUpdateStatus {
		return
	}

	if err := r.Client.Status().Update(ctx, ss); err != nil {
		logger.Error(err, "Failed to update status", "condition", condition)
	}

	logger.V(10).Info("Updated status", "condition", condition)
}

func (r *SecretSyncReconciler) initConditions(ctx context.Context, ss *secretsyncv1alpha1.SecretSync) error {
	if ss.Status.Conditions == nil {
		ss.Status.Conditions = []metav1.Condition{}
	}

	meta.SetStatusCondition(&ss.Status.Conditions, metav1.Condition{
		Type:   ConditionTypeCreate,
		Status: metav1.ConditionUnknown,
		Reason: ConditionReasonSyncStarting,
	})

	meta.SetStatusCondition(&ss.Status.Conditions, metav1.Condition{
		Type:   ConditionTypeUpdate,
		Status: metav1.ConditionUnknown,
		Reason: ConditionReasonNoUpdateAttemptedYet,
	})

	return r.Client.Status().Update(ctx, ss)
}
