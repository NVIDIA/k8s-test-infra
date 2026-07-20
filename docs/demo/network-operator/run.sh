#!/usr/bin/env bash
# Copyright 2026 NVIDIA CORPORATION
# SPDX-License-Identifier: Apache-2.0
#
# Demo: deploy the REAL NVIDIA Network Operator on a Kind cluster where the
# mock GPU/InfiniBand stack is delivered ambiently via containerd NRI (the
# node-wide-injection method), and report how far the operator gets against
# devices that exist only inside pods.
#
# This is an EXPLORATORY demo. The operator controller + NFD come up healthy,
# but the RDMA/driver components stay blocked against the mocks because:
#   - NFD scans the node's real /sys/bus/pci (mock devices exist only in pods);
#   - the RDMA device plugin runs in the operator namespace, which this demo
#     excludes from NRI injection, so it reads the node's real (empty) sysfs;
#   - the OFED/DOCA driver builds kernel modules, unsupported on Kind.
# The "push" phase manually applies the pci-15b3 NFD label + a NicClusterPolicy
# to drive the operator further and shows exactly where it stops.

set -euo pipefail

CLUSTER_NAME="nvml-mock-network-operator-demo"
KUBE_CONTEXT="kind-${CLUSTER_NAME}"
IMAGE_NAME="nvml-mock:network-operator-demo"
CHART_PATH="deployments/nvml-mock/helm/nvml-mock"
DEMO_DIR="docs/demo/network-operator"
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"

: "${GPU_PROFILE:=h100}"
: "${GPU_COUNT:=8}"
: "${FORCE_RECREATE:=false}"
: "${NVML_MOCK_NAMESPACE:=nvml-mock-system}"
: "${WORKLOAD_NAMESPACE:=default}"
: "${NET_OPERATOR_NAMESPACE:=nvidia-network-operator}"
: "${NET_OPERATOR_RELEASE:=network-operator}"
: "${NET_OPERATOR_VERSION:=v26.4.0}"
: "${SKIP_PUSH:=false}"

info() { echo "==> $*"; }
warn() { echo "WARN: $*" >&2; }
fail() { echo "ERROR: $*" >&2; exit 1; }

# Pin every kubectl call to the demo's kind context.
kubectl_ctx() { command kubectl --context "${KUBE_CONTEXT}" "$@"; }
# observe runs a command for its diagnostic value without aborting the demo:
# the operator's failure modes are the subject of the demo, not fatal errors.
observe() { echo "--- \$ $* ---"; "$@" 2>&1 || warn "(non-fatal) command failed: $*"; }

# --- Preflight ---------------------------------------------------------------
for bin in docker kind kubectl helm python3; do
  command -v "${bin}" >/dev/null 2>&1 || fail "${bin} is required"
done

info "Ensuring the NVIDIA Helm repo is available"
helm repo add nvidia https://helm.ngc.nvidia.com/nvidia >/dev/null 2>&1 || true
helm repo update nvidia >/dev/null

# --- Kind cluster ------------------------------------------------------------
if kind get clusters 2>/dev/null | grep -qx "${CLUSTER_NAME}"; then
  if [[ "${FORCE_RECREATE}" == "true" ]]; then
    info "Deleting existing Kind cluster '${CLUSTER_NAME}'"
    kind delete cluster --name "${CLUSTER_NAME}"
  else
    info "Reusing existing Kind cluster '${CLUSTER_NAME}' (set FORCE_RECREATE=true to recreate)"
  fi
fi
if ! kind get clusters 2>/dev/null | grep -qx "${CLUSTER_NAME}"; then
  info "Creating Kind cluster with containerd NRI enabled"
  kind create cluster --name "${CLUSTER_NAME}" --config="${REPO_ROOT}/${DEMO_DIR}/kind.yaml"
fi

