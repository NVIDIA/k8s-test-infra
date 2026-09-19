#!/usr/bin/env bash
# Copyright 2026 NVIDIA CORPORATION
# SPDX-License-Identifier: Apache-2.0

# Prove that the unmodified upstream DRA controller can create and recover its
# real compute-domain-daemon fleet using IMEX userspace staged by Mokka.

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
k -n "${MOKKA_NAMESPACE}" delete pod \
  -l app.kubernetes.io/name=nvml-mock --wait=false >/dev/null
k -n "${DRIVER_NAMESPACE}" delete pod \
  -l "resource.nvidia.com/computeDomain=$(k get computedomain "${DOMAIN}" -o jsonpath='{.metadata.uid}')" \
  --wait=false >/dev/null

k -n "${MOKKA_NAMESPACE}" rollout status daemonset/nvml-mock --timeout="${TIMEOUT_SECONDS}s"
k -n "${DRIVER_NAMESPACE}" rollout status "daemonset/${DAEMONSET}" --timeout="${TIMEOUT_SECONDS}s"
wait_for "ComputeDomain recovery" domain_ready
wait_for "minimal NRI adjustment after recovery" minimal_nri_adjustment

printf 'ComputeDomain recovered after concurrent node-agent and daemon restart\n'
