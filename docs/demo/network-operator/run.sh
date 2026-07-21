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
# and the demo shows how far the operator gets; remaining limits include:
#   - NFD scans the node's real /sys/bus/pci, which has no 15b3 devices (the
#     mock NICs live only in the overlay) and the kernel's sysfs can't be
#     faked, so the nvml-mock chart advertises the NIC to NFD's "local" source
#     via a features.d file (infiniband.nfd.publishNicLabel) and NFD then
#     publishes pci-15b3.present=true on every node running the mock stack;
#   - the RDMA shared device plugin crash-loops at startup ("can not get RDMA
#     subsystem network namespace mode") when the host kernel exposes no RDMA
#     subsystem (e.g. macOS/Docker Desktop linuxkit); optional Tier 3
#     (ENABLE_SOFT_ROCE, Linux-only) loads rdma_rxe (Soft-RoCE) for a real
#     generic software RDMA device (rxe0 over eth0), letting the plugin advertise
#     rdma/rdma_shared_device_a — skipped on non-Linux hosts;
#   - the OFED/DOCA driver builds kernel modules, unsupported on Kind.
# The "push" phase applies a NicClusterPolicy on top of that self-derived label
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
# Tier 3: Soft-RoCE (rdma_rxe) makes the real rdma-shared-device-plugin
# advertise rdma/*. Requires a Linux host with the rdma_rxe module. Auto: on
# for Linux, off otherwise. Set explicitly to force.
: "${ENABLE_SOFT_ROCE:=auto}"
if [[ "${ENABLE_SOFT_ROCE}" == "auto" ]]; then
  if [[ "$(uname -s)" == "Linux" ]]; then ENABLE_SOFT_ROCE=true; else ENABLE_SOFT_ROCE=false; fi
fi

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
  --set infiniband.nfd.publishNicLabel=true \
  --wait --timeout 180s

info "Restarting DaemonSets so setup.sh re-stages the overlay"
kubectl_ctx -n "${NVML_MOCK_NAMESPACE}" rollout restart daemonset/nvml-mock
kubectl_ctx -n "${NVML_MOCK_NAMESPACE}" rollout status daemonset/nvml-mock --timeout=120s
kubectl_ctx -n "${NVML_MOCK_NAMESPACE}" rollout status daemonset/nvml-mock-nri --timeout=90s

# --- Prove NRI node-wide injection of the mock IB stack ----------------------
info "Deploying ib-agent (plain workload; mock RDMA comes from NRI)"
kubectl_ctx -n "${WORKLOAD_NAMESPACE}" delete daemonset ib-agent --ignore-not-found
# The ib-agent installs IB userspace tools at start; its readiness probe only
# passes once they load the NRI-injected mock fabric, so allow generous time.
kubectl_ctx -n "${WORKLOAD_NAMESPACE}" apply -f "${REPO_ROOT}/${DEMO_DIR}/ib-agent.yaml"
kubectl_ctx -n "${WORKLOAD_NAMESPACE}" rollout status daemonset/ib-agent --timeout=240s

info "Verifying ib-agent requests no nvidia.com/gpu"
IB_AGENT_RES=$(kubectl_ctx -n "${WORKLOAD_NAMESPACE}" get daemonset ib-agent -o jsonpath='{.spec.template.spec.containers[0].resources}' || true)
if grep -q "nvidia.com/gpu" <<<"${IB_AGENT_RES}"; then
  fail "ib-agent unexpectedly requests nvidia.com/gpu"
fi

kubectl_ctx -n "${WORKLOAD_NAMESPACE}" wait --for=condition=Ready pod -l app=ib-agent --timeout=240s
IB_POD=$(kubectl_ctx -n "${WORKLOAD_NAMESPACE}" get pod -l app=ib-agent -o jsonpath='{.items[0].metadata.name}')
info "Mock RDMA devices visible inside a plain pod (${IB_POD}) via NRI:"
observe kubectl_ctx -n "${WORKLOAD_NAMESPACE}" exec "${IB_POD}" -- ibstat -l
observe kubectl_ctx -n "${WORKLOAD_NAMESPACE}" exec "${IB_POD}" -- ibv_devinfo -l

# --- Install the NVIDIA Network Operator (bundled NFD) -----------------------
info "Installing NVIDIA Network Operator ${NET_OPERATOR_VERSION} (bundled NFD)"
helm upgrade --install "${NET_OPERATOR_RELEASE}" --repo https://helm.ngc.nvidia.com/nvidia network-operator \
  --kube-context "${KUBE_CONTEXT}" \
  --namespace "${NET_OPERATOR_NAMESPACE}" --create-namespace \
  --version "${NET_OPERATOR_VERSION}" \
  -f "${REPO_ROOT}/${DEMO_DIR}/network-operator-values.yaml" \
  --wait --timeout 300s || warn "operator install did not fully converge; continuing to observe"