# --- Build + load nvml-mock --------------------------------------------------
info "Building image: ${IMAGE_NAME}"
docker build -t "${IMAGE_NAME}" -f "${REPO_ROOT}/deployments/nvml-mock/Dockerfile" "${REPO_ROOT}"
info "Loading image into Kind"
kind load docker-image "${IMAGE_NAME}" --name "${CLUSTER_NAME}"

# --- Install nvml-mock with NRI (operator namespaces excluded) ---------------
info "Installing nvml-mock (profile=${GPU_PROFILE}) with NRI enabled"
helm upgrade --install nvml-mock "${REPO_ROOT}/${CHART_PATH}" \
  --kube-context "${KUBE_CONTEXT}" \
  --namespace "${NVML_MOCK_NAMESPACE}" --create-namespace \
  --set image.repository=nvml-mock \
  --set image.tag=network-operator-demo \
  --set "gpu.profile=${GPU_PROFILE}" \
  --set "gpu.count=${GPU_COUNT}" \
  --set nri.enabled=true \
  --set "nri.excludedNamespaces={${NET_OPERATOR_NAMESPACE},node-feature-discovery}" \
  --wait --timeout 180s

info "Restarting DaemonSets so setup.sh re-stages the overlay"
kubectl_ctx -n "${NVML_MOCK_NAMESPACE}" rollout restart daemonset/nvml-mock
kubectl_ctx -n "${NVML_MOCK_NAMESPACE}" rollout status daemonset/nvml-mock --timeout=120s
kubectl_ctx -n "${NVML_MOCK_NAMESPACE}" rollout status daemonset/nvml-mock-nri --timeout=90s

# --- Prove NRI node-wide injection of the mock IB stack ----------------------
info "Deploying ib-agent (plain workload; mock RDMA comes from NRI)"
kubectl_ctx -n "${WORKLOAD_NAMESPACE}" delete daemonset ib-agent --ignore-not-found
kubectl_ctx -n "${WORKLOAD_NAMESPACE}" apply -f "${REPO_ROOT}/${DEMO_DIR}/ib-agent.yaml"
kubectl_ctx -n "${WORKLOAD_NAMESPACE}" rollout status daemonset/ib-agent --timeout=180s

info "Verifying ib-agent requests no nvidia.com/gpu"
IB_AGENT_RES=$(kubectl_ctx -n "${WORKLOAD_NAMESPACE}" get daemonset ib-agent -o jsonpath='{.spec.template.spec.containers[0].resources}' || true)
if grep -q "nvidia.com/gpu" <<<"${IB_AGENT_RES}"; then
  fail "ib-agent unexpectedly requests nvidia.com/gpu"
fi

kubectl_ctx -n "${WORKLOAD_NAMESPACE}" wait --for=condition=Ready pod -l app=ib-agent --timeout=180s
IB_POD=$(kubectl_ctx -n "${WORKLOAD_NAMESPACE}" get pod -l app=ib-agent -o jsonpath='{.items[0].metadata.name}')
info "Mock RDMA devices visible inside a plain pod (${IB_POD}) via NRI:"
observe kubectl_ctx -n "${WORKLOAD_NAMESPACE}" exec "${IB_POD}" -- ibstat -l
observe kubectl_ctx -n "${WORKLOAD_NAMESPACE}" exec "${IB_POD}" -- ibv_devinfo -l

# --- Install the NVIDIA Network Operator (bundled NFD) -----------------------
info "Installing NVIDIA Network Operator ${NET_OPERATOR_VERSION} (bundled NFD)"
helm upgrade --install "${NET_OPERATOR_RELEASE}" nvidia/network-operator \
  --kube-context "${KUBE_CONTEXT}" \
  --namespace "${NET_OPERATOR_NAMESPACE}" --create-namespace \
  --version "${NET_OPERATOR_VERSION}" \
  -f "${REPO_ROOT}/${DEMO_DIR}/network-operator-values.yaml" \
  --wait --timeout 300s || warn "operator install did not fully converge; continuing to observe"

