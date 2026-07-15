#!/bin/bash
# Copyright 2026 NVIDIA CORPORATION
# SPDX-License-Identifier: Apache-2.0
#
# Validate that a mock-injected Xid critical error surfaces through
# dcgm-exporter as DCGM_FI_DEV_XID_ERRORS (issue #370 failure-injection E2E).
#
# The mock delivers the configured Xid through the NVML event set
# (NVML_EVENT_TYPE_XID_CRITICAL_ERROR); DCGM's cache manager watches XID by
# default (DCGM_FI_DEV_XID_ERRORS is in dcgm-exporter's default-counters.csv)
# and records the last error code, which the exporter then exposes.
#
# Flow:
#   1. helm upgrade nvml-mock enabling failure injection (ecc_uncorrectable +
#      xid.code) so the device keeps answering NVML calls (stays scrapable)
#      while emitting the Xid event.
#   2. Roll the nvml-mock DaemonSet so setup.sh rewrites the on-host mock
#      config that dcgm-exporter's libnvidia-ml.so auto-discovers.
#   3. Restart dcgm-exporter so it reloads the mock library with the failure
#      config.
#   4. Scrape /metrics and assert DCGM_FI_DEV_XID_ERRORS reports the code.
#
# Usage: validate-dcgm-xid.sh <expected-gpu-count> [xid-code]
set -euo pipefail

GPU_COUNT="${1:?Usage: $0 <gpu-count> [xid-code]}"
XID_CODE="${2:-79}"

NAMESPACE=${NAMESPACE:-gpu-operator}
CHART_PATH=${CHART_PATH:-deployments/nvml-mock/helm/nvml-mock}

fail() { echo "FAIL: $*"; exit 1; }
pass() { echo "PASS: $*"; }

echo "=== Validating Xid failure injection surfaces DCGM_FI_DEV_XID_ERRORS ==="

# 1. Enable failure injection. --reuse-values keeps the healthy-path overrides
# (image, profile, dynamic metrics) already set at install time; after_calls=1
# trips on the first guarded NVML call, seed=1 keeps it deterministic. ECC
# uncorrectable mode returns SUCCESS from getters so the device stays
# enumerable and scrapable while the Xid event fires.
echo "--- Enabling failure injection (ecc_uncorrectable, xid=$XID_CODE) ---"
helm upgrade nvml-mock "$CHART_PATH" \
  --reuse-values \
  --set gpu.failureInjection.enabled=true \
  --set gpu.failureInjection.mode=ecc_uncorrectable \
  --set gpu.failureInjection.after_calls=1 \
  --set gpu.failureInjection.seed=1 \
  --set gpu.failureInjection.xid.code="$XID_CODE" \
  --wait --timeout 120s

# 2. Force the DaemonSet to re-run setup.sh so the on-host config the exporter
# reads is rewritten with the failure block (the checksum annotation already
# rolls on config change; restart is belt-and-suspenders + waits for ready).
echo "--- Rolling nvml-mock DaemonSet ---"
kubectl rollout restart daemonset/nvml-mock
kubectl rollout status daemonset/nvml-mock --timeout=120s

# 3. Restart dcgm-exporter so its embedded hostengine reloads the mock library
# and picks up the failure config.
echo "--- Restarting dcgm-exporter ---"
kubectl -n "$NAMESPACE" rollout restart daemonset/nvidia-dcgm-exporter
kubectl -n "$NAMESPACE" rollout status daemonset/nvidia-dcgm-exporter --timeout=180s
# The GPU operator reconciles the dcgm-exporter DaemonSet right after the
# restart, so there is a brief window with no pods matching the selector. A
# bare `kubectl wait` errors out with "no matching resources found" during
# that window; poll until a pod is actually Ready instead.
READY=0
for _ in $(seq 1 30); do
  if kubectl -n "$NAMESPACE" wait pod -l app=nvidia-dcgm-exporter \
      --for=condition=Ready --timeout=10s >/dev/null 2>&1; then
    READY=1
    break
  fi
  sleep 2
done
[ "$READY" -eq 1 ] || fail "dcgm-exporter pod did not become Ready after restart"

POD=$(kubectl -n "$NAMESPACE" get pods -l app=nvidia-dcgm-exporter \
  -o jsonpath='{.items[0].metadata.name}')

# 4. Scrape and assert. The exporter image has no curl/wget, so port-forward
# to the Prometheus port. The Xid event fires once the device trips on the
# first guarded call; DCGM caches the last XID, so it appears within a few
# collect intervals and then persists.
kubectl -n "$NAMESPACE" port-forward "pod/$POD" 19401:9400 >/dev/null 2>&1 &
PF_PID=$!
trap 'kill $PF_PID 2>/dev/null || true' EXIT

METRICS=""
FOUND=0
for _ in $(seq 1 45); do
  METRICS=$(curl -sf http://127.0.0.1:19401/metrics 2>/dev/null || true)
  # A GPU with the injected Xid reports the code as the metric value; the
  # healthy default is 0. Require at least one GPU line at the expected code.
  if awk -v code="$XID_CODE" \
    '/^DCGM_FI_DEV_XID_ERRORS\{/ { if (($NF + 0) == code) found = 1 } END { exit found ? 0 : 1 }' \
    <<< "$METRICS"; then
    FOUND=1
    break
  fi
  sleep 4
done

if [ "$FOUND" -ne 1 ]; then
  echo "--- DCGM_FI_DEV_XID_ERRORS samples ---"
  grep '^DCGM_FI_DEV_XID_ERRORS{' <<< "$METRICS" || echo "(none)"
  kubectl -n "$NAMESPACE" logs "$POD" --tail=40 || true
  fail "DCGM_FI_DEV_XID_ERRORS did not report injected Xid $XID_CODE"
fi
pass "DCGM_FI_DEV_XID_ERRORS reports injected Xid $XID_CODE"

echo ""
echo "=== dcgm-exporter Xid failure-injection validation PASSED ==="
