#!/usr/bin/env bash
# Copyright 2026 NVIDIA CORPORATION
#
# SPDX-License-Identifier: Apache-2.0
#
# Deploys the REAL upstream NVIDIA GPU Operator against mock GPUs.
#
# The chart is nvidia/gpu-operator from helm.ngc.nvidia.com, not a fork. Its
# device plugin, GFD, dcgm-exporter and validator operands run unmodified. The
# two operands that genuinely need hardware are turned off: nvml-mock stages
# the driver root, and CDI carries the devices instead of the container
# toolkit. Everything that makes that work lives in gpu-operator-values.yaml
# next to this script.
#
# Written for bash 3.2 (stock macOS): no mapfile, no associative arrays, no
# ${var,,}, no `wait -n`.

set -euo pipefail

# shellcheck source=../lib/preflight.sh
source "$(cd "$(dirname "${BASH_SOURCE[0]}")/../lib" && pwd)/preflight.sh"

###############################################################################
# Configuration
###############################################################################
DEMO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${DEMO_DIR}/../../.." && pwd)"
CHART_PATH="${REPO_ROOT}/deployments/nvml-mock/helm/nvml-mock"

# Every object this demo creates derives from RELEASE_NAME, and the name has
# to satisfy two separate constraints.
#
# 1. It MUST differ from the other demos' release names. The chart's
#    ClusterRole and ClusterRoleBinding are named after the release
#    (templates/rbac.yaml) and are CLUSTER-scoped, so a namespace cannot
#    separate two installs. standalone uses "nvml-mock", failure-injection
#    uses "nvml-mock-failure"; sharing either would abort the install with
#    "invalid ownership metadata".
# 2. It MUST contain the chart name "nvml-mock". nvml-mock.fullname
#    (templates/_helpers.tpl) collapses to the bare release name only when
#    `contains $name .Release.Name`; otherwise it prepends the chart name. A
#    release called "gpu-operator-demo" would render every object as
#    "gpu-operator-demo-nvml-mock", so the DaemonSet this script rolls out and
#    the one helm creates would not be the same object.
RELEASE_NAME="nvml-mock-operator"
# Namespace of its own too, so `kubectl -n mokka get all` still shows one demo
# rather than two interleaved. The distinct release name above is what keeps
# the cluster-scoped objects apart; this only separates namespaced ones.
: "${NAMESPACE:=mokka-operator}"
# The GPU Operator's own release and namespace. Independent of the nvml-mock
# release: this one is the upstream chart.
OPERATOR_RELEASE="gpu-operator"
: "${OPERATOR_NAMESPACE:=gpu-operator}"
OPERATOR_VALUES="${DEMO_DIR}/gpu-operator-values.yaml"
# Pods carry app.kubernetes.io/name=<CHART name> and
# app.kubernetes.io/instance=<RELEASE name> (nvml-mock.selectorLabels). The
# name label is the same string for every release of this chart, so selecting
# on it alone would also match the standalone and failure-injection demos'
# pods on a shared cluster. Pin the instance.
POD_SELECTOR="app.kubernetes.io/name=nvml-mock,app.kubernetes.io/instance=${RELEASE_NAME}"
: "${GPU_PROFILE:=gb300}"
# BUILD_LOCAL=true builds the image from source and side-loads it into a Kind
# cluster. It is the only path that needs Docker and Kind, and it is Kind-only
# by nature: there is no portable way to side-load into a cluster you do not
# control. The default pulls the published image.
: "${BUILD_LOCAL:=false}"
: "${LOCAL_IMAGE:=nvml-mock:gpu-operator-demo}"
# Resolved by demo::preflight. Every kubectl and helm call is pinned to it, so
# a kubeconfig that changes mid-run cannot redirect the install to a cluster
# the reader never saw.
KUBE_CONTEXT=""

###############################################################################
# Helpers
###############################################################################
info() { echo "==> $*"; }
sub()  { echo "    $*"; }
fail() { echo "ERROR: $*" >&2; exit 1; }

# Pin the context without repeating --context at 20 call sites. The namespace
# is NOT baked in here: this demo works in two namespaces (its own and the
# operator's), so every call below passes -n explicitly rather than relying on
# the context default, which the demo never changes.
kubectl_ctx() { command kubectl --context "${KUBE_CONTEXT}" "$@"; }

PROFILE_YAML="${CHART_PATH}/profiles/${GPU_PROFILE}.yaml"
if [[ ! -f "${PROFILE_YAML}" ]]; then
  echo "ERROR: profile YAML not found: ${PROFILE_YAML}" >&2
  echo "       set GPU_PROFILE to one of: $(ls "${CHART_PATH}/profiles/" | sed 's/\.yaml$//' | tr '\n' ' ')" >&2
  exit 1
fi
if [[ ! -f "${OPERATOR_VALUES}" ]]; then
  fail "values overlay not found: ${OPERATOR_VALUES}"
fi

