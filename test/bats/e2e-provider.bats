#!/usr/bin/env bats

load helpers

BATS_RESOURCE_MANIFESTS_DIR=hack/localsetup
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
  run kubectl get clusterrole/secret-sync-controller-manager-role
  assert_success

  run kubectl get clusterrolebinding/secret-sync-controller-manager-rolebinding
  assert_success
}

@test "[v1alpha1] deploy e2e-providerspc secretproviderclass crd" {
  kubectl create namespace test-v1alpha1 --dry-run=client -o yaml | kubectl apply -f -

  kubectl apply -n test-v1alpha1 -f $BATS_RESOURCE_MANIFESTS_DIR/e2e-providerspc.yaml
  kubectl wait --for condition=established -n test-v1alpha1 --timeout=60s crd/secretproviderclasses.secrets-store.csi.x-k8s.io

  cmd="kubectl get secretproviderclasses.secrets-store.csi.x-k8s.io/e2e-providerspc -n test-v1alpha1 -o yaml | grep e2e-providerspc"
  wait_for_process $WAIT_TIME $SLEEP_TIME "$cmd"
}

@test "[v1alpha1] deploy e2e-providerspc secretsync crd" {
  # Create the SPC
  kubectl apply -n test-v1alpha1 -f $BATS_RESOURCE_MANIFESTS_DIR/e2e-providerspc.yaml
  kubectl wait --for condition=established -n test-v1alpha1 --timeout=60s crd/secretproviderclasses.secrets-store.csi.x-k8s.io

  cmd="kubectl get secretproviderclasses.secrets-store.csi.x-k8s.io/e2e-providerspc -n test-v1alpha1 -o yaml | grep e2e-providerspc"
  wait_for_process $WAIT_TIME $SLEEP_TIME "$cmd"

  # Create the SecretSync
  kubectl apply -n test-v1alpha1 -f $BATS_RESOURCE_MANIFESTS_DIR/e2e-secret-sync.yaml
  kubectl wait --for condition=established -n test-v1alpha1 --timeout=60s crd/secretsyncs.secret-sync.x-k8s.io

  cmd="kubectl get secretsyncs.secret-sync.x-k8s.io/sse2esecret -n test-v1alpha1 -o yaml | grep sse2esecret"
  wait_for_process $WAIT_TIME $SLEEP_TIME "$cmd"

  # Retrieve the secret 
  cmd="kubectl get secret sse2esecret -n test-v1alpha1 -o yaml | grep 'apiVersion: secret-sync.x-k8s.io/v1alpha1'"
  wait_for_process $WAIT_TIME $SLEEP_TIME "$cmd"
}

@test "SecretProviderClass and SecretSync are deployed in different namespaces" {
  # Create namespaces
  kubectl create namespace spc-namespace --dry-run=client -o yaml | kubectl apply -f -
  kubectl create namespace ss-namespace --dry-run=client -o yaml | kubectl apply -f -

  # Deploy the SecretProviderClass in spc-namespace
  kubectl apply -n spc-namespace -f $BATS_RESOURCE_MANIFESTS_DIR/e2e-providerspc.yaml 
  kubectl wait --for condition=established -n spc-namespace --timeout=60s crd/secretproviderclasses.secrets-store.csi.x-k8s.io

  cmd="kubectl get secretproviderclasses.secrets-store.csi.x-k8s.io/e2e-providerspc -n spc-namespace -o yaml | grep e2e-providerspc"
  wait_for_process $WAIT_TIME $SLEEP_TIME "$cmd"

  # Deploy the SecretSync in ss-namespace
  kubectl apply -n ss-namespace -f $BATS_RESOURCE_MANIFESTS_DIR/e2e-secret-sync.yaml
  kubectl wait --for condition=established -n ss-namespace --timeout=60s crd/secretsyncs.secret-sync.x-k8s.io

  cmd="kubectl get secretsyncs.secret-sync.x-k8s.io/sse2esecret -n ss-namespace -o yaml | grep sse2esecret"
  wait_for_process $WAIT_TIME $SLEEP_TIME "$cmd"

  # Check that the secret is not created in ss-namespace
  cmd="kubectl get secret sse2esecret -n ss-namespace"
  run $cmd
  assert_failure
}

@test "Delete SecretSync and check that the secret is deleted" {

  # Deploy the SecretProviderClass in the same namespace
  kubectl apply -n test-v1alpha1 -f $BATS_RESOURCE_MANIFESTS_DIR/e2e-providerspc.yaml
  kubectl wait --for condition=established -n test-v1alpha1 --timeout=60s crd/secretproviderclasses.secrets-store.csi.x-k8s.io

  cmd="kubectl get secretproviderclasses.secrets-store.csi.x-k8s.io/e2e-providerspc -n test-v1alpha1 -o yaml | grep e2e-providerspc"
  wait_for_process $WAIT_TIME $SLEEP_TIME "$cmd"

  # Deploy the SecretSync in the same namespace
  kubectl apply -n test-v1alpha1 -f $BATS_RESOURCE_MANIFESTS_DIR/e2e-secret-sync.yaml
  kubectl wait --for condition=established -n test-v1alpha1 --timeout=60s crd/secretsyncs.secret-sync.x-k8s.io

  cmd="kubectl get secretsyncs.secret-sync.x-k8s.io/sse2esecret -n test-v1alpha1 -o yaml | grep sse2esecret"
  wait_for_process $WAIT_TIME $SLEEP_TIME "$cmd"

  # Check that the secret is created
  cmd="kubectl get secret sse2esecret -n test-v1alpha1 -o yaml"
  run $cmd
  assert_success

  # Delete the SecretSync
  kubectl delete secretsync sse2esecret -n test-v1alpha1
    
  # Check that the secret is deleted
  cmd="kubectl get secret sse2esecret -n test-v1alpha1"
  run $cmd
  assert_failure
}

teardown_file() {
  if [[ "${INPLACE_UPGRADE_TEST}" != "true" ]]; then
    #cleanup
    run kubectl delete namespace test-v1alpha1
    run kubectl delete namespace spc-namespace
    run kubectl delete namespace ss-namespace
    echo "Done cleaning up e2e tests"
  fi
}
