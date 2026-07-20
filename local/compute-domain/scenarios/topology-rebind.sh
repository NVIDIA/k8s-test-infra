#!/usr/bin/env bash
# Copyright 2026 NVIDIA CORPORATION
# SPDX-License-Identifier: Apache-2.0
#
# Scenario 3 payload: topology rebinding without a rebuild.
#
# `helm upgrade --reuse-values -f rebind.topology.yaml` swaps every
# node into clique 99 of a new domain UUID, DaemonSet pods are evicted
# so the mock NVML engine re-reads MOCK_TOPOLOGY_CONFIG on the next
# process start, and check-fabric re-asserts the new identity. Ported
# from docs/demo/compute-domain/run.sh Scenario 3 (lines 313-351).

set -euo pipefail

CLUSTER_NAME="nvml-mock-compute-domain"
RELEASE_NAME="nvml-mock"
CHART_PATH="deployments/nvml-mock/helm/nvml-mock"
REBIND_TOPO="local/compute-domain/rebind.topology.yaml"
NEW_UUID="00000000-0000-0000-0000-0000000000ff"

printf '==> helm upgrade --reuse-values -f %s\n' "${REBIND_TOPO}"
helm upgrade "${RELEASE_NAME}" "${CHART_PATH}" \
  --reuse-values \
  -f "${REBIND_TOPO}" \
  --wait --timeout 180s >/dev/null
printf '    upgraded\n'

printf '==> evicting pods so mock NVML re-reads MOCK_TOPOLOGY_CONFIG\n'
kubectl delete pods -l "app.kubernetes.io/name=${RELEASE_NAME}" \
  --ignore-not-found >/dev/null
kubectl rollout status "daemonset/${RELEASE_NAME}" --timeout=180s >/dev/null
printf '    rolled out\n'

pod_on_node() {
  local node=$1
  for _ in $(seq 1 30); do
    local name
    name=$(kubectl get pods -l "app.kubernetes.io/name=${RELEASE_NAME}" \
      --field-selector="spec.nodeName=${node},status.phase=Running" \
      -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true)
    if [[ -n "${name}" ]]; then
      printf '%s\n' "${name}"
      return 0
    fi
    sleep 1
  done
  return 1
}

assert_clique() {
  local node=$1 expected_clique=$2 expected_uuid=$3
  local pod
  if ! pod=$(pod_on_node "${node}"); then
    printf 'FAIL: no running pod on %s\n' "${node}" >&2
    return 1
  fi
  local out
  out=$(kubectl exec "${pod}" -- check-fabric 2>&1 || true)
  if ! printf '%s\n' "${out}" | grep -q "cliqueId    : ${expected_clique}"; then
    printf 'FAIL: %s expected cliqueId %s\n' "${node}" "${expected_clique}" >&2
    return 1
  fi
  if ! printf '%s\n' "${out}" | grep -qi "clusterUuid : ${expected_uuid}"; then
    printf 'FAIL: %s expected clusterUuid %s\n' "${node}" "${expected_uuid}" >&2
    return 1
  fi
  printf 'OK: %s clique=%s uuid=%s\n' "${node}" "${expected_clique}" "${expected_uuid}"
}

assert_clique "${CLUSTER_NAME}-worker"  99 "${NEW_UUID}"
assert_clique "${CLUSTER_NAME}-worker2" 99 "${NEW_UUID}"
assert_clique "${CLUSTER_NAME}-worker3" 99 "${NEW_UUID}"
assert_clique "${CLUSTER_NAME}-worker4" 99 "${NEW_UUID}"

printf '\n==> Scenario 3 passed: all workers rebound to clique 99 with new UUID.\n'
printf '    Re-run local/compute-domain/scenarios/check-fabric.sh (Scenario 1)\n'
printf '    to restore assertions against the original topology.\n'
