#!/usr/bin/env bash
# Copyright 2026 NVIDIA CORPORATION
# SPDX-License-Identifier: Apache-2.0
#
# Deploys the NVIDIA device plugin against gpu-mock and validates GPU count.
# Must run after setup-kind-cluster.sh.
#
# Usage: ./deploy-device-plugin.sh [--expected-gpus 8]
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"

EXPECTED_GPUS="${EXPECTED_GPUS:-8}"
CLUSTER_NAME="${CLUSTER_NAME:-gpu-mock-poc}"
LOG_DIR="${LOG_DIR:-$SCRIPT_DIR/logs}"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --expected-gpus) EXPECTED_GPUS="$2"; shift 2 ;;
    --cluster-name) CLUSTER_NAME="$2"; shift 2 ;;
    *) echo "Unknown flag: $1"; exit 1 ;;
  esac
done

mkdir -p "$LOG_DIR"

echo "=== Deploying NVIDIA Device Plugin ==="
echo "Expected GPUs: $EXPECTED_GPUS"
echo ""

# Step 1: Deploy device plugin (debug manifest with MOCK_NVML_DEBUG=1)
echo "=== Step 1: Applying device plugin manifest (debug-enabled) ==="
kubectl apply -f "$SCRIPT_DIR/device-plugin-mock-debug.yaml"

echo ""
echo "=== Step 2: Waiting for device plugin pod ==="
kubectl -n kube-system wait --for=condition=ready \
  pod -l name=nvidia-device-plugin-mock --timeout=120s

# Step 3: Capture device plugin logs (with NVML debug traces)
echo ""
echo "=== Step 3: Capturing device plugin startup logs ==="
sleep 5  # Allow time for initial NVML calls
kubectl -n kube-system logs -l name=nvidia-device-plugin-mock --tail=500 \
  > "$LOG_DIR/device-plugin-startup.log" 2>&1 || true
echo "Logs saved to $LOG_DIR/device-plugin-startup.log"

# Step 4: Capture NVML debug traces from gpu-mock pod
echo ""
echo "=== Step 4: Capturing NVML debug traces from gpu-mock ==="
# The MOCK_NVML_DEBUG traces go to stderr of the process that loaded libnvidia-ml.so.
# For device plugin, traces appear in the device plugin container's stderr.
# We also check the gpu-mock pod for any traces from setup.
kubectl logs -l app.kubernetes.io/name=gpu-mock --tail=200 \
  > "$LOG_DIR/gpu-mock-pod.log" 2>&1 || true

# Step 5: Verify allocatable GPUs
echo ""
echo "=== Step 5: Verifying allocatable GPUs ==="
NODE=$(kubectl get nodes -o jsonpath='{.items[0].metadata.name}')
for i in $(seq 1 30); do
  GPUS=$(kubectl get node "$NODE" -o jsonpath='{.status.allocatable.nvidia\.com/gpu}')
  if [ "$GPUS" = "$EXPECTED_GPUS" ]; then
    echo "PASS: Node reports $GPUS allocatable GPUs"

    # Save final state
    kubectl get node "$NODE" -o json > "$LOG_DIR/node-status.json"
    kubectl -n kube-system logs -l name=nvidia-device-plugin-mock --tail=1000 \
      > "$LOG_DIR/device-plugin-full.log" 2>&1 || true

    echo ""
    echo "=== Device Plugin Validation PASSED ==="
    echo "  Allocatable GPUs: $GPUS"
    echo "  Logs: $LOG_DIR/"
    exit 0
  fi
  echo "Attempt $i/30: allocatable GPUs=$GPUS (expected $EXPECTED_GPUS), waiting..."
  sleep 2
done

echo "FAIL: Expected $EXPECTED_GPUS allocatable GPUs, got ${GPUS:-none}"

# Collect debug info on failure
echo ""
echo "=== Collecting failure debug info ==="
kubectl -n kube-system describe pod -l name=nvidia-device-plugin-mock \
  > "$LOG_DIR/device-plugin-describe.log" 2>&1 || true
kubectl -n kube-system logs -l name=nvidia-device-plugin-mock --tail=1000 \
  > "$LOG_DIR/device-plugin-full.log" 2>&1 || true
kubectl describe nodes > "$LOG_DIR/node-describe.log" 2>&1 || true

echo "Debug logs saved to $LOG_DIR/"
exit 1
