#!/usr/bin/env bash
# Copyright 2026 NVIDIA CORPORATION
# SPDX-License-Identifier: Apache-2.0
#
# Demo: prove that NVIDIA NVSentinel DETECTS a GPU thermal condition on mock
# GPUs and REMEDIATES the node (cordon + drain), then AUTO-RECOVERS it (uncordon)
# once the GPU cools down — all on a local Kind cluster with no physical GPUs.
#
# The fault under test is a THERMAL MARGIN violation. NVSentinel's
# GpuThermalMarginWatch compares each GPU's live signed T.Limit margin
# (DCGM field 153) against the per-GPU hardware slowdown offset that the
# metadata-collector reads once from NVML field 194
# (NVML_FI_DEV_TEMPERATURE_SLOWDOWN_TLIMIT) and publishes to gpu_metadata.json.
# When the margin drops below that offset the GPU is unhealthy; when it recovers
# the check clears on its own — no DCGM restart required (unlike latched XID/ECC
# faults).
#
# Pipeline exercised:
#   nvml-mock (fake libnvidia-ml) --> GPU Operator standalone DCGM (nv-hostengine)
#     --> NVSentinel GPU Health Monitor (GpuThermalMarginWatch, field 153 vs the
#         slowdown offset from metadata-collector) --> platform-connector
#     --> MongoDB --> fault-quarantine (cordon) --> node-drainer (drain)
#   cool down --> margin re-opens --> healthy events --> fault-quarantine UNCORDON
#
# Topology: 1 control-plane + 2 workers. The mock GPUs + GPU Operator operands
# run on the workers (labeled nvml-mock-gpu=true); the NVSentinel control-plane
# pipeline + MongoDB are pinned to the control-plane so draining a GPU worker
# never evicts the pipeline doing the draining. When one worker's GPU overheats,
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

: "${NAMESPACE:=mokka}"
: "${WORKLOAD_NAMESPACE:=default}"
: "${GPU_OPERATOR_NAMESPACE:=gpu-operator}"
: "${GPU_OPERATOR_VERSION:=v26.3.3}"
: "${CERT_MANAGER_VERSION:=v1.19.1}"
: "${NVSENTINEL_NAMESPACE:=nvsentinel}"
: "${NVSENTINEL_VERSION:=v1.21.0}"
: "${NVSENTINEL_CHART:=oci://ghcr.io/nvidia/nvsentinel}"

# The GPU (index) overheated on the target worker.
: "${TARGET_GPU:=0}"
# Temperature (C) to pin on the target GPU. It must be strictly ABOVE the mock
# profile's slowdown threshold: GpuThermalMarginWatch fails only once the
# T.Limit margin (DCGM field 153) drops BELOW the slowdown offset, so pinning
# exactly at the threshold leaves a margin of 0 and never trips. Thresholds vary
# per profile (h100 slows down at 87C, gb300 at 90C), so this is set well clear
# of every profile rather than tuned to one board.
: "${HOT_TEMP_C:=142}"
# Phase 3 (GPU reset remediation) waits on a GPU Operator operand
# teardown/restore cycle, which roughly doubles the run. Set GPU_RESET=false to
# stop after the thermal phases.
: "${GPU_RESET:=true}"
# GPU to fault for the reset. Deliberately not TARGET_GPU: phase 1 and 2 leave
# that one's history in the datastore, and a fresh GPU keeps the equivalence
# group for this fault unambiguous.
: "${RESET_GPU:=1}"
# Node label that pins the mock + GPU operands to the GPU workers.
GPU_NODE_LABEL="nvml-mock-gpu=true"

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
  --namespace "${NAMESPACE}" --create-namespace \
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
kubectl_ctx -n cert-manager wait --for=condition=Available deploy --all --timeout=600s