###############################################################################
# Step 1 -- Resolve the target cluster
#
# This demo never creates a cluster, not even on the BUILD_LOCAL path, and
# that is deliberate. The Operator's toolkit-validation and the device plugin
# both go through CDI, so the node needs containerd in CDI mode with the
# nvidia runtime handler registered. A stock `kind create cluster` node has
# neither. `make cluster-create` builds and uses the node image that does
# (deployments/kind-nvidia-cdi), and the README says so; silently creating a
# stock cluster here would just fail later, in the validator, with an error
# that does not name the cause.
###############################################################################
if [[ "${BUILD_LOCAL}" == "true" ]]; then
  # Check before doing anything: a missing docker or kind would otherwise
  # surface as a raw "command not found" after the image build started.
  demo::require_build_tools
fi

# Tell the preflight where the workload lands so the announcement, which is
# the one thing the reader confirms, names the namespace too.
# shellcheck disable=SC2034  # read by demo::preflight in ../lib/preflight.sh
DEMO_NAMESPACE="${NAMESPACE}"
demo::preflight
# The other two portable demos' releases. Co-locating any two of these corrupts
# shared per-node host state that no namespace or release name scopes, and this
# is the demo most likely to meet one of the others: readers arrive here from
# the chart's NOTES.txt after having already run something else.
#
# One call per sibling, rather than one call taking a list. The helper's
# signature is shared with two scripts and six cases in preflight_test.sh;
# adding a third demo is not a reason to change it under them.
demo::require_no_sibling_release "nvml-mock" "${RELEASE_NAME}"
demo::require_no_sibling_release "nvml-mock-failure" "${RELEASE_NAME}"
KUBE_CONTEXT="${DEMO_KUBE_CONTEXT}"
IMAGE_NAME="$(demo::image_ref)"

# Split "repo:tag" for the chart's two separate values. Shared with the other
# demos, and it rejects the digest refs the chart cannot express. `exit` inside
# $() only leaves the subshell, so propagate the code rather than relying on
# set -e to notice the failed assignment.
IMAGE_PARTS="$(demo::image_parts "${IMAGE_NAME}")" || exit $?
IMAGE_REPO="${IMAGE_PARTS%|*}"
IMAGE_TAG="${IMAGE_PARTS#*|}"

###############################################################################
# Step 2 -- Build + side-load the image (BUILD_LOCAL only)
###############################################################################
if [[ "${BUILD_LOCAL}" == "true" ]]; then
  # `kind load` addresses a cluster by kind's own name, which is the context
  # name minus the "kind-" prefix. Off Kind there is no such cluster, so say
  # that rather than letting kind fail with "unknown cluster".
  case "${KUBE_CONTEXT}" in
    kind-*) KIND_CLUSTER="${KUBE_CONTEXT#kind-}" ;;
    *) fail "BUILD_LOCAL=true side-loads the image with 'kind load', which needs a Kind cluster, but the current context is '${KUBE_CONTEXT}'. Push the image to a registry your cluster can pull from and set NVML_MOCK_IMAGE instead." ;;
  esac
  info "Building image: ${IMAGE_NAME}"
  docker build -t "${IMAGE_NAME}" \
    -f "${REPO_ROOT}/deployments/nvml-mock/Dockerfile" "${REPO_ROOT}"
  info "Loading image into Kind cluster '${KIND_CLUSTER}'"
  kind load docker-image "${IMAGE_NAME}" --name "${KIND_CLUSTER}"
else
  info "Using published image: ${IMAGE_NAME} (set BUILD_LOCAL=true to build from source)"
fi

###############################################################################
# Step 3 -- Install nvml-mock
#
# This has to come first. The Operator's driver validation reads the mock
# driver root that this chart stages on the host, and its device plugin and
# GFD read the mock libnvidia-ml.so from the same place.
###############################################################################
info "Installing ${RELEASE_NAME} (profile=${GPU_PROFILE}) into namespace ${NAMESPACE}"
demo::announce_pull "${IMAGE_NAME}"
helm upgrade --install "${RELEASE_NAME}" "${CHART_PATH}" \
  --kube-context "${KUBE_CONTEXT}" \
  --namespace "${NAMESPACE}" --create-namespace \
  --set "image.repository=${IMAGE_REPO}" \
  --set "image.tag=${IMAGE_TAG}" \
  --set "gpu.profile=${GPU_PROFILE}" \
  --set-string updateStrategy.rollingUpdate.maxUnavailable=100% \
  --wait --timeout "$(demo::install_timeout)"

info "Waiting for the ${RELEASE_NAME} DaemonSet rollout"
kubectl_ctx -n "${NAMESPACE}" rollout status "daemonset/${RELEASE_NAME}" \
  --timeout="$(demo::install_timeout)"

# Prove the mock is actually serving NVML before spending another ten minutes
# pulling the Operator's images on top of it. A broken mock otherwise surfaces
# much later, as a driver-validation CrashLoopBackOff.
MOCK_POD="$(kubectl_ctx -n "${NAMESPACE}" get pods -l "${POD_SELECTOR}" \
  --field-selector=status.phase=Running \
  -o jsonpath='{.items[0].metadata.name}')"
