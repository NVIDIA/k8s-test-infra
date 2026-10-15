#!/usr/bin/env bash
# Copyright 2026 NVIDIA CORPORATION
#
# SPDX-License-Identifier: Apache-2.0
#
# ComputeDomains on mock GB200 racks through the unmodified NVIDIA DRA driver
# (NVIDIA/k8s-test-infra#821). See README.md in this directory.
#
# Creates or reuses a Kind cluster of two racks with two nodes each, provides
# the IMEX node software, installs nvml-mock with the gb200 profile and the
# rack topology, and checks three scenarios:
#
#   1. Per-node fabric identity
#      check-fabric, run in each node pod, reports the cluster UUID and clique
#      the topology assigns: worker-0 and worker-1 clique 0 of rack-1,
#      worker-2 and worker-3 clique 0 of rack-2.
#
#   2. Topology rebinding without an image rebuild
#      `helm upgrade --reuse-values` with another topology moves every node
#      into clique 99 of a new domain, and check-fabric follows once the node
#      pods restart. The rack topology is then restored.
#
#   3. A ComputeDomain through the DRA driver
#      The driver (chart 0.5.0, upstream images) starts an IMEX daemon on each
#      node a workload of the ComputeDomain lands on. The two rack-1 daemons
#      join one IMEX domain, the ComputeDomain becomes Ready with both nodes in
#      rack-1's clique, and each workload sees IMEX channel 0 only.
#
# The rebind runs before the DRA driver is installed, so no ComputeDomain
# workload sits on a node whose topology changes under it, and the example
# ComputeDomain is left running for inspection.
#
# The cluster's credentials go to a kubeconfig file of their own, and every
# kubectl and helm call names that file and the cluster's context, so the
# script never reaches the cluster your current context points at.
#
# Environment:
#   CLUSTER_NAME       Kind cluster name (default mokka-compute-domain)
#   FORCE_RECREATE     true deletes and recreates an existing cluster
#   IMEX_SOURCE        payload (default) builds the IMEX payload image and
#                      loads it into Kind; download lets each node agent
#                      download IMEX from NVIDIA
#   IMEX_PAYLOAD_IMAGE payload image to build and load
#                      (default mokka-imex:<pinned IMEX version>)
#   DEMO_KUBECONFIG    kubeconfig file for the cluster
#                      (default ~/.kube/<cluster name>.kubeconfig)
#   KIND_NODE_IMAGE    Kind node image (default: the Makefile's)
#   HELM_TIMEOUT       how long each helm install waits (default 10m)

set -euo pipefail

###############################################################################
# Configuration
###############################################################################
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
GUIDE_DIR="${REPO_ROOT}/docs/guides/compute-domain"

: "${CLUSTER_NAME:=mokka-compute-domain}"
: "${FORCE_RECREATE:=false}"
: "${IMEX_SOURCE:=payload}"
: "${DEMO_KUBECONFIG:=${HOME}/.kube/${CLUSTER_NAME}.kubeconfig}"
: "${HELM_TIMEOUT:=10m}"
KUBE_CONTEXT="kind-${CLUSTER_NAME}"

