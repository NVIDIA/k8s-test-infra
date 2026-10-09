#!/usr/bin/env bash
# Copyright 2026 NVIDIA CORPORATION
# SPDX-License-Identifier: Apache-2.0
#
# Block until the GPU Operator's ClusterPolicy is ready and every pod in its
# namespace is healthy: Running with all containers Ready, or Succeeded.
#
# The operator creates its operands (device plugin, GFD, validator) after
# `helm install` returns, so Tilt's helm_resource readiness only covers the
# operator pod itself. The ClusterPolicy alone is not enough either: it can
# report ready while a pod it does not own, such as an NFD worker, is still
# crash-looping.
#
# Usage: wait-gpu-operator.sh [namespace] [timeout-seconds]
set -euo pipefail

NAMESPACE="${1:-gpu-operator}"
TIMEOUT_S="${2:-600}"

# Prints the pods that are neither Succeeded nor Running with every container
# Ready, one per line; prints nothing when all of them are healthy.
unhealthy_pods() {
  kubectl -n "$NAMESPACE" get pods \
    -o jsonpath='{range .items[*]}{.metadata.name} {.status.phase} {range .status.containerStatuses[*]}{.ready},{end}{"\n"}{end}' |
    awk '$2 == "Succeeded" { next }
         $2 != "Running" || $3 == "" || $3 ~ /false/ { print $1 " " $2 }'
}

deadline=$(( $(date +%s) + TIMEOUT_S ))
while :; do
  state=$(kubectl get clusterpolicy -o jsonpath='{.items[0].status.state}' 2>/dev/null || true)
  pods=$(kubectl -n "$NAMESPACE" get pods -o name 2>/dev/null | wc -l | tr -d ' ')
  # Before the namespace has pods the unhealthy list is trivially empty.
  if [[ "$state" == "ready" && "$pods" -gt 0 ]]; then
    bad=$(unhealthy_pods)
    if [[ -z "$bad" ]]; then
      echo "GPU Operator: ClusterPolicy ready, $pods pods healthy in $NAMESPACE"
      break
    fi
  fi
  if (( $(date +%s) >= deadline )); then
    echo "timed out after ${TIMEOUT_S}s: ClusterPolicy state=${state:-<missing>}" >&2
    kubectl -n "$NAMESPACE" get pods -o wide >&2 || true
    exit 1
  fi
  sleep 5
done