# --- Tier 3: Soft-RoCE so the real RDMA plugin can advertise rdma/* ----------
if [[ "${ENABLE_SOFT_ROCE}" == "true" ]]; then
  info "Tier 3: enabling Soft-RoCE (rdma_rxe) on the host kernel"
  # Global, host-level bits done once here (module load + netns mode) to avoid
  # a per-node race; the DaemonSet creates the per-node rxe link.
  SUDO=""; [[ "$(id -u)" != "0" ]] && command -v sudo >/dev/null 2>&1 && SUDO="sudo"
  ${SUDO} modprobe rdma_rxe 2>/dev/null || \
    warn "modprobe rdma_rxe failed; install it (e.g. apt-get install linux-modules-extra-\$(uname -r)) — Tier 3 will not advertise rdma/*"
  ${SUDO} rdma system set netns exclusive 2>/dev/null || true

  info "Applying Soft-RoCE setup DaemonSet"
  observe kubectl_ctx -n "${WORKLOAD_NAMESPACE}" apply -f "${REPO_ROOT}/${DEMO_DIR}/soft-roce.yaml"
  kubectl_ctx -n "${WORKLOAD_NAMESPACE}" rollout status daemonset/soft-roce-setup --timeout=180s || \
    warn "soft-roce-setup not Ready; rdma/* may stay unadvertised"
  observe kubectl_ctx -n "${WORKLOAD_NAMESPACE}" logs -l app=soft-roce-setup --tail=20
else
  info "Tier 3: Soft-RoCE disabled (ENABLE_SOFT_ROCE=${ENABLE_SOFT_ROCE}); the RDMA plugin will stay blocked on this host's kernel"
fi

# --- NFD picks up the mock NIC label from the features.d file ----------------
# We do NOT patch NFD's DaemonSet: the operator reconciles it and would revert
# any live host-sys remount, and NFD exposes no values-based sysfs redirect.
# Instead the nvml-mock chart (installed above with
# infiniband.nfd.publishNicLabel=true) wrote an NFD "local" source feature file
# to each node's features.d directory. NFD's bundled worker mounts that same
# directory, reads it on its next scan, and publishes
# feature.node.kubernetes.io/pci-15b3.present=true — durably, since the file
# lives on the node and the operator manages NFD's DaemonSet, not its contents.

# --- Observe the natural blockers --------------------------------------------
info "OBSERVE: operator + NFD pods"
observe kubectl_ctx -n "${NET_OPERATOR_NAMESPACE}" get pods -o wide

# The label key is dotted, so the jsonpath MUST escape the dots — the unescaped
# bracket form silently returns empty even when the label is set.
label_jp='jsonpath={.metadata.labels['"'"'feature\.node\.kubernetes\.io/pci-15b3\.present'"'"']}'

# NFD's worker must rescan its features.d directory and its master must apply
# the local-source label before pci-15b3.present lands (a scan interval or two).
# Poll a worker so the observation reflects the published state instead of
# racing it and misreporting <none>.
info "Waiting for NFD to publish pci-15b3.present on the workers (up to 180s)"
first_worker=$(kubectl_ctx get nodes -l '!node-role.kubernetes.io/control-plane' -o 'jsonpath={.items[0].metadata.name}' 2>/dev/null || true)
for _ in $(seq 1 36); do
  [[ "$(kubectl_ctx get node "${first_worker}" -o "${label_jp}" 2>/dev/null || true)" == "true" ]] && break
  sleep 5
done

info "OBSERVE: pci-15b3 NFD label per node (NFD publishes it from the nvml-mock features.d file on every node running the mock stack)"
while IFS= read -r node; do
  [ -n "${node}" ] || continue
  label=$(kubectl_ctx get node "${node}" -o "${label_jp}" 2>/dev/null || true)
  printf '    %s pci-15b3.present=%s\n' "${node}" "${label:-<none>}"
done < <(kubectl_ctx get nodes -o 'jsonpath={range .items[*]}{.metadata.name}{"\n"}{end}')

if [[ "${SKIP_PUSH}" == "true" ]]; then
  info "SKIP_PUSH=true; stopping after observation."
  exit 0
fi

