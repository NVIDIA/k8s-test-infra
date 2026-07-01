#!/usr/bin/env bash
# Copyright 2026 NVIDIA CORPORATION
#
# SPDX-License-Identifier: Apache-2.0
#
# Demo: the nvml-mock NODE-WIDE mutating admission webhook turns an ORDINARY
# pod into a working mock GPU node. No real GPUs, HCAs, or switches -- and the
# workload requests no GPU and mounts no mock libraries of its own.
#
# Narrative:
#   * nvml-mock is installed with injector.enabled=true. The webhook mutates
#     every non-excluded pod with the mock libnvidia-ml overlay + env.
#   * `gpu-agent` is a plain DaemonSet on a stock debian image: no
#     nvidia.com/gpu request, no hostPath mounts. It only sets NODE_NAME and
#     runs nvidia-smi.
#   * The webhook injects the mock nvidia-smi/NVML into gpu-agent; combined with
#     NODE_NAME the mock engine reports that node's NVLink clique.
#   * This script proves injection worked end-to-end by execing nvidia-smi in
#     gpu-agent and asserting it reports the expected per-node clique.
set -euo pipefail

###############################################################################
# Configuration (env-overridable)
###############################################################################
CLUSTER_NAME="${CLUSTER_NAME:-nvml-mock-injection}"
IMAGE_NAME="nvml-mock:injection-demo"
CHART_PATH="deployments/nvml-mock/helm/nvml-mock"
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
DEMO_DIR="${REPO_ROOT}/docs/demo/node-wide-injection"
# Reuse the shared Kind topology (1 control-plane + 3 workers) instead of a
# demo-local copy.
KIND_CONFIG="${REPO_ROOT}/docs/demo/kind.yaml"
: "${GPU_PROFILE:=gb200}"
# nvml-mock (and its injector) MUST live in a namespace other than the one the
# injected workload runs in: the webhook always excludes its own release
# namespace to avoid a bootstrap deadlock. gpu-agent runs in `default`, so we
# install nvml-mock into a dedicated `nvml-mock` namespace -- otherwise `default`
# would be excluded and gpu-agent would never get injected.
: "${NVML_NS:=nvml-mock}"
# FORCE_RECREATE=true tears down an existing cluster of the same name first.
: "${FORCE_RECREATE:=false}"

# Shared kind.yaml provides 4 workers: two NVLink cliques of two nodes each.
CLIQUE0_NODES=("${CLUSTER_NAME}-worker" "${CLUSTER_NAME}-worker2")
CLIQUE1_NODES=("${CLUSTER_NAME}-worker3" "${CLUSTER_NAME}-worker4")

INJECTED_ANNOTATION="nvml-mock.nvidia.com/injected"

###############################################################################
# Helpers
###############################################################################
info() { echo "==> $*"; }
fail() { echo "ERROR: $*" >&2; exit 1; }

agent_pod_on() {
  # Print the gpu-agent pod name scheduled on the given node (empty if none).
  kubectl get pods -n default -l app=gpu-agent \
    --field-selector spec.nodeName="$1" \
    -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true
}

clique_of() {
  # Print "<ClusterUUID>.<CliqueId>" as reported by the injected nvidia-smi in
  # the gpu-agent pod on the given node (empty on failure).
  local pod
  pod=$(agent_pod_on "$1")
  [ -n "$pod" ] || return 0
  kubectl exec -n default "$pod" -- nvidia-smi -q 2>/dev/null \
    | awk '
        /ClusterUUID/ { split($0, a, ":"); gsub(/ /, "", a[2]); uuid=a[2] }
        /CliqueId/    { split($0, b, ":"); gsub(/ /, "", b[2]); clq=b[2] }
        END { if (uuid != "" && clq != "") printf "%s.%s", uuid, clq }'
}

