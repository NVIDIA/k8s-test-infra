#!/usr/bin/env bash
# Copyright 2026 NVIDIA CORPORATION
#
# SPDX-License-Identifier: Apache-2.0
#
# Demo: topograph derives NVLink accelerator-domain node labels from an
# nvml-mock-simulated GB200 cluster on Kind. No real GPUs, HCAs, or switches.
#
# See README.md for the full walkthrough and the documented switchless-fabric
# limitation (only the `accelerator` label is produced, not leaf/spine/core).
set -euo pipefail

###############################################################################
# Configuration (env-overridable)
###############################################################################
CLUSTER_NAME="${CLUSTER_NAME:-nvml-mock-topograph}"
IMAGE_NAME="nvml-mock:topograph-demo"
CHART_PATH="deployments/nvml-mock/helm/nvml-mock"
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
DEMO_DIR="${REPO_ROOT}/docs/demo/topograph"
: "${GPU_PROFILE:=gb200}"
: "${TOPOGRAPH_REPO:=https://NVIDIA.github.io/topograph}"
: "${TOPOGRAPH_CHART_VERSION:=0.4.0}"
: "${TOPOGRAPH_NS:=topograph}"
# FORCE_RECREATE=true tears down an existing cluster of the same name first.
: "${FORCE_RECREATE:=false}"

CLIQUE0_NODES=("${CLUSTER_NAME}-worker" "${CLUSTER_NAME}-worker2")
CLIQUE1_NODES=("${CLUSTER_NAME}-worker3" "${CLUSTER_NAME}-worker4")

###############################################################################
# Helpers
###############################################################################
info() { echo "==> $*"; }
fail() { echo "ERROR: $*" >&2; exit 1; }

accel_label() {
  # Print the topograph accelerator label value for a node (empty if unset).
  kubectl get node "$1" \
    -o jsonpath='{.metadata.labels.network\.topology\.nvidia\.com/accelerator}' \
    2>/dev/null || true
}

###############################################################################
# Step 1 -- Create (or reuse) the Kind cluster
###############################################################################
if kind get clusters 2>/dev/null | grep -qx "${CLUSTER_NAME}"; then
  if [[ "${FORCE_RECREATE}" == "true" ]]; then
    info "Deleting existing cluster '${CLUSTER_NAME}' (FORCE_RECREATE=true)"
    kind delete cluster --name "${CLUSTER_NAME}"
    info "Creating Kind cluster: ${CLUSTER_NAME}"
    kind create cluster --name "${CLUSTER_NAME}" --config="${DEMO_DIR}/kind.yaml"
  else
    info "Reusing existing Kind cluster '${CLUSTER_NAME}' (FORCE_RECREATE=true to recreate)"
  fi
else
  info "Creating Kind cluster: ${CLUSTER_NAME}"
  kind create cluster --name "${CLUSTER_NAME}" --config="${DEMO_DIR}/kind.yaml"
fi

###############################################################################
# Step 2 -- Build and load the nvml-mock image
###############################################################################
info "Building image: ${IMAGE_NAME}"
docker build -t "${IMAGE_NAME}" -f "${REPO_ROOT}/deployments/nvml-mock/Dockerfile" "${REPO_ROOT}"
info "Loading image into Kind cluster"
kind load docker-image "${IMAGE_NAME}" --name "${CLUSTER_NAME}"

###############################################################################
# Step 3 -- Install nvml-mock (gb200 + two NVLink cliques)
###############################################################################
info "Installing nvml-mock (profile=${GPU_PROFILE}, two cliques)"
helm upgrade --install nvml-mock "${REPO_ROOT}/${CHART_PATH}" \
  -f "${DEMO_DIR}/clique-topology.yaml" \
  --set image.repository=nvml-mock \
  --set image.tag=topograph-demo \
  --set integrations.fakeGpuOperator.enabled=true \
  --set "gpu.profile=${GPU_PROFILE}" \
  --wait --timeout 180s

# Force a pod recycle: when an existing cluster is reused, the image tag is
# unchanged (nvml-mock:topograph-demo), so `helm upgrade` leaves the DaemonSet
# template untouched and Kubernetes keeps the previously loaded (possibly stale)
# image. Restarting guarantees the freshly built image is the one running.
info "Restarting nvml-mock DaemonSet to pick up the freshly built image"
kubectl rollout restart daemonset/nvml-mock
info "Waiting for nvml-mock DaemonSet rollout"
kubectl rollout status daemonset/nvml-mock --timeout=120s

###############################################################################
# Step 4 -- Sanity check: nvml-mock exposes the clique to nvidia-smi -q
#
# topograph's node-data-broker reads the clique by running
# `nvidia-smi -q | grep "ClusterUUID\|CliqueId"` in the nvml-mock pod. If those
# strings are absent the whole integration cannot work, so fail fast.
###############################################################################
POD=$(kubectl get pods -l app.kubernetes.io/name=nvml-mock \
  -o jsonpath='{.items[0].metadata.name}')
