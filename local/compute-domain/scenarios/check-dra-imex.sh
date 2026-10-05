#!/usr/bin/env bash
# Copyright 2026 NVIDIA CORPORATION
# SPDX-License-Identifier: Apache-2.0

# Prove that the unmodified upstream DRA controller can create and recover its
# real compute-domain-daemon fleet using IMEX node software staged by Mokka.

set -euo pipefail

: "${KUBE_CONTEXT:=kind-mokka-compute-domain}"
: "${DRIVER_NAMESPACE:=nvidia}"
: "${MOKKA_NAMESPACE:=mokka}"

DOMAIN=mokka-imex-e2e
MANIFEST=local/compute-domain/dra-compute-domain.yaml
TIMEOUT_SECONDS=300

k() {
  kubectl --context "${KUBE_CONTEXT}" "$@"
}

cleanup() {
  k delete -f "${MANIFEST}" --ignore-not-found --wait=false >/dev/null 2>&1 || true
}

diagnostics() {
  printf '\nComputeDomain acceptance diagnostics:\n' >&2
  k get computedomain "${DOMAIN}" -o yaml >&2 || true
  k -n "${DRIVER_NAMESPACE}" get pods,daemonsets \
    -l resource.nvidia.com/computeDomain -o wide >&2 || true
  k -n "${DRIVER_NAMESPACE}" get events --sort-by=.lastTimestamp >&2 || true

  # The daemons' own output says why one is not ready or keeps restarting.
  local pod
  for pod in $(k -n "${DRIVER_NAMESPACE}" get pods -l resource.nvidia.com/computeDomain \
    -o jsonpath='{.items[*].metadata.name}' 2>/dev/null); do
    printf '\n--- %s (current, then previous)\n' "${pod}" >&2
    k -n "${DRIVER_NAMESPACE}" logs "${pod}" --tail=40 >&2 || true
    k -n "${DRIVER_NAMESPACE}" logs "${pod}" --previous --tail=40 >&2 || true
  done
  # What Mokka staged and decided for those daemons, per node.
  for pod in $(k -n "${MOKKA_NAMESPACE}" get pods -l app.kubernetes.io/name=nvml-mock \
    -o jsonpath='{.items[*].metadata.name}' 2>/dev/null); do
    printf '\n--- %s node agent (imex) and NRI (compute-domain)\n' "${pod}" >&2
    k -n "${MOKKA_NAMESPACE}" logs "${pod}" -c node-agent 2>/dev/null | grep -i imex | tail -10 >&2 || true
    k -n "${MOKKA_NAMESPACE}" logs "${pod}" -c nvml-mock-nri 2>/dev/null \
      | grep -iE 'compute|hold|unavailable|error' | tail -10 >&2 || true
  done
}

finish() {
  local status=$?
  trap - EXIT
  if (( status != 0 )); then
    diagnostics
  fi
  cleanup
  exit "${status}"
}
trap finish EXIT

wait_for() {
  local description=$1
  shift
  local deadline=$((SECONDS + TIMEOUT_SECONDS))
  until "$@"; do
    if (( SECONDS >= deadline )); then
      printf 'timed out waiting for %s\n' "${description}" >&2
      return 1
    fi
    sleep 2
  done
}

domain_ready() {
  [[ "$(k get computedomain "${DOMAIN}" -o jsonpath='{.status.status}' 2>/dev/null || true)" == Ready ]]
}

daemonset_name() {
  local uid names count
  uid=$(k get computedomain "${DOMAIN}" -o jsonpath='{.metadata.uid}' 2>/dev/null || true)
  [[ -n "${uid}" ]] || return 1
  names=$(k -n "${DRIVER_NAMESPACE}" get daemonset \
    -l "resource.nvidia.com/computeDomain=${uid}" \
    -o jsonpath='{range .items[*]}{.metadata.name}{"\n"}{end}' 2>/dev/null)
  count=$(printf '%s\n' "${names}" | awk 'NF { count++ } END { print count + 0 }')
  [[ "${count}" -eq 1 ]] || return 1
  printf '%s\n' "${names}"
}

daemonset_exists() {
  [[ -n "$(daemonset_name || true)" ]]
}

minimal_nri_adjustment() {
  local uid pods pod
  uid=$(k get computedomain "${DOMAIN}" -o jsonpath='{.metadata.uid}' 2>/dev/null || true)
  [[ -n "${uid}" ]] || return 1
  pods=$(k -n "${DRIVER_NAMESPACE}" get pods \
    -l "resource.nvidia.com/computeDomain=${uid}" \
    -o jsonpath='{range .items[*]}{.metadata.name}{"\n"}{end}' 2>/dev/null)
  [[ -n "${pods}" ]] || return 1

  while IFS= read -r pod; do
    [[ -n "${pod}" ]] || continue
    k -n "${DRIVER_NAMESPACE}" exec "${pod}" -- /busybox/sh -eu -c '
      test -x /usr/bin/nvidia-imex.real
      test -f "${MOCK_TOPOLOGY_CONFIG}"
      test -z "${MOCK_NVML_CONFIG:-}"
      test ! -d /opt/nvml-mock/driver
      case ":${PATH}:" in
        *:/opt/nvml-mock/driver/usr/bin:*) exit 1 ;;
      esac
    ' >/dev/null || return 1
  done <<< "${pods}"
}