# --- Install NVSentinel --------------------------------------------------------
# NOTE: install WITHOUT --wait. Bringing up the datastore is a multi-step dance
# (Percona operator -> PerconaServerMongoDB CR -> cert-manager certs -> the
# collection-setup Job), and the DB-consuming pods stay unready until it
# finishes. --wait would just block Helm for the whole sequence and time out.
info "Installing NVSentinel ${NVSENTINEL_VERSION} (Percona MongoDB store, DCGM health monitor)"
helm upgrade --install nvsentinel "${NVSENTINEL_CHART}" \
  --kube-context "${KUBE_CONTEXT}" \
  --version "${NVSENTINEL_VERSION}" \
  --namespace "${NVSENTINEL_NAMESPACE}" --create-namespace \
  -f "${REPO_ROOT}/${DEMO_DIR}/nvsentinel-values.yaml" \
  --timeout 5m

info "Waiting for the MongoDB collection-setup Job"
kubectl_ctx -n "${NVSENTINEL_NAMESPACE}" wait --for=condition=complete \
  job -l app.kubernetes.io/name=create-mongodb-database --timeout=900s || \
  warn "collection-setup job not complete yet; connectors will retry"


# Covers the Percona pods too — they live in the same namespace, and the
# operator + replica-set bring-up is the slowest part of the install.
info "Waiting for NVSentinel pods to become Ready"
for _ in $(seq 1 120); do
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
# PHASE 1 — HEAT the GPU and observe DETECTION + REMEDIATION (cordon + drain)
# ==============================================================================
# Target the mock on the same worker the sample workload landed on (so the drain
# is observable), else the first GPU worker.
TARGET_NODE="${WORKLOAD_NODE:-${WORKERS[0]}}"
MOCK_POD=$(kubectl_ctx -n "${NAMESPACE}" get pod -l app.kubernetes.io/name=nvml-mock \
  --field-selector "spec.nodeName=${TARGET_NODE}" -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true)
[[ -n "${MOCK_POD}" ]] || { TARGET_NODE="${WORKERS[0]}"; MOCK_POD=$(kubectl_ctx -n "${NAMESPACE}" get pod -l app.kubernetes.io/name=nvml-mock --field-selector "spec.nodeName=${TARGET_NODE}" -o jsonpath='{.items[0].metadata.name}'); }

info "PHASE 1: heating GPU ${TARGET_GPU} to ${HOT_TEMP_C}C on node ${TARGET_NODE} (pod ${MOCK_POD})"
kubectl_ctx -n "${NAMESPACE}" exec "${MOCK_POD}" -- \
  nvml-mock-ctl temp --gpu "${TARGET_GPU}" "${HOT_TEMP_C}"

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
echo "--- thermal-margin condition ---";  observe bash -c "kubectl --context ${KUBE_CONTEXT} get node ${TARGET_NODE} -o json | jq -r '.status.conditions[] | select(.type|test(\"Gpu\")) | \"\(.type)=\(.status): \(.message)\"' | grep -iE 'ThermalMargin|slowdown|margin' | head"
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
# PHASE 2 — COOL the GPU and observe AUTO-RECOVERY (uncordon)
# ==============================================================================
# Clearing the pinned temperature returns the GPU to its normal (cool) reading,
# so the T.Limit margin re-opens above the slowdown offset. Unlike a latched
# XID/ECC fault, DCGM field 153 is a live gauge: the next Health Monitor poll
# sees the healthy margin and fault-quarantine uncordons the node — NO DCGM
# restart needed. That live self-clearing behavior is the whole point of driving
# the demo through the thermal-margin check.
info "PHASE 2: cooling GPU ${TARGET_GPU} back down on ${TARGET_NODE} (clear temp override)"
kubectl_ctx -n "${NAMESPACE}" exec "${MOCK_POD}" -- nvml-mock-ctl reset --gpu "${TARGET_GPU}"

info "Waiting for NVSentinel to uncordon ${TARGET_NODE} (cooldown -> recovery)"
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

