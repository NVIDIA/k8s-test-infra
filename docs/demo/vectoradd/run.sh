#!/usr/bin/env bash
# Copyright 2026 NVIDIA CORPORATION
#
# SPDX-License-Identifier: Apache-2.0

set -euo pipefail

# This demo is an amd64 stack: the official validator image
# (nvcr.io/nvidia/k8s/cuda-sample:vectoradd-cuda12.5.0) ships only linux/amd64,
# and the mock's libcuda.so interposition is amd64-specific. Every consumer of
# the mock driver (the nvml-mock DaemonSet, the NVIDIA device plugin, and the
# validator) must therefore be amd64 too, so they all load the same amd64
# driver libs. Forcing linux/amd64 here makes Kind create amd64 nodes and all
# docker build/pull operations target amd64 — natively on an amd64 host, or via
# qemu emulation on arm64 (e.g. Apple Silicon).
export DOCKER_DEFAULT_PLATFORM=linux/amd64

###############################################################################
# Configuration
#
# GPU_PROFILE / GPU_COUNT are env-overridable so the same demo can drive any
# of the chart's built-in profiles, e.g.
#   GPU_PROFILE=gb200 GPU_COUNT=8 ./run.sh
#   GPU_PROFILE=t4    GPU_COUNT=4 ./run.sh
# The PCI-sysfs assertions in step 9 derive their expected values from
# GPU_COUNT and from the profile's `pcie_topology:` block, so switching
# profile keeps the demo correct without further edits.
###############################################################################
CLUSTER_NAME="nvml-mock-vectoradd-demo"
# Kind creates a kubeconfig context named "kind-<cluster>". Pin every kubectl
# and helm call to it so the demo never operates on whatever context happens to
# be current (which could be a real cluster).
KUBE_CONTEXT="kind-${CLUSTER_NAME}"
IMAGE_NAME="nvml-mock:demo"
CHART_PATH="deployments/nvml-mock/helm/nvml-mock"
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
DEVICE_PLUGIN_MANIFEST="${REPO_ROOT}/tests/e2e/device-plugin-mock.yaml"
# Validator Job manifest lives alongside this demo so the folder is
# self-contained.
VALIDATOR_MANIFEST="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/validator-mock.yaml"
VALIDATOR_JOB="gpu-validator-mock"
VALIDATOR_NAMESPACE="default"
: "${GPU_PROFILE:=h100}"
: "${GPU_COUNT:=8}"
# Deploy into a dedicated namespace (env-overridable) instead of default, so the
# mock stack is easy to isolate and clean up.
: "${NAMESPACE:=nvml-mock-system}"
: "${VALIDATOR_TIMEOUT:=60s}"
# Helm rollout wait. Generous by default so the stack can come up even when the
# amd64 images run under qemu emulation (e.g. on an arm64 host).
: "${HELM_TIMEOUT:=600s}"
# DaemonSet rollout wait, likewise emulation-friendly.
: "${ROLLOUT_TIMEOUT:=300s}"
# FORCE_RECREATE=true tears down an existing cluster of the same name and
# recreates it; otherwise an existing cluster is reused as-is.
: "${FORCE_RECREATE:=false}"

PROFILE_YAML="${REPO_ROOT}/${CHART_PATH}/profiles/${GPU_PROFILE}.yaml"
if [[ ! -f "${PROFILE_YAML}" ]]; then
  echo "ERROR: profile YAML not found: ${PROFILE_YAML}" >&2
  echo "       set GPU_PROFILE to one of: $(ls "${REPO_ROOT}/${CHART_PATH}/profiles/" | sed 's/\.yaml$//' | tr '\n' ' ')" >&2
  exit 1
fi

###############################################################################
# Helpers
###############################################################################
info() { echo "==> $*"; }
fail() { echo "ERROR: $*" >&2; exit 1; }

# Wrap kubectl so every call is pinned to the demo's kind context without
# repeating --context at each call site. helm keeps --kube-context inline
# (there is only one invocation). Every call below passes an explicit -n.
kubectl_ctx() { command kubectl --context "${KUBE_CONTEXT}" "$@"; }

