#!/bin/bash
# Copyright 2026 NVIDIA CORPORATION
# SPDX-License-Identifier: Apache-2.0
#
# Validate dcgm-exporter metrics served from the mock NVML in a Kind cluster.
# Assumes the GPU Operator is installed with dcgmExporter.enabled=true
# (tests/e2e/gpu-operator-values.yaml) and kubectl points at the cluster.
# Usage: validate-dcgm-metrics.sh <expected-gpu-name> <expected-gpu-count> [profile]
#
# The third argument selects the profile family: on Hopper+ profiles (h100,
# b200, gb200, gb300) the mock GPM implementation must additionally serve
# profiling metrics (DCGM_FI_PROF_*).
# Set EXPECT_PROF_PCIE_POSITIVE=1 when the selected test profile/collector
# override drives nonzero GPM PCIe throughput.
# Set EXPECT_TIME_VARYING=1 when the mock runs with dynamic metrics enabled, to
# additionally assert that DCGM_FI_DEV_* readings change over time (issue #370).
set -euo pipefail

GPU_NAME="${1:?Usage: $0 <gpu-name> <gpu-count> [profile]}"
GPU_COUNT="${2:?}"
PROFILE="${3:-}"

NAMESPACE=${NAMESPACE:-gpu-operator}
EXPECT_PROF_PCIE_POSITIVE=${EXPECT_PROF_PCIE_POSITIVE:-0}
EXPECT_TIME_VARYING=${EXPECT_TIME_VARYING:-0}

echo "=== Validating dcgm-exporter metrics (namespace $NAMESPACE) ==="

echo "--- Waiting for dcgm-exporter pod ---"
kubectl -n "$NAMESPACE" wait pod -l app=nvidia-dcgm-exporter \
  --for=condition=Ready --timeout=180s

POD=$(kubectl -n "$NAMESPACE" get pods -l app=nvidia-dcgm-exporter \
  -o jsonpath='{.items[0].metadata.name}')

# First samples appear one collect interval after the watches are set up;
# poll instead of a fixed sleep. The exporter image has no curl/wget, so
# scrape through the Prometheus port with a temporary port-forward.
kubectl -n "$NAMESPACE" port-forward "pod/$POD" 19400:9400 >/dev/null 2>&1 &
PF_PID=$!
trap 'kill $PF_PID 2>/dev/null || true' EXIT