pod_uids() {
  local namespace=$1 selector=$2
  k -n "${namespace}" get pods -l "${selector}" \
    -o jsonpath='{range .items[*]}{.metadata.uid}{"\n"}{end}' | sort
}

# True once none of the pods listed in old_uids exists any more. A DaemonSet's
# status can still describe deleted pods, so rollout status alone does not prove
# a restart happened.
pods_replaced() {
  local namespace=$1 selector=$2 old_uids=$3 current
  current=$(pod_uids "${namespace}" "${selector}" 2>/dev/null) || return 1
  [[ -z "$(comm -12 <(printf '%s\n' "${old_uids}") <(printf '%s\n' "${current}") | sed '/^$/d')" ]]
}

pods_ready() {
  local namespace=$1 selector=$2 expected=$3 ready
  ready=$(k -n "${namespace}" get pods -l "${selector}" \
    -o jsonpath='{range .items[*]}{.status.conditions[?(@.type=="Ready")].status}{"\n"}{end}' 2>/dev/null) || return 1
  [[ "$(grep -c . <<< "${ready}")" -eq "${expected}" && "$(grep -c '^True$' <<< "${ready}")" -eq "${expected}" ]]
}

printf 'Applying ComputeDomain %s\n' "${DOMAIN}"
k apply -f "${MANIFEST}" >/dev/null

wait_for "the DRA compute-domain DaemonSet" daemonset_exists
DAEMONSET=$(daemonset_name)
k -n "${DRIVER_NAMESPACE}" rollout status "daemonset/${DAEMONSET}" --timeout="${TIMEOUT_SECONDS}s"
wait_for "ComputeDomain readiness" domain_ready
wait_for "minimal NRI adjustment for the DRA daemon" minimal_nri_adjustment

READY=$(k -n "${DRIVER_NAMESPACE}" get "daemonset/${DAEMONSET}" -o jsonpath='{.status.numberReady}')
DESIRED=$(k -n "${DRIVER_NAMESPACE}" get "daemonset/${DAEMONSET}" -o jsonpath='{.status.desiredNumberScheduled}')
[[ "${READY}" == "${DESIRED}" && "${READY}" -gt 0 ]]
printf 'ComputeDomain ready with %s/%s real IMEX daemons\n' "${READY}" "${DESIRED}"

# Exercise the ordering that matters after a node-agent restart: its graceful
# teardown removes the staged executables while replacement DRA pods may start.
# Kubernetes must converge after Mokka restores the stable driver tree; no
# custom DRA image or manual node preparation participates in the recovery.
printf 'Restarting Mokka and the per-domain daemon fleet together\n'
MOKKA_SELECTOR=app.kubernetes.io/name=nvml-mock
DAEMON_SELECTOR="resource.nvidia.com/computeDomain=$(k get computedomain "${DOMAIN}" -o jsonpath='{.metadata.uid}')"
OLD_MOKKA_PODS=$(pod_uids "${MOKKA_NAMESPACE}" "${MOKKA_SELECTOR}")
OLD_DAEMON_PODS=$(pod_uids "${DRIVER_NAMESPACE}" "${DAEMON_SELECTOR}")
MOKKA_PODS=$(grep -c . <<< "${OLD_MOKKA_PODS}")
DAEMON_PODS=$(grep -c . <<< "${OLD_DAEMON_PODS}")
k -n "${MOKKA_NAMESPACE}" delete pod -l "${MOKKA_SELECTOR}" --wait=false >/dev/null
k -n "${DRIVER_NAMESPACE}" delete pod -l "${DAEMON_SELECTOR}" --wait=false >/dev/null

wait_for "the old Mokka pods to be gone" pods_replaced "${MOKKA_NAMESPACE}" "${MOKKA_SELECTOR}" "${OLD_MOKKA_PODS}"
wait_for "the old DRA daemon pods to be gone" pods_replaced "${DRIVER_NAMESPACE}" "${DAEMON_SELECTOR}" "${OLD_DAEMON_PODS}"
wait_for "${MOKKA_PODS} replacement Mokka pods to be ready" pods_ready "${MOKKA_NAMESPACE}" "${MOKKA_SELECTOR}" "${MOKKA_PODS}"
wait_for "${DAEMON_PODS} replacement DRA daemon pods to be ready" pods_ready "${DRIVER_NAMESPACE}" "${DAEMON_SELECTOR}" "${DAEMON_PODS}"
wait_for "ComputeDomain recovery" domain_ready
wait_for "minimal NRI adjustment after recovery" minimal_nri_adjustment

printf 'ComputeDomain recovered after concurrent node-agent and daemon restart\n'
