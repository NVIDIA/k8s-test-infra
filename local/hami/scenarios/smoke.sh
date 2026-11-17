#!/usr/bin/env bash
# Copyright 2026 NVIDIA CORPORATION
# SPDX-License-Identifier: Apache-2.0
#
# Schedules the pods in local/hami/gpu-share.k8s.yaml afresh, leaves them
# running for inspection, and asserts three things, each of which can fail
# while both pods read Running:
#   1. Both pods hold a slice of the same GPU, sized as they asked. Under the
#      chart's default spread policy each pod gets a board of its own, which
#      schedules fine and shares nothing.
#   2. nvidia-smi inside each pod sees exactly the GPU HAMi allocated. More
#      means the allocation did not scope the mock driver; none means the mock
#      driver never reached the container.
#   3. That GPU's total memory is the pod's nvidia.com/gpumem limit, not the
#      board's. The cap is applied by HAMi-core, a library HAMi preloads into
#      the container to intercept NVML, so this is what proves it works on top
#      of the mock's libnvidia-ml.

set -euo pipefail

NAMESPACE="${NAMESPACE:-default}"
MANIFEST="${MANIFEST:-local/hami/gpu-share.k8s.yaml}"
# The namespace of the release in local/hami/hami.tiltfile.
HAMI_NAMESPACE="kube-system"
TIMEOUT_S="${TIMEOUT_S:-120}"

# <pod> <nvidia.com/gpumem in MiB> <nvidia.com/gpucores>: the limits in MANIFEST.
EXPECTED=(
  'hami-share-a 10240 0'
  'hami-share-b 20480 30'
)

fail() { printf 'FAIL: %s\n' "$*" >&2; exit 1; }

annotation() {
  kubectl -n "${NAMESPACE}" get pod "$1" -o json | jq -r --arg key "$2" '.metadata.annotations[$key] // ""'
}

# A pod created before the webhook serves is not routed to hami-scheduler, and
# the default scheduler can never place it: no node advertises nvidia.com/gpumem.
printf '==> waiting for HAMi\n'
kubectl -n "${HAMI_NAMESPACE}" rollout status deployment/hami-scheduler --timeout="${TIMEOUT_S}s" >/dev/null \
  || fail "hami-scheduler is not ready in ${HAMI_NAMESPACE}; was Tilt started with --hami?"
kubectl -n "${HAMI_NAMESPACE}" rollout status daemonset/hami-device-plugin --timeout="${TIMEOUT_S}s" >/dev/null \
  || fail "hami-device-plugin is not ready; kubectl -n ${HAMI_NAMESPACE} logs ds/hami-device-plugin -c device-plugin"

registered=$(kubectl get nodes -o json | jq -r '
  .items[]
  | select(.metadata.annotations["hami.io/node-nvidia-register"] != null)
  | "\(.metadata.name) \(.metadata.annotations["hami.io/node-nvidia-register"] | fromjson | length)"')
[[ -n "${registered}" ]] || fail "no node carries hami.io/node-nvidia-register; the device plugin found no GPUs"
while read -r node gpus; do
  printf 'OK: %s registered %s GPUs with HAMi\n' "${node}" "${gpus}"
done <<<"${registered}"

printf '==> scheduling %s pods from %s\n' "${#EXPECTED[@]}" "${MANIFEST}"
kubectl -n "${NAMESPACE}" delete -f "${MANIFEST}" --ignore-not-found --wait >/dev/null
kubectl -n "${NAMESPACE}" apply -f "${MANIFEST}" >/dev/null
pods=()
for entry in "${EXPECTED[@]}"; do
  pods+=("pod/${entry%% *}")
done
kubectl -n "${NAMESPACE}" wait "${pods[@]}" --for=condition=Ready --timeout="${TIMEOUT_S}s" >/dev/null \
  || fail "the pods did not become Ready: $(kubectl -n "${NAMESPACE}" get "${pods[@]}" \
       -o custom-columns='POD:.metadata.name,PHASE:.status.phase,SCHEDULER:.spec.schedulerName' --no-headers | paste -sd';' -)"

shared_gpu=""
for entry in "${EXPECTED[@]}"; do
  read -r pod want_mem want_cores <<<"${entry}"

  # One container holding one device: <uuid>,<vendor>,<MiB>,<cores>:;
  allocated=$(annotation "${pod}" 'hami.io/vgpu-devices-allocated')
  IFS=, read -r gpu _ mem cores <<<"${allocated%%:*}"
  [[ "${mem}" == "${want_mem}" && "${cores}" == "${want_cores}" ]] \
    || fail "${pod} asked for ${want_mem} MiB and ${want_cores} cores but HAMi allocated '${allocated}'"
  shared_gpu="${shared_gpu:-${gpu}}"
  [[ "${gpu}" == "${shared_gpu}" ]] \
    || fail "${pod} is on ${gpu}, not on ${shared_gpu} with the pod before it; is gpuSchedulerPolicy still binpack?"
  printf 'OK: %s holds %s MiB and %s cores of %s\n' "${pod}" "${mem}" "${cores}" "${gpu}"

  seen=$(kubectl -n "${NAMESPACE}" exec "${pod}" -- \
    nvidia-smi --query-gpu=uuid,memory.total --format=csv,noheader,nounits 2>/dev/null | grep '^GPU-') \
    || fail "nvidia-smi listed no GPU inside ${pod}; the mock driver was not injected (is the nvml-mock NRI plugin running?)"
  [[ "$(wc -l <<<"${seen}" | tr -d ' ')" -eq 1 && "${seen%%,*}" == "${gpu}" ]] \
    || fail "${pod} was allocated ${gpu} but nvidia-smi lists:"$'\n'"${seen}"
  [[ "${seen##*, }" == "${want_mem}" ]] \
    || fail "${pod} is capped at ${want_mem} MiB but nvidia-smi reports ${seen##*, } MiB; HAMi-core is not intercepting NVML"
  printf 'OK: %s sees only that GPU, with %s MiB\n' "${pod}" "${seen##*, }"
done

node=$(annotation "${EXPECTED[0]%% *}" 'hami.io/vgpu-node')
printf '\n==> hami-smoke passed: HAMi packed %s pods onto mock GPU %s on %s, each capped to its own memory.\n' \
  "${#EXPECTED[@]}" "${shared_gpu}" "${node}"
