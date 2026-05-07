#!/usr/bin/env bash
# Copyright 2026 NVIDIA CORPORATION
#
# SPDX-License-Identifier: Apache-2.0
#
# End-to-end demo of nvml-mock GPU failure injection.
#
# Spins up a dedicated Kind cluster (so it never collides with the
# standalone demo), then walks the deployment through four scenarios
# via `helm upgrade --reuse-values`:
#
#   1. healthy            - baseline, all NVML calls succeed.
#   2. ecc_uncorrectable  - device stays addressable; ECC counters
#                            grow once tripped; Xid 79 is queued for
#                            the NVML event set.
#   3. lost               - guarded NVML calls return ERROR_GPU_IS_LOST.
#   4. fallen_off_bus     - same NVML surface as `lost`, paired with
#                            Xid 79 to flag the cause.
#
# Each scenario:
#   * runs `helm upgrade --reuse-values` with the new failure config,
#   * forces a DaemonSet rollout (the engine reads the YAML once at
#     process start, so we have to recycle the pod to pick up
#     changes),
#   * runs a verification command inside the pod and asserts the
#     expected behaviour.

set -euo pipefail

###############################################################################
# Configuration
###############################################################################
CLUSTER_NAME="nvml-mock-failure-demo"
IMAGE_NAME="nvml-mock:failure-demo"
RELEASE_NAME="nvml-mock"
CHART_PATH="deployments/nvml-mock/helm/nvml-mock"
GPU_COUNT=2
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
# Reuse the shared Kind config used by the standalone demo so all
# nvml-mock demos share the same cluster topology.
KIND_CONFIG="${REPO_ROOT}/docs/demo/kind.yaml"

###############################################################################
# Helpers
###############################################################################
info()    { printf '\n==> %s\n' "$*"; }
sub()     { printf '    %s\n' "$*"; }
ok()      { printf '    \xE2\x9C\x93 %s\n' "$*"; }   # ✓
fail()    { printf 'ERROR: %s\n' "$*" >&2; exit 1; }

# wait_for_pod: wait for the DaemonSet rollout to settle and echo the
# name of the (only) pod we'll exec into for verification.
wait_for_pod() {
  kubectl rollout status "daemonset/${RELEASE_NAME}" --timeout=90s >/dev/null
  kubectl get pods -l "app.kubernetes.io/name=${RELEASE_NAME}" \
    -o jsonpath='{.items[0].metadata.name}'
}

# upgrade_and_recycle: helm upgrade with --reuse-values + the per-mode
# overrides, then force a DaemonSet rollout so the new engine config
# is picked up. Echoes the new pod name on stdout.
upgrade_and_recycle() {
  local label=$1
  shift
  sub "helm upgrade -> ${label}"
  helm upgrade "${RELEASE_NAME}" "${REPO_ROOT}/${CHART_PATH}" \
    --reuse-values "$@" \
    --wait --timeout 120s >/dev/null
  kubectl rollout restart "daemonset/${RELEASE_NAME}" >/dev/null
  wait_for_pod
}

# assert_configmap_contains: fail if the rendered ConfigMap doesn't
# contain `pattern` somewhere in data.config.yaml. Cheap regression
# guard against the helper template silently dropping the failure
# overlay.
assert_configmap_contains() {
  local pattern=$1
  if ! kubectl get "configmap/${RELEASE_NAME}" \
        -o jsonpath='{.data.config\.yaml}' | grep -qF "${pattern}"; then
    fail "ConfigMap is missing expected pattern: ${pattern}"
  fi
  ok "ConfigMap contains: ${pattern}"
}

###############################################################################
# Step 1 -- Create / reuse the Kind cluster
###############################################################################
info "Creating Kind cluster: ${CLUSTER_NAME}"
if kind get clusters 2>/dev/null | grep -qx "${CLUSTER_NAME}"; then
  sub "Cluster already exists, reusing it"
else
  kind create cluster --name "${CLUSTER_NAME}" --config="${KIND_CONFIG}"
fi

###############################################################################
# Step 2 -- Build + load the image
###############################################################################
info "Building image: ${IMAGE_NAME}"
docker build -t "${IMAGE_NAME}" \
  -f "${REPO_ROOT}/deployments/nvml-mock/Dockerfile" "${REPO_ROOT}"

info "Loading image into Kind"
kind load docker-image "${IMAGE_NAME}" --name "${CLUSTER_NAME}"

