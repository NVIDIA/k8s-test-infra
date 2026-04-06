#!/usr/bin/env bash
# Copyright 2026 NVIDIA CORPORATION
#
# SPDX-License-Identifier: Apache-2.0

set -euo pipefail

###############################################################################
# Configuration
###############################################################################
CLUSTER_NAME="nvml-mock-demo"
IMAGE_NAME="nvml-mock:demo"
CHART_PATH="deployments/nvml-mock/helm/nvml-mock"
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"

###############################################################################
# Helpers
###############################################################################
info() { echo "==> $*"; }
fail() { echo "ERROR: $*" >&2; exit 1; }

###############################################################################
# Step 1 -- Create a 3-node Kind cluster
###############################################################################
info "Creating Kind cluster: ${CLUSTER_NAME}"
cat <<EOF | kind create cluster --name "${CLUSTER_NAME}" --config=-
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
nodes:
  - role: control-plane
  - role: worker
  - role: worker
EOF

###############################################################################
# Step 2 -- Build the nvml-mock image
###############################################################################
info "Building image: ${IMAGE_NAME}"
docker build -t "${IMAGE_NAME}" -f "${REPO_ROOT}/deployments/nvml-mock/Dockerfile" "${REPO_ROOT}"

###############################################################################
# Step 3 -- Load image into Kind
###############################################################################
info "Loading image into Kind cluster"
kind load docker-image "${IMAGE_NAME}" --name "${CLUSTER_NAME}"

###############################################################################
# Step 4 -- Label worker nodes
###############################################################################
info "Labelling worker nodes"
WORKERS=($(kubectl get nodes --no-headers -o custom-columns=":metadata.name" \
  | grep -v control-plane))

if [[ ${#WORKERS[@]} -lt 1 ]]; then
  fail "No worker nodes found"
fi

# First worker: integration pool (served by nvml-mock).
kubectl label node "${WORKERS[0]}" run.ai/simulated-gpu-node-pool=integration --overwrite

# Remaining workers: scale pool.
for node in "${WORKERS[@]:1}"; do
  kubectl label node "${node}" run.ai/simulated-gpu-node-pool=scale --overwrite
done

###############################################################################
# Step 5 -- Install nvml-mock via Helm
###############################################################################
info "Installing nvml-mock Helm chart"
helm install nvml-mock "${REPO_ROOT}/${CHART_PATH}" \
  --set image.repository=nvml-mock \
  --set image.tag=demo \
  --set integrations.fakeGpuOperator.enabled=true \
  --set gpu.profile=h100 \
  --set gpu.count=8 \
  --wait --timeout 120s

###############################################################################
# Step 6 -- Verify: DaemonSet rollout
###############################################################################
info "Waiting for DaemonSet rollout"
kubectl rollout status daemonset/nvml-mock --timeout=60s

###############################################################################
# Step 7 -- Verify: Profile ConfigMaps
###############################################################################
info "Checking profile ConfigMaps"
CM_COUNT=$(kubectl get configmaps -l run.ai/gpu-profile=true \
  --no-headers 2>/dev/null | wc -l | tr -d ' ')

if [[ "${CM_COUNT}" -lt 6 ]]; then
  fail "Expected at least 6 profile ConfigMaps, found ${CM_COUNT}"
fi
info "Found ${CM_COUNT} profile ConfigMap(s)"

###############################################################################
# Step 8 -- Verify: nvidia-smi
###############################################################################
info "Running nvidia-smi inside a DaemonSet pod"
POD=$(kubectl get pods -l app.kubernetes.io/name=nvml-mock -o jsonpath='{.items[0].metadata.name}')
kubectl exec "${POD}" -- nvidia-smi

###############################################################################
# Step 9 -- Show node labels
###############################################################################
info "Node labels"
kubectl get nodes --show-labels

###############################################################################
# Summary
###############################################################################
echo
info "Demo complete."
info "  Cluster : ${CLUSTER_NAME}"
info "  Workers : ${#WORKERS[@]}"
info "  ConfigMaps: ${CM_COUNT}"
info ""
info "To tear down: kind delete cluster --name ${CLUSTER_NAME}"
