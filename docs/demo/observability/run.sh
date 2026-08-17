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
: "${CLUSTER_NAME:=nvml-mock-observability}"
KUBE_CONTEXT="kind-${CLUSTER_NAME}"
# Repository and tag are the overridable knobs, not the full reference: the chart
# takes image.repository and image.tag separately, and splitting a combined
# reference back apart mis-handles both a missing tag and a registry with a port.
: "${IMAGE_REPO:=nvml-mock}"
: "${IMAGE_TAG:=observability-demo}"
IMAGE_NAME="${IMAGE_REPO}:${IMAGE_TAG}"
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
# kube-prometheus-stack only selects ServiceMonitors labelled release=<this> and
# ignores others silently, so the dcgm-exporter ServiceMonitor's release label is
# --set from this variable at install time rather than hardcoded in
# gpu-operator-values.yaml, where it could drift unnoticed.
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
    # later kubectl call. Only re-export when it is actually missing: exporting
    # also repoints the caller's current-context, which is a side effect a
    # healthy reuse has no business causing.
    if ! kubectl config get-contexts "${KUBE_CONTEXT}" >/dev/null 2>&1; then
      kind export kubeconfig --name "${CLUSTER_NAME}"
    fi
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
  # exists to render -- so an already provisioned node is left alone. It also
  # keeps re-runs working without network access.
  #
  # The CDI rewrite below is the last provisioning step, so its content is the
  # only trustworthy completion marker: the toolkit package ships a default,
  # non-CDI config.toml of its own, and probing for mere file existence would
  # silently skip a node whose first run died between install and rewrite --
  # a misconfiguration that only surfaces much later as "no GPUs in
  # dcgm-exporter".
  if docker exec "${node}" grep -q 'mode = "cdi"' /etc/nvidia-container-runtime/config.toml 2>/dev/null; then
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

# --- Build + load the nvml-mock image -----------------------------------------
info "Building nvml-mock image: ${IMAGE_NAME}"
docker build -t "${IMAGE_NAME}" -f "${REPO_ROOT}/deployments/nvml-mock/Dockerfile" "${REPO_ROOT}"
info "Loading image into Kind"
kind load docker-image "${IMAGE_NAME}" --name "${CLUSTER_NAME}"

# --- Install nvml-mock (pinned to the GPU workers) ----------------------------
# dynamicMetrics is what makes the dashboard interesting: without it temperature,
# power and utilization are static profile constants and every panel is a flat
# line.
#
# The nodeSelector reuses GPU_NODE_LABEL verbatim so selection cannot drift from
# the label actually applied to the workers above; it must stay --set-string
# because nodeSelector is map[string]string and plain --set would render `true`
# as a YAML boolean, which the API server rejects on unmarshal. This relies on
# the label key containing no dots, which Helm would read as a path separator.
info "Installing nvml-mock (profile=${GPU_PROFILE}, count=${GPU_COUNT}) on the GPU workers"
helm upgrade --install nvml-mock "${REPO_ROOT}/${CHART_PATH}" \
  --kube-context "${KUBE_CONTEXT}" \
  --namespace "${NVML_MOCK_NAMESPACE}" --create-namespace \
  --set "image.repository=${IMAGE_REPO}" \
  --set "image.tag=${IMAGE_TAG}" \
  --set "gpu.profile=${GPU_PROFILE}" \
  --set "gpu.count=${GPU_COUNT}" \
  --set gpu.dynamicMetrics.enabled=true \
  --set-string "nodeSelector.${GPU_NODE_LABEL}" \
  --wait --timeout 180s

# --- Install kube-prometheus-stack ---------------------------------------------
# Installed BEFORE the GPU Operator so the ServiceMonitor CRD exists by the time
# the operator tries to create one for dcgm-exporter.
info "Adding prometheus-community Helm repo + installing kube-prometheus-stack ${KPS_VERSION}"
helm repo add prometheus-community https://prometheus-community.github.io/helm-charts >/dev/null 2>&1 || true
helm repo update prometheus-community >/dev/null 2>&1 || helm repo update >/dev/null 2>&1
helm upgrade --install "${KPS_RELEASE}" prometheus-community/kube-prometheus-stack \
  --kube-context "${KUBE_CONTEXT}" \
  --namespace "${MONITORING_NAMESPACE}" --create-namespace \
  --version "${KPS_VERSION}" \
  -f "${REPO_ROOT}/${DEMO_DIR}/kube-prometheus-stack-values.yaml" \
  --set "grafana.adminPassword=${GRAFANA_PASSWORD}" \
  --wait --timeout 10m

