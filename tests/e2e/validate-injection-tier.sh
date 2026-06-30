#!/bin/bash
# Copyright 2026 NVIDIA CORPORATION
# SPDX-License-Identifier: Apache-2.0
#
# Validate tiered nvml-mock CDI injection via the mutating admission webhook.
# Requires: nvml-mock Helm release with injector.enabled=true, Kind CDI + nvidia
# runtimeClass, nvidia-container-toolkit on nodes.
#
# Usage: validate-injection-tier.sh <nvml|ib|full>
set -euo pipefail

TIER="${1:?Usage: $0 <nvml|ib|full>}"
case "$TIER" in
  nvml | ib | full) ;;
  *)
    echo "ERROR: unknown tier '$TIER'" >&2
    exit 1
    ;;
esac

POD="injection-tier-${TIER}"
MANIFEST="${MANIFEST:-tests/e2e/injection-test-pods.yaml}"
TIMEOUT="${INJECTION_E2E_TIMEOUT:-180s}"

echo "=== Validating injection tier=$TIER pod=$POD ==="

kubectl delete pod "$POD" --ignore-not-found --wait=false 2>/dev/null || true
kubectl apply -f "$MANIFEST"

if ! kubectl wait --for=condition=Ready "pod/${POD}" --timeout="$TIMEOUT" 2>/dev/null; then
  PHASE=$(kubectl get pod "$POD" -o jsonpath='{.status.phase}' 2>/dev/null || echo "Unknown")
  echo "WARN: pod did not become Ready (phase=$PHASE); checking Succeeded/Failed"
fi

for _ in $(seq 1 60); do
  PHASE=$(kubectl get pod "$POD" -o jsonpath='{.status.phase}' 2>/dev/null || echo "Pending")
  if [ "$PHASE" = "Succeeded" ] || [ "$PHASE" = "Failed" ]; then
    break
  fi
  sleep 2
done

PHASE=$(kubectl get pod "$POD" -o jsonpath='{.status.phase}' 2>/dev/null || echo "Unknown")
echo "--- pod phase: $PHASE ---"
kubectl logs "$POD" 2>&1 || true

INJECTED=$(kubectl get pod "$POD" -o jsonpath='{.metadata.annotations.nvml-mock\.nvidia\.com/injected-tier}' 2>/dev/null || true)
if [ "$INJECTED" != "$TIER" ]; then
  echo "FAIL: expected injected-tier=$TIER got '${INJECTED:-<empty>}'"
  kubectl describe pod "$POD" 2>&1 | tail -40 || true
  exit 1
fi

if [ "$PHASE" != "Succeeded" ]; then
  echo "FAIL: pod $POD phase=$PHASE (expected Succeeded)"
  exit 1
fi

echo "PASS: injection tier $TIER"
kubectl delete -f "$MANIFEST" --ignore-not-found --wait=false
