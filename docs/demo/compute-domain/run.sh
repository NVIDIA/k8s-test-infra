#!/usr/bin/env bash
# Copyright 2026 NVIDIA CORPORATION
#
# SPDX-License-Identifier: Apache-2.0
#
# End-to-end demo of nvml-mock ComputeDomain simulation
# (NVIDIA/k8s-test-infra#304).
#
# Spins up a dedicated 4-worker Kind cluster, installs nvml-mock with
# the gb200 profile + topology overlay, and walks through four
# assertions:
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
#   3. Real IMEX domain formation (NO GPU mode)
#      Two real nvidia-imex daemons (started via imex-nogpu-shim, which
#      execs nvidia-imex.real --nogpu) form a domain over the pod
#      network: nvidia-imex-ctl -q reports READY for a single daemon's
#      local probe, -N -j reports the domain UP with every peer READY
#      and version NO_GPU once both daemons are running, and killing a
#      peer degrades the domain.
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
DEMO_IMAGE_NAME="nvml-mock:compute-domain-imex"
RELEASE_NAME="nvml-mock"
CHART_PATH="deployments/nvml-mock/helm/nvml-mock"
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
KIND_CONFIG="${REPO_ROOT}/tests/e2e/kind-compute-domain-config.yaml"
TOPOLOGY_FILE="${REPO_ROOT}/docs/demo/compute-domain/topology.yaml"
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

command -v jq >/dev/null 2>&1 || fail "jq is required (Scenario 2 parses nvidia-imex-ctl JSON)"

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

###############################################################################
# Step 1 — Kind cluster
###############################################################################
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

# Layer the REAL nvidia-imex (NO GPU mode via imex-nogpu-shim) on top.
# Local build only — this image repackages the proprietary nvidia-imex.
info "Building demo overlay with real nvidia-imex: ${DEMO_IMAGE_NAME}"
docker build -t "${DEMO_IMAGE_NAME}" \
  --target demo \
  --build-arg "NVML_MOCK_IMAGE=${IMAGE_NAME}" \
  --build-arg "GOLANG_VERSION=$("${REPO_ROOT}/hack/golang-version.sh")" \
  -f "${REPO_ROOT}/deployments/nvml-mock/Dockerfile.compute-domain-daemon" "${REPO_ROOT}"

info "Loading image into Kind"
kind load docker-image "${DEMO_IMAGE_NAME}" --name "${CLUSTER_NAME}"

###############################################################################
# Step 3 — Install nvml-mock with gb200 + topology (real IMEX via demo image)
###############################################################################
# NOTE: `--set-file topology.domains=...` cannot be used here. That
# flag stuffs the raw file bytes in as a string literal, which would
# make `toYaml` in templates/topology-configmap.yaml render the list
# as an indented block scalar instead of the structured array the
# engine expects. Using `-f <values-file>` lets helm parse the file
# normally and deep-merge it with the defaults.
info "Installing chart (gb200 + topology; real IMEX via demo image)"
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
  --set image.tag=compute-domain-imex \
  --set gpu.profile=gb200 \
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
# Scenario 2 — Real IMEX domain (NO GPU mode) over the pod network
###############################################################################
# The demo image carries the real nvidia-imex behind imex-nogpu-shim:
# /usr/bin/nvidia-imex exec's /usr/bin/nvidia-imex.real --nogpu. The
# daemons below speak the real gRPC peer protocol (port 50000) across
# pods — no shared hostPath, no marker files. This is the protocol the
# upstream compute-domain-daemon drives; the fake marker binaries it
# replaces are deprecated.
info "Scenario 2: real IMEX domain (NO GPU mode) over the pod network"

POD_A=$(pod_on_node "${WORKER1}")
POD_B=$(pod_on_node "${WORKER2}")
sub "clique 0 pods: ${POD_A}, ${POD_B}"

IP_A=$(kubectl get pod "${POD_A}" -o jsonpath='{.status.podIP}')
IP_B=$(kubectl get pod "${POD_B}" -o jsonpath='{.status.podIP}')
sub "pod IPs: ${POD_A}=${IP_A}  ${POD_B}=${IP_B}"

# Render a per-pod IMEX config: foreground daemon, our nodes file, a
# pod-local log. Everything else keeps the package defaults.
IMEX_CFG=/tmp/imex.cfg
NODES_CFG=/tmp/nodes.cfg
for pod in "${POD_A}" "${POD_B}"; do
  kubectl exec "${pod}" -- sh -c "
    printf '%s\n%s\n' '${IP_A}' '${IP_B}' > '${NODES_CFG}'
    sed -e 's/^DAEMONIZE=1/DAEMONIZE=0/' \
        -e 's|^IMEX_NODE_CONFIG_FILE=.*|IMEX_NODE_CONFIG_FILE=${NODES_CFG}|' \
        -e 's|^LOG_FILE_NAME=.*|LOG_FILE_NAME=/tmp/nvidia-imex.log|' \
        /etc/nvidia-imex/config.cfg > '${IMEX_CFG}'"
done

start_imex() {
  local pod=$1
  kubectl exec "${pod}" -- sh -c \
    "nvidia-imex -c ${IMEX_CFG} >/tmp/imex.stdout 2>&1 & echo \$! > /tmp/imex.pid"
}