###############################################################################
# Step 1 -- Create a Kind cluster
#
# Reuse an existing cluster of the same name unless FORCE_RECREATE=true, in
# which case tear it down first and create a fresh one.
###############################################################################
# CLUSTER_REUSED tracks whether we kept an existing cluster (vs created a fresh
# one). It gates the DaemonSet restart in Step 4: a fresh cluster already runs
# the just-loaded image, so the restart is only needed on reuse.
CLUSTER_REUSED=false
if kind get clusters 2>/dev/null | grep -qx "${CLUSTER_NAME}"; then
  if [[ "${FORCE_RECREATE}" == "true" ]]; then
    info "Kind cluster '${CLUSTER_NAME}' exists; FORCE_RECREATE=true -> deleting it"
    kind delete cluster --name "${CLUSTER_NAME}"
    info "Creating Kind cluster: ${CLUSTER_NAME}"
    kind create cluster --name "${CLUSTER_NAME}" --config="$REPO_ROOT/docs/demo/kind.yaml"
  else
    info "Reusing existing Kind cluster '${CLUSTER_NAME}' (set FORCE_RECREATE=true to recreate)"
    CLUSTER_REUSED=true
  fi
else
  info "Creating Kind cluster: ${CLUSTER_NAME}"
  kind create cluster --name "${CLUSTER_NAME}" --config="$REPO_ROOT/docs/demo/kind.yaml"
fi

###############################################################################
# Step 2 -- Build the nvml-mock image
###############################################################################
info "Building image: ${IMAGE_NAME}"
# DOCKER_DEFAULT_PLATFORM (set at the top) makes this an amd64 build. When that
# means cross-building on an arm64 host, BuildKit would otherwise emit an OCI
# image index (image + attestation manifest) that `kind load docker-image`
# cannot load cleanly — the kubelet would then try to pull nvml-mock:demo from
# Docker Hub and fail. --provenance=false keeps the output a single image.
docker build --provenance=false -t "${IMAGE_NAME}" -f "${REPO_ROOT}/deployments/nvml-mock/Dockerfile" "${REPO_ROOT}"

###############################################################################
# Step 3 -- Load the nvml-mock image into Kind
#
# Only the locally built nvml-mock image needs loading. The validator runs the
# public CUDA sample image pinned in validator-mock.yaml, which the kubelet
# pulls directly (imagePullPolicy IfNotPresent), so there is no need to import
# it into the cluster.
###############################################################################
info "Loading image into Kind cluster"
kind load docker-image "${IMAGE_NAME}" --name "${CLUSTER_NAME}"

###############################################################################
# Step 4 -- Install nvml-mock via Helm
###############################################################################
info "Installing nvml-mock Helm chart (profile=${GPU_PROFILE}, count=${GPU_COUNT}, namespace=${NAMESPACE})"
helm upgrade --install nvml-mock "${REPO_ROOT}/${CHART_PATH}" \
  --kube-context "${KUBE_CONTEXT}" \
  --namespace "${NAMESPACE}" --create-namespace \
  --set image.repository=nvml-mock \
  --set image.tag=demo \
  --set integrations.fakeGpuOperator.enabled=true \
  --set "gpu.profile=${GPU_PROFILE}" \
  --set "gpu.count=${GPU_COUNT}" \
  --set gpu.dynamicMetrics.enabled=true \
  --wait --timeout "${HELM_TIMEOUT}"

# On cluster reuse, `helm upgrade` is a no-op when values are unchanged, so the
# DaemonSet keeps running its old pods and the stale driver libs (including
# libcuda.so) stay staged on the node. Force a restart so the freshly built and
# kind-loaded ${IMAGE_NAME} — and its libcuda.so interposition — is deployed. A
# freshly created cluster already runs the just-loaded image, so skip it there.
if [[ "${CLUSTER_REUSED}" == "true" ]]; then
  info "Restarting nvml-mock DaemonSet to pick up the freshly loaded image"
  kubectl_ctx -n "${NAMESPACE}" rollout restart daemonset/nvml-mock
fi

###############################################################################
# Step 5 -- Verify: DaemonSet rollout
###############################################################################
info "Waiting for DaemonSet rollout"
kubectl_ctx -n "${NAMESPACE}" rollout status daemonset/nvml-mock --timeout="${ROLLOUT_TIMEOUT}"

