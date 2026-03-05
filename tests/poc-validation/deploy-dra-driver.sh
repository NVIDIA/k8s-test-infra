#!/usr/bin/env bash
# Copyright 2026 NVIDIA CORPORATION
# SPDX-License-Identifier: Apache-2.0
#
# Deploys the NVIDIA DRA driver against gpu-mock and validates ResourceSlices.
# Must run after setup-kind-cluster.sh with --dra flag.
#
# Usage: ./deploy-dra-driver.sh [--expected-gpus 8]
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LOG_DIR="${LOG_DIR:-$SCRIPT_DIR/logs}"

EXPECTED_GPUS="${EXPECTED_GPUS:-8}"
CLUSTER_NAME="${CLUSTER_NAME:-gpu-mock-poc}"
DRA_CHART_VERSION="${DRA_CHART_VERSION:-}"  # Empty = latest

while [[ $# -gt 0 ]]; do
  case "$1" in
    --expected-gpus) EXPECTED_GPUS="$2"; shift 2 ;;
    --cluster-name) CLUSTER_NAME="$2"; shift 2 ;;
    --chart-version) DRA_CHART_VERSION="$2"; shift 2 ;;
    *) echo "Unknown flag: $1"; exit 1 ;;
  esac
done

mkdir -p "$LOG_DIR"

echo "=== Deploying NVIDIA DRA Driver ==="
echo "Expected GPUs: $EXPECTED_GPUS"
echo ""

# Step 1: Add NVIDIA Helm repo
echo "=== Step 1: Adding NVIDIA Helm repo ==="
helm repo add nvidia https://helm.ngc.nvidia.com/nvidia 2>/dev/null || true
helm repo update

# Step 2: Install DRA driver
echo ""
echo "=== Step 2: Installing DRA driver ==="
VERSION_FLAG=""
if [ -n "$DRA_CHART_VERSION" ]; then
  VERSION_FLAG="--version $DRA_CHART_VERSION"
fi

# shellcheck disable=SC2086
helm install nvidia-dra-driver nvidia/nvidia-dra-driver-gpu \
  --namespace nvidia \
  --create-namespace \
  --set nvidiaDriverRoot=/var/lib/nvidia-mock/driver \
  --set gpuResourcesEnabledOverride=true \
  --set resources.computeDomains.enabled=false \
  $VERSION_FLAG \
  --wait --timeout 180s

# Step 3: Wait for DRA pods
echo ""
echo "=== Step 3: Waiting for DRA pods ==="
kubectl -n nvidia wait --for=condition=ready pod --all --timeout=120s

# Step 4: Capture DRA driver logs
echo ""
echo "=== Step 4: Capturing DRA driver logs ==="
sleep 5  # Allow time for initial NVML discovery
kubectl -n nvidia logs -l app.kubernetes.io/name=nvidia-dra-driver-gpu --tail=500 \
  > "$LOG_DIR/dra-driver-startup.log" 2>&1 || true

# Capture kubelet-plugin logs specifically (this is where NVML calls happen)
PLUGIN_POD=$(kubectl -n nvidia get pods --no-headers -o custom-columns=':metadata.name' | grep kubelet-plugin | head -1)
if [ -n "$PLUGIN_POD" ]; then
  kubectl -n nvidia logs "$PLUGIN_POD" --all-containers --tail=500 \
    > "$LOG_DIR/dra-kubelet-plugin.log" 2>&1 || true
  echo "DRA kubelet-plugin logs saved"
fi

# Step 5: Verify ResourceSlices
echo ""
echo "=== Step 5: Verifying ResourceSlices ==="
for i in $(seq 1 30); do
  COUNT=$(kubectl get resourceslices -o json | \
    jq '[.items[].spec.devices // [] | length] | add // 0')
  if [ "$COUNT" = "$EXPECTED_GPUS" ]; then
    echo "PASS: ResourceSlice reports $COUNT GPUs"

    # Save ResourceSlice details
    kubectl get resourceslices -o yaml > "$LOG_DIR/resourceslices.yaml"
    kubectl get resourceslices -o json > "$LOG_DIR/resourceslices.json"

    # Extract GPU UUIDs from ResourceSlices
    echo ""
    echo "=== GPU UUIDs in ResourceSlices ==="
    jq -r '.items[].spec.devices[]?.basic?.attributes?.uuid?.StringValue // empty' \
      "$LOG_DIR/resourceslices.json" 2>/dev/null || \
    jq -r '.items[].spec.devices[]? | .name // "unnamed"' \
      "$LOG_DIR/resourceslices.json"

    # Save final DRA logs
    kubectl -n nvidia logs -l app.kubernetes.io/name=nvidia-dra-driver-gpu --tail=2000 \
      > "$LOG_DIR/dra-driver-full.log" 2>&1 || true

    echo ""
    echo "=== DRA Driver Validation PASSED ==="
    echo "  ResourceSlice GPUs: $COUNT"
    echo "  Logs: $LOG_DIR/"
    exit 0
  fi
  echo "Attempt $i/30: ResourceSlice GPUs=$COUNT (expected $EXPECTED_GPUS), waiting..."
  sleep 2
done

echo "FAIL: Expected $EXPECTED_GPUS GPUs in ResourceSlice, got ${COUNT:-0}"

# Collect debug info on failure
echo ""
echo "=== Collecting failure debug info ==="
kubectl -n nvidia get pods -o wide > "$LOG_DIR/dra-pods.log" 2>&1 || true
kubectl -n nvidia describe pods > "$LOG_DIR/dra-pods-describe.log" 2>&1 || true
kubectl -n nvidia logs -l app.kubernetes.io/name=nvidia-dra-driver-gpu --tail=2000 \
  > "$LOG_DIR/dra-driver-full.log" 2>&1 || true
kubectl get resourceslices -o yaml > "$LOG_DIR/resourceslices.yaml" 2>&1 || true
kubectl describe nodes > "$LOG_DIR/node-describe.log" 2>&1 || true

echo "Debug logs saved to $LOG_DIR/"
exit 1
