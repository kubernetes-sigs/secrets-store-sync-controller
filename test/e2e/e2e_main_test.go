package test

import (
	"testing"

	"sigs.k8s.io/secrets-store-sync-controller/test/e2e/apivalidations"
	"sigs.k8s.io/secrets-store-sync-controller/test/e2e/controller"
	e2elib "sigs.k8s.io/secrets-store-sync-controller/test/e2e/library"
)

var tests = map[string]func(t *testing.T, f *e2elib.Framework){
	"SecretSyncsFailures":                      controller.TestSecretSyncsFailures,
	"SecretSyncSuccess":                        controller.TestSecretSyncSuccess,
	"ControllerResync":                         controller.TestControllerResync,
	"Policies/CreateUpdateSecretsTypes-Create": controller.TestCreateUpdateSecretsTypes_Create,
	"Policies/CreateUpdateSecretsTypes-Patch":  controller.TestCreateUpdateSecretsTypes_Patch,
	"Policies/DeleteSecrets":                   controller.TestDeleteSecrets,
	"Policies/UpdateOwnersCheckOldObject":      controller.TestUpdateOwnersCheckOldObject,
	"Policies/UpdateSecretsBasedOnLabel":       controller.TestUpdateSecretsBasedOnLabel,
	"Policies/ValidateTokenConfig":             controller.TestValidateTokenConfig,
	"APIValidation":                            apivalidations.TestAPIValidation,
}

func Test(t *testing.T) {
	f := e2elib.NewFramework(t)

	for testName, testRunner := range tests {
		t.Run(testName, func(t *testing.T) {
			testRunner(t, f)
		})
	}
}
