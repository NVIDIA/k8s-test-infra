#!/usr/bin/env bash
# Copyright 2026 NVIDIA CORPORATION
# SPDX-License-Identifier: Apache-2.0
#
# Captures NVML call traces from device plugin and DRA driver containers.
# MOCK_NVML_DEBUG=1 must be set in the consumer containers (not gpu-mock).
#
# The gpu-mock chart deploys libnvidia-ml.so to the host. When device plugin
# or DRA driver loads this .so, debug traces go to that consumer's stderr.
#
# Usage: ./capture-nvml-traces.sh [--output-dir ./logs]
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
OUTPUT_DIR="${OUTPUT_DIR:-$SCRIPT_DIR/logs}"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --output-dir) OUTPUT_DIR="$2"; shift 2 ;;
    *) echo "Unknown flag: $1"; exit 1 ;;
  esac
done

mkdir -p "$OUTPUT_DIR"

echo "=== Capturing NVML Call Traces ==="
echo "Output: $OUTPUT_DIR"
echo ""

# Capture device plugin traces
echo "--- Device Plugin logs ---"
if kubectl -n kube-system get pods -l name=nvidia-device-plugin-mock --no-headers 2>/dev/null | grep -q Running; then
  kubectl -n kube-system logs -l name=nvidia-device-plugin-mock --tail=5000 \
    > "$OUTPUT_DIR/device-plugin-trace-raw.log" 2>&1

  # Extract NVML stub/bridge calls
  grep -E '\[NVML' "$OUTPUT_DIR/device-plugin-trace-raw.log" \
    > "$OUTPUT_DIR/device-plugin-nvml-calls.log" 2>/dev/null || true

  STUB_COUNT=$(grep -c '\[NVML-STUB\]' "$OUTPUT_DIR/device-plugin-nvml-calls.log" 2>/dev/null || echo 0)
  IMPL_COUNT=$(grep -c '\[NVML\]' "$OUTPUT_DIR/device-plugin-nvml-calls.log" 2>/dev/null || echo 0)
  echo "  Stub calls: $STUB_COUNT"
  echo "  Implemented calls: $IMPL_COUNT"

  # Extract unique function names called
  grep -oP '\[NVML(?:-STUB)?\]\s+\K\S+' "$OUTPUT_DIR/device-plugin-nvml-calls.log" \
    | sed 's/ called.*//' | sort -u \
    > "$OUTPUT_DIR/device-plugin-functions-called.txt" 2>/dev/null || true
  echo "  Unique functions: $(wc -l < "$OUTPUT_DIR/device-plugin-functions-called.txt" | tr -d ' ')"
else
  echo "  Device plugin not running, skipping"
fi

echo ""

# Capture DRA driver traces
echo "--- DRA Driver logs ---"
if kubectl -n nvidia get pods -l app.kubernetes.io/name=nvidia-dra-driver-gpu --no-headers 2>/dev/null | grep -q Running; then
  kubectl -n nvidia logs -l app.kubernetes.io/name=nvidia-dra-driver-gpu --all-containers --tail=5000 \
    > "$OUTPUT_DIR/dra-driver-trace-raw.log" 2>&1

  # Extract NVML stub/bridge calls
  grep -E '\[NVML' "$OUTPUT_DIR/dra-driver-trace-raw.log" \
    > "$OUTPUT_DIR/dra-driver-nvml-calls.log" 2>/dev/null || true

  STUB_COUNT=$(grep -c '\[NVML-STUB\]' "$OUTPUT_DIR/dra-driver-nvml-calls.log" 2>/dev/null || echo 0)
  IMPL_COUNT=$(grep -c '\[NVML\]' "$OUTPUT_DIR/dra-driver-nvml-calls.log" 2>/dev/null || echo 0)
  echo "  Stub calls: $STUB_COUNT"
  echo "  Implemented calls: $IMPL_COUNT"

  grep -oP '\[NVML(?:-STUB)?\]\s+\K\S+' "$OUTPUT_DIR/dra-driver-nvml-calls.log" \
    | sed 's/ called.*//' | sort -u \
    > "$OUTPUT_DIR/dra-driver-functions-called.txt" 2>/dev/null || true
  echo "  Unique functions: $(wc -l < "$OUTPUT_DIR/dra-driver-functions-called.txt" | tr -d ' ')"

  # Also capture kubelet-plugin specifically
  PLUGIN_POD=$(kubectl -n nvidia get pods --no-headers -o custom-columns=':metadata.name' | grep kubelet-plugin | head -1)
  if [ -n "$PLUGIN_POD" ]; then
    kubectl -n nvidia logs "$PLUGIN_POD" --all-containers --tail=5000 \
      > "$OUTPUT_DIR/dra-kubelet-plugin-trace.log" 2>&1 || true
  fi
else
  echo "  DRA driver not running, skipping"
fi

echo ""

# Generate summary report
echo "=== Generating trace summary ==="
{
  echo "# NVML Call Trace Summary"
  echo "# Generated: $(date -u +%Y-%m-%dT%H:%M:%SZ)"
  echo ""

  if [ -f "$OUTPUT_DIR/device-plugin-functions-called.txt" ]; then
    echo "## Device Plugin (v0.18.2)"
    echo ""
    echo "### Functions Called"
    echo ""
    while IFS= read -r func; do
      if grep -q "\[NVML-STUB\] $func" "$OUTPUT_DIR/device-plugin-nvml-calls.log" 2>/dev/null; then
        echo "- [ ] $func (NOT_SUPPORTED - stub)"
      else
        echo "- [x] $func (implemented)"
      fi
    done < "$OUTPUT_DIR/device-plugin-functions-called.txt"
    echo ""
  fi

  if [ -f "$OUTPUT_DIR/dra-driver-functions-called.txt" ]; then
    echo "## DRA Driver (v0.10.x)"
    echo ""
    echo "### Functions Called"
    echo ""
    while IFS= read -r func; do
      if grep -q "\[NVML-STUB\] $func" "$OUTPUT_DIR/dra-driver-nvml-calls.log" 2>/dev/null; then
        echo "- [ ] $func (NOT_SUPPORTED - stub)"
      else
        echo "- [x] $func (implemented)"
      fi
    done < "$OUTPUT_DIR/dra-driver-functions-called.txt"
    echo ""
  fi

  # Combined unique functions for prioritization
  echo "## Combined: All NVML Functions Called by Consumers"
  echo ""
  cat "$OUTPUT_DIR/"*-functions-called.txt 2>/dev/null | sort -u | while IFS= read -r func; do
    echo "- $func"
  done
  echo ""

  echo "## Stub Functions (Need Implementation for Full Support)"
  echo ""
  echo "These functions returned NOT_SUPPORTED and may need implementation:"
  echo ""
  grep '\[NVML-STUB\]' "$OUTPUT_DIR/"*-nvml-calls.log 2>/dev/null \
    | grep -oP '\[NVML-STUB\]\s+\K\S+' | sed 's/ called.*//' | sort -u | while IFS= read -r func; do
    echo "- $func"
  done
} > "$OUTPUT_DIR/nvml-trace-summary.md"

echo "Summary written to $OUTPUT_DIR/nvml-trace-summary.md"
echo ""
echo "=== Trace capture complete ==="
