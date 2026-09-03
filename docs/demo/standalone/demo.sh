#!/usr/bin/env bash
# Copyright 2026 NVIDIA CORPORATION
#
# SPDX-License-Identifier: Apache-2.0

set -euo pipefail

# shellcheck source=../lib/preflight.sh
source "$(cd "$(dirname "${BASH_SOURCE[0]}")/../lib" && pwd)/preflight.sh"

###############################################################################
# Configuration
#
# GPU_PROFILE / GPU_COUNT are env-overridable so the same demo can drive any
# of the chart's built-in profiles, e.g.
#   GPU_PROFILE=gb200 ./demo.sh
#   GPU_PROFILE=t4    ./demo.sh
# GPU_COUNT defaults to the selected profile's device list, so profiles that
# model fewer GPUs than an 8-GPU baseboard (t4, and the 4-GPU Grace-Blackwell
# trays gb200/gb300) need no second override. The PCI-sysfs assertions in
# step 9 derive their expected values from GPU_COUNT and from the profile's
# `pcie_topology:` block, so switching profile keeps the demo correct without
# further edits.
###############################################################################
CLUSTER_NAME="nvml-mock-demo"
# Every object this demo creates derives from RELEASE_NAME. The chart's
# ClusterRole and ClusterRoleBinding are named after the release and are
# CLUSTER-scoped (templates/rbac.yaml), so a demo that hardcodes its release
# name in several places is one edit away from colliding with the
# failure-injection demo, which installs "nvml-mock-failure".
RELEASE_NAME="nvml-mock"
# Pods carry app.kubernetes.io/name=<CHART name> and
# app.kubernetes.io/instance=<RELEASE name> (templates/_helpers.tpl,
# nvml-mock.selectorLabels). Selecting on the chart name alone matches BOTH
# demos' pods when they share a namespace, and combined with jsonpath
# .items[0] that means exec'ing into the other demo's pod. Pin the instance.
POD_SELECTOR="app.kubernetes.io/name=nvml-mock,app.kubernetes.io/instance=${RELEASE_NAME}"
# The demo used to hardcode KUBE_CONTEXT="kind-${CLUSTER_NAME}" so it could
# never operate on whatever context happened to be current. It now installs
# into the current context on purpose, so demo::preflight carries that guard
# instead: it prints the target context, server, node count and namespace,
# and unless the target is a loopback Kind cluster it requires the context
# name typed back, or DEMO_ASSUME_YES=true when there is no terminal to
# answer on. Resolved below, then every kubectl and helm call is pinned to it.
KUBE_CONTEXT=""
CHART_PATH="deployments/nvml-mock/helm/nvml-mock"
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
: "${GPU_PROFILE:=h100}"
# Deploy into a dedicated namespace (env-overridable) instead of default, so the
# mock stack is easy to isolate and clean up. The namespace is also set as the
# current context's default so the validate-*.sh helpers (which exec into pods
# without a -n flag) target it too.
: "${NAMESPACE:=mokka}"
# BUILD_LOCAL=true builds the image from source and side-loads it into a Kind
# cluster, creating that cluster if it is missing. It is the ONLY path that
# needs Docker and Kind, because there is no portable way to side-load an
# image into a cluster you do not control. The default installs the published
# image into the cluster your current context already points at, and never
# creates or deletes a cluster.
: "${BUILD_LOCAL:=false}"
: "${LOCAL_IMAGE:=nvml-mock:demo}"
# FORCE_RECREATE=true tears down an existing cluster of the same name and
# recreates it; otherwise an existing cluster is reused as-is. Only meaningful
# alongside BUILD_LOCAL=true, since that is the only path that owns a cluster.
: "${FORCE_RECREATE:=false}"
# Populated only on the BUILD_LOCAL path, when the demo switches contexts.
PRIOR_CONTEXT=""

PROFILE_YAML="${REPO_ROOT}/${CHART_PATH}/profiles/${GPU_PROFILE}.yaml"
if [[ ! -f "${PROFILE_YAML}" ]]; then
  echo "ERROR: profile YAML not found: ${PROFILE_YAML}" >&2
  echo "       set GPU_PROFILE to one of: $(ls "${REPO_ROOT}/${CHART_PATH}/profiles/" | sed 's/\.yaml$//' | tr '\n' ' ')" >&2
  exit 1
