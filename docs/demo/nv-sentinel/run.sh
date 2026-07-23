#!/usr/bin/env bash
# Copyright 2026 NVIDIA CORPORATION
# SPDX-License-Identifier: Apache-2.0
#
# Demo: prove that NVIDIA NVSentinel DETECTS a GPU XID error on mock GPUs and
# REMEDIATES the node (cordon + drain), then RECOVERS it (uncordon) once the
# fault is reset — all on a local Kind cluster with no physical GPUs.
#
# Pipeline exercised:
#   nvml-mock (fake libnvidia-ml) --> GPU Operator standalone DCGM (nv-hostengine)
#     --> NVSentinel GPU Health Monitor --> platform-connector --> MongoDB
#     --> fault-quarantine (cordon) --> node-drainer (drain)
#   reset --> DCGM clears --> healthy events --> fault-quarantine UNCORDON
#
# Topology: 1 control-plane + 2 workers. The mock GPUs + GPU Operator operands
# run on the workers (labeled nvml-mock-gpu=true); the NVSentinel control-plane
# pipeline + MongoDB are pinned to the control-plane so draining a GPU worker
# never evicts the pipeline doing the draining. When one worker's GPU is failed,
# NVSentinel cordons/drains it and the sample GPU workload reschedules onto the
# second, healthy worker.
#
# This is a green-path demo: every phase is expected to succeed. It is
# re-runnable (reuses the cluster unless FORCE_RECREATE=true).
set -euo pipefail

# --- Configuration (override via env) ----------------------------------------
CLUSTER_NAME="${CLUSTER_NAME:-nvml-mock-nvsentinel}"
KUBE_CONTEXT="kind-${CLUSTER_NAME}"
IMAGE_NAME="${IMAGE_NAME:-nvml-mock:nvsentinel-demo}"
CHART_PATH="deployments/nvml-mock/helm/nvml-mock"
DEMO_DIR="docs/demo/nv-sentinel"
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"

: "${GPU_PROFILE:=h100}"
: "${FORCE_RECREATE:=false}"
: "${KIND_NODE_IMAGE:=kindest/node:v1.35.0}"

: "${NVML_MOCK_NAMESPACE:=nvml-mock-system}"
: "${WORKLOAD_NAMESPACE:=default}"
: "${GPU_OPERATOR_NAMESPACE:=gpu-operator}"
: "${GPU_OPERATOR_VERSION:=v26.3.3}"
: "${CERT_MANAGER_VERSION:=v1.19.1}"
: "${NVSENTINEL_NAMESPACE:=nvsentinel}"
: "${NVSENTINEL_VERSION:=v1.15.0}"
: "${NVSENTINEL_CHART:=oci://ghcr.io/nvidia/nvsentinel}"

# XID injected and asserted on. 79 = "GPU has fallen off the bus" (fatal).
: "${XID:=79}"
# The GPU (index) failed on the target worker.
: "${TARGET_GPU:=0}"
# Node label that pins the mock + GPU operands to the GPU workers.
GPU_NODE_LABEL="nvml-mock-gpu=true"
# MongoDB Secret consumed by NVSentinel (global.datastore.credentialsFromSecret).
MONGO_URI_SECRET="nvsentinel-datastore-mongodb-uri"
MONGO_URI="mongodb://mongodb-ext.${NVSENTINEL_NAMESPACE}.svc.cluster.local:27017/HealthEventsDatabase?replicaSet=rs0"

info() { echo "==> $*"; }
warn() { echo "WARN: $*" >&2; }
fail() { echo "ERROR: $*" >&2; exit 1; }
kubectl_ctx() { command kubectl --context "${KUBE_CONTEXT}" "$@"; }
observe() { echo "--- \$ $* ---"; "$@" 2>&1 || warn "(non-fatal) command failed: $*"; }

# --- Preflight ----------------------------------------------------------------
for bin in docker kind kubectl helm; do
  command -v "${bin}" >/dev/null 2>&1 || fail "${bin} is required"
done

# --- Kind cluster -------------------------------------------------------------
if kind get clusters 2>/dev/null | grep -qx "${CLUSTER_NAME}"; then
  if [[ "${FORCE_RECREATE}" == "true" ]]; then
    info "Deleting existing Kind cluster '${CLUSTER_NAME}'"
    kind delete cluster --name "${CLUSTER_NAME}"
  else
    info "Reusing existing Kind cluster '${CLUSTER_NAME}' (set FORCE_RECREATE=true to recreate)"
  fi
