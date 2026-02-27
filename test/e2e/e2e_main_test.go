package test

import (
	"context"
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
	"APIValidation":                            apivalidations.TestAPIValidation,
}

func Test(t *testing.T) {
	cfg, err := e2elib.NewClientConfig()
	if err != nil {
		t.Fatalf("failed to get kube client config: %v", err)
	}

	f := e2elib.NewFramework(context.Background(), t, cfg)

	for testName, testRunner := range tests {
		t.Run(testName, func(t *testing.T) {
			testRunner(t, f)
		})
	}
}