# imex_domain_status: the ctl's JSON domain status ("UP", "DEGRADED",
# ...) as seen from `pod`; "UNREACHABLE" while the local daemon is
# still coming up.
imex_domain_status() {
  local pod=$1
  kubectl exec "${pod}" -- nvidia-imex-ctl -c "${IMEX_CFG}" -N -j 2>/dev/null \
    | jq -r '.status' 2>/dev/null || printf 'UNREACHABLE\n'
}

wait_domain_status() {
  local pod=$1 want=$2 reason=$3
  # 240s: after a fresh rollout, kindnetd's NetworkPolicy dataplane
  # reconcile plus the daemon's exponential reconnect backoff (15s,
  # 31s, 62s, 125s...) can push first convergence past 60s.
  for _ in $(seq 1 240); do
    if [[ "$(imex_domain_status "${pod}")" == "${want}" ]]; then
      ok "domain status ${want} ${reason}"
      return 0
    fi
    sleep 1
  done
  kubectl exec "${pod}" -- sh -c 'tail -20 /tmp/nvidia-imex.log 2>/dev/null' >&2 || true
  fail "domain status never became ${want} ${reason}"
}

# Start the daemon in pod A only. Its local probe (-q) must go READY —
# upstream probes local readiness, not the whole domain — while the
# domain-wide status stays degraded because pod B never connected.
sub "starting real nvidia-imex (--nogpu via shim) in ${POD_A}"
start_imex "${POD_A}"
Q_OUT=""
for _ in $(seq 1 30); do
  Q_OUT=$(kubectl exec "${POD_A}" -- nvidia-imex-ctl -c "${IMEX_CFG}" -q 2>/dev/null || true)
  [[ "${Q_OUT}" == "READY" ]] && break
  sleep 1
done
[[ "${Q_OUT}" == "READY" ]] || fail "nvidia-imex-ctl -q never reported READY in ${POD_A}"
ok "local probe READY in ${POD_A} (real ctl, exact upstream contract)"

STATUS_ONE=$(imex_domain_status "${POD_A}")
[[ "${STATUS_ONE}" != "UP" ]] || fail "domain claims UP with 1/2 daemons (want degraded)"
ok "domain not UP with 1/2 daemons (status=${STATUS_ONE})"

# Start pod B's daemon: the daemons find each other over the pod
# network and the domain converges to UP.
sub "starting real nvidia-imex (--nogpu via shim) in ${POD_B}"
start_imex "${POD_B}"
wait_domain_status "${POD_A}" "UP" "after both daemons started (real cross-node gRPC)"

# Every member must be READY and report the NO_GPU version handshake.
NODES_JSON=$(kubectl exec "${POD_A}" -- nvidia-imex-ctl -c "${IMEX_CFG}" -N -j 2>/dev/null)
READY_NODES=$(printf '%s' "${NODES_JSON}" | jq -r '[.nodes[] | select(.status=="READY")] | length')
NOGPU_NODES=$(printf '%s' "${NODES_JSON}" | jq -r '[.nodes[] | select(.version=="NO_GPU")] | length')
[[ "${READY_NODES}" == "2" ]] || fail "want 2 READY nodes, got ${READY_NODES}: ${NODES_JSON}"
[[ "${NOGPU_NODES}" == "2" ]] || fail "want 2 NO_GPU-version nodes, got ${NOGPU_NODES}: ${NODES_JSON}"
ok "2/2 nodes READY, version NO_GPU — real IMEX domain over the pod network"

# Kill pod B's daemon: pod A's view must degrade. This is real
# liveness detection — the property the deprecated marker files could
# not provide (a SIGKILLed fake left its marker behind).
sub "killing nvidia-imex in ${POD_B}"
kubectl exec "${POD_B}" -- sh -c 'kill -TERM "$(cat /tmp/imex.pid)" 2>/dev/null || true'
STATUS_AFTER="UP"
for _ in $(seq 1 60); do
  STATUS_AFTER=$(imex_domain_status "${POD_A}")
  [[ "${STATUS_AFTER}" != "UP" ]] && break
  sleep 1
done
[[ "${STATUS_AFTER}" != "UP" ]] || fail "domain still UP after peer daemon died"
ok "peer death detected: domain status=${STATUS_AFTER} (real liveness)"

# Tidy up daemon A so Scenario 3's rollout starts clean.
kubectl exec "${POD_A}" -- sh -c 'kill -TERM "$(cat /tmp/imex.pid)" 2>/dev/null || true' || true

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
   Scenario 2  real IMEX      : two real nvidia-imex daemons (NO GPU
                                mode via imex-nogpu-shim) formed a
                                domain over the pod network; ctl -q
                                printed READY, -N -j reported UP with
                                version NO_GPU, and killing a peer
                                degraded the domain (real liveness).
   Scenario 3  rebind         : helm upgrade + DaemonSet rollout promoted
                                every node to clique 99 with a new cluster
                                UUID — no image rebuild required.

==> The upstream compute-domain-controller and compute-domain-daemon
    can now run unmodified against this cluster: their NVML calls land
    on the mock library, and the real nvidia-imex / nvidia-imex-ctl are
    fronted by the imex-nogpu-shim overlay image (see
    deployments/nvml-mock/Dockerfile.compute-domain-daemon for the
    thin overlay that runs the real IMEX daemon with --nogpu).

==> Tear down
    kind delete cluster --name ${CLUSTER_NAME}
EOF
