package test

import (
	"testing"

	"sigs.k8s.io/secrets-store-sync-controller/test/e2e/apivalidations"
	"sigs.k8s.io/secrets-store-sync-controller/test/e2e/controller"
	e2elib "sigs.k8s.io/secrets-store-sync-controller/test/e2e/library"
)

var tests = map[string]func(t *testing.T, f *e2elib.Framework){
	"SecretSyncsFailures": controller.TestSecretSyncsFailures,
	"SecretSyncSuccess":   controller.TestSecretSyncSuccess,
	"ControllerResync":    controller.TestControllerResync,
	"APIValidation":       apivalidations.TestAPIValidation,
}

func Test(t *testing.T) {
	f := e2elib.NewFramework(t)

	for testName, testRunner := range tests {
		t.Run(testName, func(t *testing.T) {
			testRunner(t, f)
		})
	}
}