###############################################################################
# Step 1 -- Create (or reuse) the Kind cluster
###############################################################################
if kind get clusters 2>/dev/null | grep -qx "${CLUSTER_NAME}"; then
  if [[ "${FORCE_RECREATE}" == "true" ]]; then
    info "Deleting existing cluster '${CLUSTER_NAME}' (FORCE_RECREATE=true)"
    kind delete cluster --name "${CLUSTER_NAME}"
    info "Creating Kind cluster: ${CLUSTER_NAME}"
    kind create cluster --name "${CLUSTER_NAME}" --config="${KIND_CONFIG}"
  else
    info "Reusing existing Kind cluster '${CLUSTER_NAME}' (FORCE_RECREATE=true to recreate)"
  fi
else
  info "Creating Kind cluster: ${CLUSTER_NAME}"
  kind create cluster --name "${CLUSTER_NAME}" --config="${KIND_CONFIG}"
fi

###############################################################################
# Step 2 -- Build and load the nvml-mock image
###############################################################################
info "Building image: ${IMAGE_NAME}"
docker build -t "${IMAGE_NAME}" -f "${REPO_ROOT}/deployments/nvml-mock/Dockerfile" "${REPO_ROOT}"
info "Loading image into Kind cluster"
kind load docker-image "${IMAGE_NAME}" --name "${CLUSTER_NAME}"

###############################################################################
# Step 3 -- Install nvml-mock (gb200 + two NVLink cliques + node-wide injector)
#
# nvml-mock lives in its own namespace (${NVML_NS}), which we also list in the
# webhook's excludedNamespaces so nvml-mock's own pods (and the injector) are
# never mutated. The injected workload (gpu-agent) runs in `default`, which
# stays injectable.
###############################################################################
info "Installing nvml-mock into namespace '${NVML_NS}' (profile=${GPU_PROFILE}, two cliques, injector enabled)"
helm upgrade --install nvml-mock "${REPO_ROOT}/${CHART_PATH}" \
  --namespace "${NVML_NS}" --create-namespace \
  -f "${DEMO_DIR}/clique-topology.yaml" \
  --set image.repository=nvml-mock \
  --set image.tag=injection-demo \
  --set integrations.fakeGpuOperator.enabled=true \
  --set injector.enabled=true \
  --set "injector.excludedNamespaces={kube-system,${NVML_NS}}" \
  --set "gpu.profile=${GPU_PROFILE}" \
  --wait --timeout 180s

# Force a pod recycle: when an existing cluster is reused, the image tag is
# unchanged (nvml-mock:injection-demo), so `helm upgrade` leaves the DaemonSet
# template untouched and Kubernetes keeps the previously loaded (possibly stale)
# image. Restarting guarantees the freshly built image is the one running.
info "Restarting nvml-mock DaemonSet to pick up the freshly built image"
kubectl rollout restart daemonset/nvml-mock -n "${NVML_NS}"
info "Waiting for nvml-mock DaemonSet rollout"
kubectl rollout status daemonset/nvml-mock -n "${NVML_NS}" --timeout=120s

info "Waiting for the nvml-mock injector webhook to be ready"
kubectl rollout status deployment/nvml-mock-injector -n "${NVML_NS}" --timeout=120s
kubectl get mutatingwebhookconfiguration nvml-mock-injector >/dev/null 2>&1 \
  || fail "MutatingWebhookConfiguration nvml-mock-injector not found; injector did not install"

###############################################################################
# Step 4 -- Deploy the ORDINARY gpu-agent DaemonSet
#
# gpu-agent requests no GPU and mounts no mock libraries; it only sets NODE_NAME
# and runs nvidia-smi. The webhook must mutate it (annotation + mock nvidia-smi
# + env) for nvidia-smi to work at all.
###############################################################################
info "Deploying gpu-agent DaemonSet (plain pod; GPU stack comes only from injection)"
kubectl apply -f "${DEMO_DIR}/gpu-agent.yaml"
info "Waiting for gpu-agent pods to be Ready"
kubectl rollout status daemonset/gpu-agent -n default --timeout=120s

###############################################################################
# Step 5 -- Prove the pod was injected (annotation + no GPU request)
###############################################################################
AGENT0=$(agent_pod_on "${CLIQUE0_NODES[0]}")
AGENT1=$(agent_pod_on "${CLIQUE1_NODES[0]}")
[[ -n "${AGENT0}" ]] || fail "no gpu-agent pod found on ${CLIQUE0_NODES[0]}"
[[ -n "${AGENT1}" ]] || fail "no gpu-agent pod found on ${CLIQUE1_NODES[0]}"

