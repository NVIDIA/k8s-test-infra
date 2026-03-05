#!/usr/bin/env bash
# Copyright 2026 NVIDIA CORPORATION
# SPDX-License-Identifier: Apache-2.0
#
# End-to-end PoC validation: creates Kind cluster, deploys gpu-mock,
# tests both device plugin and DRA driver, captures NVML traces.
#
# Usage: ./run-all.sh [--profile a100] [--gpu-count 8]
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

GPU_PROFILE="${GPU_PROFILE:-a100}"
GPU_COUNT="${GPU_COUNT:-8}"
export CLUSTER_NAME="gpu-mock-poc"
export LOG_DIR="$SCRIPT_DIR/logs"
export EXPECTED_GPUS="$GPU_COUNT"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --profile) GPU_PROFILE="$2"; shift 2 ;;
    --gpu-count) GPU_COUNT="$2"; EXPECTED_GPUS="$GPU_COUNT"; shift 2 ;;
    *) echo "Unknown flag: $1"; exit 1 ;;
  esac
done

RESULTS_FILE="$LOG_DIR/results.txt"
mkdir -p "$LOG_DIR"

pass() { echo "PASS: $1" | tee -a "$RESULTS_FILE"; }
fail() { echo "FAIL: $1" | tee -a "$RESULTS_FILE"; }

echo "=== Full PoC Validation Run ==="
echo "Profile: $GPU_PROFILE, GPUs: $GPU_COUNT"
echo "Logs: $LOG_DIR"
echo ""
> "$RESULTS_FILE"

cleanup() {
  echo ""
  echo "=== Cleanup ==="
  kind delete cluster --name "$CLUSTER_NAME" 2>/dev/null || true
}
trap cleanup EXIT

# -------------------------------------------------------
# Phase 1: Device Plugin validation (standard Kind cluster)
# -------------------------------------------------------
echo "=========================================="
echo "Phase 1: Device Plugin on standard Kind"
echo "=========================================="

GPU_PROFILE="$GPU_PROFILE" GPU_COUNT="$GPU_COUNT" \
  "$SCRIPT_DIR/setup-kind-cluster.sh" --profile "$GPU_PROFILE" --gpu-count "$GPU_COUNT"

if "$SCRIPT_DIR/deploy-device-plugin.sh" --expected-gpus "$GPU_COUNT"; then
  pass "Device Plugin reports $GPU_COUNT GPUs"
else
  fail "Device Plugin GPU count"
fi

# Capture device plugin traces
"$SCRIPT_DIR/capture-nvml-traces.sh" --output-dir "$LOG_DIR" || true

# Tear down for Phase 2
kind delete cluster --name "$CLUSTER_NAME"

# -------------------------------------------------------
# Phase 2: DRA Driver validation (DRA-enabled Kind cluster)
# -------------------------------------------------------
echo ""
echo "=========================================="
echo "Phase 2: DRA Driver on DRA-enabled Kind"
echo "=========================================="

GPU_PROFILE="$GPU_PROFILE" GPU_COUNT="$GPU_COUNT" \
  "$SCRIPT_DIR/setup-kind-cluster.sh" --profile "$GPU_PROFILE" --gpu-count "$GPU_COUNT" --dra

if "$SCRIPT_DIR/deploy-dra-driver.sh" --expected-gpus "$GPU_COUNT"; then
  pass "DRA Driver publishes $GPU_COUNT GPUs in ResourceSlices"
else
  fail "DRA Driver ResourceSlice GPU count"
fi

# Capture DRA traces
"$SCRIPT_DIR/capture-nvml-traces.sh" --output-dir "$LOG_DIR" || true

# -------------------------------------------------------
# Summary
# -------------------------------------------------------
echo ""
echo "=========================================="
echo "PoC Validation Results"
echo "=========================================="
cat "$RESULTS_FILE"
echo ""
echo "NVML trace summary: $LOG_DIR/nvml-trace-summary.md"
echo "Full logs: $LOG_DIR/"

if grep -q "FAIL" "$RESULTS_FILE"; then
  echo ""
  echo "Some validations FAILED. See logs for details."
  exit 1
fi

echo ""
echo "All validations PASSED."
