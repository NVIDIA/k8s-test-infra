#!/usr/bin/env bash
# Copyright 2026 NVIDIA CORPORATION
#
# SPDX-License-Identifier: Apache-2.0

set -euo pipefail

###############################################################################
# Configuration
#
# GPU_PROFILE / GPU_COUNT are env-overridable so the same demo can drive any
# of the chart's built-in profiles, e.g.
#   GPU_PROFILE=gb200 GPU_COUNT=8 ./demo.sh
#   GPU_PROFILE=t4    GPU_COUNT=4 ./demo.sh
# The PCI-sysfs assertions in step 9 derive their expected values from
# GPU_COUNT and from the profile's `pcie_topology:` block, so switching
# profile keeps the demo correct without further edits.
###############################################################################
CLUSTER_NAME="nvml-mock-demo"
# Kind creates a kubeconfig context named "kind-<cluster>". Pin every kubectl
# and helm call to it so the demo never operates on whatever context happens to
# be current (which could be a real cluster).
KUBE_CONTEXT="kind-${CLUSTER_NAME}"
IMAGE_NAME="nvml-mock:demo"
CHART_PATH="deployments/nvml-mock/helm/nvml-mock"
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
: "${GPU_PROFILE:=h100}"
: "${GPU_COUNT:=8}"
# Deploy into a dedicated namespace (env-overridable) instead of default, so the
# mock stack is easy to isolate and clean up. The namespace is also set as the
# current context's default so the validate-*.sh helpers (which exec into pods
# without a -n flag) target it too.
: "${NAMESPACE:=mokka}"
# FORCE_RECREATE=true tears down an existing cluster of the same name and
# recreates it; otherwise an existing cluster is reused as-is.
: "${FORCE_RECREATE:=false}"

PROFILE_YAML="${REPO_ROOT}/${CHART_PATH}/profiles/${GPU_PROFILE}.yaml"
if [[ ! -f "${PROFILE_YAML}" ]]; then
  echo "ERROR: profile YAML not found: ${PROFILE_YAML}" >&2
  echo "       set GPU_PROFILE to one of: $(ls "${REPO_ROOT}/${CHART_PATH}/profiles/" | sed 's/\.yaml$//' | tr '\n' ' ')" >&2
  exit 1
fi

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

# Wrap kubectl so every call is pinned to the demo's kind context without
# repeating --context at each call site. helm keeps --kube-context inline
# (there is only one invocation). The external validate-*.sh helpers still
# rely on the context's default namespace set after the helm install below.
kubectl_ctx() { command kubectl --context "${KUBE_CONTEXT}" "$@"; }

###############################################################################
# Step 1 -- Create a Kind cluster
#
# Reuse an existing cluster of the same name unless FORCE_RECREATE=true, in
# which case tear it down first and create a fresh one.
###############################################################################
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

###############################################################################
# Step 2 -- Build the nvml-mock image
###############################################################################
info "Building image: ${IMAGE_NAME}"
docker build -t "${IMAGE_NAME}" -f "${REPO_ROOT}/deployments/nvml-mock/Dockerfile" "${REPO_ROOT}"

###############################################################################
# Step 3 -- Load image into Kind
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
  --wait --timeout 120s

# Make the demo namespace the context default so the validate-*.sh helpers
# (which run `kubectl exec <pod>` without -n) resolve pods in it. Pin the
# kind-${CLUSTER_NAME} context explicitly (not --current): on the
# cluster-reuse branch nothing has switched contexts, so --current could
# silently repoint an unrelated kubeconfig's default namespace. This context
# is torn down with the cluster.
info "Setting default namespace to ${NAMESPACE} for the kind-${CLUSTER_NAME} context"
command kubectl config set-context "kind-${CLUSTER_NAME}" --namespace="${NAMESPACE}"

###############################################################################
# Step 5 -- Verify: DaemonSet rollout
###############################################################################
info "Waiting for DaemonSet rollout"
kubectl_ctx -n "${NAMESPACE}" rollout status daemonset/nvml-mock --timeout=60s

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
POD=$(kubectl_ctx -n "${NAMESPACE}" get pods -l app.kubernetes.io/name=nvml-mock -o jsonpath='{.items[0].metadata.name}')
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
###############################################################################
info "Validating NVLink / NVSwitch topology"
NODE_CONTAINER=$(kubectl_ctx -n "${NAMESPACE}" get pod "${POD}" -o jsonpath='{.spec.nodeName}')
"${REPO_ROOT}/tests/e2e/validate-nvlink.sh" "${NODE_CONTAINER}" "${GPU_PROFILE}" "${GPU_COUNT}"

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
# block at the top of the script, so e.g. h100/a100/b200/l40s -> 2,
# gb200 -> 4, t4 -> 1. A regression that collapsed all devices onto a
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
  done < <(kubectl_ctx -n "${NAMESPACE}" get pods -l app.kubernetes.io/name=nvml-mock \
    --field-selector=status.phase=Running \
    -o jsonpath='{range .items[*]}{.metadata.name}{"\n"}{end}')
  if [[ "${#IB_PODS[@]}" -lt 2 ]]; then
    fail "Expected at least 2 running nvml-mock pods for cross-node ibping, found ${#IB_PODS[@]}"
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
info "  Cluster   : ${CLUSTER_NAME}"
info "  Namespace : ${NAMESPACE}"
info "  Profile   : ${GPU_PROFILE} (gpu.count=${GPU_COUNT})"
info "  Workers   : ${#WORKERS[@]}"
info "  ConfigMaps: ${CM_COUNT}"
info "  Mock HCAs : ${HCA_COUNT} per pod"
info "  PCI devs  : ${PCI_DEV_COUNT} across ${ROOT_COUNT} root complex(es)"
info "  NVLink    : topo -m + nvlink validated (profile=${GPU_PROFILE})"
if [[ "${IB_ENABLED}" == "true" ]]; then
  info "  ibping    : cross-node OK (${SERVER_POD} -> ${CLIENT_POD})"
  info "  ibv_devinfo / iblinkinfo: validated (profile=${GPU_PROFILE})"
else
  info "  ibping    : skipped (profile=${GPU_PROFILE} has InfiniBand disabled)"
  info "  ibv_devinfo / iblinkinfo: skipped"
fi
info ""
info "To uninstall the release: helm uninstall nvml-mock -n ${NAMESPACE}"
info "To tear down: kind delete cluster --name ${CLUSTER_NAME}"
