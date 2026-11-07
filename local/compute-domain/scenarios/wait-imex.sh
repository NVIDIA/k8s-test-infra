#!/usr/bin/env bash
# Copyright 2026 NVIDIA CORPORATION
# SPDX-License-Identifier: Apache-2.0
#
# Waits until every nvml-mock node pod has staged IMEX, so that the DRA driver
# installs after it: the compute-domain kubelet plugin looks for the IMEX
# binaries only when it starts. After TIMEOUT seconds it warns and lets the
# install go ahead; a node that gets IMEX later needs its kubelet plugin
# restarted.

set -euo pipefail

RELEASE_NAME="nvml-mock"
# compute_domain.tiltfile installs the release here.
NAMESPACE="${NAMESPACE:-mokka}"
TIMEOUT="${TIMEOUT:-300}"
# The node agent publishes nvidia-imex.real last, so it marks IMEX staged.
STAGED="/var/lib/nvml-mock/driver/usr/bin/nvidia-imex.real"

kubectl rollout status -n "${NAMESPACE}" "daemonset/${RELEASE_NAME}" --timeout="${TIMEOUT}s"

deadline=$((SECONDS + TIMEOUT))
while :; do
  missing=()
  for pod in $(kubectl get pods -n "${NAMESPACE}" \
    -l "app.kubernetes.io/name=${RELEASE_NAME},app.kubernetes.io/component!=control-plane" \
    -o jsonpath='{.items[*].metadata.name}'); do
    kubectl exec -n "${NAMESPACE}" "${pod}" -- test -f "${STAGED}" 2>/dev/null || missing+=("${pod}")
  done
  if (( ${#missing[@]} == 0 )); then
    echo "IMEX is staged on every node"
    exit 0
  fi
  if (( SECONDS >= deadline )); then
    printf 'WARNING: no IMEX in %s after %ss, so ComputeDomains fail on those nodes. Once IMEX is staged there, restart the DRA kubelet plugin on them.\n' \
      "${missing[*]}" "${TIMEOUT}" >&2
    exit 0
  fi
  sleep 5
done