fi

# Default the GPU count to the profile's device list, matching what the chart
# derives when gpu.count is empty. Asking for more than the profile declares
# would leave the step 9 PCI-sysfs assertions expecting devices the renderer
# never materializes.
: "${GPU_COUNT:=$(grep -c "^[[:space:]]*- index:" "${PROFILE_YAML}")}"

# Count the `- id: "pci...` rows under `pcie_topology.root_complexes`. The
# renderer falls back to a flat single-root layout for profiles without an
# explicit block, so default to 1 if the YAML carries no pcie_topology.
EXPECTED_ROOTS=$(awk '/^    - id: "pci/ {n++} END {print (n>0)?n:1}' "${PROFILE_YAML}")
IB_ENABLED=$(awk '
  /^infiniband:/ {in_ib=1; next}
  in_ib && /^[^[:space:]]/ {in_ib=0}
  in_ib && /^[[:space:]]+enabled:/ {print $2; found=1; exit}
  END {if (!found) print "false"}
' "${PROFILE_YAML}")

###############################################################################
# Helpers
###############################################################################
info() { echo "==> $*"; }
fail() { echo "ERROR: $*" >&2; exit 1; }

# Wrap kubectl so every call is pinned to the context the preflight resolved
# and announced, without repeating --context at each call site. helm keeps
# --kube-context inline (there is only one invocation). The external
# validate-*.sh helpers still rely on the context's default namespace set
# after the helm install below.
kubectl_ctx() { command kubectl --context "${KUBE_CONTEXT}" "$@"; }

###############################################################################
# Step 1 -- Resolve the target cluster
#
# BUILD_LOCAL=true owns a Kind cluster: it creates one if missing (reusing an
# existing cluster of the same name unless FORCE_RECREATE=true) and switches
# to it, because a locally built image can only be side-loaded into a cluster
# this script controls. Otherwise nothing is created and the demo installs
# into whatever context is already current, which demo::preflight announces
# and, unless it is a loopback Kind cluster, makes you confirm.
###############################################################################
if [[ "${BUILD_LOCAL}" == "true" ]]; then
  # Before creating anything: a missing docker or kind here would otherwise
  # surface as a raw "command not found" after a cluster already existed.
  demo::require_build_tools
  if kind get clusters 2>/dev/null | grep -qx "${CLUSTER_NAME}"; then
    if [[ "${FORCE_RECREATE}" == "true" ]]; then
      info "Kind cluster '${CLUSTER_NAME}' exists; FORCE_RECREATE=true -> deleting it"
      kind delete cluster --name "${CLUSTER_NAME}"
      info "Creating Kind cluster: ${CLUSTER_NAME}"
      kind create cluster --name "${CLUSTER_NAME}" --config="$REPO_ROOT/docs/demo/kind.yaml"
    else
      info "Reusing existing Kind cluster '${CLUSTER_NAME}' (set FORCE_RECREATE=true to recreate)"
    fi
  else
    info "Creating Kind cluster: ${CLUSTER_NAME}"
    kind create cluster --name "${CLUSTER_NAME}" --config="$REPO_ROOT/docs/demo/kind.yaml"
  fi
  # Remember where the reader was so the summary can offer to put them back.
  PRIOR_CONTEXT="$(command kubectl config current-context 2>/dev/null || true)"
  command kubectl config use-context "kind-${CLUSTER_NAME}"
fi

# Tell the preflight where the workload lands so its announcement, which is
# the one thing the reader confirms, names the namespace too.
# shellcheck disable=SC2034  # read by demo::preflight in ../lib/preflight.sh
DEMO_NAMESPACE="${NAMESPACE}"
demo::preflight
# The failure-injection demo's release. Co-locating the two corrupts shared
# per-node host state that no namespace or release name scopes.
demo::require_no_sibling_release "nvml-mock-failure" "${RELEASE_NAME}"
# And the GPU Operator demo's release, added later. Every demo has to know
# about every other one: a guard that covers one direction only is the same
# asymmetry that shipped the original defect.
demo::require_no_sibling_release "nvml-mock-operator" "${RELEASE_NAME}"
KUBE_CONTEXT="${DEMO_KUBE_CONTEXT}"
IMAGE_NAME="$(demo::image_ref)"

# Split "repo:tag" for the chart's two separate values. Shared with the
# failure-injection demo, and it rejects digest refs the chart cannot express.
# `exit` inside $() only leaves the subshell, so propagate the code explicitly
# rather than relying on set -e to notice the failed assignment.
IMAGE_PARTS="$(demo::image_parts "${IMAGE_NAME}")" || exit $?
IMAGE_REPO="${IMAGE_PARTS%|*}"
IMAGE_TAG="${IMAGE_PARTS#*|}"

###############################################################################
# Step 2 -- Build the nvml-mock image (BUILD_LOCAL only)
###############################################################################
if [[ "${BUILD_LOCAL}" == "true" ]]; then
  info "Building image: ${IMAGE_NAME}"
  docker build -t "${IMAGE_NAME}" -f "${REPO_ROOT}/deployments/nvml-mock/Dockerfile" "${REPO_ROOT}"

  ###############################################################################
  # Step 3 -- Load image into Kind (BUILD_LOCAL only)
  ###############################################################################
  info "Loading image into Kind cluster"
  kind load docker-image "${IMAGE_NAME}" --name "${CLUSTER_NAME}"
else
  info "Using published image: ${IMAGE_NAME} (set BUILD_LOCAL=true to build from source)"
fi

###############################################################################
# Step 4 -- Install nvml-mock via Helm
###############################################################################
info "Installing ${RELEASE_NAME} Helm chart (profile=${GPU_PROFILE}, count=${GPU_COUNT}, namespace=${NAMESPACE})"
demo::announce_pull "${IMAGE_NAME}"
# maxUnavailable=100% matters on RE-RUNS, not on a first install: a fresh
# DaemonSet creates every pod at once regardless, so it is the rolling update
# on a second run that would otherwise serialise four multi-minute image
# pulls at the chart's 25% default. See demo::install_timeout.
helm upgrade --install "${RELEASE_NAME}" "${REPO_ROOT}/${CHART_PATH}" \
  --kube-context "${KUBE_CONTEXT}" \
  --namespace "${NAMESPACE}" --create-namespace \
  --set "image.repository=${IMAGE_REPO}" \
  --set "image.tag=${IMAGE_TAG}" \
  --set integrations.fakeGpuOperator.enabled=true \
  --set "gpu.profile=${GPU_PROFILE}" \
  --set "gpu.count=${GPU_COUNT}" \
  --set gpu.dynamicMetrics.enabled=true \
  --set-string updateStrategy.rollingUpdate.maxUnavailable=100% \
  --wait --timeout "$(demo::install_timeout)"

# Make the demo namespace the context default so the validate-*.sh helpers
# (which run `kubectl exec <pod>` without -n) resolve pods in it. Name the
# context explicitly (not --current) so this always writes to the context the
# preflight announced, even if something repoints the kubeconfig mid-run.
#
# This edits your kubeconfig. It is the one persistent change the demo makes
# outside the cluster, so it says how to undo it, restoring the namespace that
# was actually current beforehand rather than assuming it was "default": a
# reader working in "team-a" would otherwise be moved silently.
info "Setting default namespace to ${NAMESPACE} for the '${KUBE_CONTEXT}' context"
command kubectl config set-context "${KUBE_CONTEXT}" --namespace="${NAMESPACE}"
info "  undo with: $(demo::namespace_undo_hint)"

###############################################################################
# Step 5 -- Verify: DaemonSet rollout
###############################################################################
info "Waiting for DaemonSet rollout"
kubectl_ctx -n "${NAMESPACE}" rollout status "daemonset/${RELEASE_NAME}" --timeout="$(demo::install_timeout)"

###############################################################################
# Step 6 -- Verify: Profile ConfigMaps
###############################################################################
info "Checking profile ConfigMaps"
CM_COUNT=$(kubectl_ctx -n "${NAMESPACE}" get configmaps -l run.ai/gpu-profile=true \
  --no-headers 2>/dev/null | wc -l | tr -d ' ')

if [[ "${CM_COUNT}" -lt 6 ]]; then
  fail "Expected at least 6 profile ConfigMaps, found ${CM_COUNT}"
fi
info "Found ${CM_COUNT} profile ConfigMap(s)"

###############################################################################
# Step 7 -- Verify: nvidia-smi
###############################################################################
info "Running nvidia-smi inside a DaemonSet pod"
POD=$(kubectl_ctx -n "${NAMESPACE}" get pods -l "${POD_SELECTOR}" -o jsonpath='{.items[0].metadata.name}')
kubectl_ctx -n "${NAMESPACE}" exec "${POD}" -- nvidia-smi

###############################################################################
# Step 7b -- Verify: NVLink / NVSwitch topology (nvidia-smi topo -m + nvlink)
#
# validate-nvlink.sh asserts the profile-specific NV# matrix (a100 -> NV12,
# h100/gb200/gb300 -> NV18; t4/l40s/standalone-b200 -> none), that topo -m
# prints the legend + CPU/NUMA Affinity columns, and that `nvlink -s`/`-c`
# enumerate links for NVLink profiles. It runs the host-driver-root nvidia-smi
# via `docker exec` on the Kind node, so resolve the node container (== the
# Kubernetes node name in Kind) from the pod we just exec'd into.
#
# It therefore only works when the node really is a Kind container on this
# host. On any other cluster the node is not a local docker container, so
# skip rather than aborting a run that has already installed successfully.
###############################################################################
NODE_CONTAINER=$(kubectl_ctx -n "${NAMESPACE}" get pod "${POD}" -o jsonpath='{.spec.nodeName}')
NVLINK_STATUS="validated (profile=${GPU_PROFILE})"
if command -v docker >/dev/null 2>&1 && docker inspect "${NODE_CONTAINER}" >/dev/null 2>&1; then
  info "Validating NVLink / NVSwitch topology"
  "${REPO_ROOT}/tests/e2e/validate-nvlink.sh" "${NODE_CONTAINER}" "${GPU_PROFILE}" "${GPU_COUNT}"
else
  NVLINK_STATUS="skipped (node '${NODE_CONTAINER}' is not a local Kind container)"
  info "Skipping NVLink / NVSwitch validation: it runs the host-driver-root"
  info "  nvidia-smi via 'docker exec' on the Kind node, which needs a local"
  info "  Kind cluster. ${NVLINK_STATUS}"
fi

###############################################################################
# Step 8 -- Verify: InfiniBand mock (libibmocksys.so + mock-ib render)
###############################################################################
HCA_COUNT=0
if [[ "${IB_ENABLED}" == "true" ]]; then
  info "Listing simulated InfiniBand HCAs (ibstat -l)"
  kubectl_ctx -n "${NAMESPACE}" exec "${POD}" -- ibstat -l

  info "Running ibstatus inside the DaemonSet pod (first 40 lines)"
  # Run head inside the pod: piping locally triggers SIGPIPE (exit 141) with set -o pipefail.
  kubectl_ctx -n "${NAMESPACE}" exec "${POD}" -- sh -c 'ibstatus | head -40'

  HCA_COUNT=$(kubectl_ctx -n "${NAMESPACE}" exec "${POD}" -- ibstat -l | wc -l | tr -d ' ')
  if [[ "${HCA_COUNT}" -lt 1 ]]; then
    fail "Expected at least 1 mock HCA, found ${HCA_COUNT}"
  fi
  info "Found ${HCA_COUNT} mock HCA(s)"

  info "Validating ibv_devinfo (list + smoke output)"
  "${REPO_ROOT}/tests/e2e/validate-ibv-devinfo.sh" "${POD}" "${GPU_PROFILE}" "${HCA_COUNT}"
else
  info "Skipping InfiniBand validation for profile=${GPU_PROFILE} (infiniband.enabled=false)"
fi

###############################################################################
# Step 9 -- Verify: PCI sysfs mock (render-pci-sysfs)
#
# The init container materialized a fake /sys/bus/pci tree at
# /var/lib/nvml-mock/sys/... from the profile's `pcie_topology:` block.
# Topology-aware consumers (NVIDIA DRA driver `dra.k8s.io/pcieRoot`,
# device-plugin NUMA hints) resolve PCIe root complex via readlink() on
# /sys/bus/pci/devices/<bdf>, so we exercise the same path here: list,
# readlink, and read a numa_node file through the symlink.
###############################################################################
PCI_DEV_DIR="/var/lib/nvml-mock/sys/bus/pci/devices"

info "Listing rendered PCI devices under ${PCI_DEV_DIR}"
kubectl_ctx -n "${NAMESPACE}" exec "${POD}" -- ls "${PCI_DEV_DIR}"

PCI_DEV_COUNT=$(kubectl_ctx -n "${NAMESPACE}" exec "${POD}" -- sh -c "ls ${PCI_DEV_DIR} 2>/dev/null | wc -l" \
  | tr -d ' ')
# One symlink per device must appear under bus/pci/devices. We expect
# exactly GPU_COUNT of them (the helm install above set gpu.count to the
# same value, and the chart wires that into the profile's `devices:` list).
if [[ "${PCI_DEV_COUNT}" -ne "${GPU_COUNT}" ]]; then
  fail "Expected ${GPU_COUNT} rendered PCI devices (profile=${GPU_PROFILE}, gpu.count=${GPU_COUNT}), found ${PCI_DEV_COUNT}"
fi
info "Found ${PCI_DEV_COUNT} rendered PCI device symlink(s)"

# The deviceattribute library extracts the PCIe root complex by
# readlink()'ing the device path and parsing out the "pciDDDD:BB"
# component. Exercise that exact contract on the first device so a
# missing or absolute-path symlink would fail the demo loudly.
FIRST_DEV=$(kubectl_ctx -n "${NAMESPACE}" exec "${POD}" -- sh -c "ls ${PCI_DEV_DIR} | sort | head -1" \
  | tr -d '[:space:]')
TARGET=$(kubectl_ctx -n "${NAMESPACE}" exec "${POD}" -- readlink "${PCI_DEV_DIR}/${FIRST_DEV}" \
  | tr -d '[:space:]')
info "readlink ${FIRST_DEV} -> ${TARGET}"
if [[ "${TARGET}" != ../../../devices/pci*/* ]]; then
  fail "Expected relative ../../../devices/pciDDDD:BB/<bdf> target, got '${TARGET}'"
fi

# numa_node is the second half of the contract: the DRA driver may also
# read it to surface a NUMA hint alongside pcieRoot.
NUMA_NODE=$(kubectl_ctx -n "${NAMESPACE}" exec "${POD}" -- cat "${PCI_DEV_DIR}/${FIRST_DEV}/numa_node" \
  | tr -d '[:space:]')
if ! [[ "${NUMA_NODE}" =~ ^-?[0-9]+$ ]]; then
  fail "numa_node for ${FIRST_DEV} is not a number: '${NUMA_NODE}'"
fi
info "${FIRST_DEV} numa_node=${NUMA_NODE}"

# Count distinct root complexes the symlinks resolve to. The expected
# count was derived from the profile's `pcie_topology.root_complexes`
# block at the top of the script, so e.g. h100/a100/b200/l40s/gb200 -> 2,
# t4 -> 1. A regression that collapsed all devices onto a
# single root would silently break NUMA-aware scheduling.
# readlink target shape: "../../../devices/pciDDDD:BB/<bdf>"
# Splitting on "/" yields: $1=.. $2=.. $3=.. $4=devices $5=pciDDDD:BB
# so the root complex is field #5.
ROOT_COUNT=$(kubectl_ctx -n "${NAMESPACE}" exec "${POD}" -- sh -c \
  "for d in ${PCI_DEV_DIR}/*; do readlink \"\$d\"; done" \
  | awk -F/ '{print $5}' | sort -u | wc -l | tr -d ' ')
if [[ "${ROOT_COUNT}" -ne "${EXPECTED_ROOTS}" ]]; then
  fail "Expected ${EXPECTED_ROOTS} distinct PCI root complexes for ${GPU_PROFILE}, found ${ROOT_COUNT}"
fi
info "Devices span ${ROOT_COUNT} distinct PCI root complex(es)"

###############################################################################
# Step 10 -- Verify: cross-node mock ibping (mock-ib + libibmockumad)
###############################################################################
SERVER_POD=""
CLIENT_POD=""
if [[ "${IB_ENABLED}" == "true" ]]; then
  # Collect all Running nvml-mock pod names into an array and check the count
  # before indexing. Reading jsonpath '{.items[1]}' directly would error when
  # only one pod is Running and, under `set -e`, abort the demo right here —
  # before the friendly check below could explain why.
  #
  # Use a `while read` loop rather than `mapfile`/`readarray`: those are
  # bash 4.0+ builtins and macOS still ships bash 3.2, so `mapfile` aborts
  # the demo with "command not found" on stock macOS.
  IB_PODS=()
  while IFS= read -r ib_pod; do
    [[ -n "${ib_pod}" ]] && IB_PODS+=("${ib_pod}")
  done < <(kubectl_ctx -n "${NAMESPACE}" get pods -l "${POD_SELECTOR}" \
    --field-selector=status.phase=Running \
    -o jsonpath='{range .items[*]}{.metadata.name}{"\n"}{end}')
  if [[ "${#IB_PODS[@]}" -lt 2 ]]; then
    fail "Expected at least 2 running ${RELEASE_NAME} pods for cross-node ibping, found ${#IB_PODS[@]}"
  fi
  SERVER_POD="${IB_PODS[0]}"
  CLIENT_POD="${IB_PODS[1]}"
  info "Cross-node ibping: server=${SERVER_POD} client=${CLIENT_POD}"
  "${REPO_ROOT}/tests/e2e/validate-ibping.sh" "${SERVER_POD}" "${CLIENT_POD}"

  info "Validating cross-node iblinkinfo (fabric scan includes peer HCAs)"
  "${REPO_ROOT}/tests/e2e/validate-iblinkinfo.sh" "${SERVER_POD}" "${CLIENT_POD}" \
    "${GPU_PROFILE}" "${HCA_COUNT}"
else
  info "Skipping cross-node ibping/iblinkinfo for profile=${GPU_PROFILE} (infiniband.enabled=false)"
fi

###############################################################################
# Step 11 -- Show node labels
###############################################################################
info "Node labels"
kubectl_ctx get nodes --show-labels

WORKERS=($(kubectl_ctx get nodes --no-headers -o custom-columns=":metadata.name" \
  | grep -v control-plane))

###############################################################################
# Summary
###############################################################################
echo
info "Demo complete."
info "  Context   : ${KUBE_CONTEXT}"
info "  Image     : ${IMAGE_NAME}"
info "  Namespace : ${NAMESPACE}"
info "  Profile   : ${GPU_PROFILE} (gpu.count=${GPU_COUNT})"
info "  Workers   : ${#WORKERS[@]}"
info "  ConfigMaps: ${CM_COUNT}"
info "  Mock HCAs : ${HCA_COUNT} per pod"
info "  PCI devs  : ${PCI_DEV_COUNT} across ${ROOT_COUNT} root complex(es)"
info "  NVLink    : topo -m + nvlink ${NVLINK_STATUS}"
if [[ "${IB_ENABLED}" == "true" ]]; then
  info "  ibping    : cross-node OK (${SERVER_POD} -> ${CLIENT_POD})"
  info "  ibv_devinfo / iblinkinfo: validated (profile=${GPU_PROFILE})"
else
  info "  ibping    : skipped (profile=${GPU_PROFILE} has InfiniBand disabled)"
  info "  ibv_devinfo / iblinkinfo: skipped"
fi
info ""
info "To uninstall the release: helm uninstall ${RELEASE_NAME} -n ${NAMESPACE} --kube-context ${KUBE_CONTEXT}"
info "To restore your context's default namespace:"
info "  $(demo::namespace_undo_hint)"
if [[ "${BUILD_LOCAL}" == "true" ]]; then
  info "To tear down the cluster this run owns: kind delete cluster --name ${CLUSTER_NAME}"
  if [[ -n "${PRIOR_CONTEXT}" && "${PRIOR_CONTEXT}" != "${KUBE_CONTEXT}" ]]; then
    info "This run switched your current context. To switch back:"
    info "  kubectl config use-context ${PRIOR_CONTEXT}"
  fi
else
  info "The cluster was already yours, so nothing here deletes it."
fi