METRICS=""
for _ in $(seq 1 30); do
  METRICS=$(curl -sf http://127.0.0.1:19400/metrics 2>/dev/null || true)
  if grep -q '^DCGM_FI_DEV_GPU_TEMP' <<< "$METRICS"; then
    break
  fi
  sleep 2
done

if ! grep -q '^DCGM_FI_DEV_GPU_TEMP' <<< "$METRICS"; then
  echo "FAIL: no DCGM_FI_DEV_GPU_TEMP samples after 60s"
  kubectl -n "$NAMESPACE" logs "$POD" --tail=40 || true
  exit 1
fi

fail() { echo "FAIL: $*"; exit 1; }
pass() { echo "PASS: $*"; }

# One temperature sample per GPU, each within a plausible range.
TEMP_LINES=$(grep -c '^DCGM_FI_DEV_GPU_TEMP{' <<< "$METRICS")
[ "$TEMP_LINES" -eq "$GPU_COUNT" ] \
  || fail "DCGM_FI_DEV_GPU_TEMP: $TEMP_LINES samples (expected $GPU_COUNT)"
pass "DCGM_FI_DEV_GPU_TEMP present for all $GPU_COUNT GPUs"

# The value is the last field: label values contain spaces (modelName="NVIDIA
# H100 80GB HBM3"), so positional fields after -F' ' land inside the labels.
awk '/^DCGM_FI_DEV_GPU_TEMP\{/ { v = $NF + 0; if (v < 20 || v > 100) exit 1 }' <<< "$METRICS" \
  || fail "DCGM_FI_DEV_GPU_TEMP out of plausible range [20,100]"
pass "DCGM_FI_DEV_GPU_TEMP values plausible"

awk '/^DCGM_FI_DEV_POWER_USAGE\{/ { if ($NF + 0 <= 0) exit 1 }' <<< "$METRICS" \
  || fail "DCGM_FI_DEV_POWER_USAGE not positive"
pass "DCGM_FI_DEV_POWER_USAGE positive"

grep -q "modelName=\"$GPU_NAME\"" <<< "$METRICS" \
  || fail "modelName label '$GPU_NAME' not found"
pass "modelName label matches '$GPU_NAME'"

# The exporter's default counter CSV enables only the PCIe pair of the PROF
# family; the rest are commented out upstream. PROF fields warm up a few
# collect intervals after the DEV fields, so poll again if needed.
case "$PROFILE" in
  h100|b200|gb200|gb300)
    echo "--- Hopper+ profile ($PROFILE): checking profiling metrics (GPM) ---"
    for _ in $(seq 1 30); do
      grep -q '^DCGM_FI_PROF_PCIE_TX_BYTES{' <<< "$METRICS" && break
      sleep 2
      METRICS=$(curl -sf http://127.0.0.1:19400/metrics 2>/dev/null || true)
    done
    grep -q '^DCGM_FI_PROF_PCIE_TX_BYTES{' <<< "$METRICS" \
      || fail "DCGM_FI_PROF_PCIE_TX_BYTES missing (GPM path broken)"
    PROF_LINES=$(grep -c '^DCGM_FI_PROF_PCIE_TX_BYTES{' <<< "$METRICS")
    [ "$PROF_LINES" -eq "$GPU_COUNT" ] \
      || fail "DCGM_FI_PROF_PCIE_TX_BYTES: $PROF_LINES samples (expected $GPU_COUNT)"
    pass "DCGM_FI_PROF_PCIE_TX_BYTES present for all $GPU_COUNT GPUs (GPM live)"
    if [ "$EXPECT_PROF_PCIE_POSITIVE" = "1" ]; then
      awk '/^DCGM_FI_PROF_PCIE_TX_BYTES\{/ { seen = 1; if ($NF + 0 <= 0) bad = 1 } END { exit (seen && !bad) ? 0 : 1 }' <<< "$METRICS" \
        || fail "DCGM_FI_PROF_PCIE_TX_BYTES not positive"
      pass "DCGM_FI_PROF_PCIE_TX_BYTES positive"
    fi
    ;;
  *)
    echo "--- Pre-Hopper profile ('$PROFILE'): PROF metrics intentionally absent ---"
    ;;
esac

# Time-varying DEV metrics (issue #370): with dynamic metrics enabled the mock
# returns readings that change over time. Power draw carries per-sample
# variance, so two scrapes at least one collect interval apart must differ for
# at least one GPU. Poll instead of a single sleep so a rare equal pair on one
# interval doesn't flake the check.
if [ "$EXPECT_TIME_VARYING" = "1" ]; then
  echo "--- Checking time-varying DCGM_FI_DEV_POWER_USAGE ---"
  power_samples() {
    curl -sf http://127.0.0.1:19400/metrics 2>/dev/null \
      | awk '/^DCGM_FI_DEV_POWER_USAGE\{/ { printf "%s ", $NF }'
  }
  BASELINE=$(power_samples)
  [ -n "$BASELINE" ] || fail "DCGM_FI_DEV_POWER_USAGE not present for time-varying check"
  CHANGED=0
  for _ in $(seq 1 30); do
    sleep 6
    CURRENT=$(power_samples)
    if [ -n "$CURRENT" ] && [ "$CURRENT" != "$BASELINE" ]; then
      CHANGED=1
      break
    fi
  done
  [ "$CHANGED" -eq 1 ] || fail "DCGM_FI_DEV_POWER_USAGE did not vary over time"
  pass "DCGM_FI_DEV_POWER_USAGE varies over time"
fi

echo ""
echo "=== dcgm-exporter validation PASSED ==="