KIND_CONFIG="${REPO_ROOT}/tests/e2e/kind-compute-domain-racks.yaml"
# The node image the Makefile pins for every Kind cluster, CI's included.
: "${KIND_NODE_IMAGE:=$(sed -n 's/^KIND_NODE_IMAGE[[:space:]]*?=[[:space:]]*//p' "${REPO_ROOT}/Makefile")}"
# The IMEX version the node agent pins, read as the Makefile reads it.
IMEX_VERSION="$(sed -n 's/^ *"version": *"\([^"]*\)".*/\1/p' "${REPO_ROOT}/internal/agent/imex/imex.lock.json")"
: "${IMEX_PAYLOAD_IMAGE:=mokka-imex:${IMEX_VERSION}}"

IMAGE_REPOSITORY="nvml-mock"
IMAGE_TAG="compute-domain"
RELEASE_NAME="nvml-mock"
MOCK_NAMESPACE="mokka"
MOCK_SELECTOR="app.kubernetes.io/name=${RELEASE_NAME}"
CHART_PATH="${REPO_ROOT}/deployments/nvml-mock/helm/nvml-mock"
TOPOLOGY_FILE="${GUIDE_DIR}/topology.yaml"
# Where the node agent stages Mokka's nvidia-imex shim, its sign that IMEX is
# staged, as seen from its own container.
STAGED_IMEX="/var/lib/nvml-mock/driver/usr/bin/nvidia-imex.real"

DRA_RELEASE="nvidia-dra-driver"
DRA_NAMESPACE="nvidia"
DRA_CHART_REPO="https://helm.ngc.nvidia.com/nvidia"
DRA_CHART="dra-driver-nvidia-gpu"
DRA_CHART_VERSION="0.5.0"
DRA_VALUES="${GUIDE_DIR}/dra-driver.values.yaml"
KUBELET_PLUGIN_SELECTOR="dra-driver-nvidia-gpu-component=kubelet-plugin"

CD_MANIFEST="${GUIDE_DIR}/compute-domain.yaml"
CD_NAMESPACE="compute-domain-demo"
CD_NAME="rack-1"
CD_WORKLOADS=(rack-1-a rack-1-b)

# Kubernetes node names. tests/e2e/kind-compute-domain-racks.yaml sets them
# whatever the cluster is called, and topology.yaml lists them.
WORKERS=(worker-0 worker-1 worker-2 worker-3)
RACK1_UUID="00000000-0000-0000-0000-0000000000a1"
RACK2_UUID="00000000-0000-0000-0000-0000000000b2"
REBIND_UUID="00000000-0000-0000-0000-0000000000ff"

###############################################################################
# Helpers
###############################################################################
info() { printf '\n==> %s\n' "$*" >&2; }
sub()  { printf '    %s\n' "$*" >&2; }
ok()   { printf '    \xE2\x9C\x93 %s\n' "$*" >&2; }
fail() { printf 'ERROR: %s\n' "$*" >&2; exit 1; }

# Every Kubernetes operation names this cluster's own kubeconfig and context,
# so neither the caller's current context nor a KUBECONFIG that changes during
# the run can redirect it.
kubectl_ctx() { command kubectl --kubeconfig "${DEMO_KUBECONFIG}" --context "${KUBE_CONTEXT}" "$@"; }
helm_ctx() { command helm --kubeconfig "${DEMO_KUBECONFIG}" --kube-context "${KUBE_CONTEXT}" "$@"; }

require() {
  command -v "$1" >/dev/null 2>&1 || fail "$1 is required: $2"
}

# rack_uuid: the cluster UUID of the rack a worker belongs to.
rack_uuid() {
  case "$1" in
    worker-0 | worker-1) printf '%s\n' "${RACK1_UUID}" ;;
    worker-2 | worker-3) printf '%s\n' "${RACK2_UUID}" ;;
    *) return 1 ;;
  esac
}

# wait_until: run a check every two seconds until it succeeds, for at most the
# given number of seconds.
wait_until() {
  local timeout=$1 deadline
  shift
  deadline=$((SECONDS + timeout))
  until "$@"; do
    if ((SECONDS >= deadline)); then
      return 1
    fi
    sleep 2
  done
}

# cluster_has_racks: whether an existing cluster has the four workers of
# kind-compute-domain-racks.yaml with their rack labels. A cluster of the same
# name made from another config, such as the Tilt compute-domain profile's, has
# other cliques.
cluster_has_racks() {
  local node want got
  for node in "${WORKERS[@]}"; do
    want="$(rack_uuid "${node}").0"
    if ! got=$(kubectl_ctx get node "${node}" \
      -o jsonpath='{.metadata.labels.mokka\.nvidia\.com/clique}' 2>/dev/null); then
      sub "node ${node} not found"
      return 1
    fi
    if [[ "${got}" != "${want}" ]]; then
      sub "${node} has mokka.nvidia.com/clique=${got:-<unset>}, expected ${want}"
      return 1
    fi
  done
}