[[ -n "${MOCK_POD}" ]] || fail "no Running pod matches ${POD_SELECTOR} in namespace ${NAMESPACE}"
info "Mock NVML is live in ${MOCK_POD}"
kubectl_ctx -n "${NAMESPACE}" exec "${MOCK_POD}" -- nvidia-smi -L

###############################################################################
# Step 4 -- Install the GPU Operator
###############################################################################
info "Adding the NVIDIA Helm repository"
helm repo add nvidia https://helm.ngc.nvidia.com/nvidia --force-update
helm repo update nvidia

info "Installing ${OPERATOR_RELEASE} into namespace ${OPERATOR_NAMESPACE}"
sub "values: ${OPERATOR_VALUES}"
sub "The operator pulls several images of its own on a cold cluster, so this"
sub "waits up to $(demo::install_timeout) as well. Raise it with HELM_TIMEOUT=30m."
helm upgrade --install "${OPERATOR_RELEASE}" nvidia/gpu-operator \
  --kube-context "${KUBE_CONTEXT}" \
  --namespace "${OPERATOR_NAMESPACE}" --create-namespace \
  -f "${OPERATOR_VALUES}" \
  --wait --timeout "$(demo::install_timeout)"

info "Waiting for the operands to become ready"
kubectl_ctx -n "${OPERATOR_NAMESPACE}" wait --for=condition=ready pod --all \
  --timeout="$(demo::install_timeout)"

###############################################################################
# Step 5 -- Verify: the node advertises mock GPUs
#
# Check EVERY node, not just .items[0]. The operands do not tolerate the
# control-plane NoSchedule taint, so on a cluster whose first node is the
# control plane, reading .items[0] reports an empty allocatable on a perfectly
# healthy install.
###############################################################################
info "Checking nvidia.com/gpu in node allocatable"
GPU_NODES=0
while IFS=' ' read -r node_name node_gpus; do
  [[ -n "${node_name}" ]] || continue
  sub "${node_name}: nvidia.com/gpu=${node_gpus:-<none>}"
  if [[ -n "${node_gpus}" && "${node_gpus}" != "0" && "${node_gpus}" != "<none>" ]]; then
    GPU_NODES=$((GPU_NODES + 1))
  fi
done < <(kubectl_ctx get nodes --no-headers \
  -o 'custom-columns=NAME:.metadata.name,GPU:.status.allocatable.nvidia\.com/gpu')

if [[ "${GPU_NODES}" -lt 1 ]]; then
  echo "ERROR: no node advertises nvidia.com/gpu; the device plugin did not register" >&2
  kubectl_ctx -n "${OPERATOR_NAMESPACE}" get pods >&2
  exit 1
fi
info "${GPU_NODES} node(s) advertise mock GPUs"

###############################################################################
# Step 6 -- Verify: GFD labelled the node from mock NVML
#
# nvidia.com/gpu.product is the one label that can only come from reading the
# device, so it is the check that distinguishes "GFD ran" from "GFD read the
# mock". The nvml-mock chart sets nvidia.com/gpu.present itself, so that label
# would be present even with GFD broken.
###############################################################################
info "Checking the GFD labels"
PRODUCT_LABELS="$(kubectl_ctx get nodes \
  -o 'custom-columns=NAME:.metadata.name,PRODUCT:.metadata.labels.nvidia\.com/gpu\.product' \
  --no-headers | grep -v '<none>' || true)"
if [[ -z "${PRODUCT_LABELS}" ]]; then
  echo "ERROR: no node carries nvidia.com/gpu.product; GFD did not publish a label from the mock" >&2
  kubectl_ctx -n "${OPERATOR_NAMESPACE}" get pods >&2
  exit 1
fi
echo "${PRODUCT_LABELS}" | while IFS= read -r line; do sub "${line}"; done

###############################################################################
# Summary
###############################################################################
echo
info "Demo complete. The real GPU Operator is running against mock GPUs."
info "  Context        : ${KUBE_CONTEXT}"
info "  Image          : ${IMAGE_NAME}"
info "  nvml-mock      : release ${RELEASE_NAME} in namespace ${NAMESPACE}"
info "  GPU Operator   : release ${OPERATOR_RELEASE} in namespace ${OPERATOR_NAMESPACE}"
info "  Profile        : ${GPU_PROFILE}"
info "  GPU nodes      : ${GPU_NODES}"
echo
info "Look around:"
info "  kubectl --context ${KUBE_CONTEXT} -n ${OPERATOR_NAMESPACE} get pods"
info "  kubectl --context ${KUBE_CONTEXT} get nodes -o json | jq '.items[].metadata.labels'"
info "  kubectl --context ${KUBE_CONTEXT} -n ${NAMESPACE} exec ds/${RELEASE_NAME} -- nvidia-smi -L"
echo
info "Clean up (the -n and --kube-context are load-bearing: without them helm"
info "resolves the release from whatever context and namespace are current):"
info "  helm uninstall ${OPERATOR_RELEASE} -n ${OPERATOR_NAMESPACE} --kube-context ${KUBE_CONTEXT}"
info "  helm uninstall ${RELEASE_NAME} -n ${NAMESPACE} --kube-context ${KUBE_CONTEXT}"
info "This demo never created a cluster, so nothing here deletes one."