# --- Observe the natural blockers --------------------------------------------
info "OBSERVE: operator + NFD pods"
observe kubectl_ctx -n "${NET_OPERATOR_NAMESPACE}" get pods -o wide
info "OBSERVE: pci-15b3 NFD label per node (expect <none> — NFD scans node PCI)"
while IFS= read -r node; do
  [ -n "${node}" ] || continue
  label=$(kubectl_ctx get node "${node}" -o "jsonpath={.metadata.labels['feature.node.kubernetes.io/pci-15b3.present']}" 2>/dev/null || true)
  printf '    %s pci-15b3.present=%s\n' "${node}" "${label:-<none>}"
done < <(kubectl_ctx get nodes -o 'jsonpath={range .items[*]}{.metadata.name}{"\n"}{end}')

if [[ "${SKIP_PUSH}" == "true" ]]; then
  info "SKIP_PUSH=true; stopping after observation."
  exit 0
fi

# --- Push: fake the NFD label + apply a NicClusterPolicy ---------------------
info "PUSH: labelling worker nodes feature.node.kubernetes.io/pci-15b3.present=true"
while IFS= read -r node; do
  [ -n "${node}" ] || continue
  kubectl_ctx label node "${node}" feature.node.kubernetes.io/pci-15b3.present=true --overwrite
done < <(kubectl_ctx get nodes -l '!node-role.kubernetes.io/control-plane' -o 'jsonpath={range .items[*]}{.metadata.name}{"\n"}{end}')

info "PUSH: applying NicClusterPolicy (rdma-shared-device-plugin)"
observe kubectl_ctx apply -f "${REPO_ROOT}/${DEMO_DIR}/nic-cluster-policy.yaml"

info "Waiting for the operator to reconcile the NicClusterPolicy"
sleep 30
observe kubectl_ctx -n "${NET_OPERATOR_NAMESPACE}" get pods -o wide
observe kubectl_ctx get nicclusterpolicy nic-cluster-policy -o yaml
info "OBSERVE: rdma resources advertised per node (expect <none> — Go plugin bypasses LD_PRELOAD)"
while IFS= read -r node; do
  [ -n "${node}" ] || continue
  rdma=$(kubectl_ctx get node "${node}" -o "jsonpath={.status.allocatable['rdma/rdma_shared_device_a']}" 2>/dev/null || true)
  printf '    %s rdma/rdma_shared_device_a=%s\n' "${node}" "${rdma:-<none>}"
done < <(kubectl_ctx get nodes -o 'jsonpath={range .items[*]}{.metadata.name}{"\n"}{end}')

# --- Summary -----------------------------------------------------------------
cat <<EOF

==> Network Operator + NRI mock-injection experiment complete.

  Cluster              : ${CLUSTER_NAME}
  nvml-mock namespace  : ${NVML_MOCK_NAMESPACE} (profile ${GPU_PROFILE})
  operator namespace   : ${NET_OPERATOR_NAMESPACE} (network-operator ${NET_OPERATOR_VERSION})

  What worked:
    - Mock RDMA HCAs visible inside a PLAIN pod via NRI (ibstat/ibv_devinfo).
    - Network Operator controller + bundled NFD installed and running.

  What stayed blocked (mock semantics vs the real operator):
    - NFD did not label nodes pci-15b3.present: it scans the node's real
      /sys/bus/pci, but the mock devices exist only inside pods.
    - After faking that label + applying a NicClusterPolicy, the
      rdma-shared-device-plugin runs but advertises no rdma/* resources: it
      runs in the NRI-excluded operator namespace, so it reads the node's
      real (empty) host sysfs (the mock IB fabric is injected only into
      workloads in non-excluded namespaces).
    - The OFED/DOCA driver is intentionally not enabled: it builds kernel
      modules against the host kernel, which Kind cannot support.

  Inspect further:
    kubectl --context ${KUBE_CONTEXT} -n ${NET_OPERATOR_NAMESPACE} get pods
    kubectl --context ${KUBE_CONTEXT} get nicclusterpolicy -o yaml

  Cleanup:
    kind delete cluster --name ${CLUSTER_NAME}
EOF
