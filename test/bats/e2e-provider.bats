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
    "Secret update failed because the controller could not retrieve the Secret Provider Class or the SPC is misconfigured. Check the logs or the events for more information." \
    "ControllerSPCError" \
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
    "Secret update failed due to validating admission policy check failure, check the logs or the events for more information." \
    "ValidatingAdmissionPolicyCheckFailed" \
    "False"

  kubectl delete -f "$BATS_RESOURCE_YAML_DIR/api_credential_secretsync.yaml"
}

@test "Cannot create secret with disallowed type" {
  deploy_spc_ss_verify_conditions \
    "$BATS_RESOURCE_MANIFESTS_DIR/e2e-providerspc.yaml" \
    "$BATS_RESOURCE_YAML_DIR/service_account_token_secretsync.yaml" \
    "sse2eserviceaccountsecret" \
    "Secret update failed due to validating admission policy check failure, check the logs or the events for more information." \
    "ValidatingAdmissionPolicyCheckFailed" \
    "False"

  kubectl delete -f $BATS_RESOURCE_YAML_DIR/service_account_token_secretsync.yaml
}

@test "Cannot create a secret with invalid annotations" {
  expected_message="The secretsyncs \"sse2einvalidannotationssecret\" is invalid: : ValidatingAdmissionPolicy 'secrets-store-sync-controller-validate-annotation-policy' with binding 'secrets-store-sync-controller-validate-annotation-policy-binding' denied request: One of the annotations applied on the secret has an invalid format. Update the annotation and try again."

  deploy_spc_ss_expect_failure \
    "$BATS_RESOURCE_MANIFESTS_DIR/e2e-providerspc.yaml" \
    "$BATS_RESOURCE_YAML_DIR/invalid_annotation_key_secretsync.yaml" \
    "sse2einvalidannotationssecret" \
    "$expected_message"

  deploy_spc_ss_expect_failure \
    "$BATS_RESOURCE_MANIFESTS_DIR/e2e-providerspc.yaml" \
    "$BATS_RESOURCE_YAML_DIR/invalid_annotation_value_secretsync.yaml" \
    "sse2einvalidannotationssecret" \
    "$expected_message"
}

@test "Cannot create a secret with invalid labels" {
  expected_message="The secretsyncs \"sse2einvalidlabelsecret\" is invalid: : ValidatingAdmissionPolicy 'secrets-store-sync-controller-validate-label-policy' with binding 'secrets-store-sync-controller-validate-label-policy-binding' denied request: One of the labels applied on the secret has an invalid format. Update the label and try again."

  deploy_spc_ss_expect_failure \
    "$BATS_RESOURCE_MANIFESTS_DIR/e2e-providerspc.yaml" \
    "$BATS_RESOURCE_YAML_DIR/invalid_label_key_secretsync.yaml" \
    "sse2einvalidlabelsecret" \
    "$expected_message"

  deploy_spc_ss_expect_failure \
    "$BATS_RESOURCE_MANIFESTS_DIR/e2e-providerspc.yaml" \
    "$BATS_RESOURCE_YAML_DIR/invalid_label_value_secretsync.yaml" \
    "sse2einvalidlabelsecret" \
    "$expected_message"
}

teardown_file() {
  archive_provider "app=secrets-store-sync-controller" || true
  archive_info || true

  run kubectl delete namespace test-v1alpha1
  run kubectl delete namespace spc-namespace
  run kubectl delete namespace ss-namespace

  echo "Done cleaning up e2e tests"
}