info "Checking nvidia-smi -q clique output in pod ${POD}"
SMI_Q=$(kubectl exec "${POD}" -- nvidia-smi -q)
if ! grep -qE 'ClusterUUID|CliqueId' <<<"${SMI_Q}"; then
  echo "${SMI_Q}" | grep -iE 'fabric|clique|cluster' || true
  fail "nvml-mock nvidia-smi -q did not report ClusterUUID/CliqueId; topograph cannot derive the accelerator domain"
fi
info "nvidia-smi -q reports fabric clique identity (ClusterUUID/CliqueId present)"

###############################################################################
# Step 5 -- Install topograph (infiniband-k8s provider + k8s engine)
###############################################################################
info "Adding topograph Helm repo"
helm repo add topograph "${TOPOGRAPH_REPO}" >/dev/null 2>&1 || true
helm repo update >/dev/null
info "Installing topograph chart ${TOPOGRAPH_CHART_VERSION}"
helm upgrade --install topograph topograph/topograph \
  --version "${TOPOGRAPH_CHART_VERSION}" \
  --namespace "${TOPOGRAPH_NS}" --create-namespace \
  -f "${DEMO_DIR}/topograph-values.yaml" \
  --wait --timeout 180s

###############################################################################
# Step 6 -- Wait for topograph to label the nodes
#
# topograph's node-data-broker init container reads each node's NVLink clique
# (`nvidia-smi -q` in the nvml-mock pod) and annotates the node with
# `topograph.nvidia.com/cluster-id=<ClusterUUID>.<CliqueId>`. The topograph
# server's k8s engine then turns that annotation into the accelerator label.
# (The broker also attempts `ibnetdiscover`, but nvml-mock's fabric is
# switchless, so it contributes no switch tiers -- see Step 8.)
###############################################################################
info "Waiting for topograph to apply network.topology.nvidia.com/accelerator labels"
DEADLINE=$(( $(date +%s) + 180 ))
while :; do
  LABELED=0
  for n in "${CLIQUE0_NODES[@]}" "${CLIQUE1_NODES[@]}"; do
    [[ -n "$(accel_label "$n")" ]] && LABELED=$(( LABELED + 1 ))
  done
  [[ "${LABELED}" -eq 4 ]] && break
  if [[ "$(date +%s)" -ge "${DEADLINE}" ]]; then
    info "topograph server logs (last 60 lines):"
    kubectl logs -n "${TOPOGRAPH_NS}" -l app.kubernetes.io/name=topograph --tail=60 || true
    fail "timed out waiting for accelerator labels (${LABELED}/4 nodes labeled)"
  fi
  sleep 5
done
info "All 4 worker nodes carry an accelerator label"

###############################################################################
# Step 7 -- Verify: accelerator labels partition nodes by clique
###############################################################################
A0=$(accel_label "${CLIQUE0_NODES[0]}")
A0b=$(accel_label "${CLIQUE0_NODES[1]}")
A1=$(accel_label "${CLIQUE1_NODES[0]}")
A1b=$(accel_label "${CLIQUE1_NODES[1]}")
info "clique0: ${CLIQUE0_NODES[0]}=${A0} ${CLIQUE0_NODES[1]}=${A0b}"
info "clique1: ${CLIQUE1_NODES[0]}=${A1} ${CLIQUE1_NODES[1]}=${A1b}"

[[ "${A0}" == "${A0b}" ]] || fail "clique-0 nodes have different accelerator labels (${A0} vs ${A0b})"
[[ "${A1}" == "${A1b}" ]] || fail "clique-1 nodes have different accelerator labels (${A1} vs ${A1b})"
[[ "${A0}" != "${A1}" ]]  || fail "clique-0 and clique-1 share the same accelerator label (${A0}); cliques not distinguished"
info "Accelerator labels correctly partition the two NVLink cliques"

###############################################################################
# Step 8 -- Document the switchless limitation (leaf/spine/core absent)
###############################################################################
LEAF=$(kubectl get node "${CLIQUE0_NODES[0]}" \
  -o jsonpath='{.metadata.labels.network\.topology\.nvidia\.com/leaf}' 2>/dev/null || true)
if [[ -n "${LEAF}" ]]; then
  info "NOTE: a leaf label is present (${LEAF}) -- the fabric exposed switches"
else
  info "NOTE: no leaf/spine/core labels -- nvml-mock's fabric is switchless"
  info "      (point-to-point); only the NVLink accelerator domain is derived."
  info "      Follow-up: add mock leaf/spine switches to nvml-mock's ibnetdiscover."
fi

###############################################################################
# Summary
###############################################################################
echo
info "Demo complete."
info "  Cluster      : ${CLUSTER_NAME}"
info "  Profile      : ${GPU_PROFILE}"
info "  Cliques      : 0=[${CLIQUE0_NODES[*]}] 1=[${CLIQUE1_NODES[*]}]"
info "  Accelerator  : clique0=${A0} clique1=${A1}"
info ""
info "Inspect labels: kubectl get nodes -L network.topology.nvidia.com/accelerator"
info "To tear down  : kind delete cluster --name ${CLUSTER_NAME}"