# Query Prometheus through the API-server service proxy rather than the host
# port: the same approach tests/e2e/go/assertions/dcgm.go uses, and it keeps the
# assertions working even if the host port mapping is unavailable.
promq() {
  kubectl_ctx get --raw \
    "/api/v1/namespaces/${MONITORING_NAMESPACE}/services/${PROM_SVC}:9090/proxy/api/v1/$1"
}

info "Waiting for the Prometheus API to answer"
prom_ready=false
for _ in $(seq 1 60); do
  if promq "query?query=up" >/dev/null 2>&1; then prom_ready=true; break; fi
  sleep 5
done
[[ "${prom_ready}" == "true" ]] || fail "Prometheus API never became reachable"
info "Prometheus is serving queries"

# --- Install the NVIDIA GPU Operator (dcgm-exporter + ServiceMonitor) ---------
# The ServiceMonitor's release label is passed here instead of being written into
# gpu-operator-values.yaml so it is derived from KPS_RELEASE and cannot drift
# from the kube-prometheus-stack release name -- a mismatch makes Prometheus
# ignore the monitor with no error anywhere.
info "Adding NVIDIA Helm repo + installing GPU Operator ${GPU_OPERATOR_VERSION}"
helm repo add nvidia https://helm.ngc.nvidia.com/nvidia >/dev/null 2>&1 || true
helm repo update nvidia >/dev/null 2>&1 || helm repo update >/dev/null 2>&1
helm upgrade --install gpu-operator nvidia/gpu-operator \
  --kube-context "${KUBE_CONTEXT}" \
  --namespace "${GPU_OPERATOR_NAMESPACE}" --create-namespace \
  --version "${GPU_OPERATOR_VERSION}" \
  -f "${REPO_ROOT}/${DEMO_DIR}/gpu-operator-values.yaml" \
  --set "dcgmExporter.serviceMonitor.additionalLabels.release=${KPS_RELEASE}" \
  --wait --timeout 10m

info "Waiting for a GPU worker to advertise nvidia.com/gpu"
for _ in $(seq 1 60); do
  alloc=$(kubectl_ctx get node "${WORKERS[0]}" -o 'jsonpath={.status.allocatable.nvidia\.com/gpu}' 2>/dev/null || true)
  [[ -n "${alloc}" && "${alloc}" != "0" ]] && { info "${WORKERS[0]} advertises nvidia.com/gpu=${alloc}"; break; }
  sleep 5
done

info "Waiting for dcgm-exporter to be rolled out"
kubectl_ctx -n "${GPU_OPERATOR_NAMESPACE}" rollout status ds/nvidia-dcgm-exporter --timeout=300s

# The single most fragile link in the demo: if the ServiceMonitor's release label
# does not match the kube-prometheus-stack release, Prometheus ignores it with no
# error at all. Assert the target is actually being scraped.
info "Waiting for Prometheus to scrape the dcgm-exporter target"
target_up=false
for _ in $(seq 1 60); do
  if promq "targets?state=active" \
      | jq -e '.data.activeTargets[] | select(.labels.job | test("dcgm")) | select(.health == "up")' \
      >/dev/null 2>&1; then
    target_up=true; break
  fi
  sleep 5
done
if [[ "${target_up}" != "true" ]]; then
  observe kubectl_ctx -n "${GPU_OPERATOR_NAMESPACE}" get servicemonitor -o yaml
  fail "Prometheus never scraped dcgm-exporter. Check that the ServiceMonitor carries release=${KPS_RELEASE}"
fi
info "dcgm-exporter target is UP in Prometheus"

info "Confirming the DCGM series are present"
for series in DCGM_FI_DEV_GPU_TEMP DCGM_FI_DEV_POWER_USAGE DCGM_FI_DEV_GPU_UTIL; do
  count=$(promq "query?query=${series}" | jq '.data.result | length')
  [[ "${count}" -gt 0 ]] || fail "${series} returned no series"
  info "  ${series}: ${count} series"
done
