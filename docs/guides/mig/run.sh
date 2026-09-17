#!/usr/bin/env bash
# Copyright 2026 NVIDIA CORPORATION
#
# SPDX-License-Identifier: Apache-2.0
#
# End-to-end demo of the nvml-mock MIG instance lifecycle, driven entirely
# through `nvidia-smi mig` the way an operator or nvidia-mig-parted drives it
# on real hardware.
#
# Installs an UNPARTITIONED board and partitions it at runtime, which is the
# opposite of what the guide's Step 1 does. Both are worth showing and they
# answer different questions: the chart path produces a board Kubernetes can
# schedule against, while this path exercises the NVML calls a partitioning
# tool makes. A runtime layout is deliberately NOT allocatable — see "Limits"
# in README.md — so this demo never involves the device plugin.
#
# The walkthrough, on GPU 0 of one node:
#
#   1. baseline    - MIG off, the board reports as whole GPUs.
#   2. enable      - `nvidia-smi -i 0 -mig 1`, read back from a separate
#                    process to prove it was recorded rather than held in
#                    the memory of the process that set it.
#   3. profiles    - `mig -lgip`, asserting Free never exceeds Total.
#   4. create      - `mig -cgi <profile> -C`, then the three listings that
#                    should now show it (-lgi, -lci, and nvidia-smi -L).
#   5. refuse      - `mig -dgi` while the compute instance is live, which
#                    NVML rejects with NVML_ERROR_IN_USE. Asserts the
#                    refusal changed nothing.
#   6. delete      - the order that does work: -dci, then -dgi.
#   7. disable     - `nvidia-smi -i 0 -mig 0`, back to whole GPUs.
#
# Steps 3 and 5 are assertions rather than demonstrations: both were real
# defects, and a mock that gets them wrong lets a partitioning tool pass here
# and fail on hardware.
#
# Written for bash 3.2 (stock macOS): no mapfile, no associative arrays,
# no ${var,,}.

set -euo pipefail

# shellcheck source=../lib/preflight.sh
source "$(cd "$(dirname "${BASH_SOURCE[0]}")/../lib" && pwd)/preflight.sh"

###############################################################################
# Configuration
###############################################################################
CLUSTER_NAME="nvml-mock-mig-demo"
# BUILD_LOCAL=true builds the image from source and side-loads it into a Kind
# cluster, creating that cluster if it is missing. It is the ONLY path that
# needs Docker and Kind. BUILD_LOCAL=false installs the published image into
# the cluster your current context already points at, and never creates one.
#
# It defaults to true, unlike the sibling demos, because the MIG instance
# lifecycle has not reached a published release: the published image cannot run
# this demo at all. Step 2 detects that on the false path and says so rather
# than failing on an assertion twenty lines later.
: "${BUILD_LOCAL:=true}"
: "${LOCAL_IMAGE:=nvml-mock:mig-demo}"
# Populated only on the BUILD_LOCAL path, when the demo switches contexts.
PRIOR_CONTEXT=""
# Resolved and announced by demo::preflight, then pinned on every kubectl and
# helm call so a kubeconfig that changes under the demo cannot redirect it.
KUBE_CONTEXT=""
# MUST contain the chart name, so nvml-mock.fullname collapses to exactly this
# string.
#
# Deliberately the standard release name and namespace rather than a pair of
# this demo's own. Every nvml-mock release mounts the same per-node hostPaths,
# which neither a namespace nor a release name scopes, so a demo-specific name
# would only stand a second mock up beside the usual one and leave the two
# writing the same files. Sharing the name makes that impossible by
# construction: Step 2's `helm upgrade --install` adopts an existing nvml-mock
# rather than installing alongside it. It also means an install here can be an
# upgrade of a release the reader already had, which Step 2 says out loud.
RELEASE_NAME="nvml-mock"
: "${NAMESPACE:=mokka}"
CHART_PATH="deployments/nvml-mock/helm/nvml-mock"
# Pods carry app.kubernetes.io/name=<chart> and instance=<release>. Selecting
# on name alone would match another demo's pods on a shared cluster.
POD_SELECTOR="app.kubernetes.io/name=nvml-mock,app.kubernetes.io/instance=${RELEASE_NAME}"
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
KIND_CONFIG="${REPO_ROOT}/docs/guides/kind.yaml"

