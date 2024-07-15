#!/bin/bash

assert_success() {
  if [[ "${status:-}" != 0 ]]; then
    echo "expected: 0"
    echo "actual: ${status:-}"
    echo "output: ${output:-}"
    return 1
  fi
}

assert_failure() {
  if [[ "${status:-}" == 0 ]]; then
    echo "expected: non-zero exit code"
    echo "actual: ${status:-}"
    echo "output: ${output:-}"
    return 1
  fi
}

archive_provider() {
    # Determine log directory
  if [[ -z "${ARTIFACTS}" ]]; then
    return 0
  fi

  FILE_PREFIX=$(date +"%FT%H%M%S")

  kubectl logs -l "$1" --tail -1 -n secrets-store-sync-controller-system > "${ARTIFACTS}/${FILE_PREFIX}-provider.logs"
}

archive_info() {
  # Determine log directory
  if [[ -z "${ARTIFACTS}" ]]; then
    return 0
  fi

  LOGS_DIR="${ARTIFACTS}/$(date +"%FT%H%M%S")"
  mkdir -p "${LOGS_DIR}"

  # print all pod information
  kubectl get pods -A -o json > "${LOGS_DIR}/pods.json"

  # print detailed pod information
  kubectl describe pods --all-namespaces > "${LOGS_DIR}/pods-describe.txt"

  # print logs from the secrets-store-sync-controller
  #
  # assumes secrets-store-sync-controller is installed with helm into the `secrets-store-sync-controller-system` namespace which
  # sets the `app` selector to `secrets-store-sync-controller`.
  #
  # Note: the yaml deployment would require `app=secrets-store-sync-controller`
  kubectl logs -l app=secrets-store-sync-controller --tail -1 -c manager -n secrets-store-sync-controller-system > "${LOGS_DIR}/secrets-store-sync-controller.log"
  kubectl logs -l app=secrets-store-sync-controller   --tail -1 -c provider-e2e-installer -n secrets-store-sync-controller-system > "${LOGS_DIR}/e2e-provider.log"

  # print client and server version information
  kubectl version > "${LOGS_DIR}/kubectl-version.txt"

  # print generic cluster information
  kubectl cluster-info dump > "${LOGS_DIR}/cluster-info.txt"

  # collect metrics
  local curl_pod_name
  curl_pod_name="curl-$(openssl rand -hex 5)"
  kubectl run "${curl_pod_name}" -n default --image=curlimages/curl:7.75.0 --labels="test=metrics_test" --overrides='{"spec": { "nodeSelector": {"kubernetes.io/os": "linux"}}}' -- tail -f /dev/null
  kubectl wait --for=condition=Ready --timeout=60s -n default pod "${curl_pod_name}"

  for pod_ip in $(kubectl get pod -n secrets-store-sync-controller-system -l app=secrets-store-sync-controller  -o jsonpath="{.items[*].status.podIP}")
  do
    kubectl exec -n default "${curl_pod_name}" -- curl -s http://"${pod_ip}":8085/metrics > "${LOGS_DIR}/${pod_ip}.metrics"
  done

  kubectl delete pod -n default "${curl_pod_name}"
}

compare_owner_count() {
  secret="$1"
  namespace="$2"
  ownercount="$3"

  [[ "$(kubectl get secret "${secret}" -n "${namespace}" -o json | jq '.metadata.ownerReferences | length')" -eq $ownercount ]]
}

wait_for_process() {
  wait_time="$1"
  sleep_time="$2"
  cmd="$3"
  while [ "$wait_time" -gt 0 ]; do
    if eval "$cmd"; then
      return 0
    else
      sleep "$sleep_time"
      wait_time=$((wait_time-sleep_time))
    fi
  done
  return 1
}
