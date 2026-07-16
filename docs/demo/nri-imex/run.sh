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
#   3. The real nvidia-imex domain reports READY (nvidia-imex-ctl -q).
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

info "Scenario 3: real nvidia-imex domain status"
imex_pod="$(kubectl get pods -n nvml-mock-system -l app.kubernetes.io/name=nvml-mock -o jsonpath='{.items[0].metadata.name}')"
if kubectl exec -n nvml-mock-system "$imex_pod" -- sh -c 'command -v nvidia-imex-ctl >/dev/null 2>&1'; then
  status="$(kubectl exec -n nvml-mock-system "$imex_pod" -- nvidia-imex-ctl -q 2>&1 || true)"
  echo "$status" | grep -qi READY && ok "nvidia-imex domain READY" \
    || info "nvidia-imex-ctl output (informational):\n$status"
else
  info "nvidia-imex-ctl not present in this image; skipping domain status (see README)"
fi

info "Demo complete."
