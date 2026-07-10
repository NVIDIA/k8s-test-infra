#!/usr/bin/env bash
# Copyright 2026 NVIDIA CORPORATION
# SPDX-License-Identifier: Apache-2.0

set -euo pipefail

CLUSTER_NAME="nvml-mock-node-wide-demo"
IMAGE_NAME="nvml-mock:node-wide-demo"
CHART_PATH="deployments/nvml-mock/helm/nvml-mock"
DEMO_DIR="docs/demo/node-wide-injection"
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
# gb200 by default so the ComputeDomain overlay has a fabric-attached profile
# to rewrite per node. A non-fabric profile (t4/l40s) still demonstrates plain
# node-wide injection; set WITH_COMPUTE_DOMAIN=false to skip the fabric checks.
: "${GPU_PROFILE:=gb200}"
: "${GPU_COUNT:=8}"
: "${FORCE_RECREATE:=false}"
: "${NVML_MOCK_NAMESPACE:=nvml-mock-system}"
: "${WORKLOAD_NAMESPACE:=default}"
# Enable the ComputeDomain overlay by default only for fabric-attached profiles
# (gb200/gb300); other profiles have no fabric block for the overlay to rewrite.
# An explicit WITH_COMPUTE_DOMAIN in the environment always wins.
case "${GPU_PROFILE}" in
  gb200 | gb300) : "${WITH_COMPUTE_DOMAIN:=true}" ;;
  *) : "${WITH_COMPUTE_DOMAIN:=false}" ;;
esac
TOPOLOGY_FILE="${REPO_ROOT}/${DEMO_DIR}/topology.yaml"
# Keep in sync with the domain UUID in topology.yaml.
EXPECTED_DOMAIN_UUID="00000000-0000-0000-0000-0000000000cd"

info() { echo "==> $*"; }
fail() { echo "ERROR: $*" >&2; exit 1; }

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

TOPOLOGY_ARGS=()
if [[ "${WITH_COMPUTE_DOMAIN}" == "true" ]]; then
  # `-f` (not `--set-file`) so helm parses the structured topology.domains
  # list and merges it with the chart defaults; --set-file would inject the
  # raw bytes as a string literal. See topology.yaml for the rationale.
  info "ComputeDomain overlay enabled (topology: ${TOPOLOGY_FILE})"
  TOPOLOGY_ARGS=(-f "${TOPOLOGY_FILE}")
fi

info "Installing nvml-mock with NRI enabled in namespace: ${NVML_MOCK_NAMESPACE}"
helm upgrade --install nvml-mock "${REPO_ROOT}/${CHART_PATH}" \
  --namespace "${NVML_MOCK_NAMESPACE}" \
  --create-namespace \
  --set image.repository=nvml-mock \
  --set image.tag=node-wide-demo \
  --set "gpu.profile=${GPU_PROFILE}" \
  --set "gpu.count=${GPU_COUNT}" \
  --set nri.enabled=true \
  "${TOPOLOGY_ARGS[@]}" \
  --wait --timeout 180s

info "Waiting for setup and NRI DaemonSets"
# Restart the daemon so setup.sh re-runs and (re)stages the topology overlay
# into /var/lib/nvml-mock/topology on every node, even when reusing a cluster
# whose pods predate a topology change.
kubectl -n "${NVML_MOCK_NAMESPACE}" rollout restart daemonset/nvml-mock
kubectl -n "${NVML_MOCK_NAMESPACE}" rollout status daemonset/nvml-mock --timeout=120s
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

info "Verifying gpu-agent sees mock GPUs from NRI injection"
kubectl -n "${WORKLOAD_NAMESPACE}" wait --for=condition=Ready pod -l app=gpu-agent --timeout=120s
GPU_AGENT_POD=$(kubectl -n "${WORKLOAD_NAMESPACE}" get pod -l app=gpu-agent -o jsonpath='{.items[0].metadata.name}')
GPU_AGENT_GPUS=$(kubectl -n "${WORKLOAD_NAMESPACE}" exec "${GPU_AGENT_POD}" -- nvidia-smi -L)
echo "${GPU_AGENT_GPUS}"
GPU_AGENT_GPU_COUNT=$(grep -c '^GPU [0-9]\+:' <<<"${GPU_AGENT_GPUS}" || true)
if [[ "${GPU_AGENT_GPU_COUNT}" -lt 1 ]]; then
  fail "gpu-agent did not report any GPUs:
${GPU_AGENT_GPUS}"
fi
info "gpu-agent sees ${GPU_AGENT_GPU_COUNT} GPU(s)"

if [[ "${WITH_COMPUTE_DOMAIN}" == "true" ]]; then
  info "Verifying per-node ComputeDomain fabric identity via NRI-injected pods"
  # Kind names workers "<cluster>-worker[N]"; keep in sync with topology.yaml.
  WORKER1="${CLUSTER_NAME}-worker"
  WORKER2="${CLUSTER_NAME}-worker2"
  WORKER3="${CLUSTER_NAME}-worker3"
  WORKER4="${CLUSTER_NAME}-worker4"

  # assert_clique runs the staged check-fabric consumer inside the plain
  # gpu-agent pod on a node and asserts the clique / cluster UUID the topology
  # overlay assigned to that node. The pod requests no nvidia.com/gpu and sets
  # no MOCK_* env: the fabric identity comes entirely from NRI injection.
  assert_clique() {
    local node="$1" expected_clique="$2"
    local pod
    pod=$(kubectl -n "${WORKLOAD_NAMESPACE}" get pod -l app=gpu-agent \
      --field-selector "spec.nodeName=${node}" \
      -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true)
    if [[ -z "${pod}" ]]; then
      fail "no gpu-agent pod found on node ${node}"
    fi
    local out
    out=$(kubectl -n "${WORKLOAD_NAMESPACE}" exec "${pod}" -- check-fabric 2>&1 || true)
    echo "${out}" | sed 's/^/    /'
    if ! grep -q "cliqueId    : ${expected_clique}" <<<"${out}"; then
      fail "${node}: expected cliqueId ${expected_clique} from check-fabric"
    fi
    if ! grep -qi "clusterUuid : ${EXPECTED_DOMAIN_UUID}" <<<"${out}"; then
      fail "${node}: expected clusterUuid ${EXPECTED_DOMAIN_UUID} from check-fabric"
    fi
    info "${node}: clique=${expected_clique} uuid=${EXPECTED_DOMAIN_UUID}"
  }

  assert_clique "${WORKER1}" 0
  assert_clique "${WORKER2}" 0
  assert_clique "${WORKER3}" 1
  assert_clique "${WORKER4}" 1
  info "All nodes report their assigned ComputeDomain clique via NRI injection"
fi

echo
info "Node-wide NRI injection demo complete."
info "  Cluster : ${CLUSTER_NAME}"
info "  nvml-mock namespace : ${NVML_MOCK_NAMESPACE}"
info "  Profile : ${GPU_PROFILE} (gpu.count=${GPU_COUNT})"
info "  Workload namespace : ${WORKLOAD_NAMESPACE}"
if [[ "${WITH_COMPUTE_DOMAIN}" == "true" ]]; then
  info "  ComputeDomain : ${EXPECTED_DOMAIN_UUID} (workers 1-2 -> clique 0, workers 3-4 -> clique 1)"
fi
info "  Cleanup : kind delete cluster --name ${CLUSTER_NAME}"