# restart_node_pods: recreate the nvml-mock node pods, so they stage the current
# image and topology and their NRI plugins register again. Helm cannot tell that
# a same-tag image was rebuilt, and a topology change does not roll the pods.
restart_node_pods() {
  kubectl_ctx -n "${MOCK_NAMESPACE}" rollout restart "daemonset/${RELEASE_NAME}" >/dev/null
  kubectl_ctx -n "${MOCK_NAMESPACE}" rollout status "daemonset/${RELEASE_NAME}" --timeout=300s >/dev/null
}

# node_pod: echo the running, not terminating, nvml-mock pod on a node.
node_pod() {
  local node=$1 name
  # Poll briefly: the pod list can lag behind a finished rollout.
  for _ in $(seq 1 30); do
    name=$(kubectl_ctx -n "${MOCK_NAMESPACE}" get pods -l "${MOCK_SELECTOR}" \
      --field-selector="spec.nodeName=${node},status.phase=Running" \
      -o jsonpath='{range .items[*]}{.metadata.name}{" "}{.metadata.deletionTimestamp}{"\n"}{end}' 2>/dev/null |
      awk 'NF == 1 { print $1; exit }') || name=""
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
  pod=$(node_pod "${node}") || fail "no running nvml-mock pod found on node ${node}"
  sub "${node} (pod=${pod}) — running check-fabric"
  local out command_status
  if out=$(kubectl_ctx -n "${MOCK_NAMESPACE}" exec "${pod}" -c node-agent -- check-fabric 2>&1); then
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

# assert_rack_identity: every worker reports clique 0 of its own rack.
assert_rack_identity() {
  local node
  for node in "${WORKERS[@]}"; do
    assert_clique "${node}" 0 "$(rack_uuid "${node}")"
  done
}

# imex_staged: whether the node agent on every worker has staged IMEX.
imex_staged() {
  local node pod
  for node in "${WORKERS[@]}"; do
    pod=$(node_pod "${node}") || return 1
    kubectl_ctx -n "${MOCK_NAMESPACE}" exec "${pod}" -c node-agent -- \
      test -f "${STAGED_IMEX}" >/dev/null 2>&1 || return 1
  done
}

# show_imex_logs: the node agents' recent IMEX log lines, which name the
# archive they expect, where they looked, and why they staged none.
show_imex_logs() {
  kubectl_ctx -n "${MOCK_NAMESPACE}" logs -l "${MOCK_SELECTOR}" -c node-agent \
    --prefix --tail=200 2>/dev/null | grep -i 'imex' | tail -n 20 >&2 || true
}

# cd_get: a jsonpath of the example ComputeDomain.
cd_get() {
  kubectl_ctx -n "${CD_NAMESPACE}" get computedomain "${CD_NAME}" -o jsonpath="$1" 2>/dev/null
}

# cd_nodes: "<node> <clique>" for each node in the ComputeDomain's status.
cd_nodes() {
  cd_get '{range .status.nodes[*]}{.name}{" "}{.cliqueID}{"\n"}{end}' | sort
}

# ready_daemon_nodes: the nodes where an IMEX daemon of the ComputeDomain is
# ready. The driver labels its daemon pods with the ComputeDomain's UID.
ready_daemon_nodes() {
  local uid
  uid=$(cd_get '{.metadata.uid}') || return 1
  [[ -n "${uid}" ]] || return 1
  kubectl_ctx -n "${DRA_NAMESPACE}" get pods -l "resource.nvidia.com/computeDomain=${uid}" \
    -o jsonpath='{range .items[*]}{.spec.nodeName}{" "}{.status.containerStatuses[*].ready}{"\n"}{end}' \
    2>/dev/null | awk 'NF > 1 && !/false/ { print $1 }' | sort
}

EXPECTED_CD_NODES=$(printf 'worker-0 %s.0\nworker-1 %s.0' "${RACK1_UUID}" "${RACK1_UUID}")
EXPECTED_DAEMON_NODES=$(printf 'worker-0\nworker-1')

# cd_converged: the ComputeDomain is Ready, lists both rack-1 nodes in rack-1's
# clique, and runs a ready IMEX daemon on each.
cd_converged() {
  [[ "$(cd_get '{.status.status}')" == "Ready" ]] &&
    [[ "$(cd_nodes)" == "${EXPECTED_CD_NODES}" ]] &&
    [[ "$(ready_daemon_nodes)" == "${EXPECTED_DAEMON_NODES}" ]]
}

###############################################################################
# Prerequisites
###############################################################################
info "Checking prerequisites"
require docker "https://docs.docker.com/get-docker/"
require kind "https://kind.sigs.k8s.io/docs/user/quick-start/#installation"
require kubectl "https://kubernetes.io/docs/tasks/tools/"
require helm "https://helm.sh/docs/intro/install/"
docker info >/dev/null 2>&1 || fail "the Docker daemon is not reachable; start it and rerun"
case "${IMEX_SOURCE}" in
  payload)
    require make "install GNU make"
    require go "https://go.dev/doc/install (make imex-payload-image runs a Go helper)"
    docker buildx version >/dev/null 2>&1 ||
      fail "docker buildx is required: make imex-payload-image builds with it"
    case "${IMEX_PAYLOAD_IMAGE}" in
      *@*) fail "IMEX_PAYLOAD_IMAGE must be <repository>:<tag>, not a digest: ${IMEX_PAYLOAD_IMAGE}" ;;
    esac
    IMEX_PAYLOAD_REPOSITORY="${IMEX_PAYLOAD_IMAGE%:*}"
    IMEX_PAYLOAD_TAG="${IMEX_PAYLOAD_IMAGE##*:}"
    case "${IMEX_PAYLOAD_TAG}" in
      "${IMEX_PAYLOAD_IMAGE}" | */*) fail "IMEX_PAYLOAD_IMAGE must be <repository>:<tag>: ${IMEX_PAYLOAD_IMAGE}" ;;
    esac
    ;;
  download) ;;
  *) fail "IMEX_SOURCE must be payload or download, not '${IMEX_SOURCE}'" ;;
esac
[[ -n "${KIND_NODE_IMAGE}" ]] || fail "could not read KIND_NODE_IMAGE from the Makefile; set it explicitly"
[[ -n "${IMEX_VERSION}" ]] || fail "could not read the IMEX version from internal/agent/imex/imex.lock.json"
sub "cluster ${CLUSTER_NAME}, kubeconfig ${DEMO_KUBECONFIG}, IMEX from ${IMEX_SOURCE}"

WORK_DIR=$(mktemp -d)
trap 'rm -rf "${WORK_DIR}"' EXIT

###############################################################################
# Step 1 — Kind cluster
###############################################################################
info "Kind cluster: ${CLUSTER_NAME}"
mkdir -p "$(dirname "${DEMO_KUBECONFIG}")"

create_cluster() {
  kind create cluster --name "${CLUSTER_NAME}" --image "${KIND_NODE_IMAGE}" \
    --config "${KIND_CONFIG}" --kubeconfig "${DEMO_KUBECONFIG}"
}

if kind get clusters 2>/dev/null | grep -qx "${CLUSTER_NAME}"; then
  if [[ "${FORCE_RECREATE}" == "true" ]]; then
    sub "cluster already exists; FORCE_RECREATE=true, deleting it"
    kind delete cluster --name "${CLUSTER_NAME}" --kubeconfig "${DEMO_KUBECONFIG}"
    create_cluster
  else
    kind export kubeconfig --name "${CLUSTER_NAME}" --kubeconfig "${DEMO_KUBECONFIG}" >/dev/null 2>&1 ||
      fail "could not write the kubeconfig of '${CLUSTER_NAME}' to ${DEMO_KUBECONFIG}"
    if cluster_has_racks; then
      sub "cluster already exists with the two-rack layout, reusing it"
    else
      fail "existing cluster '${CLUSTER_NAME}' was not created from tests/e2e/kind-compute-domain-racks.yaml; rerun with FORCE_RECREATE=true to delete and recreate it, or pick another CLUSTER_NAME"
    fi
  fi
else
  create_cluster
fi
kubectl_ctx wait --for=condition=Ready nodes --all --timeout=300s >/dev/null

# A ComputeDomain left by an earlier run would keep its workloads on nodes whose
# node pods restart below. The DRA driver, still installed, cleans up after it.
if kubectl_ctx get namespace "${CD_NAMESPACE}" >/dev/null 2>&1; then
  sub "removing namespace ${CD_NAMESPACE} left by an earlier run"
  kubectl_ctx delete namespace "${CD_NAMESPACE}" --wait=true --timeout=300s >/dev/null
fi

###############################################################################
# Step 2 — nvml-mock image
###############################################################################
info "Building image: ${IMAGE_REPOSITORY}:${IMAGE_TAG}"
docker build -t "${IMAGE_REPOSITORY}:${IMAGE_TAG}" \
  --build-arg "GOLANG_VERSION=$("${REPO_ROOT}/hack/golang-version.sh")" \
  -f "${REPO_ROOT}/deployments/nvml-mock/Dockerfile" "${REPO_ROOT}"
kind load docker-image "${IMAGE_REPOSITORY}:${IMAGE_TAG}" --name "${CLUSTER_NAME}"

###############################################################################
# Step 3 — IMEX node software
###############################################################################
case "${IMEX_SOURCE}" in
  payload)
    # Kind nodes run on the Docker daemon's architecture, which can differ from
    # the Go toolchain's, the make target's default.
    NODE_ARCH=$(docker version --format '{{.Server.Arch}}')
    info "Building the IMEX payload image ${IMEX_PAYLOAD_IMAGE} for linux/${NODE_ARCH}"
    sub "IMEX ${IMEX_VERSION} from NVIDIA, SHA-256 verified; Mokka does not publish this image, so it stays local"
    make -C "${REPO_ROOT}" imex-payload-image \
      IMEX_PAYLOAD_ARCH="${NODE_ARCH}" IMEX_PAYLOAD_IMAGE="${IMEX_PAYLOAD_IMAGE}"
    kind load docker-image "${IMEX_PAYLOAD_IMAGE}" --name "${CLUSTER_NAME}"
    # The payload image is the only source: the node agents never contact NVIDIA.
    IMEX_HELM_ARGS=(
      --set "imex.payload.image.repository=${IMEX_PAYLOAD_REPOSITORY}"
      --set "imex.payload.image.tag=${IMEX_PAYLOAD_TAG}"
      --set featureGates.IMEXDownload=false
    )
    IMEX_STAGE_TIMEOUT=300
    ;;
  download)
    info "IMEX: each node agent downloads it from NVIDIA (IMEXDownload feature gate)"
    IMEX_HELM_ARGS=(--set featureGates.IMEXDownload=true)
    IMEX_STAGE_TIMEOUT=900
    ;;
esac

###############################################################################
# Step 4 — nvml-mock
###############################################################################
# NOTE: `--set-file topology.domains=...` cannot be used here. That flag stuffs
# the raw file bytes in as a string literal, which would make `toYaml` in
# templates/topology-configmap.yaml render the list as an indented block scalar
# instead of the structured array the engine expects. `-f <values-file>` lets
# helm parse the file and deep-merge it with the defaults.
info "Installing nvml-mock (gb200, rack topology, IMEX from ${IMEX_SOURCE})"
helm_ctx upgrade --install "${RELEASE_NAME}" "${CHART_PATH}" \
  --namespace "${MOCK_NAMESPACE}" --create-namespace \
  -f "${TOPOLOGY_FILE}" \
  --set image.repository="${IMAGE_REPOSITORY}" \
  --set image.tag="${IMAGE_TAG}" \
  --set gpu.profile=gb200 \
  --set 'nodeSelector.mokka\.nvidia\.com/type=sgpu' \
  "${IMEX_HELM_ARGS[@]}" \
  --set-string updateStrategy.rollingUpdate.maxUnavailable=100% \
  --wait --timeout "${HELM_TIMEOUT}" >/dev/null

# Always recycle the node pods, so reruns stage the rebuilt image and the
# restored topology. A fresh cluster pays for one redundant rollout; one path for
# fresh and reused clusters is the deliberate tradeoff.
sub "restarting the node pods"
restart_node_pods

###############################################################################
# Scenario 1 — Per-node fabric identity
###############################################################################
info "Scenario 1: per-node fabric identity (rack-1 ${RACK1_UUID}, rack-2 ${RACK2_UUID})"
assert_rack_identity

###############################################################################
# Scenario 2 — Topology rebinding (helm upgrade, no image rebuild)
###############################################################################
info "Scenario 2: rebind every node into clique 99 of a new domain"
REBIND_VALUES="${WORK_DIR}/rebind.topology.yaml"
# A full values fragment, not just the list under `domains`: `-f` merges it on
# top of the release's values without disturbing anything else.
cat >"${REBIND_VALUES}" <<EOF
topology:
  enabled: true
  domains:
    - name: rebinder
      uuid: "${REBIND_UUID}"
      cliques:
        - id: 99
          nodes:
            - worker-0
            - worker-1
            - worker-2
            - worker-3
EOF
helm_ctx upgrade "${RELEASE_NAME}" "${CHART_PATH}" \
  --namespace "${MOCK_NAMESPACE}" --reuse-values -f "${REBIND_VALUES}" \
  --wait --timeout "${HELM_TIMEOUT}" >/dev/null
sub "restarting the node pods, so they stage the new topology"
restart_node_pods
for node in "${WORKERS[@]}"; do
  assert_clique "${node}" 99 "${REBIND_UUID}"
done

sub "restoring the rack topology"
helm_ctx upgrade "${RELEASE_NAME}" "${CHART_PATH}" \
  --namespace "${MOCK_NAMESPACE}" --reuse-values -f "${TOPOLOGY_FILE}" \
  --wait --timeout "${HELM_TIMEOUT}" >/dev/null
restart_node_pods
assert_rack_identity

###############################################################################
# Step 5 — DRA driver
###############################################################################
info "Waiting for every node agent to stage IMEX (up to ${IMEX_STAGE_TIMEOUT}s)"
if ! wait_until "${IMEX_STAGE_TIMEOUT}" imex_staged; then
  show_imex_logs
  fail "IMEX was not staged on every worker; the node agent log lines above say what it is missing (see 'Providing IMEX' in the README)"
fi
ok "IMEX ${IMEX_VERSION} staged on ${WORKERS[*]}"

# --repo instead of `helm repo add`, so the run leaves your Helm repository
# list as it was.
info "Installing the DRA driver ${DRA_CHART} ${DRA_CHART_VERSION} with ComputeDomains"
helm_ctx upgrade --install "${DRA_RELEASE}" "${DRA_CHART}" \
  --repo "${DRA_CHART_REPO}" --version "${DRA_CHART_VERSION}" \
  --namespace "${DRA_NAMESPACE}" --create-namespace \
  -f "${DRA_VALUES}" \
  --wait --timeout "${HELM_TIMEOUT}" >/dev/null

# The kubelet plugin reads the node's clique from NVML, and the IMEX binaries
# from the driver root, when it starts. The node pods restaged both above, so
# restart it on every run.
sub "restarting the DRA kubelet plugin, so it reads the staged clique and IMEX"
# Every plugin pod names its DaemonSet as its controller; list order is not
# guaranteed, so collect them all and require exactly one.
PLUGIN_DAEMONSET=$(kubectl_ctx -n "${DRA_NAMESPACE}" get pods -l "${KUBELET_PLUGIN_SELECTOR}" \
  -o jsonpath='{range .items[*]}{range .metadata.ownerReferences[?(@.controller==true)]}{.name}{"\n"}{end}{end}' 2>/dev/null |
  sort -u) || PLUGIN_DAEMONSET=""
[[ -n "${PLUGIN_DAEMONSET}" ]] ||
  fail "no DRA kubelet plugin pods in namespace ${DRA_NAMESPACE}; check the kubeletPlugin affinity in ${DRA_VALUES}"
[[ "${PLUGIN_DAEMONSET}" != *$'\n'* ]] ||
  fail "DRA kubelet plugin pods in namespace ${DRA_NAMESPACE} belong to more than one DaemonSet: ${PLUGIN_DAEMONSET//$'\n'/ }"
kubectl_ctx -n "${DRA_NAMESPACE}" rollout restart "daemonset/${PLUGIN_DAEMONSET}" >/dev/null
kubectl_ctx -n "${DRA_NAMESPACE}" rollout status "daemonset/${PLUGIN_DAEMONSET}" --timeout=300s >/dev/null

###############################################################################
# Scenario 3 — A ComputeDomain through the DRA driver
###############################################################################
info "Scenario 3: ComputeDomain ${CD_NAME} across worker-0 and worker-1"
kubectl_ctx apply -f "${CD_MANIFEST}" >/dev/null

# A workload starts once the IMEX daemon on its node is ready: until then the
# kubelet cannot prepare its channel claim.
if ! kubectl_ctx -n "${CD_NAMESPACE}" wait --for=condition=Ready \
  pod/rack-1-a pod/rack-1-b --timeout=600s >/dev/null; then
  kubectl_ctx -n "${CD_NAMESPACE}" get events --sort-by=.lastTimestamp 2>/dev/null | tail -n 20 >&2 || true
  fail "the workloads did not start; see Troubleshooting in the README"
fi
ok "workloads ${CD_WORKLOADS[*]} are running"

if ! wait_until 300 cd_converged; then
  kubectl_ctx -n "${CD_NAMESPACE}" get computedomain "${CD_NAME}" -o yaml >&2 || true
  kubectl_ctx -n "${DRA_NAMESPACE}" get pods -o wide >&2 || true
  fail "ComputeDomain ${CD_NAME} did not become Ready with a ready IMEX daemon on worker-0 and worker-1"
fi
ok "ComputeDomain ${CD_NAME} is Ready"
cd_nodes | sed 's/^/      /' >&2
ok "both nodes are in rack-1's clique ${RACK1_UUID}.0, each with a ready IMEX daemon"

for pod in "${CD_WORKLOADS[@]}"; do
  channels=$(kubectl_ctx -n "${CD_NAMESPACE}" exec "${pod}" -- ls /dev/nvidia-caps-imex-channels |
    tr '\n' ' ') || channels="none"
  [[ "${channels}" == "channel0 " ]] ||
    fail "${pod} sees IMEX channels '${channels}', expected channel0 only"
  ok "${pod}: /dev/nvidia-caps-imex-channels holds channel0 only (allocationMode Single)"
done

###############################################################################
# Summary
###############################################################################
cat <<EOF

==> All three ComputeDomain scenarios passed.

   Scenario 1  fabric identity : check-fabric reports clique 0 of rack-1 on
                                 worker-0 and worker-1, and clique 0 of rack-2
                                 on worker-2 and worker-3.
   Scenario 2  rebind          : a helm upgrade moved every node to clique 99
                                 of a new domain and back, with no image
                                 rebuild.
   Scenario 3  ComputeDomain   : the unmodified DRA driver joined worker-0 and
                                 worker-1 into one Ready IMEX domain, and each
                                 workload got IMEX channel 0.

==> The cluster and the example ComputeDomain are left running. Inspect them:
    export KUBECONFIG=${DEMO_KUBECONFIG}
    kubectl -n ${CD_NAMESPACE} get computedomain ${CD_NAME} -o yaml
    kubectl -n ${DRA_NAMESPACE} get pods -l resource.nvidia.com/computeDomain -o wide

==> To tear it down:
    kind delete cluster --name ${CLUSTER_NAME} --kubeconfig ${DEMO_KUBECONFIG}
EOF
