#!/usr/bin/env bats

load helpers

BATS_RESOURCE_MANIFESTS_DIR=hack/localsetup
BATS_RESOURCE_YAML_DIR=test/bats/tests/e2e_provider
WAIT_TIME=60
SLEEP_TIME=1

@test "secretproviderclasses crd is established" {
  kubectl wait --for condition=established --timeout=60s crd/secretproviderclasses.secrets-store.csi.x-k8s.io

  run kubectl get crd/secretproviderclasses.secrets-store.csi.x-k8s.io
  assert_success
}

@test "secretsync crd is established" {
  kubectl wait --for condition=established --timeout=60s crd/secretsyncs.secret-sync.x-k8s.io

  run kubectl get crd/secretsyncs.secret-sync.x-k8s.io
  assert_success
}

@test "Test rbac roles and role bindings exist" {
  run kubectl get clusterrole/secrets-store-sync-controller-manager-role
  assert_success

  run kubectl get clusterrolebinding/secrets-store-sync-controller-manager-rolebinding
  assert_success 
}

@test "[v1alpha1] validate secret creation and deletion with SecretProviderClass and SecretSync" { 
  create_namespace test-v1alpha1
  
  # Create the SPC
  kubectl apply -n test-v1alpha1 -f $BATS_RESOURCE_MANIFESTS_DIR/e2e-providerspc.yaml

  cmd="kubectl get secretproviderclasses.secrets-store.csi.x-k8s.io/e2e-providerspc -n test-v1alpha1 -o yaml | grep e2e-providerspc"
  wait_for_process $WAIT_TIME $SLEEP_TIME "$cmd"

  # Create the SecretSync
  kubectl apply -n test-v1alpha1 -f $BATS_RESOURCE_MANIFESTS_DIR/e2e-secret-sync.yaml

  cmd="kubectl get secretsyncs.secret-sync.x-k8s.io/sse2esecret -n test-v1alpha1 -o yaml | grep sse2esecret"
  wait_for_process $WAIT_TIME $SLEEP_TIME "$cmd"

  # Retrieve the secret 
  cmd="kubectl get secret sse2esecret -n test-v1alpha1 -o yaml | grep 'apiVersion: secret-sync.x-k8s.io/v1alpha1'"
  wait_for_process $WAIT_TIME $SLEEP_TIME "$cmd"

  # Check the data in the secret
  expected_data="secret"
  secret_data=$(kubectl get secret sse2esecret -n test-v1alpha1 -o jsonpath='{.data.bar}' | base64 --decode)
  [ "$secret_data" = "$expected_data" ]

  # Check owner_count is 1
  cmd="compare_owner_count sse2esecret test-v1alpha1 1"
  wait_for_process $WAIT_TIME $SLEEP_TIME "$cmd"

  # Delete the SecretSync
  cmd="kubectl delete secretsync sse2esecret -n test-v1alpha1"
  wait_for_process $WAIT_TIME $SLEEP_TIME "$cmd"

  # Check that the secret is deleted
  cmd="kubectl get secret sse2esecret -n test-v1alpha1"
  wait_for_process $WAIT_TIME $SLEEP_TIME "! $cmd"
}

@test "SecretProviderClass and SecretSync are deployed in different namespaces" {
  create_namespace "spc-namespace"
  create_namespace "ss-namespace"

  deploy_spc_ss_verify_conditions \
    "$BATS_RESOURCE_MANIFESTS_DIR/e2e-providerspc.yaml" \
    "$BATS_RESOURCE_MANIFESTS_DIR/e2e-secret-sync.yaml" \
    "sse2esecret" \
    "SecretCreated" \
    "failed to get SecretProviderClass \\\"e2e-providerspc\\\": SecretProviderClass.secrets-store.csi.x-k8s.io \\\"e2e-providerspc\\\" not found" \
    "SecretProviderClassMisconfigured" \
    "False" \
    "spc-namespace" \
    "ss-namespace"

  kubectl delete -f "$BATS_RESOURCE_MANIFESTS_DIR/e2e-secret-sync.yaml" -n ss-namespace
}

