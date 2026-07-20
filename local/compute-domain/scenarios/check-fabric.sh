#!/usr/bin/env bash
# Copyright 2026 NVIDIA CORPORATION
# SPDX-License-Identifier: Apache-2.0
#
# Scenario 1 payload: per-node fabric identity via nvmlDeviceGetGpuFabricInfo.
#
# For each worker, kubectl exec the bundled `check-fabric` binary in the
# node's nvml-mock pod and assert the reported cliqueId + clusterUuid +
# state match what local/compute-domain/topology.yaml assigned to that
# node. Ported from docs/demo/compute-domain/run.sh (Scenario 1,
# lines 193-199 and the assert_clique helper at 98-123).

set -euo pipefail

CLUSTER_NAME="nvml-mock-compute-domain"
RELEASE_NAME="nvml-mock"
EXPECTED_UUID="00000000-0000-0000-0000-0000000000ab"

pod_on_node() {
  local node=$1
  # Poll: the pod list can lag briefly after a rollout.
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
  printf '%s\n' "${out}" | sed 's/^/      /'
  if ! printf '%s\n' "${out}" | grep -q "cliqueId    : ${expected_clique}"; then
    printf 'FAIL: %s expected cliqueId %s\n' "${node}" "${expected_clique}" >&2
    return 1
  fi
  if ! printf '%s\n' "${out}" | grep -qi "clusterUuid : ${expected_uuid}"; then
    printf 'FAIL: %s expected clusterUuid %s\n' "${node}" "${expected_uuid}" >&2
    return 1
  fi
  if ! printf '%s\n' "${out}" | grep -q 'state       : completed'; then
    printf 'FAIL: %s expected state=completed\n' "${node}" >&2
    return 1
  fi
  printf 'OK: %s clique=%s uuid=%s state=completed\n' "${node}" "${expected_clique}" "${expected_uuid}"
}

assert_clique "${CLUSTER_NAME}-worker"  0 "${EXPECTED_UUID}"
assert_clique "${CLUSTER_NAME}-worker2" 0 "${EXPECTED_UUID}"
assert_clique "${CLUSTER_NAME}-worker3" 1 "${EXPECTED_UUID}"
assert_clique "${CLUSTER_NAME}-worker4" 1 "${EXPECTED_UUID}"

printf '\n==> Scenario 1 passed: 4/4 workers report expected fabric identity.\n'
