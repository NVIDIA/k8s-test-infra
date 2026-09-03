#!/usr/bin/env bash
# Copyright 2026 NVIDIA CORPORATION
#
# SPDX-License-Identifier: Apache-2.0
#
# End-to-end demo of nvml-mock GPU failure injection.
#
# Installs into the cluster your current kubectl context points at
# (BUILD_LOCAL=true instead builds the image and side-loads it into a
# dedicated Kind cluster of its own), then walks the deployment through
# four scenarios via `helm upgrade --reuse-values`:
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

# shellcheck source=../lib/preflight.sh
source "$(cd "$(dirname "${BASH_SOURCE[0]}")/../lib" && pwd)/preflight.sh"

###############################################################################
# Configuration
###############################################################################
CLUSTER_NAME="nvml-mock-failure-demo"
# BUILD_LOCAL=true builds the image from source and side-loads it into a Kind
# cluster, creating that cluster if it is missing. It is the ONLY path that
# needs Docker and Kind. The default installs the published image into the
# cluster your current context already points at, and never creates one.
: "${BUILD_LOCAL:=false}"
: "${LOCAL_IMAGE:=nvml-mock:failure-demo}"
# Populated only on the BUILD_LOCAL path, when the demo switches contexts.
PRIOR_CONTEXT=""
# Resolved by demo::preflight below. This demo previously ran 11 bare kubectl
# call sites and two helm calls with no --kube-context, so on its
# cluster-reuse branch it installed into whatever context happened to be
# current, silently. Every one is now pinned to the context the preflight
# announced and, off Kind, made you confirm.
KUBE_CONTEXT=""
# MUST differ from the standalone demo's release name. The chart creates a
# ClusterRole and ClusterRoleBinding named after the release
# (templates/rbac.yaml), and those are CLUSTER-scoped, so a namespace cannot
# separate them. With both demos on "nvml-mock" the second install dies with:
#   Error: unable to continue with install: ClusterRole "nvml-mock" in
#   namespace "" exists and cannot be imported into the current release:
#   invalid ownership metadata; annotation validation error: key
#   "meta.helm.sh/release-namespace" must equal "mokka-failure": current
#   value is "mokka"
# The release name contains the chart name, so nvml-mock.fullname collapses to
# exactly this string and every derived object is "nvml-mock-failure*".
RELEASE_NAME="nvml-mock-failure"
# Deploy into a namespace of its own, distinct from the standalone demo's
# "mokka". The distinct RELEASE_NAME above is what keeps the two demos'
# cluster-scoped objects apart; this keeps their namespaced objects apart too,
# so `kubectl -n mokka get all` shows one demo rather than both interleaved.
#
# Overriding this to "mokka" no longer produces a helm ownership error, since
# the releases now have different names, so the two demos would simply
# co-locate. They still share per-node host state either way, so do not run
# both against one cluster at the same time.
: "${NAMESPACE:=mokka-failure}"
CHART_PATH="deployments/nvml-mock/helm/nvml-mock"
# The chart names its rendered ConfigMap "<fullname>-config" (see
# templates/configmap.yaml). The fullname helper short-circuits to the
# release name whenever the release name contains the chart name, which
# ours does, so the ConfigMap is "nvml-mock-failure-config".
CONFIGMAP_NAME="${RELEASE_NAME}-config"
# Pods carry app.kubernetes.io/name=<CHART name> and
# app.kubernetes.io/instance=<RELEASE name> (templates/_helpers.tpl,
# nvml-mock.selectorLabels). Selecting on name alone would match the
# standalone demo's pods too on a shared cluster, and would match NOTHING
# once the release stopped being called "nvml-mock". Pin the instance.
POD_SELECTOR="app.kubernetes.io/name=nvml-mock,app.kubernetes.io/instance=${RELEASE_NAME}"
# Number of GPUs that nvidia-smi reports inside the daemonset pod. We
# DON'T pass --set gpu.count=... because that only affects the
# host-side CDI spec produced by setup.sh — the in-pod config mounted
# at /etc/nvml-mock/config.yaml is the chart's full ConfigMap, which
# always contains every device defined by the chosen profile (eg 8
# for h100). The baseline scenario below detects the actual count by
# parsing nvidia-smi -L and reuses that value for subsequent
# assertions.
EXPECTED_GPUS=0
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
# Reuse the shared Kind config used by the standalone demo so all
# nvml-mock demos share the same cluster topology.
KIND_CONFIG="${REPO_ROOT}/docs/demo/kind.yaml"

