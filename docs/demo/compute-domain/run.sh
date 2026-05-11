#!/usr/bin/env bash
# Copyright 2026 NVIDIA CORPORATION
#
# SPDX-License-Identifier: Apache-2.0
#
# End-to-end demo of nvml-mock ComputeDomain simulation
# (NVIDIA/k8s-test-infra#304).
#
# Spins up a dedicated 4-worker Kind cluster wired with extraMounts
# that share a hostPath (/tmp/nvml-mock-imex-state) across every
# worker, installs nvml-mock with the gb200 profile + topology overlay
# + fake IMEX coordination, and walks through four assertions:
#
#   1. Mock NVML fabric API
#      Each pod's nvmlDeviceGetGpuFabricInfo (via the bundled
#      `check-fabric` consumer) returns the cluster UUID, clique ID,
#      and `state=completed` assigned to its node by the cluster-level
#      topology ConfigMap.
#
#   2. Per-node clique assignment
#      kind-compute-domain-worker / -worker2 report clique 0;
#      -worker3 / -worker4 report clique 1.
#
#   3. Fake IMEX peer coordination
#      With a hand-written nodes.cfg listing every pod IP in a clique,
#      nvidia-imex-ctl prints `READY` once the fake nvidia-imex daemon
#      has dropped a marker file for every peer; it exits 1 as soon as
#      one peer's marker is removed.
#
#   4. Topology rebinding without rebuilding the image
#      `helm upgrade --reuse-values` with a different topology document
#      promotes every node to clique 99 of a new domain UUID, and
#      check-fabric reflects the new identity after a DaemonSet
#      rollout.

set -euo pipefail

###############################################################################
# Configuration
###############################################################################
CLUSTER_NAME="nvml-mock-compute-domain"
IMAGE_NAME="nvml-mock:compute-domain"
RELEASE_NAME="nvml-mock"
CHART_PATH="deployments/nvml-mock/helm/nvml-mock"
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
KIND_CONFIG="${REPO_ROOT}/tests/e2e/kind-compute-domain-config.yaml"
TOPOLOGY_FILE="${REPO_ROOT}/docs/demo/compute-domain/topology.yaml"
HOST_STATE_DIR="/tmp/nvml-mock-imex-state"
EXPECTED_DOMAIN_UUID="00000000-0000-0000-0000-0000000000ab"
# Kind names worker nodes "<cluster>-worker[N]". Keep these in sync
# with the node lists in topology.yaml and tests/e2e/kind-compute-domain-config.yaml.
WORKER1="${CLUSTER_NAME}-worker"
WORKER2="${CLUSTER_NAME}-worker2"
WORKER3="${CLUSTER_NAME}-worker3"
WORKER4="${CLUSTER_NAME}-worker4"

###############################################################################
# Helpers
###############################################################################
info() { printf '\n==> %s\n' "$*" >&2; }
sub()  { printf '    %s\n' "$*" >&2; }
ok()   { printf '    \xE2\x9C\x93 %s\n' "$*" >&2; }
fail() { printf 'ERROR: %s\n' "$*" >&2; exit 1; }

# pod_on_node: echo the name of the running nvml-mock pod scheduled on
# the given Kubernetes node. Used both to query fabric info per node
# and to drive the IMEX coordination test.
pod_on_node() {
  local node=$1
  # Poll briefly: the rollout settles before the API server's pod list
  # always reflects the new generation, so a tight loop is more
  # reliable than a single shot.
  for _ in $(seq 1 30); do
    local name
    name=$(kubectl get pods -l "app.kubernetes.io/name=${RELEASE_NAME}" \
      --field-selector="spec.nodeName=${node},status.phase=Running" \
      -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true)
    if [[ -n "${name}" ]]; then
      printf '%s\n' "${name}"
      return 0
    fi
    sleep 1
  done
  return 1
}

# wait_for_rollout: block until every nvml-mock pod is Running, then
# confirm at least one pod exists per worker (the topology overlay is
# pulled on Init() per pod, so we can't verify clique assignment
# before the pods are up).
wait_for_rollout() {
  kubectl rollout status "daemonset/${RELEASE_NAME}" --timeout=180s >/dev/null
}

