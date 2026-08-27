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
#      Two real nvidia-imex daemons (started via nvidia-imex-shim, which
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
KUBE_CONTEXT="kind-${CLUSTER_NAME}"
IMAGE_NAME="nvml-mock:compute-domain"
WORKLOAD_IMAGE_NAME="nvml-mock:compute-domain-workload"
RELEASE_NAME="nvml-mock"
MOCK_NAMESPACE="mokka"
WORKLOAD_NAMESPACE="compute-domain-workload"
WORKLOAD_NAME="compute-domain-demo-workload"
WORKLOAD_SELECTOR="app.kubernetes.io/name=${WORKLOAD_NAME}"
CHART_PATH="deployments/nvml-mock/helm/nvml-mock"
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
: "${FORCE_RECREATE:=false}"
KIND_CONFIG="${REPO_ROOT}/tests/e2e/kind-compute-domain-config.yaml"
TOPOLOGY_FILE="${REPO_ROOT}/docs/demo/compute-domain/topology.yaml"
EXPECTED_DOMAIN_UUID="00000000-0000-0000-0000-0000000000ab"
# Kind names worker nodes "<cluster>-worker[N]". Keep these in sync
# with the node lists in topology.yaml and tests/e2e/kind-compute-domain-config.yaml.
WORKER1="${CLUSTER_NAME}-worker"
WORKER2="${CLUSTER_NAME}-worker2"
WORKER3="${CLUSTER_NAME}-worker3"
WORKER4="${CLUSTER_NAME}-worker4"
KIND_NODES=("${CLUSTER_NAME}-control-plane" "${WORKER1}" "${WORKER2}" "${WORKER3}" "${WORKER4}")

###############################################################################
# Helpers
###############################################################################
info() { printf '\n==> %s\n' "$*" >&2; }
sub()  { printf '    %s\n' "$*" >&2; }
ok()   { printf '    \xE2\x9C\x93 %s\n' "$*" >&2; }
fail() { printf 'ERROR: %s\n' "$*" >&2; exit 1; }

# Keep every Kubernetes operation scoped to this demo's Kind cluster.
# In particular, an existing cluster is reused without Kind changing the
# caller's current kubeconfig context.
kubectl_ctx() { command kubectl --context "${KUBE_CONTEXT}" "$@"; }

# A cluster created by an older version of this demo may lack the compatible
# containerd NRI plugin configuration. Check every expected Kind node for both
# the enabled plugin and the socket path used by the chart before reusing it.
cluster_has_nri_enabled() {
  local node
  for node in "${KIND_NODES[@]}"; do
    if ! docker exec "${node}" awk '
      /^[[:space:]]*\[plugins\."io\.containerd\.nri\.v1\.nri"\][[:space:]]*$/ {
        in_nri = 1
        next
      }
      in_nri && /^[[:space:]]*\[/ { in_nri = 0 }
      in_nri && /^[[:space:]]*disable[[:space:]]*=[[:space:]]*false[[:space:]]*$/ {
        enabled = 1
      }
      in_nri && /^[[:space:]]*socket_path[[:space:]]*=[[:space:]]*"\/var\/run\/nri\/nri\.sock"[[:space:]]*$/ {
        compatible_socket = 1
      }
      END { exit(enabled && compatible_socket ? 0 : 1) }
    ' /etc/containerd/config.toml; then
      sub "${node} does not have compatible containerd NRI config (requires disable=false and socket_path=\"/var/run/nri/nri.sock\")"
      return 1
    fi
  done
}

# Pick two majors unused on every Kind node. The chart defaults are intended
# for its DRA fixtures, but Kind's host kernel may already assign them (for
# example, major 236 is hidraw on current nodes). Reusing one would make the
# substitute /proc/devices lie, so setup.sh correctly rejects it.
choose_imex_device_majors() {
  local node candidate
  local used_majors
  used_majors=$(for node in "${KIND_NODES[@]}"; do
    docker exec "${node}" awk '$1 ~ /^[0-9]+$/ { print $1 }' /proc/devices
  done | sort -nu | tr '\n' ' ')

  for candidate in $(seq 240 4095); do
    case " ${used_majors} " in
      *" ${candidate} "*) ;;
      *) IMEX_CHANNEL_MAJOR=${candidate}; break ;;
    esac
  done
  [[ -n "${IMEX_CHANNEL_MAJOR:-}" ]] || fail "could not find an unused IMEX channel device major"
  used_majors="${used_majors} ${IMEX_CHANNEL_MAJOR}"

  for candidate in $(seq 240 4095); do
    case " ${used_majors} " in
      *" ${candidate} "*) ;;
      *) IMEX_CAPS_MAJOR=${candidate}; break ;;
    esac
  done
  [[ -n "${IMEX_CAPS_MAJOR:-}" ]] || fail "could not find an unused IMEX caps device major"
  sub "selected unused IMEX device majors: channels=${IMEX_CHANNEL_MAJOR}, caps=${IMEX_CAPS_MAJOR}"
}