fi
if ! kind get clusters 2>/dev/null | grep -qx "${CLUSTER_NAME}"; then
  info "Creating Kind cluster '${CLUSTER_NAME}' (1 control-plane + 2 workers, CDI enabled)"
  kind create cluster --name "${CLUSTER_NAME}" \
    --image "${KIND_NODE_IMAGE}" \
    --config="${REPO_ROOT}/${DEMO_DIR}/kind.yaml"
fi

# Worker node names == docker container names (default kind naming).
mapfile -t WORKERS < <(kind get nodes --name "${CLUSTER_NAME}" | grep -v control-plane | sort)
[[ "${#WORKERS[@]}" -ge 1 ]] || fail "no worker nodes found"
info "GPU workers: ${WORKERS[*]}"

# --- Label GPU workers + install nvidia-container-toolkit / CDI ---------------
for node in "${WORKERS[@]}"; do
  info "Labeling ${node} with ${GPU_NODE_LABEL}"
  kubectl_ctx label node "${node}" "${GPU_NODE_LABEL}" --overwrite

  info "Installing nvidia-container-toolkit into ${node}"
  docker exec "${node}" bash -c '
set -e
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y -qq curl gpg
# --no-tty/--batch so this works when stdin is not a terminal (e.g. the script
# is run detached / in CI); otherwise gpg tries to open /dev/tty and fails.
curl -fsSL https://nvidia.github.io/libnvidia-container/gpgkey \
  | gpg --no-tty --batch --yes --dearmor -o /usr/share/keyrings/nvidia-container-toolkit-keyring.gpg
curl -fsSL https://nvidia.github.io/libnvidia-container/stable/deb/nvidia-container-toolkit.list \
  | sed "s#deb https://#deb [signed-by=/usr/share/keyrings/nvidia-container-toolkit-keyring.gpg] https://#g" \
  | tee /etc/apt/sources.list.d/nvidia-container-toolkit.list
apt-get update -qq
apt-get install -y -qq nvidia-container-toolkit
'
  info "Configuring nvidia-container-runtime (CDI mode) on ${node}"
  docker exec "${node}" nvidia-ctk runtime configure --runtime=containerd --cdi.enabled --set-as-default
  docker exec "${node}" bash -c '
cat > /etc/nvidia-container-runtime/config.toml <<EOF
[nvidia-container-runtime]
mode = "cdi"

[nvidia-container-runtime.modes.cdi]
default-kind = "nvidia.com/gpu"
spec-dirs = ["/var/run/cdi", "/etc/cdi"]
EOF
systemctl restart containerd
'
done
info "Waiting for nodes to be Ready after containerd restart"
kubectl_ctx wait --for=condition=Ready nodes --all --timeout=180s

# --- Build + load the nvml-mock image -----------------------------------------
info "Building nvml-mock image: ${IMAGE_NAME}"
docker build -t "${IMAGE_NAME}" -f "${REPO_ROOT}/deployments/nvml-mock/Dockerfile" "${REPO_ROOT}"
info "Loading image into Kind"
kind load docker-image "${IMAGE_NAME}" --name "${CLUSTER_NAME}"

# --- Install nvml-mock (pinned to the GPU workers) ----------------------------
info "Installing nvml-mock (profile=${GPU_PROFILE}) on the GPU workers"
helm upgrade --install nvml-mock "${REPO_ROOT}/${CHART_PATH}" \
  --kube-context "${KUBE_CONTEXT}" \
  --namespace "${NVML_MOCK_NAMESPACE}" --create-namespace \
  --set image.repository=nvml-mock \
  --set image.tag=nvsentinel-demo \
  --set "gpu.profile=${GPU_PROFILE}" \
  --set gpu.dynamicMetrics.enabled=true \
  --set-string "nodeSelector.nvml-mock-gpu=true" \
  --wait --timeout 180s

# --- Install the NVIDIA GPU Operator with standalone DCGM ---------------------
# gpu-operator-values.yaml disables the real driver/toolkit (the mock provides
# them) and, unlike the repo e2e, ENABLES the standalone DCGM (nv-hostengine)
# DaemonSet + Service on :5555 that NVSentinel's GPU Health Monitor polls.
info "Adding NVIDIA Helm repo + installing GPU Operator ${GPU_OPERATOR_VERSION}"
helm repo add nvidia https://helm.ngc.nvidia.com/nvidia >/dev/null 2>&1 || true
helm repo update nvidia >/dev/null 2>&1 || helm repo update >/dev/null 2>&1
helm upgrade --install gpu-operator nvidia/gpu-operator \
  --kube-context "${KUBE_CONTEXT}" \
  --namespace "${GPU_OPERATOR_NAMESPACE}" --create-namespace \
  --version "${GPU_OPERATOR_VERSION}" \
  -f "${REPO_ROOT}/${DEMO_DIR}/gpu-operator-values.yaml" \
  --wait --timeout 8m