# Which board to simulate, and the slice to carve out of it. The slice name is
# derived rather than hardcoded because it is a property of the board: a slice
# is named for the share of its own GPU's memory it holds, so the smallest
# slice of an h100 is 1g.10gb where an a100's is 1g.5gb. MIG_PROFILE overrides
# the derivation, which is how you demo a larger slice such as 3g.40gb.
: "${GPU_PROFILE:=h100}"
: "${MIG_PROFILE:=}"
if [ -z "${MIG_PROFILE}" ]; then
  case "${GPU_PROFILE}" in
    a100)  MIG_PROFILE="1g.5gb" ;;
    h100)  MIG_PROFILE="1g.10gb" ;;
    b200)  MIG_PROFILE="1g.24gb" ;;
    gb200) MIG_PROFILE="1g.24gb" ;;
    gb300) MIG_PROFILE="1g.36gb" ;;
    *)
      echo "ERROR: no MIG slice known for profile '${GPU_PROFILE}'." >&2
      echo "       MIG-capable profiles: a100 h100 b200 gb200 gb300." >&2
      echo "       l40s and t4 are not MIG-capable hardware." >&2
      echo "       Set MIG_PROFILE=<slice> to name one yourself." >&2
      exit 1
      ;;
  esac
fi
# The demo partitions this GPU only, and asserts its neighbours are untouched.
GPU_INDEX=0

###############################################################################
# Helpers
###############################################################################
# All log helpers write to stderr so functions that echo a captured value on
# stdout stay safe to use inside command substitution.
info()    { printf '\n==> %s\n' "$*" >&2; }
sub()     { printf '    %s\n' "$*" >&2; }
ok()      { printf '    \xE2\x9C\x93 %s\n' "$*" >&2; }   # ✓
fail()    { printf 'ERROR: %s\n' "$*" >&2; exit 1; }

kubectl_ctx() { command kubectl --context "${KUBE_CONTEXT}" -n "${NAMESPACE}" "$@"; }

# Name of a Running pod to exec into. `kubectl rollout status` returns once the
# new pods are Ready, but a terminating old pod can still appear in the
# listing, so filter on the phase rather than taking the first item.
wait_for_pod() {
  kubectl_ctx rollout status "daemonset/${RELEASE_NAME}" \
    --timeout="$(demo::install_timeout)" >/dev/null
  kubectl_ctx get pods -l "${POD_SELECTOR}" \
    --field-selector=status.phase=Running \
    -o jsonpath='{.items[0].metadata.name}'
}

# smi: run nvidia-smi in the demo pod and echo its output.
#
# Every call is its own process, and its own NVML engine. That is the point of
# driving the demo this way rather than from one long-lived process: a
# mutation reaches the next command only because the mock recorded it in the
# runtime override document, which is the same mechanism that makes a
# partition visible to DCGM and GFD.
smi() {
  kubectl_ctx exec "${POD}" -- nvidia-smi "$@"
}

# smi_expect_failure: run nvidia-smi expecting NVML to refuse, and echo the
# output. A zero exit is the failure here, so this cannot go through smi(),
# where `set -e` would abort on the non-zero exit a refusal produces.
smi_expect_failure() {
  local out status=0
  out="$(kubectl_ctx exec "${POD}" -- nvidia-smi "$@" 2>&1)" || status=$?
  if [ "${status}" -eq 0 ]; then
    fail "nvidia-smi $* was expected to be refused, but it succeeded:
${out}"
  fi
  printf '%s\n' "${out}"
}

# Count the GPU instances mig -lgi reports on one GPU. The listing's first
# column is the GPU index, so this counts rows for ours and tolerates the
# "No GPU instances found" case, which exits non-zero by design.
gpu_instance_count() {
  local out
  out="$(kubectl_ctx exec "${POD}" -- nvidia-smi mig -lgi 2>&1)" || true
  printf '%s\n' "${out}" | grep -cE "^\|[[:space:]]+${GPU_INDEX}[[:space:]]+MIG" || true
}

# Count the compute instances mig -lci reports. The listing has no column an
# assertion could filter one GPU on, so callers compare against a baseline
# taken while this GPU was empty rather than against an absolute.
compute_instance_count() {
  local out
  out="$(kubectl_ctx exec "${POD}" -- nvidia-smi mig -lci 2>&1)" || true
  printf '%s\n' "${out}" | grep -cE '^\|[[:space:]]+[0-9]+[[:space:]]+[0-9]+[[:space:]]+MIG' || true
}