command -v jq >/dev/null 2>&1 || fail "jq is required (Scenario 2 parses nvidia-imex-ctl JSON)"

# pod_on_node: echo the NRI-injected demo workload pod on the requested node.
pod_on_node() {
  local node=$1
  # Poll briefly: the rollout settles before the API server's pod list
  # always reflects the new generation, so a tight loop is more
  # reliable than a single shot.
  for _ in $(seq 1 30); do
    local name
    name=$(kubectl_ctx -n "${WORKLOAD_NAMESPACE}" get pods -l "${WORKLOAD_SELECTOR}" \
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

# assert_clique: assert every GPU reported by check-fabric on `node` has the
# expected fabric identity. The discovered count, GPU indices, and complete
# per-GPU blocks must agree so matches cannot be combined across devices.
assert_clique() {
  local node=$1 expected_clique=$2 expected_uuid=$3
  local pod
  pod=$(pod_on_node "${node}")
  if [[ -z "${pod}" ]]; then
    fail "no running pod found on node ${node}"
  fi
  sub "${node} (NRI-injected pod=${pod}) — running check-fabric"
  local out command_status
  if out=$(kubectl_ctx -n "${WORKLOAD_NAMESPACE}" exec "${pod}" -- check-fabric 2>&1); then
    command_status=0
  else
    command_status=$?
  fi
  printf '%s\n' "${out}" | sed 's/^/      /' >&2
  if [[ ${command_status} -ne 0 ]]; then
    fail "${node}: check-fabric exited with status ${command_status}"
  fi

  local parsed_count
  if ! parsed_count=$(printf '%s\n' "${out}" | awk \
    -v expected_clique="${expected_clique}" \
    -v expected_uuid="${expected_uuid}" '
    function reject(message) {
      print message
      rejected = 1
      exit 1
    }
    function finish_gpu() {
      if (current_gpu < 0) {
        return
      }
      if (!have_uuid || !have_clique || !have_state) {
        reject("GPU " current_gpu " has an incomplete fabric-info block")
      }
      current_gpu = -1
    }
    BEGIN {
      discovered = -1
      current_gpu = -1
      expected_uuid = tolower(expected_uuid)
    }
    /^Discovered [0-9][0-9]* GPU\(s\)$/ {
      if (discovered >= 0) {
        reject("multiple discovered-GPU count lines")
      }
      discovered = $2 + 0
      if (discovered <= 0) {
        reject("discovered GPU count must be positive")
      }
      next
    }
    /^Discovered / {
      reject("malformed discovered-GPU count line: " $0)
    }
    /^GPU [0-9][0-9]* \(.*\)$/ {
      if (discovered < 0) {
        reject("GPU block appears before the discovered-GPU count")
      }
      finish_gpu()
      current_gpu = $2 + 0
      if (current_gpu >= discovered) {
        reject("GPU index " current_gpu " is outside discovered count " discovered)
      }
      if (seen_gpu[current_gpu]) {
        reject("duplicate GPU index " current_gpu)
      }
      seen_gpu[current_gpu] = 1
      parsed++
      have_uuid = have_clique = have_state = 0
      next
    }
    /^GPU / {
      reject("malformed GPU block header: " $0)
    }
    /^[[:space:]]*clusterUuid[[:space:]]*:/ {
      if (current_gpu < 0 || have_uuid) {
        reject("misplaced or duplicate clusterUuid field")
      }
      value = $0
      sub(/^[^:]*:[[:space:]]*/, "", value)
      if (tolower(value) != expected_uuid) {
        reject("GPU " current_gpu " has clusterUuid " value ", expected " expected_uuid)
      }
      have_uuid = 1
      next
    }
    /^[[:space:]]*cliqueId[[:space:]]*:/ {
      if (current_gpu < 0 || have_clique) {
        reject("misplaced or duplicate cliqueId field")
      }
      value = $0
      sub(/^[^:]*:[[:space:]]*/, "", value)
      if (value != expected_clique) {
        reject("GPU " current_gpu " has cliqueId " value ", expected " expected_clique)
      }
      have_clique = 1
      next
    }
    /^[[:space:]]*state[[:space:]]*:/ {
      if (current_gpu < 0 || have_state) {
        reject("misplaced or duplicate state field")
      }
      value = $0
      sub(/^[^:]*:[[:space:]]*/, "", value)
      if (value !~ /^completed[[:space:]]+\(3\)$/) {
        reject("GPU " current_gpu " has state " value ", expected completed (3)")
      }
      have_state = 1
      next
    }
    END {
      if (rejected) {
        exit 1
      }
      finish_gpu()
      if (discovered < 0) {
        reject("missing discovered-GPU count")
      }
      if (parsed != discovered) {
        reject("parsed " parsed " GPU blocks, discovered count is " discovered)
      }
      print parsed
    }
  '); then
    fail "${node}: invalid check-fabric output: ${parsed_count}"
  fi
  ok "${node}: ${parsed_count}/${parsed_count} GPUs have clique=${expected_clique} uuid=${expected_uuid} state=completed"
}

###############################################################################
# Step 1 — Kind cluster
###############################################################################
info "Creating Kind cluster: ${CLUSTER_NAME}"
if kind get clusters 2>/dev/null | grep -qx "${CLUSTER_NAME}"; then
  if [[ "${FORCE_RECREATE}" == "true" ]]; then
    sub "Cluster already exists; FORCE_RECREATE=true, deleting it"
    kind delete cluster --name "${CLUSTER_NAME}"
    kind create cluster --name "${CLUSTER_NAME}" --config="${KIND_CONFIG}"
  elif cluster_has_nri_enabled; then
    sub "Cluster already exists with containerd NRI enabled, reusing it"
  else
    fail "existing cluster '${CLUSTER_NAME}' is incompatible with this NRI-based demo; rerun with FORCE_RECREATE=true to delete and recreate it"
  fi
else
  kind create cluster --name "${CLUSTER_NAME}" --config="${KIND_CONFIG}"
fi

choose_imex_device_majors

###############################################################################
# Step 2 — Build + load the image
###############################################################################
info "Building image: ${IMAGE_NAME}"
docker build -t "${IMAGE_NAME}" \
  -f "${REPO_ROOT}/deployments/nvml-mock/Dockerfile" "${REPO_ROOT}"

# Build the demo workload with real nvidia-imex in NO GPU mode.
# Local build only — this image repackages the proprietary nvidia-imex.
info "Building demo workload image with real nvidia-imex: ${WORKLOAD_IMAGE_NAME}"
docker build -t "${WORKLOAD_IMAGE_NAME}" \
  --build-arg "GOLANG_VERSION=$("${REPO_ROOT}/hack/golang-version.sh")" \
  -f "${REPO_ROOT}/docs/demo/compute-domain/Dockerfile" "${REPO_ROOT}"

info "Loading images into Kind"
kind load docker-image "${IMAGE_NAME}" "${WORKLOAD_IMAGE_NAME}" --name "${CLUSTER_NAME}"

###############################################################################
# Step 3 — Stage nvml-mock and deploy the NRI-injected real-IMEX demo workload
# with its namespace-local peer ingress policy
###############################################################################
# NOTE: `--set-file topology.domains=...` cannot be used here. That
# flag stuffs the raw file bytes in as a string literal, which would
# make `toYaml` in templates/topology-configmap.yaml render the list
# as an indented block scalar instead of the structured array the
# engine expects. Using `-f <values-file>` lets helm parse the file
# normally and deep-merge it with the defaults.
info "Installing chart (gb200 + topology + NRI channel injection)"
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
  --kube-context "${KUBE_CONTEXT}" \
  --namespace "${MOCK_NAMESPACE}" --create-namespace \
  -f "${TOPOLOGY_FILE}" \
  --set image.repository=nvml-mock \
  --set image.tag=compute-domain \
  --set gpu.profile=gb200 \
  --set nri.enabled=true \
  --set imex.mockChannels.enabled=true \
  --set imex.mockChannels.channelMajor="${IMEX_CHANNEL_MAJOR}" \
  --set imex.mockChannels.capsMajor="${IMEX_CAPS_MAJOR}" \
  --set-string updateStrategy.rollingUpdate.maxUnavailable=100% \
  --set terminationGracePeriodSeconds=1 \
  --wait --timeout 180s >/dev/null

# Helm cannot detect that a same-tag local image was rebuilt, and changing the
# topology ConfigMap does not rerun setup.sh or restart the demo workload.
# Always recycle in dependency order so reruns stage the current topology and
# image, register the current NRI plugin, and discard any IMEX process left by a
# failed run. A fresh cluster pays for one redundant rollout; keeping one path
# for fresh and reused clusters is the deliberate simplicity tradeoff.
info "Refreshing staging and NRI DaemonSets"
kubectl_ctx -n "${MOCK_NAMESPACE}" rollout restart "daemonset/${RELEASE_NAME}" >/dev/null
kubectl_ctx -n "${MOCK_NAMESPACE}" rollout status "daemonset/${RELEASE_NAME}" --timeout=180s >/dev/null
kubectl_ctx -n "${MOCK_NAMESPACE}" rollout restart "daemonset/${RELEASE_NAME}-nri" >/dev/null
kubectl_ctx -n "${MOCK_NAMESPACE}" rollout status "daemonset/${RELEASE_NAME}-nri" --timeout=180s >/dev/null

info "Deploying real-IMEX demo workload with peer-only ingress (mock delivery is entirely NRI)"
kubectl_ctx create namespace "${WORKLOAD_NAMESPACE}" --dry-run=client -o yaml | kubectl_ctx apply -f - >/dev/null
# This manifest contains both the DaemonSet and its own NetworkPolicy. The
# chart's ibping policy selects nvml-mock daemon pods in ${MOCK_NAMESPACE}; it
# does not govern this separate demo workload.
kubectl_ctx -n "${WORKLOAD_NAMESPACE}" apply -f "${REPO_ROOT}/docs/demo/compute-domain/demo-workload.yaml" >/dev/null
kubectl_ctx -n "${WORKLOAD_NAMESPACE}" rollout restart "daemonset/${WORKLOAD_NAME}" >/dev/null
kubectl_ctx -n "${WORKLOAD_NAMESPACE}" rollout status "daemonset/${WORKLOAD_NAME}" --timeout=180s >/dev/null

###############################################################################
# Step 4 — Verify the rendered topology ConfigMap
###############################################################################
info "Rendered topology ConfigMap"
kubectl_ctx -n "${MOCK_NAMESPACE}" get "configmap/${RELEASE_NAME}-topology" \
  -o jsonpath='{.data.topology\.yaml}' | sed 's/^/    /'
echo

###############################################################################
# Scenario 1 — Per-node clique assignment via mock NVML fabric API
###############################################################################
info "Scenario 1: NRI-delivered per-node fabric identity (cluster ${EXPECTED_DOMAIN_UUID})"
assert_clique "${WORKER1}" 0 "${EXPECTED_DOMAIN_UUID}"
assert_clique "${WORKER2}" 0 "${EXPECTED_DOMAIN_UUID}"
assert_clique "${WORKER3}" 1 "${EXPECTED_DOMAIN_UUID}"
assert_clique "${WORKER4}" 1 "${EXPECTED_DOMAIN_UUID}"

###############################################################################
# Scenario 2 — Real IMEX domain (NO GPU mode) over the pod network
###############################################################################
# The demo workload image carries the real nvidia-imex behind nvidia-imex-shim;
# NRI supplies the mock NVML overlay, topology environment, and IMEX channels.
# /usr/bin/nvidia-imex exec's /usr/bin/nvidia-imex.real --nogpu. The
# daemons below speak the real gRPC peer protocol (port 50000) and exchange
# command/status data (port 50005) across pods. The demo workload's NetworkPolicy
# admits those ports only from its peer pods in the same namespace and leaves
# egress unrestricted. There is no shared hostPath or marker file. This is the
# protocol the upstream compute-domain-daemon drives; the fake marker binaries
# it replaces are deprecated.
info "Scenario 2: real IMEX domain (NO GPU mode) over the pod network"

POD_A=$(pod_on_node "${WORKER1}")
POD_B=$(pod_on_node "${WORKER2}")
sub "clique 0 pods: ${POD_A}, ${POD_B}"

for pod in "${POD_A}" "${POD_B}"; do
  kubectl_ctx -n "${WORKLOAD_NAMESPACE}" exec "${pod}" -- test -c /dev/nvidia-caps-imex-channels/channel0
done
ok "NRI injected mock IMEX channel nodes into both demo workload pods"

IP_A=$(kubectl_ctx -n "${WORKLOAD_NAMESPACE}" get pod "${POD_A}" -o jsonpath='{.status.podIP}')
IP_B=$(kubectl_ctx -n "${WORKLOAD_NAMESPACE}" get pod "${POD_B}" -o jsonpath='{.status.podIP}')
sub "pod IPs: ${POD_A}=${IP_A}  ${POD_B}=${IP_B}"

# Render a per-pod IMEX config: foreground daemon, our nodes file, a
# pod-local log. Everything else keeps the package defaults.
IMEX_CFG=/tmp/imex.cfg
NODES_CFG=/tmp/nodes.cfg
for pod in "${POD_A}" "${POD_B}"; do
  kubectl_ctx -n "${WORKLOAD_NAMESPACE}" exec "${pod}" -- sh -c "
    printf '%s\n%s\n' '${IP_A}' '${IP_B}' > '${NODES_CFG}'
    sed -e 's/^DAEMONIZE=1/DAEMONIZE=0/' \
        -e 's|^IMEX_NODE_CONFIG_FILE=.*|IMEX_NODE_CONFIG_FILE=${NODES_CFG}|' \
        -e 's|^LOG_FILE_NAME=.*|LOG_FILE_NAME=/tmp/nvidia-imex.log|' \
        /etc/nvidia-imex/config.cfg > '${IMEX_CFG}'"
done

start_imex() {
  local pod=$1
  kubectl_ctx -n "${WORKLOAD_NAMESPACE}" exec "${pod}" -- sh -c \
    "nvidia-imex -c ${IMEX_CFG} >/tmp/imex.stdout 2>&1 & echo \$! > /tmp/imex.pid"
}

# imex_domain_status: the ctl's JSON domain status ("UP", "DEGRADED",
# ...) as seen from `pod`; "UNREACHABLE" while the local daemon is
# still coming up.
imex_domain_status() {
  local pod=$1
  kubectl_ctx -n "${WORKLOAD_NAMESPACE}" exec "${pod}" -- nvidia-imex-ctl -c "${IMEX_CFG}" -N -j 2>/dev/null \
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
  kubectl_ctx -n "${WORKLOAD_NAMESPACE}" exec "${pod}" -- sh -c 'tail -20 /tmp/nvidia-imex.log 2>/dev/null' >&2 || true
  fail "domain status never became ${want} ${reason}"
}

# Start the daemon in pod A only. Its local probe (-q) must go READY —
# upstream probes local readiness, not the whole domain — while the
# domain-wide status stays degraded because pod B never connected.
sub "starting real nvidia-imex (--nogpu via shim) in ${POD_A}"
start_imex "${POD_A}"
Q_OUT=""
for _ in $(seq 1 30); do
  Q_OUT=$(kubectl_ctx -n "${WORKLOAD_NAMESPACE}" exec "${POD_A}" -- nvidia-imex-ctl -c "${IMEX_CFG}" -q 2>/dev/null || true)
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
NODES_JSON=$(kubectl_ctx -n "${WORKLOAD_NAMESPACE}" exec "${POD_A}" -- nvidia-imex-ctl -c "${IMEX_CFG}" -N -j 2>/dev/null)
READY_NODES=$(printf '%s' "${NODES_JSON}" | jq -r '[.nodes[] | select(.status=="READY")] | length')
NOGPU_NODES=$(printf '%s' "${NODES_JSON}" | jq -r '[.nodes[] | select(.version=="NO_GPU")] | length')
[[ "${READY_NODES}" == "2" ]] || fail "want 2 READY nodes, got ${READY_NODES}: ${NODES_JSON}"
[[ "${NOGPU_NODES}" == "2" ]] || fail "want 2 NO_GPU-version nodes, got ${NOGPU_NODES}: ${NODES_JSON}"
ok "2/2 nodes READY, version NO_GPU — real IMEX domain over the pod network"

# Kill pod B's daemon: pod A's view must degrade. This is real
# liveness detection — the property the deprecated marker files could
# not provide (a SIGKILLed fake left its marker behind).
sub "killing nvidia-imex in ${POD_B}"
kubectl_ctx -n "${WORKLOAD_NAMESPACE}" exec "${POD_B}" -- sh -c 'kill -TERM "$(cat /tmp/imex.pid)" 2>/dev/null || true'
STATUS_AFTER="UP"
for _ in $(seq 1 60); do
  STATUS_AFTER=$(imex_domain_status "${POD_A}")
  [[ "${STATUS_AFTER}" != "UP" ]] && break
  sleep 1
done
[[ "${STATUS_AFTER}" != "UP" ]] || fail "domain still UP after peer daemon died"
ok "peer death detected: domain status=${STATUS_AFTER} (real liveness)"

# Tidy up daemon A so Scenario 3's rollout starts clean.
kubectl_ctx -n "${WORKLOAD_NAMESPACE}" exec "${POD_A}" -- sh -c 'kill -TERM "$(cat /tmp/imex.pid)" 2>/dev/null || true' || true

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
  --kube-context "${KUBE_CONTEXT}" \
  --namespace "${MOCK_NAMESPACE}" \
  --reuse-values \
  -f "${NEW_TOPO}" \
  --wait --timeout 180s >/dev/null

# The staging DaemonSet copies the topology into the node overlay, and the
# NRI-injected engine reads it once at process start. Recycle staging first,
# then the demo workload, so every new container receives the new file.
sub "recycling staging pods and NRI demo workload pods to re-read topology"
kubectl_ctx -n "${MOCK_NAMESPACE}" delete pods -l "app.kubernetes.io/name=${RELEASE_NAME}" \
  --ignore-not-found >/dev/null
kubectl_ctx -n "${MOCK_NAMESPACE}" rollout status "daemonset/${RELEASE_NAME}" --timeout=180s >/dev/null
kubectl_ctx -n "${WORKLOAD_NAMESPACE}" delete pods -l "${WORKLOAD_SELECTOR}" \
  --ignore-not-found >/dev/null
kubectl_ctx -n "${WORKLOAD_NAMESPACE}" rollout status "daemonset/${WORKLOAD_NAME}" --timeout=180s >/dev/null

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
                                mode via nvidia-imex-shim) formed a
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
    fronted by the nvidia-imex-shim overlay image (see
    deployments/nvml-mock/Dockerfile.compute-domain-daemon for the
    thin overlay that runs the real IMEX daemon with --nogpu).

==> The cluster is left running for inspection. To tear it down:
    kind delete cluster --name ${CLUSTER_NAME}
EOF