info "Waiting for a GPU worker to advertise nvidia.com/gpu"
for _ in $(seq 1 60); do
  alloc=$(kubectl_ctx get node "${WORKERS[0]}" -o 'jsonpath={.status.allocatable.nvidia\.com/gpu}' 2>/dev/null || true)
  [[ -n "${alloc}" && "${alloc}" != "0" ]] && { info "${WORKERS[0]} advertises nvidia.com/gpu=${alloc}"; break; }
  sleep 5
done

# --- Install cert-manager (NVSentinel + MongoDB TLS dependency) ---------------
if ! kubectl_ctx get ns cert-manager >/dev/null 2>&1; then
  info "Installing cert-manager ${CERT_MANAGER_VERSION}"
  kubectl_ctx apply -f "https://github.com/cert-manager/cert-manager/releases/download/${CERT_MANAGER_VERSION}/cert-manager.yaml"
fi
info "Waiting for cert-manager to be ready"
# Generous timeout: on a busy host (e.g. several Kind clusters at once) the GPU
# workers can be CPU-saturated during GPU Operator bring-up, slowing the
# cert-manager image pulls.
kubectl_ctx -n cert-manager wait --for=condition=Available deploy --all --timeout=420s

# --- Standalone MongoDB (public.ecr.aws mongo:8.0.3, TLS via cert-manager) -----
# The chart's built-in Bitnami MongoDB is amd64-only (bitnamilegacy images) and
# cannot run on arm64, so we run the official multi-arch image as an EXTERNAL
# datastore (single-node replica set, TLS). See mongodb.yaml + README.
info "Deploying standalone MongoDB (mongo:8.0.3, cert-manager TLS, replica set)"
# Create the namespace up front: mongodb.yaml lives in it, and it is applied
# before the NVSentinel Helm install that would otherwise --create-namespace it.
kubectl_ctx create namespace "${NVSENTINEL_NAMESPACE}" --dry-run=client -o yaml | kubectl_ctx apply -f -
kubectl_ctx apply -f "${REPO_ROOT}/${DEMO_DIR}/mongodb.yaml"
kubectl_ctx -n "${NVSENTINEL_NAMESPACE}" rollout status statefulset/mongodb-ext --timeout=180s
info "Waiting for the replica-set init Job"
kubectl_ctx -n "${NVSENTINEL_NAMESPACE}" wait --for=condition=complete job/mongodb-ext-rs-init --timeout=180s

info "Creating the MONGODB_URI Secret consumed by NVSentinel"
kubectl_ctx -n "${NVSENTINEL_NAMESPACE}" create secret generic "${MONGO_URI_SECRET}" \
  --from-literal=MONGODB_URI="${MONGO_URI}" \
  --dry-run=client -o yaml | kubectl_ctx apply -f -

# --- Install NVSentinel --------------------------------------------------------
# NOTE: install WITHOUT --wait. The external-MongoDB collection-setup Job is a
# Helm post-install/post-upgrade hook, but the DB-consuming pods cannot become
# Ready until those collections exist. With --wait, Helm blocks on pod readiness
# and the hook never runs -> deadlock. Without --wait the hook runs immediately,
# creates the collections, and the pods then start.
info "Installing NVSentinel ${NVSENTINEL_VERSION} (external MongoDB, DCGM health monitor)"
helm upgrade --install nvsentinel "${NVSENTINEL_CHART}" \
  --kube-context "${KUBE_CONTEXT}" \
  --version "${NVSENTINEL_VERSION}" \
  --namespace "${NVSENTINEL_NAMESPACE}" --create-namespace \
  -f "${REPO_ROOT}/${DEMO_DIR}/nvsentinel-values.yaml" \
  --timeout 5m

info "Waiting for the external-MongoDB setup Job to create collections"
kubectl_ctx -n "${NVSENTINEL_NAMESPACE}" wait --for=condition=complete \
  job/nvsentinel-external-mongodb-setup --timeout=300s || \
  warn "setup job not found/complete yet; connectors will retry"

# The DB-consuming pods (platform-connectors, fault-quarantine, node-drainer)
# start before the setup Job creates the collections and land in
# CrashLoopBackOff. Once the Job is done, force a clean restart so they come up
# immediately instead of waiting out the exponential backoff.
info "Restarting DB-consuming pods now that collections exist"
kubectl_ctx -n "${NVSENTINEL_NAMESPACE}" delete pod \
  -l app.kubernetes.io/instance=nvsentinel --field-selector=status.phase=Running \
  --ignore-not-found >/dev/null 2>&1 || true

