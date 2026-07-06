#!/usr/bin/env bash
# Copyright 2026 NVIDIA CORPORATION
# SPDX-License-Identifier: Apache-2.0

set -euo pipefail

CLUSTER_NAME="nvml-mock-node-wide-demo"
IMAGE_NAME="nvml-mock:node-wide-demo"
CHART_PATH="deployments/nvml-mock/helm/nvml-mock"
DEMO_DIR="docs/demo/node-wide-injection"
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
: "${GPU_PROFILE:=h100}"
: "${GPU_COUNT:=8}"
: "${FORCE_RECREATE:=false}"
: "${NVML_MOCK_NAMESPACE:=nvml-mock-system}"
: "${WORKLOAD_NAMESPACE:=default}"

info() { echo "==> $*"; }
fail() { echo "ERROR: $*" >&2; exit 1; }

wait_for_pod_success() {
  local pod="$1"
  if ! kubectl -n "${WORKLOAD_NAMESPACE}" wait --for=jsonpath='{.status.phase}'=Succeeded "pod/${pod}" --timeout=120s; then
    kubectl -n "${WORKLOAD_NAMESPACE}" logs "pod/${pod}" --all-containers=true || true
    kubectl -n "${WORKLOAD_NAMESPACE}" describe "pod/${pod}" || true
    fail "${pod} self-check failed"
  fi
  kubectl -n "${WORKLOAD_NAMESPACE}" logs "pod/${pod}" --all-containers=true
}

if kind get clusters 2>/dev/null | grep -qx "${CLUSTER_NAME}"; then
  if [[ "${FORCE_RECREATE}" == "true" ]]; then
    info "Deleting existing Kind cluster '${CLUSTER_NAME}'"
    kind delete cluster --name "${CLUSTER_NAME}"
  else
    info "Reusing existing Kind cluster '${CLUSTER_NAME}' (set FORCE_RECREATE=true to recreate)"
  fi
fi

if ! kind get clusters 2>/dev/null | grep -qx "${CLUSTER_NAME}"; then
  info "Creating Kind cluster with containerd NRI enabled"
  kind create cluster --name "${CLUSTER_NAME}" --config="${REPO_ROOT}/${DEMO_DIR}/kind.yaml"
fi

info "Building image: ${IMAGE_NAME}"
docker build -t "${IMAGE_NAME}" -f "${REPO_ROOT}/deployments/nvml-mock/Dockerfile" "${REPO_ROOT}"

info "Loading image into Kind"
kind load docker-image "${IMAGE_NAME}" --name "${CLUSTER_NAME}"

info "Installing nvml-mock with NRI enabled in namespace: ${NVML_MOCK_NAMESPACE}"
helm upgrade --install nvml-mock "${REPO_ROOT}/${CHART_PATH}" \
  --namespace "${NVML_MOCK_NAMESPACE}" \
  --create-namespace \
  --set image.repository=nvml-mock \
  --set image.tag=node-wide-demo \
  --set "gpu.profile=${GPU_PROFILE}" \
  --set "gpu.count=${GPU_COUNT}" \
  --set nri.enabled=true \
  --wait --timeout 180s

info "Waiting for setup and NRI DaemonSets"
kubectl -n "${NVML_MOCK_NAMESPACE}" rollout status daemonset/nvml-mock --timeout=90s
kubectl -n "${NVML_MOCK_NAMESPACE}" rollout status daemonset/nvml-mock-nri --timeout=90s

if [[ "${WORKLOAD_NAMESPACE}" != "default" ]]; then
  info "Preparing workload namespace: ${WORKLOAD_NAMESPACE}"
  kubectl create namespace "${WORKLOAD_NAMESPACE}" --dry-run=client -o yaml | kubectl apply -f -
fi

info "Deploying gpu-agent DaemonSet (plain workload; GPU stack comes from NRI)"
kubectl -n "${WORKLOAD_NAMESPACE}" delete daemonset gpu-agent --ignore-not-found
kubectl -n "${WORKLOAD_NAMESPACE}" apply -f "${REPO_ROOT}/${DEMO_DIR}/gpu-agent.yaml"
kubectl -n "${WORKLOAD_NAMESPACE}" rollout status daemonset/gpu-agent --timeout=120s

info "Verifying gpu-agent has no nvidia.com/gpu resource request"
GPU_AGENT_RESOURCES=$(kubectl -n "${WORKLOAD_NAMESPACE}" get daemonset gpu-agent -o jsonpath='{.spec.template.spec.containers[0].resources}' || true)
if grep -q "nvidia.com/gpu" <<<"${GPU_AGENT_RESOURCES}"; then
  fail "gpu-agent unexpectedly requests nvidia.com/gpu"
fi

info "Verifying gpu-agent runs nvidia-smi from NRI injection"
kubectl -n "${WORKLOAD_NAMESPACE}" logs daemonset/gpu-agent --tail=80 | grep -q "GPU 0:"

info "Creating demo pods"
kubectl -n "${WORKLOAD_NAMESPACE}" delete pod node-wide-plain node-wide-opt-out node-wide-device-opt-in --ignore-not-found
kubectl -n "${WORKLOAD_NAMESPACE}" apply -f "${REPO_ROOT}/${DEMO_DIR}/plain-pod.yaml"
kubectl -n "${WORKLOAD_NAMESPACE}" apply -f "${REPO_ROOT}/${DEMO_DIR}/opt-out-pod.yaml"
kubectl -n "${WORKLOAD_NAMESPACE}" apply -f "${REPO_ROOT}/${DEMO_DIR}/device-opt-in-pod.yaml"

info "Waiting for demo pod self-checks"
wait_for_pod_success node-wide-plain
wait_for_pod_success node-wide-opt-out
wait_for_pod_success node-wide-device-opt-in

info "Verifying the authored pod spec has no injected volumes or env"
VOLUME_COUNT=$(kubectl -n "${WORKLOAD_NAMESPACE}" get pod node-wide-plain -o jsonpath='{.spec.volumes}' | wc -c | tr -d ' ')
ENV_COUNT=$(kubectl -n "${WORKLOAD_NAMESPACE}" get pod node-wide-plain -o jsonpath='{.spec.containers[0].env}' | wc -c | tr -d ' ')
if [[ "${VOLUME_COUNT}" != "0" || "${ENV_COUNT}" != "0" ]]; then
  fail "Expected pod spec to remain unmodified (volumes bytes=${VOLUME_COUNT}, env bytes=${ENV_COUNT})"
fi

echo
info "Node-wide NRI injection demo complete."
info "  Cluster : ${CLUSTER_NAME}"
info "  nvml-mock namespace : ${NVML_MOCK_NAMESPACE}"
info "  Profile : ${GPU_PROFILE} (gpu.count=${GPU_COUNT})"
info "  Workload namespace : ${WORKLOAD_NAMESPACE}"
info "  Cleanup : kind delete cluster --name ${CLUSTER_NAME}"