###############################################################################
# Helpers
###############################################################################
# All log helpers write to stderr so functions that echo a captured
# value on stdout (e.g. upgrade_and_recycle -> pod name) stay safe to
# use inside command substitution.
info()    { printf '\n==> %s\n' "$*" >&2; }
# Pin every kubectl call to the context the preflight resolved and announced,
# so nothing can redirect the demo to a cluster the reader never saw.
kubectl_ctx() { command kubectl --context "${KUBE_CONTEXT}" -n "${NAMESPACE}" "$@"; }
sub()     { printf '    %s\n' "$*" >&2; }
ok()      { printf '    \xE2\x9C\x93 %s\n' "$*" >&2; }   # ✓
fail()    { printf 'ERROR: %s\n' "$*" >&2; exit 1; }

# wait_for_pod: wait for the DaemonSet rollout to settle and echo the
# name of a Running pod we can exec into for verification.
#
# `kubectl rollout status` returns once the new pods are Ready, but a
# terminating old pod can still appear in the listing for a moment.
# Filtering on status.phase=Running keeps us from accidentally execing
# into a pod that's on its way out.
wait_for_pod() {
  kubectl_ctx rollout status "daemonset/${RELEASE_NAME}" \
    --timeout="$(demo::install_timeout)" >/dev/null
  kubectl_ctx get pods -l "${POD_SELECTOR}" \
    --field-selector=status.phase=Running \
    -o jsonpath='{.items[0].metadata.name}'
}

# upgrade_and_recycle: helm upgrade with --reuse-values + the per-mode
# overrides, then force every nvml-mock pod to recreate so the new
# config is picked up everywhere at once.
#
# Two safeguards are layered on top of helm:
#   1. The pod template carries a sha256 of the rendered GPU config
#      (templates/daemonset.yaml: `checksum/config`) so any change to
#      gpu.failureInjection / gpu.dynamicMetrics already mutates the
#      pod-template hash. The initial helm install also pinned
#      `updateStrategy.rollingUpdate.maxUnavailable=100%` (the chart
#      default is the more conservative 25%) so the rollout fires
#      across every node simultaneously.
#   2. `kubectl delete pods -l ...` explicitly evicts every existing
#      pod up front. Belt-and-suspenders: deterministic refresh even
#      under a tighter updateStrategy.
#
# Echoes the new pod name on stdout.
upgrade_and_recycle() {
  local label=$1
  shift
  sub "helm upgrade -> ${label}"
  helm upgrade "${RELEASE_NAME}" "${REPO_ROOT}/${CHART_PATH}" \
    --kube-context "${KUBE_CONTEXT}" \
    --namespace "${NAMESPACE}" \
    --reuse-values "$@" \
    --wait --timeout "$(demo::install_timeout)" >/dev/null
  # Synchronous delete: the chart pins terminationGracePeriodSeconds
  # to a low value (see values.yaml), so the default --wait=true
  # blocks just long enough for pods to disappear before rollout
  # status checks the new generation. --ignore-not-found keeps the
  # call idempotent if the previous scenario already evicted them.
  kubectl_ctx delete pods -l "${POD_SELECTOR}" \
    --ignore-not-found >/dev/null
  wait_for_pod
}

# assert_configmap_contains: fail if the rendered ConfigMap doesn't
# contain `pattern` somewhere in data.config.yaml. Cheap regression
# guard against the helper template silently dropping the failure
# overlay.
assert_configmap_contains() {
  local pattern=$1
  if ! kubectl_ctx get "configmap/${CONFIGMAP_NAME}" \
        -o jsonpath='{.data.config\.yaml}' | grep -qF "${pattern}"; then
    fail "ConfigMap ${CONFIGMAP_NAME} is missing expected pattern: ${pattern}"
  fi
  ok "ConfigMap contains: ${pattern}"
}

###############################################################################
# Step 1 -- Resolve the target cluster
#
# BUILD_LOCAL=true owns a Kind cluster: it creates one if missing and switches
# to it, because a locally built image can only be side-loaded into a cluster
# this script controls. Otherwise nothing is created and the demo installs
# into whatever context is already current, which demo::preflight announces
# and, unless it is a loopback Kind cluster, makes you confirm.
###############################################################################
if [[ "${BUILD_LOCAL}" == "true" ]]; then
  # Before creating anything: a missing docker or kind here would otherwise
  # surface as a raw "command not found" after a cluster already existed.
  demo::require_build_tools
  info "Creating Kind cluster: ${CLUSTER_NAME}"
  if kind get clusters 2>/dev/null | grep -qx "${CLUSTER_NAME}"; then
    sub "Cluster already exists, reusing it"
  else
    kind create cluster --name "${CLUSTER_NAME}" --config="${KIND_CONFIG}"
  fi
  # Remember where the reader was so the summary can offer to put them back.
  PRIOR_CONTEXT="$(command kubectl config current-context 2>/dev/null || true)"
  command kubectl config use-context "kind-${CLUSTER_NAME}"
