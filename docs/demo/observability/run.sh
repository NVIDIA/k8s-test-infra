#!/usr/bin/env bash
# Copyright 2026 NVIDIA CORPORATION
# SPDX-License-Identifier: Apache-2.0
#
# Demo: observe mock GPUs through a real Prometheus + Grafana stack, then inject
# GPU faults and watch them land on the dashboard as a step change in the
# recorded time series.
#
# Pipeline exercised:
#   nvml-mock (fake libnvidia-ml) --> real dcgm-exporter :9400
#     --> ServiceMonitor (GPU Operator generated) --> Prometheus --> Grafana
#
# Faults are injected with nvml-mock-ctl rather than a `helm upgrade`, because a
# Helm upgrade recycles the exporter pod and breaks the very time series this
# demo exists to render. The CLI writes a node-local overlay that the already
# running exporter picks up within the engine's TTL, so the series stays
# continuous and shows a visible discontinuity.
#
# This is a green-path demo: every phase is expected to succeed, and the script
# FAILS if a metric it injected never reaches Prometheus. It is re-runnable
# (reuses the cluster unless FORCE_RECREATE=true).
set -euo pipefail

# --- Configuration (override via env) ----------------------------------------
CLUSTER_NAME="${CLUSTER_NAME:-nvml-mock-observability}"
KUBE_CONTEXT="kind-${CLUSTER_NAME}"
IMAGE_NAME="${IMAGE_NAME:-nvml-mock:observability-demo}"
CHART_PATH="deployments/nvml-mock/helm/nvml-mock"
DEMO_DIR="docs/demo/observability"
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"

: "${GPU_PROFILE:=h100}"
: "${GPU_COUNT:=8}"
: "${FORCE_RECREATE:=false}"
: "${KIND_NODE_IMAGE:=kindest/node:v1.35.0}"

: "${NVML_MOCK_NAMESPACE:=nvml-mock-system}"
: "${GPU_OPERATOR_NAMESPACE:=gpu-operator}"
: "${GPU_OPERATOR_VERSION:=v26.3.3}"
: "${MONITORING_NAMESPACE:=monitoring}"
# Release name MUST match dcgmExporter.serviceMonitor.additionalLabels.release
# in gpu-operator-values.yaml: kube-prometheus-stack only selects
# ServiceMonitors labelled release=<this>, and ignores others silently.
: "${KPS_RELEASE:=monitoring}"
: "${KPS_VERSION:=88.3.0}"
: "${GRAFANA_PASSWORD:=mokka}"

# Fault-injection targets.
: "${TARGET_GPU:=0}"
: "${HOT_TEMP_C:=90}"
: "${XID_CODE:=79}"

# Node label pinning the mock + GPU operands to the GPU workers.
GPU_NODE_LABEL="nvml-mock-gpu=true"
# Prometheus Service created by kube-prometheus-stack.
PROM_SVC="${KPS_RELEASE}-kube-prometheus-prometheus"

info() { echo "==> $*"; }
warn() { echo "WARN: $*" >&2; }
fail() { echo "ERROR: $*" >&2; exit 1; }
kubectl_ctx() { command kubectl --context "${KUBE_CONTEXT}" "$@"; }
observe() { echo "--- \$ $* ---"; "$@" 2>&1 || warn "(non-fatal) command failed: $*"; }

# --- Preflight ----------------------------------------------------------------
for bin in docker kind kubectl helm jq; do
  command -v "${bin}" >/dev/null 2>&1 || fail "${bin} is required"
done

# --- Kind cluster -------------------------------------------------------------
if kind get clusters 2>/dev/null | grep -qxF "${CLUSTER_NAME}"; then
  if [[ "${FORCE_RECREATE}" == "true" ]]; then
    info "Deleting existing Kind cluster '${CLUSTER_NAME}'"
    kind delete cluster --name "${CLUSTER_NAME}"
  else
    info "Reusing existing Kind cluster '${CLUSTER_NAME}' (set FORCE_RECREATE=true to recreate)"
    # The kubeconfig entry may have been pruned since the cluster was created
    # (common when juggling several kind clusters), which would break every
    # later kubectl call; re-exporting it makes the reuse path self-healing.
    kind export kubeconfig --name "${CLUSTER_NAME}"
  fi
fi
if ! kind get clusters 2>/dev/null | grep -qxF "${CLUSTER_NAME}"; then
  info "Creating Kind cluster '${CLUSTER_NAME}' (1 control-plane + 2 workers, CDI enabled)"
  kind create cluster --name "${CLUSTER_NAME}" \
    --image "${KIND_NODE_IMAGE}" \
    --config="${REPO_ROOT}/${DEMO_DIR}/kind.yaml"
fi

mapfile -t WORKERS < <(kind get nodes --name "${CLUSTER_NAME}" | grep -v control-plane | sort)
[[ "${#WORKERS[@]}" -ge 1 ]] || fail "no worker nodes found"
info "GPU workers: ${WORKERS[*]}"

# --- Label GPU workers + install nvidia-container-toolkit / CDI ---------------
for node in "${WORKERS[@]}"; do
  info "Labeling ${node} with ${GPU_NODE_LABEL}"
  kubectl_ctx label node "${node}" "${GPU_NODE_LABEL}" --overwrite

  # Re-provisioning ends in `systemctl restart containerd`, which cycles the
  # node through NotReady and tears a hole in the GPU metrics series this demo
  # exists to render -- so a node that already has the toolkit is left alone.
  # It also keeps re-runs working without network access.
  if docker exec "${node}" test -f /etc/nvidia-container-runtime/config.toml; then
    info "Skipping nvidia-container-toolkit install on ${node} (already provisioned)"
    continue
  fi

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
set -e
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
info "Waiting for all nodes to be Ready"
kubectl_ctx wait --for=condition=Ready nodes --all --timeout=180s