@test "Cannot create secret with type not in allowed list" {
  deploy_spc_ss_verify_conditions \
    "$BATS_RESOURCE_MANIFESTS_DIR/e2e-providerspc.yaml" \
    "$BATS_RESOURCE_YAML_DIR/api_credential_secretsync.yaml" \
    "my-custom-api-secret" \
    "SecretCreated" \
    "failed to patch secret \\\"my-custom-api-secret\\\": secrets \\\"my-custom-api-secret\\\" is forbidden: ValidatingAdmissionPolicy 'secrets-store-sync-controller-create-update-policy' with binding 'secrets-store-sync-controller-create-update-policy-binding' denied request: secrets-store-sync-controller has failed to CREATE secret with example.com/api-credentials type in the default namespace. The controller can only create or update secrets in the allowed types list with a single secretsync owner." \
    "ControllerPatchError" \
    "False"

  kubectl delete -f "$BATS_RESOURCE_YAML_DIR/api_credential_secretsync.yaml"
}

@test "Cannot create secret with disallowed type" {
  deploy_spc_ss_verify_conditions \
    "$BATS_RESOURCE_MANIFESTS_DIR/e2e-providerspc.yaml" \
    "$BATS_RESOURCE_YAML_DIR/service_account_token_secretsync.yaml" \
    "sse2eserviceaccountsecret" \
    "SecretCreated" \
    "failed to patch secret \\\"sse2eserviceaccountsecret\\\": secrets \\\"sse2eserviceaccountsecret\\\" is forbidden: ValidatingAdmissionPolicy 'secrets-store-sync-controller-create-update-policy' with binding 'secrets-store-sync-controller-create-update-policy-binding' denied request: secrets-store-sync-controller has failed to CREATE secret with kubernetes.io/service-account-token type in the default namespace. The controller is not allowed to create or update secrets with this type." \
    "ControllerPatchError" \
    "False"

  kubectl delete -f $BATS_RESOURCE_YAML_DIR/service_account_token_secretsync.yaml
}

@test "Cannot create a secret with invalid annotations" {
  expected_message="failed to patch secret \\\"sse2einvalidannotationssecret\\\": Secret \\\"sse2einvalidannotationssecret\\\" is invalid: metadata.annotations: Invalid value: \\\"my.annotation/with_invalid_characters!\\\": name part must consist of alphanumeric characters, '-', '_' or '.', and must start and end with an alphanumeric character (e.g. 'MyName',  or 'my.name',  or '123-abc', regex used for validation is '([A-Za-z0-9][-A-Za-z0-9_.]*)?[A-Za-z0-9]')"

  deploy_spc_ss_verify_conditions \
    "$BATS_RESOURCE_MANIFESTS_DIR/e2e-providerspc.yaml" \
    "$BATS_RESOURCE_YAML_DIR/invalid_annotation_key_secretsync.yaml" \
    "sse2einvalidannotationssecret" \
    "SecretCreated" \
    "$expected_message" \
    "ControllerPatchError" \
    "False"
}

@test "Cannot create a secret with invalid labels" {
  expected_message="failed to patch secret \\\"sse2einvalidlabelsecret\\\": Secret \\\"sse2einvalidlabelsecret\\\" is invalid: metadata.labels: Invalid value: \\\"invalid/key_with_invalid_characters!\\\": name part must consist of alphanumeric characters, '-', '_' or '.', and must start and end with an alphanumeric character (e.g. 'MyName',  or 'my.name',  or '123-abc', regex used for validation is '([A-Za-z0-9][-A-Za-z0-9_.]*)?[A-Za-z0-9]')"

  deploy_spc_ss_verify_conditions \
    "$BATS_RESOURCE_MANIFESTS_DIR/e2e-providerspc.yaml" \
    "$BATS_RESOURCE_YAML_DIR/invalid_label_key_secretsync.yaml" \
    "sse2einvalidlabelsecret" \
    "SecretCreated" \
    "$expected_message" \
    "ControllerPatchError" \
    "False"
}

@test "API validations" {
  create_secretsync_expect_fail \
    "$BATS_RESOURCE_MANIFESTS_DIR/e2e-providerspc.yaml" \
    "$BATS_RESOURCE_YAML_DIR/invalid_label_value_secretsync.yaml" \
    "The SecretSync \"sse2einvalidlabelsecret\" is invalid: spec.secretObject.labels: Invalid value: \"object\": Label keys must not exceed 317 characters (254 for prefix+separator, 63 for name), label values must not exceed 63 characters."
}

teardown_file() {
  archive_provider "app=secrets-store-sync-controller" || true
  archive_info || true

  run kubectl delete namespace test-v1alpha1
  run kubectl delete namespace spc-namespace
  run kubectl delete namespace ss-namespace

  echo "Done cleaning up e2e tests"
}
