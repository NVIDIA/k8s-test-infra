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
IMAGE_NAME="nvml-mock:demo"
CHART_PATH="deployments/nvml-mock/helm/nvml-mock"
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
: "${GPU_PROFILE:=h100}"
: "${GPU_COUNT:=8}"

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

###############################################################################
# Step 1 -- Create a Kind cluster
###############################################################################
info "Creating Kind cluster: ${CLUSTER_NAME}"
kind create cluster --name "${CLUSTER_NAME}" --config=$REPO_ROOT/docs/demo/kind.yaml

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
info "Installing nvml-mock Helm chart (profile=${GPU_PROFILE}, count=${GPU_COUNT})"
helm install nvml-mock "${REPO_ROOT}/${CHART_PATH}" \
  --set image.repository=nvml-mock \
  --set image.tag=demo \
  --set integrations.fakeGpuOperator.enabled=true \
  --set "gpu.profile=${GPU_PROFILE}" \
  --set "gpu.count=${GPU_COUNT}" \
  --set gpu.dynamicMetrics.enabled=true \
  --wait --timeout 120s

###############################################################################
# Step 5 -- Verify: DaemonSet rollout
###############################################################################
info "Waiting for DaemonSet rollout"
kubectl rollout status daemonset/nvml-mock --timeout=60s

###############################################################################
# Step 6 -- Verify: Profile ConfigMaps
###############################################################################
info "Checking profile ConfigMaps"
CM_COUNT=$(kubectl get configmaps -l run.ai/gpu-profile=true \
  --no-headers 2>/dev/null | wc -l | tr -d ' ')

if [[ "${CM_COUNT}" -lt 6 ]]; then
  fail "Expected at least 6 profile ConfigMaps, found ${CM_COUNT}"
fi
info "Found ${CM_COUNT} profile ConfigMap(s)"

###############################################################################
# Step 7 -- Verify: nvidia-smi
###############################################################################
info "Running nvidia-smi inside a DaemonSet pod"
POD=$(kubectl get pods -l app.kubernetes.io/name=nvml-mock -o jsonpath='{.items[0].metadata.name}')
kubectl exec "${POD}" -- nvidia-smi

###############################################################################
# Step 8 -- Verify: InfiniBand mock (libibmocksys.so + mock-ib render)
###############################################################################
HCA_COUNT=0
if [[ "${IB_ENABLED}" == "true" ]]; then
  info "Listing simulated InfiniBand HCAs (ibstat -l)"
  kubectl exec "${POD}" -- ibstat -l

  info "Running ibstatus inside the DaemonSet pod (first 40 lines)"
  # Run head inside the pod: piping locally triggers SIGPIPE (exit 141) with set -o pipefail.
  kubectl exec "${POD}" -- sh -c 'ibstatus | head -40'

  HCA_COUNT=$(kubectl exec "${POD}" -- ibstat -l | wc -l | tr -d ' ')
  if [[ "${HCA_COUNT}" -lt 1 ]]; then
    fail "Expected at least 1 mock HCA, found ${HCA_COUNT}"
  fi
  info "Found ${HCA_COUNT} mock HCA(s)"

  info "Validating ibv_devinfo (list + smoke output)"
  chmod +x "${REPO_ROOT}/tests/e2e/validate-ibv-devinfo.sh"
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
kubectl exec "${POD}" -- ls "${PCI_DEV_DIR}"

PCI_DEV_COUNT=$(kubectl exec "${POD}" -- sh -c "ls ${PCI_DEV_DIR} 2>/dev/null | wc -l" \
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
FIRST_DEV=$(kubectl exec "${POD}" -- sh -c "ls ${PCI_DEV_DIR} | sort | head -1" \
  | tr -d '[:space:]')
TARGET=$(kubectl exec "${POD}" -- readlink "${PCI_DEV_DIR}/${FIRST_DEV}" \
  | tr -d '[:space:]')
info "readlink ${FIRST_DEV} -> ${TARGET}"
if [[ "${TARGET}" != ../../../devices/pci*/* ]]; then
  fail "Expected relative ../../../devices/pciDDDD:BB/<bdf> target, got '${TARGET}'"
fi

# numa_node is the second half of the contract: the DRA driver may also
# read it to surface a NUMA hint alongside pcieRoot.
NUMA_NODE=$(kubectl exec "${POD}" -- cat "${PCI_DEV_DIR}/${FIRST_DEV}/numa_node" \
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
ROOT_COUNT=$(kubectl exec "${POD}" -- sh -c \
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
  done < <(kubectl get pods -l app.kubernetes.io/name=nvml-mock \
    --field-selector=status.phase=Running \
    -o jsonpath='{range .items[*]}{.metadata.name}{"\n"}{end}')
  if [[ "${#IB_PODS[@]}" -lt 2 ]]; then
    fail "Expected at least 2 running nvml-mock pods for cross-node ibping, found ${#IB_PODS[@]}"
  fi
  SERVER_POD="${IB_PODS[0]}"
  CLIENT_POD="${IB_PODS[1]}"
  info "Cross-node ibping: server=${SERVER_POD} client=${CLIENT_POD}"
  chmod +x "${REPO_ROOT}/tests/e2e/validate-ibping.sh"
  "${REPO_ROOT}/tests/e2e/validate-ibping.sh" "${SERVER_POD}" "${CLIENT_POD}"

  info "Validating cross-node iblinkinfo (fabric scan includes peer HCAs)"
  chmod +x "${REPO_ROOT}/tests/e2e/validate-iblinkinfo.sh"
  "${REPO_ROOT}/tests/e2e/validate-iblinkinfo.sh" "${SERVER_POD}" "${CLIENT_POD}" \
    "${GPU_PROFILE}" "${HCA_COUNT}"
else
  info "Skipping cross-node ibping/iblinkinfo for profile=${GPU_PROFILE} (infiniband.enabled=false)"
fi

###############################################################################
# Step 11 -- Show node labels
###############################################################################
info "Node labels"
kubectl get nodes --show-labels

WORKERS=($(kubectl get nodes --no-headers -o custom-columns=":metadata.name" \
  | grep -v control-plane))

###############################################################################
# Summary
###############################################################################
echo
info "Demo complete."
info "  Cluster   : ${CLUSTER_NAME}"
info "  Profile   : ${GPU_PROFILE} (gpu.count=${GPU_COUNT})"
info "  Workers   : ${#WORKERS[@]}"
info "  ConfigMaps: ${CM_COUNT}"
info "  Mock HCAs : ${HCA_COUNT} per pod"
info "  PCI devs  : ${PCI_DEV_COUNT} across ${ROOT_COUNT} root complex(es)"
if [[ "${IB_ENABLED}" == "true" ]]; then
  info "  ibping    : cross-node OK (${SERVER_POD} -> ${CLIENT_POD})"
  info "  ibv_devinfo / iblinkinfo: validated (profile=${GPU_PROFILE})"
else
  info "  ibping    : skipped (profile=${GPU_PROFILE} has InfiniBand disabled)"
  info "  ibv_devinfo / iblinkinfo: skipped"
fi
info ""
info "To tear down: kind delete cluster --name ${CLUSTER_NAME}"
