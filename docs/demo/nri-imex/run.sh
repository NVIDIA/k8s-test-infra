#!/usr/bin/env bash
# Copyright 2026 NVIDIA CORPORATION
#
# SPDX-License-Identifier: Apache-2.0
#
# Demo: mock IMEX channels injected over the NRI foundation (issue #437).
#
# Spins up a 2-worker Kind cluster with containerd NRI enabled, installs
# nvml-mock (gb200 profile) with NRI + IMEX channels + a single ComputeDomain
# spanning both workers, deploys a plain annotated workload, and asserts:
#   1. Both workers' workloads see /dev/nvidia-caps-imex-channels/channel0..15.
#   2. check-fabric reports the same clusterUuid on both workers.
#   3. Real nvidia-imex NO-GPU daemons on both workers form a domain:
#      nvidia-imex-ctl -q reports READY, -N -j reports UP with 2 nodes
#      READY / version NO_GPU.
set -euo pipefail

CLUSTER_NAME="nvml-mock-nri-imex"
RELEASE_NAME="nvml-mock"
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
CHART_PATH="${REPO_ROOT}/deployments/nvml-mock/helm/nvml-mock"
KIND_CONFIG="${REPO_ROOT}/docs/demo/nri-imex/kind.yaml"
TOPOLOGY_FILE="${REPO_ROOT}/docs/demo/nri-imex/topology.yaml"
WORKLOAD_FILE="${REPO_ROOT}/docs/demo/nri-imex/imex-workload.yaml"
IMAGE_NAME="${IMAGE_NAME:-nvml-mock:nri-imex}"
OVERLAY_IMAGE_NAME="${OVERLAY_IMAGE_NAME:-nvml-mock:nri-imex-real-imex}"
SYSTEM_NS="nvml-mock-system"
CHANNEL_COUNT="${CHANNEL_COUNT:-16}"
EXPECTED_DOMAIN_UUID="00000000-0000-0000-0000-0000000000ce"
WORKER1="${CLUSTER_NAME}-worker"
WORKER2="${CLUSTER_NAME}-worker2"

info() { printf '\n==> %s\n' "$*" >&2; }
ok()   { printf '    \xE2\x9C\x93 %s\n' "$*" >&2; }
fail() { printf 'ERROR: %s\n' "$*" >&2; exit 1; }

command -v jq >/dev/null 2>&1 || fail "jq is required (Scenario 3 parses nvidia-imex-ctl JSON)"

cleanup() { kind delete cluster --name "$CLUSTER_NAME" >/dev/null 2>&1 || true; }
trap cleanup EXIT

workload_pod_on_node() {
  kubectl get pods -l app=imex-workload -o \
    jsonpath="{range .items[?(@.spec.nodeName=='$1')]}{.metadata.name}{end}"
}

nvml_pod_on_node() {
  kubectl get pods -n "$SYSTEM_NS" -l app.kubernetes.io/name=nvml-mock \
    --field-selector="spec.nodeName=$1,status.phase=Running" \
    -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true
}

info "Building nvml-mock image"
docker build -t "$IMAGE_NAME" -f "${REPO_ROOT}/deployments/nvml-mock/Dockerfile" "$REPO_ROOT"

info "Building real-IMEX overlay (--nogpu via imex-nogpu-shim); LOCAL BUILD ONLY"
docker build -t "$OVERLAY_IMAGE_NAME" \
  --target demo \
  --build-arg "NVML_MOCK_IMAGE=${IMAGE_NAME}" \
  --build-arg "GOLANG_VERSION=$("${REPO_ROOT}/hack/golang-version.sh")" \
  -f "${REPO_ROOT}/deployments/nvml-mock/Dockerfile.compute-domain-daemon" "$REPO_ROOT"

info "Creating Kind cluster $CLUSTER_NAME"
kind create cluster --name "$CLUSTER_NAME" --config "$KIND_CONFIG"
kind load docker-image "$OVERLAY_IMAGE_NAME" --name "$CLUSTER_NAME"

info "Installing nvml-mock (gb200) with NRI + IMEX channels + topology"
helm upgrade --install "$RELEASE_NAME" "$CHART_PATH" \
  --namespace nvml-mock-system --create-namespace --wait \
  --set gpu.profile=gb200 \
  --set image.repository="${OVERLAY_IMAGE_NAME%%:*}" \
  --set image.tag="${OVERLAY_IMAGE_NAME##*:}" \
  --set nri.enabled=true \
  --set imexChannels.count="$CHANNEL_COUNT" \
  -f "$TOPOLOGY_FILE"

info "Deploying annotated workload (created AFTER overlay staging)"
kubectl apply -f "$WORKLOAD_FILE"
kubectl rollout status daemonset/imex-workload --timeout=180s

info "Scenario 1: both workers see $CHANNEL_COUNT IMEX channels"
for w in "$WORKER1" "$WORKER2"; do
  pod="$(workload_pod_on_node "$w")"
  [ -n "$pod" ] || fail "no imex-workload pod on $w"
  n="$(kubectl exec "$pod" -- sh -c 'ls -1 /dev/nvidia-caps-imex-channels | wc -l' | tr -d '[:space:]')"
  [ "$n" = "$CHANNEL_COUNT" ] || fail "$w: expected $CHANNEL_COUNT channels, got $n"
  ok "$w: $n channels"