# --- Push: apply a NicClusterPolicy ------------------------------------------
# The pci-15b3.present label the operator keys on is already published by NFD
# from the nvml-mock features.d file (see above), so the push is just the
# policy.
info "PUSH: applying NicClusterPolicy (rdma-shared-device-plugin)"
observe kubectl_ctx apply -f "${REPO_ROOT}/${DEMO_DIR}/nic-cluster-policy.yaml"

info "Waiting for the operator to reconcile the NicClusterPolicy"
sleep 30
observe kubectl_ctx -n "${NET_OPERATOR_NAMESPACE}" get pods -o wide
observe kubectl_ctx get nicclusterpolicy nic-cluster-policy -o yaml
if [[ "${ENABLE_SOFT_ROCE}" == "true" ]]; then
  info "OBSERVE: rdma resources advertised per node (expect a count — Soft-RoCE gives the plugin a real RDMA device)"
else
  info "OBSERVE: rdma resources advertised per node (expect <none> — no RDMA kernel subsystem on this host)"
fi
while IFS= read -r node; do
  [ -n "${node}" ] || continue
  rdma=$(kubectl_ctx get node "${node}" -o "jsonpath={.status.allocatable['rdma/rdma_shared_device_a']}" 2>/dev/null || true)
  printf '    %s rdma/rdma_shared_device_a=%s\n' "${node}" "${rdma:-<none>}"
done < <(kubectl_ctx get nodes -o 'jsonpath={range .items[*]}{.metadata.name}{"\n"}{end}')

if [[ "${ENABLE_SOFT_ROCE}" == "true" ]]; then
  info "Deploying GPUDirect-RDMA-style test pod (requests rdma/rdma_shared_device_a)"
  kubectl_ctx -n "${WORKLOAD_NAMESPACE}" delete pod rdma-test --ignore-not-found || true
  observe kubectl_ctx -n "${WORKLOAD_NAMESPACE}" apply -f "${REPO_ROOT}/${DEMO_DIR}/rdma-test-pod.yaml"
  if kubectl_ctx -n "${WORKLOAD_NAMESPACE}" wait --for=condition=Ready pod/rdma-test --timeout=120s; then
    info "OBSERVE: rdma-test pod scheduled and running; injected devices:"
    observe kubectl_ctx -n "${WORKLOAD_NAMESPACE}" logs rdma-test
  else
    warn "rdma-test pod did not become Ready; check scheduling/resource advertisement"
    observe kubectl_ctx -n "${WORKLOAD_NAMESPACE}" describe pod rdma-test
  fi
fi

# --- Summary -----------------------------------------------------------------
cat <<EOF

==> Network Operator + NRI mock-injection experiment complete.

  Cluster              : ${CLUSTER_NAME}
  nvml-mock namespace  : ${NVML_MOCK_NAMESPACE} (profile ${GPU_PROFILE})
  operator namespace   : ${NET_OPERATOR_NAMESPACE} (network-operator ${NET_OPERATOR_VERSION})

  What worked:
    - Mock RDMA HCAs visible inside a PLAIN pod via NRI (ibstat/ibv_devinfo).
    - Network Operator controller + bundled NFD installed and running.
    - NFD publishes pci-15b3.present on every node running the mock stack:
      the nvml-mock chart (infiniband.nfd.publishNicLabel) writes an NFD
      "local" source feature file into each node's features.d directory, which
      NFD's bundled worker reads on its next scan — no kubectl patch, no
      kind.yaml stamp, and durable across operator reconciles.

  What stayed blocked (mock semantics vs the real operator):
    - RDMA plugin (rdma-shared-device-plugin):
      * On Linux with Soft-RoCE (ENABLE_SOFT_ROCE=true): a real software RDMA
        device (rxe0 over eth0) lets the plugin enumerate a device and
        advertise rdma/rdma_shared_device_a; the rdma-test pod schedules.
      * Without Soft-RoCE (e.g. macOS/Kind kernel): it crash-loops with "can
        not get RDMA subsystem network namespace mode" — no RDMA netlink
        subsystem — and advertises no rdma/*. rxe is unavailable there.
    - The OFED/DOCA driver is intentionally not enabled: it builds kernel
      modules against the host kernel, which Kind cannot support.

  Inspect further:
    kubectl --context ${KUBE_CONTEXT} -n ${NET_OPERATOR_NAMESPACE} get pods
    kubectl --context ${KUBE_CONTEXT} get nicclusterpolicy -o yaml

  Cleanup:
    kind delete cluster --name ${CLUSTER_NAME}
EOF
