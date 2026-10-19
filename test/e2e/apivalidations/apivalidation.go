package apivalidations

import (
	"context"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	e2elib "sigs.k8s.io/secrets-store-sync-controller/test/e2e/library"
	"sigs.k8s.io/secrets-store-sync-controller/test/e2e/testdata"
)

const (
	testNameAnnotationKey = "e2e.test-name"
	testExpectedErrorKey  = "e2e.test-expected-errors"
)

func TestAPIValidation(t *testing.T, f *e2elib.Framework) {
	tests := testdata.GetAPIValidationTestsOrDie()

	for _, tc := range tests {
		f.RunTest(t, tc.Annotations[testNameAnnotationKey], func(t *testing.T, testCfg *e2elib.TestConfig) {
			testCtx, cancel := context.WithTimeout(testCfg.Context, 5*time.Second)
			defer cancel()

			var expectedErrors []string
			if errAnnotations, ok := tc.Annotations[testExpectedErrorKey]; ok {
				expectedErrors = strings.Split(errAnnotations, "\n")
			}
			_, err := testCfg.Clients.SSClient().SecretSyncV1alpha1().SecretSyncs(testCfg.Namespace).Create(testCtx, tc, metav1.CreateOptions{})
			if len(expectedErrors) != 0 && err == nil {
				t.Fatalf("expected error but it did not occur: %s", expectedErrors)
			}

			if len(expectedErrors) == 0 && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			for _, expectedError := range expectedErrors {
				if len(expectedError) == 0 {
					continue
				}

				if !strings.Contains(err.Error(), expectedError) {
					t.Fatalf("expected: %q, to contain: %v", err.Error(), expectedError)
				}
			}

		})
	}
}