done

info "Scenario 2: consistent ComputeDomain identity across workers"
for w in "$WORKER1" "$WORKER2"; do
  pod="$(workload_pod_on_node "$w")"
  out="$(kubectl exec "$pod" -- check-fabric 2>&1 || true)"
  echo "$out" | grep -qi "clusterUuid : $EXPECTED_DOMAIN_UUID" \
    || fail "$w: expected clusterUuid $EXPECTED_DOMAIN_UUID\n$out"
  ok "$w: clusterUuid $EXPECTED_DOMAIN_UUID"
done

info "Scenario 3: real IMEX domain (NO GPU mode) queried with nvidia-imex-ctl"
IMEX_CFG=/tmp/imex.cfg
NODES_CFG=/tmp/nodes.cfg

POD_A="$(nvml_pod_on_node "$WORKER1")"
POD_B="$(nvml_pod_on_node "$WORKER2")"
[ -n "$POD_A" ] || fail "no running nvml-mock pod on $WORKER1"
[ -n "$POD_B" ] || fail "no running nvml-mock pod on $WORKER2"

IP_A="$(kubectl get pod -n "$SYSTEM_NS" "$POD_A" -o jsonpath='{.status.podIP}')"
IP_B="$(kubectl get pod -n "$SYSTEM_NS" "$POD_B" -o jsonpath='{.status.podIP}')"

# Render a per-pod IMEX config from the package default: foreground
# daemon, our two-node file (both pod IPs), pod-local log.
for pod in "$POD_A" "$POD_B"; do
  kubectl exec -n "$SYSTEM_NS" "$pod" -- sh -c "
    printf '%s\n%s\n' '$IP_A' '$IP_B' > '$NODES_CFG'
    sed -e 's/^DAEMONIZE=1/DAEMONIZE=0/' \
        -e 's|^IMEX_NODE_CONFIG_FILE=.*|IMEX_NODE_CONFIG_FILE=$NODES_CFG|' \
        -e 's|^LOG_FILE_NAME=.*|LOG_FILE_NAME=/tmp/nvidia-imex.log|' \
        /etc/nvidia-imex/config.cfg > '$IMEX_CFG'"
done

start_imex() {
  kubectl exec -n "$SYSTEM_NS" "$1" -- sh -c \
    "nvidia-imex -c $IMEX_CFG >/tmp/imex.stdout 2>&1 & echo \$! > /tmp/imex.pid"
}

domain_status() {
  kubectl exec -n "$SYSTEM_NS" "$1" -- nvidia-imex-ctl -c "$IMEX_CFG" -N -j 2>/dev/null \
    | jq -r '.status' 2>/dev/null || printf 'UNREACHABLE\n'
}

info "Starting real nvidia-imex (--nogpu via shim) on both workers"
start_imex "$POD_A"
start_imex "$POD_B"

# Local readiness probe (-q): exact upstream contract, prints "READY".
for pod in "$POD_A" "$POD_B"; do
  q=""
  for _ in $(seq 1 30); do
    q="$(kubectl exec -n "$SYSTEM_NS" "$pod" -- nvidia-imex-ctl -c "$IMEX_CFG" -q 2>/dev/null || true)"
    [ "$q" = "READY" ] && break
    sleep 1
  done
  [ "$q" = "READY" ] || fail "$pod: nvidia-imex-ctl -q never reported READY"
  ok "$pod: nvidia-imex-ctl -q READY"
done

# Domain-wide status converges to UP once both daemons connect over the
# pod network (gRPC :50000). Backoff + NetworkPolicy reconcile can take
# a couple of minutes, so poll up to 240s.
status="UNREACHABLE"
for _ in $(seq 1 240); do
  status="$(domain_status "$POD_A")"
  [ "$status" = "UP" ] && break
  sleep 1
done
if [ "$status" != "UP" ]; then
  kubectl exec -n "$SYSTEM_NS" "$POD_A" -- sh -c 'tail -20 /tmp/nvidia-imex.log 2>/dev/null' >&2 || true
  fail "domain never reached UP (status=$status)"
fi
ok "nvidia-imex-ctl -N -j: domain UP"

nodes_json="$(kubectl exec -n "$SYSTEM_NS" "$POD_A" -- nvidia-imex-ctl -c "$IMEX_CFG" -N -j 2>/dev/null)"
ready="$(printf '%s' "$nodes_json" | jq -r '[.nodes[] | select(.status=="READY")] | length')"
nogpu="$(printf '%s' "$nodes_json" | jq -r '[.nodes[] | select(.version=="NO_GPU")] | length')"
[ "$ready" = "2" ] || fail "want 2 READY nodes, got $ready: $nodes_json"
[ "$nogpu" = "2" ] || fail "want 2 NO_GPU nodes, got $nogpu: $nodes_json"
ok "2/2 nodes READY, version NO_GPU"

# Tidy up the demo daemons.
for pod in "$POD_A" "$POD_B"; do
  kubectl exec -n "$SYSTEM_NS" "$pod" -- sh -c 'kill -TERM "$(cat /tmp/imex.pid)" 2>/dev/null || true' || true
done

info "Demo complete."