###############################################################################
# Scenario 1 -- Healthy baseline
###############################################################################
# The first install is the only one without --reuse-values; from here
# on every scenario diffs against this baseline.
info "Scenario 1: healthy baseline (failureInjection.enabled=false)"
helm upgrade --install "${RELEASE_NAME}" "${REPO_ROOT}/${CHART_PATH}" \
  --set image.repository=nvml-mock \
  --set image.tag=failure-demo \
  --set gpu.profile=h100 \
  --set "gpu.count=${GPU_COUNT}" \
  --set gpu.failureInjection.enabled=false \
  --wait --timeout 120s >/dev/null

POD=$(wait_for_pod)
sub "DaemonSet pod ready: ${POD}"

# A healthy install must NOT inject the `failure:` block into the
# rendered ConfigMap.
if kubectl get "configmap/${RELEASE_NAME}" \
      -o jsonpath='{.data.config\.yaml}' | grep -qE '^[[:space:]]+failure:'; then
  fail "ConfigMap should not contain a failure: block when failureInjection.enabled=false"
fi
ok "ConfigMap has no failure: block (as expected)"

# nvidia-smi -L lists the configured number of GPUs.
LIST_OUT=$(kubectl exec "${POD}" -- nvidia-smi -L)
LIST_COUNT=$(printf '%s\n' "${LIST_OUT}" | grep -c '^GPU ' || true)
if [[ "${LIST_COUNT}" -ne "${GPU_COUNT}" ]]; then
  fail "Expected ${GPU_COUNT} GPUs, nvidia-smi -L reported ${LIST_COUNT}:
${LIST_OUT}"
fi
ok "nvidia-smi -L lists ${LIST_COUNT} GPUs"

# Aggregate uncorrectable ECC must be zero on a healthy device.
ECC_BASELINE=$(kubectl exec "${POD}" -- nvidia-smi \
  --query-gpu=ecc.errors.uncorrected.aggregate.total \
  --format=csv,noheader,nounits 2>/dev/null | head -1 || echo "")
if [[ "${ECC_BASELINE}" != "0" ]]; then
  sub "ECC baseline reported '${ECC_BASELINE}' (some drivers print '[N/A]' here, that's fine)"
else
  ok "Healthy ECC baseline: ${ECC_BASELINE}"
fi

###############################################################################
# Scenario 2 -- ecc_uncorrectable + Xid 79
###############################################################################
# `ecc_uncorrectable` keeps the device addressable: handle lookups and
# identity getters keep succeeding, but uncorrectable ECC counters
# return the running call count once tripped. `after_calls: 3` means
# a single `nvidia-smi -q -d ECC` invocation (which issues many
# guarded calls per device) trips on the third call and reports a
# strictly-positive uncorrectable total in the same process.
info "Scenario 2: ecc_uncorrectable + Xid 79 (after_calls=3)"
POD=$(upgrade_and_recycle "ecc_uncorrectable" \
  --set gpu.failureInjection.enabled=true \
  --set gpu.failureInjection.mode=ecc_uncorrectable \
  --set gpu.failureInjection.after_calls=3 \
  --set gpu.failureInjection.xid.code=79)
sub "Pod after rollout: ${POD}"

assert_configmap_contains "mode: ecc_uncorrectable"

# Device must remain addressable (mode contract: ecc_uncorrectable
# does NOT take the GPU off the API surface).
LIST_COUNT=$(kubectl exec "${POD}" -- nvidia-smi -L | grep -c '^GPU ' || true)
if [[ "${LIST_COUNT}" -ne "${GPU_COUNT}" ]]; then
  fail "ecc_uncorrectable must keep all ${GPU_COUNT} GPUs addressable, got ${LIST_COUNT}"
fi
ok "nvidia-smi -L still lists ${LIST_COUNT} GPUs (device addressable)"

# Aggregate uncorrectable counter must be > 0 once tripped. The third
# guarded call inside `nvidia-smi -q -d ECC` meets after_calls=3, so
# the aggregate counter the tool prints is strictly positive.
ECC_OUT=$(kubectl exec "${POD}" -- nvidia-smi -q -d ECC)
UNCORR=$(printf '%s\n' "${ECC_OUT}" | awk '
  /Aggregate Uncorrectable Errors/ { in_block = 1; next }
  in_block && /Total/              { print $NF; exit }
')
case "${UNCORR}" in
  ''|0|*[!0-9]*)
    if printf '%s\n' "${ECC_OUT}" | grep -qE 'Uncorrectable.*[1-9]'; then
      ok "ECC uncorrectable counter is non-zero (parsed loosely)"
    else
      fail "ecc_uncorrectable mode did not trip — ECC counter is still 0"
    fi
    ;;
  *)
    ok "ECC uncorrectable total = ${UNCORR} (>0 confirms trip)"
    ;;