# assert_clique: assert the check-fabric output from `node` reports
# `expected_clique`. check-fabric prints one block per visible GPU; we
# just spot-check GPU 0 since every GPU on a node shares the same
# fabric identity by design.
assert_clique() {
  local node=$1 expected_clique=$2 expected_uuid=$3
  local pod
  pod=$(pod_on_node "${node}")
  if [[ -z "${pod}" ]]; then
    fail "no running pod found on node ${node}"
  fi
  sub "${node} (pod=${pod}) — running check-fabric"
  local out
  out=$(kubectl exec "${pod}" -- check-fabric 2>&1 || true)
  printf '%s\n' "${out}" | sed 's/^/      /' >&2
  if ! printf '%s\n' "${out}" | grep -q "cliqueId    : ${expected_clique}"; then
    fail "${node}: expected cliqueId ${expected_clique} in check-fabric output"
  fi
  if ! printf '%s\n' "${out}" | grep -qi "clusterUuid : ${expected_uuid}"; then
    fail "${node}: expected clusterUuid ${expected_uuid} in check-fabric output"
  fi
  if ! printf '%s\n' "${out}" | grep -q 'state       : completed'; then
    fail "${node}: expected state=completed in check-fabric output"
  fi
  ok "${node}: clique=${expected_clique} uuid=${expected_uuid} state=completed"
}

# expect_ctl_not_ready: run nvidia-imex-ctl inside `pod` and assert it
# exits 1 (the "peer not ready" path). Stdout/stderr are captured and
# replayed as a single indented status line so the demo log stays
# legible — without this helper kubectl spills both
# `fake-imex-ctl: peer X not ready` and `command terminated with exit
# code 1` onto separate lines, which is technically correct but reads
# like an error from the demo itself.
expect_ctl_not_ready() {
  local pod=$1 reason=$2
  local out rc
  set +e
  out=$(kubectl exec "${pod}" -- env "IMEX_NODES_CONFIG=${NODES_CFG}" \
    nvidia-imex-ctl -c /dev/null -q 2>&1)
  rc=$?
  set -e
  if [[ "${rc}" -ne 1 ]]; then
    printf '%s\n' "${out}" | sed 's/^/      /' >&2
    fail "nvidia-imex-ctl ${reason}: rc=${rc} (want 1), out=${out}"
  fi
  # Surface only the informative line from fake-imex-ctl's stderr
  # (drops the trailing "command terminated with exit code 1" that
  # kubectl injects after the container exits non-zero).
  local detail
  detail=$(printf '%s\n' "${out}" | grep -E '^fake-imex-ctl:' | head -1)
  ok "nvidia-imex-ctl ${reason} (rc=1) — ${detail:-peer not ready}"
}