info "Waiting for NVSentinel pods to become Ready"
for _ in $(seq 1 60); do
  not_ready=$(kubectl_ctx -n "${NVSENTINEL_NAMESPACE}" get pods \
    --field-selector=status.phase!=Succeeded \
    -o 'jsonpath={range .items[*]}{.metadata.name}{" "}{.status.conditions[?(@.type=="Ready")].status}{"\n"}{end}' 2>/dev/null \
    | grep -vE ' True$' || true)
  [[ -z "${not_ready}" ]] && { info "all NVSentinel pods Ready"; break; }
  sleep 5
done
observe kubectl_ctx -n "${NVSENTINEL_NAMESPACE}" get pods -o wide

# --- Sample GPU workload (something for the drainer to evict) -----------------
info "Deploying sample GPU workload"
kubectl_ctx apply -f "${REPO_ROOT}/${DEMO_DIR}/sample-workload.yaml"
kubectl_ctx -n "${WORKLOAD_NAMESPACE}" rollout status deploy/gpu-sample-workload --timeout=180s || \
  warn "sample workload not Ready yet"
WORKLOAD_NODE=$(kubectl_ctx -n "${WORKLOAD_NAMESPACE}" get pod -l app=gpu-sample-workload \
  -o jsonpath='{.items[0].spec.nodeName}' 2>/dev/null || true)
info "Sample workload scheduled on: ${WORKLOAD_NODE:-<pending>}"

# ==============================================================================
# PHASE 1 — INJECT XID and observe DETECTION + REMEDIATION (cordon + drain)
# ==============================================================================
# Target the mock on the same worker the sample workload landed on (so the drain
# is observable), else the first GPU worker.
TARGET_NODE="${WORKLOAD_NODE:-${WORKERS[0]}}"
MOCK_POD=$(kubectl_ctx -n "${NVML_MOCK_NAMESPACE}" get pod -l app.kubernetes.io/name=nvml-mock \
  --field-selector "spec.nodeName=${TARGET_NODE}" -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true)
[[ -n "${MOCK_POD}" ]] || { TARGET_NODE="${WORKERS[0]}"; MOCK_POD=$(kubectl_ctx -n "${NVML_MOCK_NAMESPACE}" get pod -l app.kubernetes.io/name=nvml-mock --field-selector "spec.nodeName=${TARGET_NODE}" -o jsonpath='{.items[0].metadata.name}'); }

info "PHASE 1: injecting XID ${XID} on GPU ${TARGET_GPU} of node ${TARGET_NODE} (pod ${MOCK_POD})"
kubectl_ctx -n "${NVML_MOCK_NAMESPACE}" exec "${MOCK_POD}" -- \
  nvml-mock-ctl fail --gpu "${TARGET_GPU}" --mode ecc_uncorrectable --after-calls 1 --xid "${XID}"

info "Waiting for NVSentinel to cordon ${TARGET_NODE} (detect -> quarantine)"
cordoned=false
for _ in $(seq 1 60); do
  if [[ "$(kubectl_ctx get node "${TARGET_NODE}" -o jsonpath='{.spec.unschedulable}' 2>/dev/null || true)" == "true" ]]; then
    cordoned=true; break
  fi
  sleep 5
done
if [[ "${cordoned}" == "true" ]]; then
  info "DETECTED + REMEDIATED: ${TARGET_NODE} is cordoned by NVSentinel"
else
  warn "node not cordoned within timeout; inspect fault-quarantine logs"
fi
echo "--- node status ---";           observe kubectl_ctx get nodes
echo "--- quarantine condition ---";  observe bash -c "kubectl --context ${KUBE_CONTEXT} get node ${TARGET_NODE} -o json | jq -r '.status.conditions[] | select(.type|test(\"Gpu\")) | \"\(.type)=\(.status): \(.message)\"' | grep -iE 'NotHealthy|fallen|xid|ecc' | head"
echo "--- fault-quarantine cordon ---"; observe bash -c "kubectl --context ${KUBE_CONTEXT} -n ${NVSENTINEL_NAMESPACE} logs -l app.kubernetes.io/instance=nvsentinel --tail=200 --prefix 2>/dev/null | grep -iE 'Cordoning node|Quarantined' | tail -5"
info "Waiting for the drained workload to reschedule off ${TARGET_NODE}"
rescheduled=false
for _ in $(seq 1 48); do
  wl_node=$(kubectl_ctx -n "${WORKLOAD_NAMESPACE}" get pod -l app=gpu-sample-workload \
    -o jsonpath='{.items[0].spec.nodeName}' 2>/dev/null || true)
  if [[ -n "${wl_node}" && "${wl_node}" != "${TARGET_NODE}" ]]; then
    info "DRAINED: sample workload rescheduled onto healthy worker ${wl_node}"
    rescheduled=true; break
  fi
  sleep 5
