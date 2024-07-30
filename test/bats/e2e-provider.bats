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
  kubectl create namespace test-v1alpha1 --dry-run=client -o yaml | kubectl apply -f -

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
  # Create namespaces
  kubectl create namespace spc-namespace --dry-run=client -o yaml | kubectl apply -f -
  kubectl create namespace ss-namespace --dry-run=client -o yaml | kubectl apply -f -

  # Deploy the SecretProviderClass in spc-namespace
  kubectl apply -n spc-namespace -f $BATS_RESOURCE_MANIFESTS_DIR/e2e-providerspc.yaml 

  cmd="kubectl get secretproviderclasses.secrets-store.csi.x-k8s.io/e2e-providerspc -n spc-namespace -o yaml | grep e2e-providerspc"
  wait_for_process $WAIT_TIME $SLEEP_TIME "$cmd"

  # Deploy the SecretSync in ss-namespace
  kubectl apply -n ss-namespace -f $BATS_RESOURCE_MANIFESTS_DIR/e2e-secret-sync.yaml

  cmd="kubectl get secretsyncs.secret-sync.x-k8s.io/sse2esecret -n ss-namespace -o yaml | grep sse2esecret"
  wait_for_process $WAIT_TIME $SLEEP_TIME "$cmd"

  # Check the status of SecretSync in ss-namespace
  status=$(kubectl get secretsyncs.secret-sync.x-k8s.io/sse2esecret -n ss-namespace -o jsonpath='{.status.conditions[0]}')

  expected_message="Secret update failed because the controller could not retrieve the Secret Provider Class or the SPC is misconfigured. Check the logs or the events for more information."
  expected_reason="ControllerSPCError"
  expected_status="False"

  # Extract individual fields from the status
  message=$(echo $status | jq -r .message)
  reason=$(echo $status | jq -r .reason)
  status_value=$(echo $status | jq -r .status)

  # Verify the status fields
  [ "$message" = "$expected_message" ]
  [ "$reason" = "$expected_reason" ]
  [ "$status_value" = "$expected_status" ]

  # Check that the secret is not created in ss-namespace
  cmd="kubectl get secret sse2esecret -n ss-namespace"
  run $cmd
  assert_failure
}

teardown_file() {
  archive_provider "app=secrets-store-sync-controller" || true
  archive_info || true

  if [[ "${INPLACE_UPGRADE_TEST}" != "true" ]]; then
    #cleanup
    run kubectl delete namespace test-v1alpha1
    run kubectl delete namespace spc-namespace
    run kubectl delete namespace ss-namespace
    echo "Done cleaning up e2e tests"
  fi
}