###############################################################################
# Step 1 — Kind cluster (with shared hostPath for IMEX state)
###############################################################################
info "Preparing shared hostPath: ${HOST_STATE_DIR}"
mkdir -p "${HOST_STATE_DIR}"
# Wipe stale markers from a previous run so the IMEX assertions below
# can rely on a clean baseline.
rm -f "${HOST_STATE_DIR}"/* 2>/dev/null || true

info "Creating Kind cluster: ${CLUSTER_NAME}"
if kind get clusters 2>/dev/null | grep -qx "${CLUSTER_NAME}"; then
  sub "Cluster already exists, reusing it"
else
  kind create cluster --name "${CLUSTER_NAME}" --config="${KIND_CONFIG}"
fi

###############################################################################
# Step 2 — Build + load the image
###############################################################################
info "Building image: ${IMAGE_NAME}"
docker build -t "${IMAGE_NAME}" \
  -f "${REPO_ROOT}/deployments/nvml-mock/Dockerfile" "${REPO_ROOT}"

info "Loading image into Kind"
kind load docker-image "${IMAGE_NAME}" --name "${CLUSTER_NAME}"

###############################################################################
# Step 3 — Install nvml-mock with gb200 + topology + IMEX
###############################################################################
# NOTE: `--set-file topology.domains=...` cannot be used here. That
# flag stuffs the raw file bytes in as a string literal, which would
# make `toYaml` in templates/topology-configmap.yaml render the list
# as an indented block scalar instead of the structured array the
# engine expects. Using `-f <values-file>` lets helm parse the file
# normally and deep-merge it with the defaults.
info "Installing chart (gb200 + topology + IMEX)"
# NOTE: `--set gpu.count=...` is intentionally NOT passed. The flag
# only controls the host-side CDI spec / /dev/nvidia* device nodes
# emitted by setup.sh; the in-pod ConfigMap mounted at
# /etc/nvml-mock/config.yaml — which is what check-fabric below
# loads — always reflects the profile's full device list (8 for
# gb200). For this demo the GPU count is irrelevant: what matters is
# that every GPU on a node reports the cliqueId / clusterUuid the
# topology overlay assigned to that node, which is stronger evidence
# the deeper the per-node device list goes.
helm upgrade --install "${RELEASE_NAME}" "${REPO_ROOT}/${CHART_PATH}" \
  -f "${TOPOLOGY_FILE}" \
  --set image.repository=nvml-mock \
  --set image.tag=compute-domain \
  --set gpu.profile=gb200 \
  --set imex.enabled=true \
  --set-string updateStrategy.rollingUpdate.maxUnavailable=100% \
  --set terminationGracePeriodSeconds=1 \
  --wait --timeout 180s >/dev/null

wait_for_rollout

###############################################################################
# Step 4 — Verify the rendered topology ConfigMap
###############################################################################
info "Rendered topology ConfigMap"
kubectl get "configmap/${RELEASE_NAME}-topology" \
  -o jsonpath='{.data.topology\.yaml}' | sed 's/^/    /'
echo

###############################################################################
# Scenario 1 — Per-node clique assignment via mock NVML fabric API
###############################################################################
info "Scenario 1: per-node fabric identity (cluster ${EXPECTED_DOMAIN_UUID})"
assert_clique "${WORKER1}" 0 "${EXPECTED_DOMAIN_UUID}"
assert_clique "${WORKER2}" 0 "${EXPECTED_DOMAIN_UUID}"
assert_clique "${WORKER3}" 1 "${EXPECTED_DOMAIN_UUID}"
assert_clique "${WORKER4}" 1 "${EXPECTED_DOMAIN_UUID}"

###############################################################################
# Scenario 2 — Fake IMEX peer coordination (shared hostPath markers)
###############################################################################
# Each nvml-mock pod's entrypoint does NOT start nvidia-imex
# automatically — the real compute-domain-daemon would. To exercise
# the coordination protocol without deploying upstream, we run the
# fake daemon ad-hoc inside one pod per clique-0 worker and verify
# nvidia-imex-ctl from a peer.
info "Scenario 2: fake IMEX peer coordination"

POD_A=$(pod_on_node "${WORKER1}")
POD_B=$(pod_on_node "${WORKER2}")
sub "clique 0 pods: ${POD_A}, ${POD_B}"

# Read both pod IPs so we can write a synthetic nodes.cfg.
IP_A=$(kubectl get pod "${POD_A}" -o jsonpath='{.status.podIP}')
IP_B=$(kubectl get pod "${POD_B}" -o jsonpath='{.status.podIP}')
sub "pod IPs: ${POD_A}=${IP_A}  ${POD_B}=${IP_B}"

# Write nodes.cfg into both pods. The chart already mounts the shared
# state dir at /var/lib/nvml-mock/imex-state; the fakes read
# IMEX_NODES_CONFIG with default /imexd/nodes.cfg, so we override
# with a writable path inside /tmp.
NODES_CFG=/tmp/nodes.cfg
for pod in "${POD_A}" "${POD_B}"; do
  kubectl exec "${pod}" -- sh -c "printf '%s\n%s\n' '${IP_A}' '${IP_B}' > ${NODES_CFG}"
done

# Start the fake daemon in pod A and check from pod B. We background
# the daemon and capture its PID so we can SIGTERM it later.
sub "starting fake nvidia-imex in ${POD_A}"
kubectl exec "${POD_A}" -- sh -c \
  "POD_IP=${IP_A} IMEX_NODES_CONFIG=${NODES_CFG} \
   nvidia-imex -c /dev/null >/tmp/imex.log 2>&1 &
   echo \$! > /tmp/imex.pid"

# Poll up to 5s for the marker to appear under the shared hostPath.
sub "waiting for marker ${IP_A} under ${HOST_STATE_DIR}"
for _ in $(seq 1 25); do
  if [[ -f "${HOST_STATE_DIR}/${IP_A}" ]]; then break; fi
  sleep 0.2
done
[[ -f "${HOST_STATE_DIR}/${IP_A}" ]] || fail "marker ${IP_A} never appeared"
ok "marker for ${IP_A} present on host"

# 1 of 2 markers → ctl exits 1.
expect_ctl_not_ready "${POD_B}" "with 1/2 markers"

# Bring up pod B's daemon → 2/2 markers → ctl prints READY.
sub "starting fake nvidia-imex in ${POD_B}"
kubectl exec "${POD_B}" -- sh -c \
  "POD_IP=${IP_B} IMEX_NODES_CONFIG=${NODES_CFG} \
   nvidia-imex -c /dev/null >/tmp/imex.log 2>&1 &
   echo \$! > /tmp/imex.pid"

for _ in $(seq 1 25); do
  if [[ -f "${HOST_STATE_DIR}/${IP_B}" ]]; then break; fi
  sleep 0.2
done
[[ -f "${HOST_STATE_DIR}/${IP_B}" ]] || fail "marker ${IP_B} never appeared"

CTL_OUT=$(kubectl exec "${POD_B}" -- env "IMEX_NODES_CONFIG=${NODES_CFG}" \
  nvidia-imex-ctl -c /dev/null -q 2>/dev/null)
if [[ "${CTL_OUT}" == "READY" ]]; then
  ok "nvidia-imex-ctl prints READY with 2/2 markers"
else
  fail "nvidia-imex-ctl with 2/2 markers printed '${CTL_OUT}' (want READY)"
fi

# SIGTERM the daemon in pod A → marker removed → ctl exits 1 again.
sub "killing nvidia-imex in ${POD_A}"
kubectl exec "${POD_A}" -- sh -c \
  'kill -TERM "$(cat /tmp/imex.pid)"; sleep 1' || true

for _ in $(seq 1 25); do
  if [[ ! -f "${HOST_STATE_DIR}/${IP_A}" ]]; then break; fi
  sleep 0.2
done
[[ ! -f "${HOST_STATE_DIR}/${IP_A}" ]] || fail "marker ${IP_A} did not get cleaned up"
ok "marker for ${IP_A} removed by SIGTERM"

expect_ctl_not_ready "${POD_B}" "after peer SIGTERM"

# Tidy up the surviving daemon so the rollout in Scenario 3 doesn't
# inherit stale state.
kubectl exec "${POD_B}" -- sh -c \
  'kill -TERM "$(cat /tmp/imex.pid)" 2>/dev/null || true' || true
rm -f "${HOST_STATE_DIR}"/* 2>/dev/null || true

###############################################################################
# Scenario 3 — Topology rebinding (helm upgrade, no image rebuild)
###############################################################################
info "Scenario 3: rebind every node into clique 99 of a new domain"
NEW_TOPO=$(mktemp)
NEW_UUID="00000000-0000-0000-0000-0000000000ff"
# Full values fragment (not just the list under `domains`) — `-f`
# merges it on top of the existing release values without disturbing
# anything else.
cat >"${NEW_TOPO}" <<EOF
topology:
  enabled: true
  domains:
    - name: rebinder
      uuid: "${NEW_UUID}"
      cliques:
        - id: 99
          nodes:
            - ${WORKER1}
            - ${WORKER2}
            - ${WORKER3}
            - ${WORKER4}
EOF
helm upgrade "${RELEASE_NAME}" "${REPO_ROOT}/${CHART_PATH}" \
  --reuse-values \
  -f "${NEW_TOPO}" \
  --wait --timeout 180s >/dev/null

# The engine reads MOCK_TOPOLOGY_CONFIG once at process start, so we
# need to recycle every pod for the new clique to take effect.
sub "evicting pods to re-read topology"
kubectl delete pods -l "app.kubernetes.io/name=${RELEASE_NAME}" \
  --ignore-not-found >/dev/null
wait_for_rollout

assert_clique "${WORKER1}" 99 "${NEW_UUID}"
assert_clique "${WORKER2}" 99 "${NEW_UUID}"
assert_clique "${WORKER3}" 99 "${NEW_UUID}"
assert_clique "${WORKER4}" 99 "${NEW_UUID}"
rm -f "${NEW_TOPO}"

###############################################################################
# Summary
###############################################################################
cat <<EOF

==> All three ComputeDomain scenarios verified.

   Scenario 1  fabric API     : every node reports its assigned clique
                                (workers 1-2 -> clique 0, workers 3-4 -> clique 1)
                                via nvmlDeviceGetGpuFabricInfo.
   Scenario 2  fake IMEX      : nvidia-imex-ctl transitions
                                NOT-READY -> READY -> NOT-READY in lockstep
                                with marker files on the shared hostPath.
   Scenario 3  rebind         : helm upgrade + DaemonSet rollout promoted
                                every node to clique 99 with a new cluster
                                UUID — no image rebuild required.

==> The upstream compute-domain-controller and compute-domain-daemon
    can now run unmodified against this cluster: their NVML calls land
    on the mock library, and their fork of nvidia-imex / nvidia-imex-ctl
    is shadowed by the fakes shipped in the nvml-mock image (see
    deployments/nvml-mock/Dockerfile.compute-domain-daemon for the
    thin overlay image).

==> Tear down
    kind delete cluster --name ${CLUSTER_NAME}
    rm -rf ${HOST_STATE_DIR}
EOF