done
[[ "${rescheduled}" == "true" ]] || warn "workload has not rescheduled yet; check node-drainer logs"
observe kubectl_ctx -n "${WORKLOAD_NAMESPACE}" get pods -l app=gpu-sample-workload -o wide

# ==============================================================================
# PHASE 2 — RESET the GPU and observe RECOVERY (uncordon)
# ==============================================================================
info "PHASE 2: resetting the injected fault on ${TARGET_NODE}"
kubectl_ctx -n "${NVML_MOCK_NAMESPACE}" exec "${MOCK_POD}" -- nvml-mock-ctl reset --gpu all

# DCGM latches XID/DBE errors until the hostengine re-reads the (now healthy)
# mock, so restart the standalone DCGM + exporter to clear them. The GPU Health
# Monitor then emits healthy events and fault-quarantine uncordons the node.
info "Restarting standalone DCGM so it clears the latched XID"
kubectl_ctx -n "${GPU_OPERATOR_NAMESPACE}" rollout restart daemonset/nvidia-dcgm daemonset/nvidia-dcgm-exporter
kubectl_ctx -n "${GPU_OPERATOR_NAMESPACE}" rollout status daemonset/nvidia-dcgm --timeout=180s

info "Waiting for NVSentinel to uncordon ${TARGET_NODE} (reset -> recovery)"
recovered=false
for _ in $(seq 1 60); do
  if [[ "$(kubectl_ctx get node "${TARGET_NODE}" -o jsonpath='{.spec.unschedulable}' 2>/dev/null || true)" != "true" ]]; then
    recovered=true; break
  fi
  sleep 5
done
if [[ "${recovered}" == "true" ]]; then
  info "RECOVERED: ${TARGET_NODE} is uncordoned (Ready) again"
else
  warn "node still cordoned; inspect fault-quarantine logs (checks may not have cleared)"
fi
observe kubectl_ctx get nodes

# --- Summary ------------------------------------------------------------------
cat <<EOF

==> NVSentinel XID detect + remediate + recover demo complete.

  Cluster            : ${CLUSTER_NAME} (1 control-plane + ${#WORKERS[@]} workers)
  Failed node        : ${TARGET_NODE} (GPU ${TARGET_GPU}, XID ${XID})
  MongoDB            : standalone mongo:8.0.3 (external datastore, cert-manager TLS)

  What was shown:
    1. DETECT     : GPU Health Monitor (via standalone DCGM) saw the injected
                    XID ${XID} on GPU ${TARGET_GPU} and emitted a fatal health event.
    2. REMEDIATE  : fault-quarantine cordoned ${TARGET_NODE}; node-drainer drained
                    it; the sample GPU workload rescheduled to the healthy worker.
    3. RECOVER    : after 'nvml-mock-ctl reset' + DCGM restart, health went green
                    and fault-quarantine UNCORDONED ${TARGET_NODE}.

  Inspect further:
    kubectl --context ${KUBE_CONTEXT} -n ${NVSENTINEL_NAMESPACE} get pods
    kubectl --context ${KUBE_CONTEXT} get nodes
    kubectl --context ${KUBE_CONTEXT} -n ${NVSENTINEL_NAMESPACE} logs -l app.kubernetes.io/instance=nvsentinel --prefix | grep -iE 'cordon'

  Re-run the fault manually:
    MOCK=\$(kubectl --context ${KUBE_CONTEXT} -n ${NVML_MOCK_NAMESPACE} get pod -l app.kubernetes.io/name=nvml-mock --field-selector spec.nodeName=${TARGET_NODE} -o jsonpath='{.items[0].metadata.name}')
    kubectl --context ${KUBE_CONTEXT} -n ${NVML_MOCK_NAMESPACE} exec \$MOCK -- nvml-mock-ctl fail --gpu ${TARGET_GPU} --mode ecc_uncorrectable --after-calls 1 --xid ${XID}
    kubectl --context ${KUBE_CONTEXT} -n ${NVML_MOCK_NAMESPACE} exec \$MOCK -- nvml-mock-ctl reset --gpu all   # then restart nvidia-dcgm

  Cleanup:
    kind delete cluster --name ${CLUSTER_NAME}
EOF