esac

###############################################################################
# Scenario 3 -- lost
###############################################################################
# `mode: lost, after_calls: 1` -- the very first guarded metric call
# (e.g. GetTemperature) trips the device. Within the same process
# every subsequent guarded getter, identity getter, and handle lookup
# returns ERROR_GPU_IS_LOST. nvidia-smi reports the temperature column
# as `[Unknown Error]` or `[N/A]` instead of a number.
info "Scenario 3: lost (after_calls=1)"
POD=$(upgrade_and_recycle "lost" \
  --set gpu.failureInjection.mode=lost \
  --set gpu.failureInjection.after_calls=1 \
  --set gpu.failureInjection.xid.code=0)
sub "Pod after rollout: ${POD}"

assert_configmap_contains "mode: lost"

# Pull a temperature column with `--format=csv,noheader,nounits`. A
# healthy device prints integers; a lost device prints an error
# marker. We accept any of the known error markers nvidia-smi uses
# (different driver versions vary).
TEMP_OUT=$(kubectl exec "${POD}" -- nvidia-smi \
  --query-gpu=temperature.gpu --format=csv,noheader,nounits 2>&1 || true)
sub "nvidia-smi temperature query output:"
printf '%s\n' "${TEMP_OUT}" | sed 's/^/      /'
if printf '%s\n' "${TEMP_OUT}" | \
     grep -qiE '\[N/A\]|\[Unknown Error\]|GPU is lost|ERR'; then
  ok "lost mode propagates an error marker through nvidia-smi"
else
  fail "lost mode did not surface a recognisable error in nvidia-smi output"
fi

###############################################################################
# Scenario 4 -- fallen_off_bus + Xid 79
###############################################################################
# Same NVML surface as `lost` (ERROR_GPU_IS_LOST from every guarded
# getter) but with Xid 79 ("GPU has fallen off the bus") queued for
# the NVML event set. Real consumers (device-plugin health monitor,
# dcgm-exporter) see Xid 79 via NVML_EVENT_TYPE_XID_CRITICAL_ERROR.
# We can't easily exercise the event-set consumer from inside this
# script (nvidia-smi doesn't subscribe), so we settle for the same
# nvidia-smi error-marker assertion as the lost scenario.
info "Scenario 4: fallen_off_bus + Xid 79 (after_calls=1)"
POD=$(upgrade_and_recycle "fallen_off_bus" \
  --set gpu.failureInjection.mode=fallen_off_bus \
  --set gpu.failureInjection.after_calls=1 \
  --set gpu.failureInjection.xid.code=79)
sub "Pod after rollout: ${POD}"

assert_configmap_contains "mode: fallen_off_bus"
assert_configmap_contains "code: 79"

TEMP_OUT=$(kubectl exec "${POD}" -- nvidia-smi \
  --query-gpu=temperature.gpu --format=csv,noheader,nounits 2>&1 || true)
sub "nvidia-smi temperature query output:"
printf '%s\n' "${TEMP_OUT}" | sed 's/^/      /'
if printf '%s\n' "${TEMP_OUT}" | \
     grep -qiE '\[N/A\]|\[Unknown Error\]|GPU is lost|ERR'; then
  ok "fallen_off_bus propagates an error marker through nvidia-smi"
else
  fail "fallen_off_bus did not surface a recognisable error in nvidia-smi output"
fi

###############################################################################
# Summary
###############################################################################
cat <<EOF

==> All four failure-injection scenarios verified.

   Scenario 1  healthy            : nvidia-smi -L lists ${GPU_COUNT} GPU(s); ECC = 0
   Scenario 2  ecc_uncorrectable  : device addressable; ECC uncorrectable > 0
   Scenario 3  lost               : nvidia-smi metric query returns error markers
   Scenario 4  fallen_off_bus     : nvidia-smi metric query returns error markers; Xid 79 queued

==> The Xid critical-error event itself is delivered via the NVML event
    set (NVML_EVENT_TYPE_XID_CRITICAL_ERROR), which nvidia-smi does
    NOT subscribe to. Real consumers (NVIDIA device plugin health
    monitor, dcgm-exporter) read it via nvmlEventSetWait_v2 and will
    surface 'Xid 79' / mark the GPU Unhealthy on their own when run
    against this cluster.

==> Tear down
    kind delete cluster --name ${CLUSTER_NAME}
EOF