# ==============================================================================
# PHASE 3 — REMEDIATE IN PLACE: NVSentinel resets the GPU
# ==============================================================================
# An uncorrectable ECC error is a resettable fault: DCGM reports
# DCGM_FR_VOLATILE_DBE_DETECTED, which NVSentinel maps to COMPONENT_RESET, and
# with the values above that becomes a GPUReset CR rather than a node reboot.
# The janitor then runs NVIDIA's real gpu-reset image, whose script reaches
# nvidia-smi only as `chroot /run/nvidia/driver nvidia-smi` — the path that
# needed the mock driver root to become chroot-able. See issue #759.
if [[ "${GPU_RESET}" == "true" ]]; then
  # The janitor sets imagePullPolicy: Always on the reset Job, so every attempt
  # re-pulls this image. Seeding it into the Kind nodes first keeps a slow
  # registry from eating the janitor's Job deadline and failing the first
  # attempt for reasons that have nothing to do with the reset itself.
  # Strictly best-effort: the janitor retries a failed reset, so a seed that
  # does not work here costs a slower first attempt, not the demo.
  reset_image="ghcr.io/nvidia/nvsentinel/gpu-reset:${NVSENTINEL_VERSION}"
  info "Pre-loading ${reset_image} into Kind"
  if ! { docker pull "${reset_image}" && \
    kind load docker-image "${reset_image}" --name "${CLUSTER_NAME}"; }; then
    warn "could not seed ${reset_image} into the nodes; the reset Job will pull it itself, and its first attempt may lose the janitor's deadline to the registry"
  fi

  # A re-run against a reused cluster still has the previous run's GPUResets,
  # and they would satisfy the wait below instantly. Recording them first lets
  # the wait insist on a CR this run caused.
  gpureset_names() {
    kubectl_ctx get gpuresets.janitor.dgxc.nvidia.com \
      --sort-by=.metadata.creationTimestamp \
      -o "jsonpath={.items[?(@.spec.nodeName=='${TARGET_NODE}')].metadata.name}" 2>/dev/null || true
  }
  crs_before=" $(gpureset_names) "

  info "PHASE 3: injecting an uncorrectable ECC error on GPU ${RESET_GPU} (${TARGET_NODE})"
  kubectl_ctx -n "${NAMESPACE}" exec "${MOCK_POD}" -- \
    nvml-mock-ctl fail --gpu "${RESET_GPU}" --mode ecc_uncorrectable

  info "Waiting for NVSentinel to create a GPUReset for ${TARGET_NODE}"
  # Every remediation attempt creates its own GPUReset, so this keeps the newest
  # new one rather than concatenating the names of all of them.
  reset_cr=""
  for _ in $(seq 1 60); do
    for name in $(gpureset_names); do
      [[ "${crs_before}" == *" ${name} "* ]] && continue
      reset_cr="${name}"
    done
    [[ -n "${reset_cr}" ]] && break
    sleep 5
  done
  if [[ -z "${reset_cr}" ]]; then
    warn "no GPUReset created; check fault-remediation logs and that COMPONENT_RESET maps to GPUReset"
  else
    info "GPUReset created: ${reset_cr}"

    # The janitor decides success purely from the reset Job's exit status, and
    # the Job's first act is the chroot preflight (nvidia-smi --version) under
    # set -e. A Succeeded phase therefore means the chroot worked.
    info "Waiting for ${reset_cr} to complete (teardown -> reset Job -> restore)"
    kubectl_ctx wait --for=condition=Complete \
      "gpuresets.janitor.dgxc.nvidia.com/${reset_cr}" --timeout=900s || \
      warn "GPUReset did not complete; inspect its conditions below"

    echo "--- GPUReset conditions ---"
    observe bash -c "kubectl --context ${KUBE_CONTEXT} get gpuresets.janitor.dgxc.nvidia.com/${reset_cr} -o json | jq -r '.status.conditions[] | \"\(.type)=\(.status): \(.reason)\"'"

    # Complete=True only means the janitor stopped working on this CR; it is
    # also how a failed reset ends. The phase distinguishes the two.
    reset_phase=$(kubectl_ctx get "gpuresets.janitor.dgxc.nvidia.com/${reset_cr}" \
      -o jsonpath='{.status.phase}' 2>/dev/null || true)
    if [[ "${reset_phase}" == "Succeeded" ]]; then
      info "GPUReset ${reset_cr} succeeded (the chroot preflight and reset both ran)"
    else
      warn "GPUReset ${reset_cr} ended in phase '${reset_phase:-unknown}'; see the Job output below"
    fi

    # The CR names its own Job, which matters because concurrent remediations
    # put several reset Jobs in this namespace at once — picking the newest one
    # would happily show another node's logs.
    echo "--- reset Job output (the chroot preflight is its first line) ---"
    reset_job=$(kubectl_ctx get "gpuresets.janitor.dgxc.nvidia.com/${reset_cr}" \
      -o jsonpath='{.status.jobRef.name}' 2>/dev/null || true)
    if [[ -n "${reset_job}" ]]; then
      observe kubectl_ctx -n "${NVSENTINEL_NAMESPACE}" logs "job/${reset_job}" --tail=40
    else
      warn "GPUReset ${reset_cr} has no jobRef yet; no reset Job to show"
    fi
  fi

  # The reset clears the device's injected overrides, which is the mock's
  # equivalent of clearing a GPU's transient error state. An empty status here
  # is the proof the reset did something, not merely that it reported success:
  # a reset that cannot reach the mock's config would report success having
  # cleared nothing, and the surviving fault is what gives that away.
  echo "--- GPU ${RESET_GPU} overrides after the reset ---"
  overrides_after=$(kubectl_ctx -n "${NAMESPACE}" exec "${MOCK_POD}" -- \
    nvml-mock-ctl status --gpu "${RESET_GPU}" 2>&1 || true)
  echo "${overrides_after}"
  if [[ "${overrides_after}" == *"no active overrides"* ]]; then
    info "VERIFIED: the reset cleared GPU ${RESET_GPU}'s injected ECC fault"
  else
    warn "GPU ${RESET_GPU} still carries its injected fault: the reset reported success but cleared nothing"
  fi