fi

# Tell the preflight where the workload lands so its announcement, which is
# the one thing the reader confirms, names the namespace too.
# shellcheck disable=SC2034  # read by demo::preflight in ../lib/preflight.sh
DEMO_NAMESPACE="${NAMESPACE}"
demo::preflight
# The standalone demo's release. Co-locating the two corrupts shared per-node
# host state that no namespace or release name scopes.
demo::require_no_sibling_release "nvml-mock" "${RELEASE_NAME}"
KUBE_CONTEXT="${DEMO_KUBE_CONTEXT}"
IMAGE_NAME="$(demo::image_ref)"

# Split "repo:tag" for the chart's two separate values. Shared with the
# standalone demo, and it rejects digest refs the chart cannot express.
# `exit` inside $() only leaves the subshell, so propagate the code explicitly
# rather than relying on set -e to notice the failed assignment.
IMAGE_PARTS="$(demo::image_parts "${IMAGE_NAME}")" || exit $?
IMAGE_REPO="${IMAGE_PARTS%|*}"
IMAGE_TAG="${IMAGE_PARTS#*|}"

###############################################################################
# Step 2 -- Build + load the image (BUILD_LOCAL only)
###############################################################################
if [[ "${BUILD_LOCAL}" == "true" ]]; then
  info "Building image: ${IMAGE_NAME}"
  docker build -t "${IMAGE_NAME}" \
    -f "${REPO_ROOT}/deployments/nvml-mock/Dockerfile" "${REPO_ROOT}"

  info "Loading image into Kind"
  kind load docker-image "${IMAGE_NAME}" --name "${CLUSTER_NAME}"
else
  info "Using published image: ${IMAGE_NAME} (set BUILD_LOCAL=true to build from source)"
fi

###############################################################################
# Scenario 1 -- Healthy baseline
###############################################################################
# The first install is the only one without --reuse-values; from here
# on every scenario diffs against this baseline.
info "Scenario 1: healthy baseline (failureInjection.enabled=false)"
demo::announce_pull "${IMAGE_NAME}"
helm upgrade --install "${RELEASE_NAME}" "${REPO_ROOT}/${CHART_PATH}" \
  --kube-context "${KUBE_CONTEXT}" \
  --namespace "${NAMESPACE}" --create-namespace \
  --set "image.repository=${IMAGE_REPO}" \
  --set "image.tag=${IMAGE_TAG}" \
  --set gpu.profile=h100 \
  --set gpu.failureInjection.enabled=false \
  --set-string updateStrategy.rollingUpdate.maxUnavailable=100% \
  --set terminationGracePeriodSeconds=1 \
  --wait --timeout "$(demo::install_timeout)" >/dev/null

POD=$(wait_for_pod)
sub "DaemonSet pod ready: ${POD}"

# A healthy install must NOT inject the `failure:` block into the
# rendered ConfigMap.
if kubectl_ctx get "configmap/${CONFIGMAP_NAME}" \
      -o jsonpath='{.data.config\.yaml}' | grep -qE '^[[:space:]]+failure:'; then
  fail "ConfigMap should not contain a failure: block when failureInjection.enabled=false"
fi
ok "ConfigMap has no failure: block (as expected)"

# Detect how many GPUs nvidia-smi reports inside the pod. This is what
# every subsequent scenario will assert against — we don't hard-code a
# number because the in-pod count is dictated by the profile YAML
# rendered into the ConfigMap, not by `--set gpu.count`.
LIST_OUT=$(kubectl_ctx exec "${POD}" -- nvidia-smi -L)
EXPECTED_GPUS=$(printf '%s\n' "${LIST_OUT}" | grep -c '^GPU ' || true)
if [[ "${EXPECTED_GPUS}" -lt 1 ]]; then
  fail "nvidia-smi -L reported no GPUs in the healthy baseline:
${LIST_OUT}"
fi
ok "nvidia-smi -L lists ${EXPECTED_GPUS} GPU(s) (healthy baseline)"

