#!/usr/bin/env bash
# Copyright 2026 NVIDIA CORPORATION
# SPDX-License-Identifier: Apache-2.0
#
# Creates a Kind cluster with gpu-mock installed (A100 profile).
# Usage: ./setup-kind-cluster.sh [--profile a100] [--gpu-count 8] [--dra]
#
# Prerequisites: docker, kind, helm, kubectl
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"

# Defaults
GPU_PROFILE="${GPU_PROFILE:-a100}"
GPU_COUNT="${GPU_COUNT:-8}"
GOLANG_VERSION="${GOLANG_VERSION:-1.25}"
ENABLE_DRA="${ENABLE_DRA:-false}"
CLUSTER_NAME="gpu-mock-poc"
MOCK_NVML_DEBUG="${MOCK_NVML_DEBUG:-1}"

# Parse arguments
while [[ $# -gt 0 ]]; do
  case "$1" in
    --profile) GPU_PROFILE="$2"; shift 2 ;;
    --gpu-count) GPU_COUNT="$2"; shift 2 ;;
    --dra) ENABLE_DRA="true"; shift ;;
    --cluster-name) CLUSTER_NAME="$2"; shift 2 ;;
    --no-debug) MOCK_NVML_DEBUG=""; shift ;;
    *) echo "Unknown flag: $1"; exit 1 ;;
  esac
done

echo "=== GPU Mock PoC Validation ==="
echo "Profile: $GPU_PROFILE"
echo "GPU Count: $GPU_COUNT"
echo "DRA Enabled: $ENABLE_DRA"
echo "Cluster: $CLUSTER_NAME"
echo "Debug: ${MOCK_NVML_DEBUG:+enabled}"
echo ""

# Verify prerequisites
for tool in docker kind helm kubectl; do
  if ! command -v "$tool" &>/dev/null; then
    echo "ERROR: $tool is required but not found" >&2
    exit 1
  fi
done

# Check Docker daemon
if ! docker info &>/dev/null; then
  echo "ERROR: Docker daemon is not running" >&2
  exit 1
fi

# Clean up any existing cluster
if kind get clusters 2>/dev/null | grep -q "^${CLUSTER_NAME}$"; then
  echo "Deleting existing cluster: $CLUSTER_NAME"
  kind delete cluster --name "$CLUSTER_NAME"
fi

# Step 1: Create Kind cluster
echo ""
echo "=== Step 1: Creating Kind cluster ==="
if [ "$ENABLE_DRA" = "true" ]; then
  kind create cluster --name "$CLUSTER_NAME" \
    --config "$REPO_ROOT/tests/e2e/kind-dra-config.yaml"
else
  kind create cluster --name "$CLUSTER_NAME"
fi

# Step 2: Build gpu-mock image
echo ""
echo "=== Step 2: Building gpu-mock image ==="
docker build -t gpu-mock:poc -f "$REPO_ROOT/deployments/gpu-mock/Dockerfile" \
  --build-arg GOLANG_VERSION="$GOLANG_VERSION" \
  "$REPO_ROOT"

# Step 3: Load image into Kind
echo ""
echo "=== Step 3: Loading image into Kind ==="
kind load docker-image gpu-mock:poc --name "$CLUSTER_NAME"

# Step 4: Install gpu-mock Helm chart
echo ""
echo "=== Step 4: Installing gpu-mock Helm chart (profile=$GPU_PROFILE, count=$GPU_COUNT) ==="
helm install gpu-mock "$REPO_ROOT/deployments/gpu-mock/helm/gpu-mock" \
  --set image.repository=gpu-mock \
  --set image.tag=poc \
  --set gpu.profile="$GPU_PROFILE" \
  --set gpu.count="$GPU_COUNT" \
  --wait --timeout 120s

# Step 5: Wait for DaemonSet
echo ""
echo "=== Step 5: Waiting for gpu-mock DaemonSet ==="
kubectl rollout status daemonset/gpu-mock --timeout=60s

# Step 6: Verify mock files on node
echo ""
echo "=== Step 6: Verifying mock files on node ==="
NODE_CONTAINER="${CLUSTER_NAME}-control-plane"
docker exec "$NODE_CONTAINER" test -L /var/lib/nvidia-mock/driver/usr/lib64/libnvidia-ml.so.1
docker exec "$NODE_CONTAINER" test -f /var/lib/nvidia-mock/driver/config/config.yaml
docker exec "$NODE_CONTAINER" test -e /var/lib/nvidia-mock/dev/nvidia0
echo "Mock files verified on node"

# Step 7: Verify node labels
echo ""
echo "=== Step 7: Verifying node labels ==="
NODE=$(kubectl get nodes -o jsonpath='{.items[0].metadata.name}')
LABEL=$(kubectl get node "$NODE" -o jsonpath='{.metadata.labels.nvidia\.com/gpu\.present}')
echo "nvidia.com/gpu.present=$LABEL"
if [ "$LABEL" != "true" ]; then
  echo "WARNING: Node label nvidia.com/gpu.present not set"
fi

echo ""
echo "=== Kind cluster ready ==="
echo "Cluster: $CLUSTER_NAME"
echo "Node container: $NODE_CONTAINER"
echo ""
echo "Next steps:"
echo "  Deploy device plugin: ./deploy-device-plugin.sh"
echo "  Deploy DRA driver:    ./deploy-dra-driver.sh"