###############################################################################
# Step 5b -- Deploy NVIDIA device plugin mock
#
# The VectorAdd validator requests `nvidia.com/gpu: 1`. nvml-mock provides the
# mock driver root and node labels; the device plugin publishes the allocatable
# GPU resource to kubelet so normal workloads can schedule.
###############################################################################
info "Deploying NVIDIA device plugin mock"
kubectl_ctx apply -f "${DEVICE_PLUGIN_MANIFEST}"
kubectl_ctx -n kube-system rollout status daemonset/nvidia-device-plugin-mock --timeout="${ROLLOUT_TIMEOUT}"

info "Waiting for allocatable GPUs to be published"
GPU_NODE=""
ALLOCATABLE_GPUS=""
for _ in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 19 20 21 22 23 24 25 26 27 28 29 30; do
  while IFS=$'\t' read -r node_name gpu_count; do
    if [[ "${gpu_count}" == "${GPU_COUNT}" ]]; then
      GPU_NODE="${node_name}"
      ALLOCATABLE_GPUS="${gpu_count}"
      break
    fi
  done < <(kubectl_ctx get nodes -l nvidia.com/gpu.present=true \
    -o jsonpath='{range .items[*]}{.metadata.name}{"\t"}{.status.allocatable.nvidia\.com/gpu}{"\n"}{end}')

  [[ -n "${GPU_NODE}" ]] && break
  sleep 2
done

if [[ -z "${GPU_NODE}" ]]; then
  kubectl_ctx get nodes -o jsonpath='{range .items[*]}{.metadata.name}{"\t"}{.status.allocatable.nvidia\.com/gpu}{"\n"}{end}' >&2 || true
  fail "Timed out waiting for any node to report ${GPU_COUNT} allocatable GPUs"
fi
info "${GPU_NODE} reports ${ALLOCATABLE_GPUS} allocatable GPU(s)"

###############################################################################
# Step 6 -- Verify: nvidia-smi
###############################################################################
info "Running nvidia-smi inside a DaemonSet pod"
POD=$(kubectl_ctx -n "${NAMESPACE}" get pods -l app.kubernetes.io/name=nvml-mock -o jsonpath='{.items[0].metadata.name}')
kubectl_ctx -n "${NAMESPACE}" exec "${POD}" -- nvidia-smi

###############################################################################
# Step 7 -- Verify: GPU Operator Validator mock (cuda-vectorAdd)
###############################################################################
info "Deploying GPU validator mock (${VALIDATOR_JOB})"
kubectl_ctx -n "${VALIDATOR_NAMESPACE}" delete job "${VALIDATOR_JOB}" --ignore-not-found --wait=true
kubectl_ctx apply -f "${VALIDATOR_MANIFEST}"

info "Waiting for GPU validator mock to complete"
if ! kubectl_ctx -n "${VALIDATOR_NAMESPACE}" wait \
  --for=condition=complete "job/${VALIDATOR_JOB}" \
  --timeout="${VALIDATOR_TIMEOUT}"; then
  info "Validator job did not complete; collecting diagnostics"
  kubectl_ctx -n "${VALIDATOR_NAMESPACE}" describe "job/${VALIDATOR_JOB}" || true
  kubectl_ctx -n "${VALIDATOR_NAMESPACE}" get pods -l name="${VALIDATOR_JOB}" -o wide || true
  kubectl_ctx -n "${VALIDATOR_NAMESPACE}" logs "job/${VALIDATOR_JOB}" --all-containers=true || true
  fail "GPU validator mock did not complete successfully"
fi

info "GPU validator mock logs"
kubectl_ctx -n "${VALIDATOR_NAMESPACE}" logs "job/${VALIDATOR_JOB}" --all-containers=true

###############################################################################
# Summary
###############################################################################
echo
info "Demo complete."
info "  Cluster   : ${CLUSTER_NAME}"
info "  Namespace : ${NAMESPACE}"
info "  Profile   : ${GPU_PROFILE} (gpu.count=${GPU_COUNT})"
info "  GPU node  : ${GPU_NODE}"
info "  VectorAdd : validator job completed (${VALIDATOR_JOB})"
info ""
info "To uninstall the release: helm uninstall nvml-mock -n ${NAMESPACE}"
info "To remove the validator job: kubectl --context ${KUBE_CONTEXT} -n ${VALIDATOR_NAMESPACE} delete job ${VALIDATOR_JOB}"
info "To tear down: kind delete cluster --name ${CLUSTER_NAME}"