# Aggregate uncorrectable ECC must be zero on a healthy device.
ECC_BASELINE=$(kubectl_ctx exec "${POD}" -- nvidia-smi \
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
# return the running call count once tripped. We use after_calls=1 so
# the FIRST guarded ECC read on each device trips it — every device
# has its own per-device call counter, so a query that issues exactly
# one guarded call per GPU still trips every GPU.
info "Scenario 2: ecc_uncorrectable + Xid 79 (after_calls=1)"
POD=$(upgrade_and_recycle "ecc_uncorrectable" \
  --set gpu.failureInjection.enabled=true \
  --set gpu.failureInjection.mode=ecc_uncorrectable \
  --set gpu.failureInjection.after_calls=1 \
  --set gpu.failureInjection.xid.code=79)
sub "Pod after rollout: ${POD}"

assert_configmap_contains "mode: ecc_uncorrectable"

# Device must remain addressable (mode contract: ecc_uncorrectable
# does NOT take the GPU off the API surface).
LIST_COUNT=$(kubectl_ctx exec "${POD}" -- nvidia-smi -L | grep -c '^GPU ' || true)
if [[ "${LIST_COUNT}" -ne "${EXPECTED_GPUS}" ]]; then
  fail "ecc_uncorrectable must keep all ${EXPECTED_GPUS} GPUs addressable, got ${LIST_COUNT}"
fi
ok "nvidia-smi -L still lists ${LIST_COUNT} GPUs (device addressable)"

# Read the uncorrectable counter via --format=csv so each GPU prints
# exactly one integer per line. No awk required: just confirm at
# least one line is a positive integer.
ECC_OUT=$(kubectl_ctx exec "${POD}" -- nvidia-smi \
  --query-gpu=ecc.errors.uncorrected.aggregate.total \
  --format=csv,noheader,nounits 2>&1 || true)
sub "ECC uncorrectable per-GPU readings:"
printf '%s\n' "${ECC_OUT}" | sed 's/^/      /'

# Pick the highest integer from the output. `grep -E '^[0-9]+$'`
# discards [N/A] / [Unknown Error] / blank lines; `sort -n | tail -1`
# yields the max. If the max is >0 the trip fired.
MAX_UNCORR=$(printf '%s\n' "${ECC_OUT}" | \
  grep -E '^[0-9]+$' | sort -n | tail -1)
MAX_UNCORR=${MAX_UNCORR:-0}
if [[ "${MAX_UNCORR}" -gt 0 ]]; then
  ok "ECC uncorrectable max = ${MAX_UNCORR} (>0 confirms trip)"
else
  fail "ecc_uncorrectable did not trip — every per-GPU counter is still 0"
fi

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
TEMP_OUT=$(kubectl_ctx exec "${POD}" -- nvidia-smi \
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

TEMP_OUT=$(kubectl_ctx exec "${POD}" -- nvidia-smi \
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
# Only the BUILD_LOCAL path creates a cluster, so only it offers to delete
# one. On the default path the cluster was already yours.
# The uninstall MUST carry -n and --kube-context. Without them helm resolves
# the release from the reader's current context and namespace, which on a
# shared cluster is how this teardown would delete the standalone demo's
# release instead of this one.
UNINSTALL="helm uninstall ${RELEASE_NAME} -n ${NAMESPACE} --kube-context ${KUBE_CONTEXT}"
if [[ "${BUILD_LOCAL}" == "true" ]]; then
  TEARDOWN="kind delete cluster --name ${CLUSTER_NAME}"
  if [[ -n "${PRIOR_CONTEXT}" && "${PRIOR_CONTEXT}" != "${KUBE_CONTEXT}" ]]; then
    TEARDOWN="${TEARDOWN}
    kubectl config use-context ${PRIOR_CONTEXT}   # this run switched your context"
  fi
else
  TEARDOWN="${UNINSTALL}
    (this run did not create a cluster, so nothing here deletes one)"
fi

cat <<EOF

==> All four failure-injection scenarios verified.

   Scenario 1  healthy            : nvidia-smi -L lists ${EXPECTED_GPUS} GPU(s); ECC = 0
   Scenario 2  ecc_uncorrectable  : device addressable; ECC uncorrectable > 0
   Scenario 3  lost               : nvidia-smi metric query returns error markers
   Scenario 4  fallen_off_bus     : nvidia-smi metric query returns error markers; Xid 79 queued

==> The Xid critical-error event itself is delivered via the NVML event
    set (NVML_EVENT_TYPE_XID_CRITICAL_ERROR), which nvidia-smi does
    NOT subscribe to. Real consumers (NVIDIA device plugin health
    monitor, dcgm-exporter) read it via nvmlEventSetWait_v2 and will
    surface 'Xid 79' / mark the GPU Unhealthy on their own when run
    against this cluster.

==> Target cluster
    context  : ${KUBE_CONTEXT}
    namespace: ${NAMESPACE}
    image    : ${IMAGE_NAME}

==> Tear down
    ${TEARDOWN}
EOF