# 5a. The pod carries no nvidia.com/gpu resource request (it is a plain pod).
GPU_REQ=$(kubectl get pod -n default "${AGENT0}" \
  -o jsonpath='{.spec.containers[0].resources.limits.nvidia\.com/gpu}' 2>/dev/null || true)
[[ -z "${GPU_REQ}" ]] || fail "gpu-agent unexpectedly requests nvidia.com/gpu=${GPU_REQ}; it should be a plain pod"
info "Confirmed gpu-agent requests no nvidia.com/gpu resource"

# 5b. The webhook stamped the injected annotation.
INJECTED=$(kubectl get pod -n default "${AGENT0}" \
  -o jsonpath="{.metadata.annotations.${INJECTED_ANNOTATION//./\\.}}" 2>/dev/null || true)
[[ "${INJECTED}" == "true" ]] \
  || fail "gpu-agent pod ${AGENT0} lacks '${INJECTED_ANNOTATION}=true'; the webhook did not inject it"
info "Confirmed webhook injected gpu-agent (${INJECTED_ANNOTATION}=true)"

###############################################################################
# Step 6 -- Exercise nvidia-smi through the injected mock stack
#
# The injected nvidia-smi must run and report this node's NVLink clique. We also
# show that per-node injection resolves distinct cliques (clique 0 vs clique 1),
# which proves the NODE_NAME-keyed overlay works.
###############################################################################
info "Running nvidia-smi -L in ${AGENT0}"
kubectl exec -n default "${AGENT0}" -- nvidia-smi -L 2>&1 | sed 's/^/    /' \
  || fail "nvidia-smi -L failed in ${AGENT0}; injection did not provide a working mock GPU stack"

info "Reading NVLink clique via injected nvidia-smi -q"
C0=$(clique_of "${CLIQUE0_NODES[0]}")
C0b=$(clique_of "${CLIQUE0_NODES[1]}")
C1=$(clique_of "${CLIQUE1_NODES[0]}")
C1b=$(clique_of "${CLIQUE1_NODES[1]}")
info "clique0: ${CLIQUE0_NODES[0]}=${C0:-<none>} ${CLIQUE0_NODES[1]}=${C0b:-<none>}"
info "clique1: ${CLIQUE1_NODES[0]}=${C1:-<none>} ${CLIQUE1_NODES[1]}=${C1b:-<none>}"

[[ -n "${C0}" ]] || fail "injected nvidia-smi in ${CLIQUE0_NODES[0]} did not report a clique"
[[ "${C0}" == "${C0b}" ]] || fail "clique-0 nodes report different cliques (${C0} vs ${C0b})"
[[ "${C1}" == "${C1b}" ]] || fail "clique-1 nodes report different cliques (${C1} vs ${C1b})"
[[ "${C0}" != "${C1}" ]]  || fail "clique-0 and clique-1 report the same clique (${C0}); per-node injection not distinguished"
info "Injected nvidia-smi reports correct, distinct per-node NVLink cliques"

###############################################################################
# Summary
###############################################################################
echo
info "Demo complete."
info "  Cluster      : ${CLUSTER_NAME}"
info "  Profile      : ${GPU_PROFILE}"
info "  nvml-mock ns : ${NVML_NS} (webhook excludes its own ns; gpu-agent is in default)"
info "  GPU source   : injected gpu-agent pods (no GPU request, no mock mounts)"
info "  Cliques      : 0=[${CLIQUE0_NODES[*]}]=${C0}  1=[${CLIQUE1_NODES[*]}]=${C1}"
info ""
info "Inspect agent logs : kubectl logs -n default ds/gpu-agent"
info "Run nvidia-smi     : kubectl exec -n default ${AGENT0} -- nvidia-smi"
info "To tear down       : kind delete cluster --name ${CLUSTER_NAME}"
