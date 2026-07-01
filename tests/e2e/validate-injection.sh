#!/bin/bash
# Copyright 2026 NVIDIA CORPORATION
# SPDX-License-Identifier: Apache-2.0
set -euo pipefail

apply() { kubectl apply -f tests/e2e/injection-test-pods.yaml; }
cleanup() { kubectl delete -f tests/e2e/injection-test-pods.yaml --ignore-not-found >/dev/null 2>&1 || true; }
trap cleanup EXIT

expect_phase() {
  local pod="$1" want="$2"
  kubectl wait --for=jsonpath='{.status.phase}'="$want" "pod/$pod" --timeout=120s 2>/dev/null || true
  local got
  got=$(kubectl get pod "$pod" -o jsonpath='{.status.phase}')
  if [ "$got" != "$want" ]; then
    echo "FAIL: $pod phase=$got want=$want"; kubectl logs "$pod" || true; exit 1
  fi
  echo "PASS: $pod ($got)"
}

apply
expect_phase injection-ambient Succeeded
expect_phase injection-optout  Succeeded
expect_phase injection-devices Succeeded

# The ambient pod must carry the injected marker; the opt-out pod must not.
inj=$(kubectl get pod injection-ambient -o jsonpath='{.metadata.annotations.nvml-mock\.nvidia\.com/injected}')
[ "$inj" = "true" ] || { echo "FAIL: ambient pod missing injected marker"; exit 1; }
out=$(kubectl get pod injection-optout -o jsonpath='{.metadata.annotations.nvml-mock\.nvidia\.com/injected}')
[ -z "$out" ] || { echo "FAIL: opt-out pod was injected"; exit 1; }
echo "PASS: injection markers correct"