# Count the MIG devices nvidia-smi -L derives from this GPU's partitions,
# which is not the same as its GPU-instance count: a GPU instance holding no
# compute instance yields no MIG device.
mig_device_count() {
  smi -L | awk -v gpu="${GPU_INDEX}" '
    /^GPU / { current = ($2 + 0); next }
    /^[[:space:]]+MIG / { if (current == gpu) n++ }
    END { print n + 0 }'
}

mig_mode_of_gpu() {
  smi --query-gpu=mig.mode.current --format=csv,noheader \
    -i "${GPU_INDEX}" | head -1 | tr -d ' '
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
if [ "${BUILD_LOCAL}" = "true" ]; then
  # Before creating anything: a missing docker or kind here would otherwise
  # surface as a raw "command not found" after a cluster already existed.
  demo::require_build_tools
  # Capture where the reader was BEFORE anything can move them. `kind create
  # cluster` switches the current context as a side effect.
  PRIOR_CONTEXT="$(command kubectl config current-context 2>/dev/null || true)"
  info "Creating Kind cluster: ${CLUSTER_NAME}"
  if kind get clusters 2>/dev/null | grep -qx "${CLUSTER_NAME}"; then
    sub "Cluster already exists, reusing it"
  else
    kind create cluster --name "${CLUSTER_NAME}" --config="${KIND_CONFIG}"
  fi
  command kubectl config use-context "kind-${CLUSTER_NAME}"
fi

# Tell the preflight where the workload lands, so the announcement the reader
# confirms names the namespace and not only the cluster.
# shellcheck disable=SC2034  # read by demo::preflight in ../lib/preflight.sh
DEMO_NAMESPACE="${NAMESPACE}"
demo::preflight
# The demos that install under a name of their own. All of them mount the same
# per-node hostPaths, which no namespace or release name scopes, so a
# co-located run leaves the shared mock config in whichever state the last one
# wrote.
#
# Plain "nvml-mock" is absent on purpose: that is this demo's own release name,
# and the guard matches names exactly with no notion of self. Checking for it
# would refuse against this demo's own leftovers — nothing here uninstalls, so
# every run after the first — when Step 2 upgrades them in place instead.
demo::require_no_sibling_release "nvml-mock-failure" "${RELEASE_NAME}"
demo::require_no_sibling_release "nvml-mock-operator" "${RELEASE_NAME}"
KUBE_CONTEXT="${DEMO_KUBE_CONTEXT}"
IMAGE_NAME="$(demo::image_ref)"

# Split "repo:tag" for the chart's two separate values. `exit` inside $() only
# leaves the subshell, so propagate the code rather than trusting set -e.
IMAGE_PARTS="$(demo::image_parts "${IMAGE_NAME}")" || exit $?
IMAGE_REPO="${IMAGE_PARTS%|*}"
IMAGE_TAG="${IMAGE_PARTS#*|}"

if [ "${BUILD_LOCAL}" = "true" ]; then
  info "Building image: ${IMAGE_NAME}"
  docker build -t "${IMAGE_NAME}" \
    -f "${REPO_ROOT}/deployments/nvml-mock/Dockerfile" "${REPO_ROOT}"

  info "Loading image into Kind"
  kind load docker-image "${IMAGE_NAME}" --name "${CLUSTER_NAME}"
else
  info "Using published image: ${IMAGE_NAME} (set BUILD_LOCAL=true to build from source)"
fi

###############################################################################
# Step 2 -- Install an unpartitioned board
#
# No gpu.mig.* values at all: MIG arrives through nvidia-smi below, so the
# install is the plain one any other demo would do. Partitioning at install
# time is the guide's Step 1 and takes gpu.mig.enabled with an explicit
# gpu.mig.gpuInstances layout.
###############################################################################
info "Step 2: install ${GPU_PROFILE} with MIG off"
# Step 1's guard cannot speak for this release, so say plainly when an install
# is really an upgrade of one the reader already had: it moves that release onto
# GPU_PROFILE and this demo's image, and the steps below reset its runtime
# override.
if helm status "${RELEASE_NAME}" --kube-context "${KUBE_CONTEXT}" \
  --namespace "${NAMESPACE}" >/dev/null 2>&1; then
  sub "${RELEASE_NAME} already exists in ${NAMESPACE}; upgrading it to ${GPU_PROFILE}"
fi
demo::announce_pull "${IMAGE_NAME}"
helm upgrade --install "${RELEASE_NAME}" "${REPO_ROOT}/${CHART_PATH}" \
  --kube-context "${KUBE_CONTEXT}" \
  --namespace "${NAMESPACE}" --create-namespace \
  --set-string "image.repository=${IMAGE_REPO}" \
  --set-string "image.tag=${IMAGE_TAG}" \
  --set "gpu.profile=${GPU_PROFILE}" \
  --wait --timeout "$(demo::install_timeout)" >/dev/null

POD=$(wait_for_pod)
sub "DaemonSet pod ready: ${POD}"

# Start from the layout the chart installed. Every step below is a runtime
# mutation recorded in the override document, which outlives the pod, so a
# previous run — or one that stopped partway — would otherwise leave this run
# asserting a baseline that is no longer there.
kubectl_ctx exec "${POD}" -- nvml-mock-ctl reset >/dev/null
sub "cleared any runtime override left by an earlier run"

TOTAL_GPUS=$(smi -L | grep -c '^GPU ' || true)
if [ "${TOTAL_GPUS}" -lt 1 ]; then
  fail "nvidia-smi -L reported no GPUs after install"
fi
ok "nvidia-smi -L lists ${TOTAL_GPUS} whole ${GPU_PROFILE} GPU(s)"

if [ "$(mig_mode_of_gpu)" != "Disabled" ]; then
  fail "GPU ${GPU_INDEX} should start with MIG disabled, got '$(mig_mode_of_gpu)'"
fi
ok "GPU ${GPU_INDEX} starts with MIG disabled"

# The capability gate. Every assertion below depends on the MIG instance
# lifecycle, so an image that predates it has to be reported here rather than
# surfacing as a puzzling failure further down.
if ! kubectl_ctx exec "${POD}" -- nvidia-smi -i "${GPU_INDEX}" -mig 1 >/dev/null 2>&1; then
  fail "this image cannot enable MIG on a ${GPU_PROFILE} board: ${IMAGE_NAME}

The MIG instance lifecycle may not have reached a published release yet.
Build the image from source and side-load it into a Kind cluster of its own:

  BUILD_LOCAL=true $0"
fi

###############################################################################
# Step 3 -- Enable MIG, and read it back from another process
###############################################################################
info "Step 3: MIG mode is driver state, not process state"
sub "nvidia-smi -i ${GPU_INDEX} -mig 1   (already run by the capability check)"

# A SEPARATE nvidia-smi process, which is the assertion: the engine that set
# the mode has already exited, so a mode still visible here can only have come
# from the runtime override document.
if [ "$(mig_mode_of_gpu)" != "Enabled" ]; then
  fail "GPU ${GPU_INDEX} reports MIG '$(mig_mode_of_gpu)' from a second process; the mode was not recorded"
fi
ok "a second nvidia-smi sees MIG enabled on GPU ${GPU_INDEX}"

if [ "$(gpu_instance_count)" -ne 0 ]; then
  fail "enabling MIG should partition nothing on its own, found $(gpu_instance_count) instance(s)"
fi
ok "the board is MIG-enabled and carries no partitions yet"

###############################################################################
# Step 4 -- What the board offers
#
# Take slice names from this listing rather than from the MIG user guide. The
# mock names a slice for the share of its own board's memory it holds, so the
# names track the profile's declared memory and not NVIDIA's published listing
# for the hardware the profile stands in for.
###############################################################################
info "Step 4: nvidia-smi mig -lgip, the profiles this board offers"
LGIP="$(smi mig -lgip)"
printf '%s\n' "${LGIP}" | grep -E '^\|[[:space:]]+[0-9]+[[:space:]]+MIG' | sed 's/^/    /' >&2

if ! printf '%s\n' "${LGIP}" | grep -qF "MIG ${MIG_PROFILE}"; then
  fail "GPU ${GPU_INDEX} offers no ${MIG_PROFILE} profile:
${LGIP}"
fi
ok "the board offers ${MIG_PROFILE}"

# Free can never exceed Total: Free is how many more of a profile fit, Total is
# how many the profile declares. A +me variant occupies one slice but exists
# once per board, so counting free slices alone reported "7/1" here.
BAD_RATIO="$(printf '%s\n' "${LGIP}" | awk '
  match($0, /[0-9]+\/[0-9]+/) {
    ratio = substr($0, RSTART, RLENGTH)
    split(ratio, part, "/")
    if (part[1] > part[2]) print ratio
  }')"
if [ -n "${BAD_RATIO}" ]; then
  fail "mig -lgip reports more free instances than exist in total: ${BAD_RATIO}
No board can report this. Full listing:
${LGIP}"
fi
ok "every profile reports Free no greater than Total"

###############################################################################
# Step 5 -- Create one partition
###############################################################################
info "Step 5: create a ${MIG_PROFILE} partition"
BASE_CIS="$(compute_instance_count)"

# -C is what makes the partition usable. MIG devices are derived from compute
# instances, so a GPU instance created without it is invisible to nvidia-smi -L
# and to anything that allocates. Step 6 shows that difference directly.
sub "nvidia-smi mig -cgi ${MIG_PROFILE} -C -i ${GPU_INDEX}"
smi mig -cgi "${MIG_PROFILE}" -C -i "${GPU_INDEX}" | sed 's/^/    /' >&2

if [ "$(gpu_instance_count)" -ne 1 ]; then
  fail "mig -lgi should report one GPU instance, got $(gpu_instance_count)"
fi
ok "mig -lgi reports the new GPU instance"

if [ "$(compute_instance_count)" -ne $((BASE_CIS + 1)) ]; then
  fail "-C should have created one compute instance, count went from ${BASE_CIS} to $(compute_instance_count)"
fi
ok "mig -lci reports its compute instance"

if [ "$(mig_device_count)" -ne 1 ]; then
  fail "nvidia-smi -L should show one MIG device on GPU ${GPU_INDEX}, got $(mig_device_count)"
fi
ok "nvidia-smi -L shows the partition with its own MIG-… UUID"
smi -L | grep -E "^([[:space:]]+MIG |GPU ${GPU_INDEX}:)" | head -2 | sed 's/^/    /' >&2

# The instance id NVML chose. Read it rather than assuming 0: the ids are the
# allocator's to pick, and a rebuild draws fresh ones.
#
# The id is the column before the "Start:Size" placement, found by shape rather
# than by number: the name column ahead of it is two fields today but nothing
# guarantees that, and taking a fixed offset from the end lands on the
# placement itself.
GI_ID="$(smi mig -lgi | awk -v gpu="${GPU_INDEX}" '
  $2 == gpu {
    for (i = 3; i <= NF; i++) {
      if ($i ~ /^[0-9]+:[0-9]+$/) { print $(i - 1); exit }
    }
  }' | tr -d ' ')"
if ! printf '%s' "${GI_ID}" | grep -qE '^[0-9]+$'; then
  fail "could not read the new GPU instance's id from mig -lgi:
$(smi mig -lgi)"
fi
sub "NVML assigned GPU instance id ${GI_ID}"

# Occupancy has to come from the instances that exist, not from what the board
# could hold. One taken means one fewer free.
if ! smi mig -lgip | grep -E "MIG ${MIG_PROFILE}[[:space:]]" | grep -qE '[0-9]+/[0-9]+'; then
  fail "mig -lgip no longer reports a ${MIG_PROFILE} row"
fi
ok "mig -lgip now shows one ${MIG_PROFILE} taken"

###############################################################################
# Step 6 -- The refusal
#
# NVML will not destroy a GPU instance that still holds a compute instance, so
# a partitioning tool has to tear the tree down leaf-first. The mock enforces
# it for the same reason it exists at all: a tool with the order wrong should
# fail here, not on the hardware this stands in for.
###############################################################################
info "Step 6: -dgi is refused while the partition is in use"
sub "nvidia-smi mig -dgi -gi ${GI_ID} -i ${GPU_INDEX}"
REFUSAL="$(smi_expect_failure mig -dgi -gi "${GI_ID}" -i "${GPU_INDEX}")"
printf '%s\n' "${REFUSAL}" | sed 's/^/    /' >&2

if ! printf '%s\n' "${REFUSAL}" | grep -qiE 'in use'; then
  fail "the refusal should report the instance as in use, got:
${REFUSAL}"
fi
ok "NVML refused it: In use by another client"

# A refusal that had torn half the tree down would be worse than either
# outcome on its own, because a caller retrying in the right order would find
# the state already gone.
if [ "$(gpu_instance_count)" -ne 1 ]; then
  fail "a refused destroy must leave the GPU instance standing, got $(gpu_instance_count)"
fi
if [ "$(compute_instance_count)" -ne $((BASE_CIS + 1)) ]; then
  fail "a refused destroy must leave the compute instance standing"
fi
if [ "$(mig_device_count)" -ne 1 ]; then
  fail "a refused destroy must leave the derived MIG device standing"
fi
ok "the partition, its compute instance and its MIG device all survived"

###############################################################################
# Step 7 -- Delete it, in the order that works
###############################################################################
info "Step 7: delete the partition, compute instance first"
sub "nvidia-smi mig -dci -gi ${GI_ID} -ci 0"
smi mig -dci -gi "${GI_ID}" -ci 0 | sed 's/^/    /' >&2
sub "nvidia-smi mig -dgi -gi ${GI_ID} -i ${GPU_INDEX}"
smi mig -dgi -gi "${GI_ID}" -i "${GPU_INDEX}" | sed 's/^/    /' >&2

if [ "$(gpu_instance_count)" -ne 0 ]; then
  fail "GPU ${GPU_INDEX} should carry no instances after -dgi, got $(gpu_instance_count)"
fi
if [ "$(compute_instance_count)" -ne "${BASE_CIS}" ]; then
  fail "the compute instance count should be back to ${BASE_CIS}, got $(compute_instance_count)"
fi
ok "the partition is gone, and so is its compute instance"

###############################################################################
# Step 8 -- Turn MIG off again
###############################################################################
info "Step 8: nvidia-smi -i ${GPU_INDEX} -mig 0"
smi -i "${GPU_INDEX}" -mig 0 | sed 's/^/    /' >&2

if [ "$(mig_mode_of_gpu)" != "Disabled" ]; then
  fail "GPU ${GPU_INDEX} still reports MIG '$(mig_mode_of_gpu)'"
fi
ok "GPU ${GPU_INDEX} is back to MIG disabled"

if [ "$(smi -L | grep -c '^GPU ' || true)" -ne "${TOTAL_GPUS}" ]; then
  fail "the board should be back to ${TOTAL_GPUS} whole GPU(s)"
fi
ok "the board reports ${TOTAL_GPUS} whole GPU(s) again"

# Clear the runtime override so the node is left reporting the layout the
# chart installed, rather than the empty one this demo finished on.
info "Clearing the runtime override"
kubectl_ctx exec "${POD}" -- nvml-mock-ctl reset | sed 's/^/    /' >&2

###############################################################################
# Summary
###############################################################################
cat >&2 <<SUMMARY

==> Done. On GPU ${GPU_INDEX} of ${POD}:

      enabled MIG, created a ${MIG_PROFILE} partition, listed it three ways,
      watched NVML refuse an out-of-order delete, deleted it properly, and
      turned MIG back off — every step a separate nvidia-smi process.

    A runtime layout moves the NVML view only. What the node can ALLOCATE is
    the layout it booted with, so to schedule pods onto slices install the
    chart partitioned instead. See "Limits" and Step 1 in README.md.

    Inspect it yourself:
      kubectl --context ${KUBE_CONTEXT} -n ${NAMESPACE} exec ${POD} -- nvidia-smi mig -lgip

    Remove it. This is the standard ${RELEASE_NAME} release, not a
    demo-specific one, so on a cluster you share this takes the mock away from
    everything else on these nodes too:
      helm uninstall ${RELEASE_NAME} -n ${NAMESPACE} --kube-context ${KUBE_CONTEXT}
SUMMARY

if [ "${BUILD_LOCAL}" = "true" ]; then
  cat >&2 <<LOCAL

    Delete the cluster this demo created:
      kind delete cluster --name ${CLUSTER_NAME}
LOCAL
  # Only when it actually moved you. On a re-run you are already on the demo's
  # own context, and printing a no-op instruction there reads as though
  # something needs undoing.
  if [ -n "${PRIOR_CONTEXT}" ] && [ "${PRIOR_CONTEXT}" != "kind-${CLUSTER_NAME}" ]; then
    printf '\n    Return to the context you started on:\n      kubectl config use-context %s\n' \
      "${PRIOR_CONTEXT}" >&2
  fi
fi