else
  info "PHASE 3 (GPU reset remediation) skipped by GPU_RESET=false"
fi

# --- Summary ------------------------------------------------------------------
cat <<EOF

==> NVSentinel thermal-margin detect + remediate + auto-recover demo complete.

  Cluster            : ${CLUSTER_NAME} (1 control-plane + ${#WORKERS[@]} workers)
  Overheated node    : ${TARGET_NODE} (GPU ${TARGET_GPU} pinned to ${HOT_TEMP_C}C)
  MongoDB            : NVSentinel's Percona store (single-member rs0, cert-manager TLS)

  What was shown:
    1. DETECT     : the metadata-collector published GPU ${TARGET_GPU}'s slowdown
                    T.Limit offset (NVML field 194) to gpu_metadata.json; heating
                    the GPU drove DCGM field 153 negative, so GpuThermalMarginWatch
                    fired GPU_TEMP_HW_SLOWDOWN_VIOLATION.
    2. REMEDIATE  : fault-quarantine cordoned ${TARGET_NODE}; node-drainer drained
                    it; the sample GPU workload rescheduled to the healthy worker.
    3. RECOVER    : clearing the temperature re-opened the margin; the next Health
                    Monitor poll saw it healthy and fault-quarantine UNCORDONED
                    ${TARGET_NODE} — no DCGM restart needed.

  Inspect further:
    kubectl --context ${KUBE_CONTEXT} -n ${NVSENTINEL_NAMESPACE} get pods
    kubectl --context ${KUBE_CONTEXT} get nodes
    kubectl --context ${KUBE_CONTEXT} -n ${NVSENTINEL_NAMESPACE} logs -l app.kubernetes.io/instance=nvsentinel --prefix | grep -iE 'cordon'

  Re-run the fault manually:
    MOCK=\$(kubectl --context ${KUBE_CONTEXT} -n ${NAMESPACE} get pod -l app.kubernetes.io/name=nvml-mock --field-selector spec.nodeName=${TARGET_NODE} -o jsonpath='{.items[0].metadata.name}')
    kubectl --context ${KUBE_CONTEXT} -n ${NAMESPACE} exec \$MOCK -- nvml-mock-ctl temp --gpu ${TARGET_GPU} ${HOT_TEMP_C}   # heat -> cordon
    kubectl --context ${KUBE_CONTEXT} -n ${NAMESPACE} exec \$MOCK -- nvml-mock-ctl reset --gpu ${TARGET_GPU}               # cool -> auto-uncordon

  Cleanup:
    kind delete cluster --name ${CLUSTER_NAME}
EOF
