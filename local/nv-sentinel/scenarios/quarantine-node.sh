#!/usr/bin/env bash
# Copyright 2026 NVIDIA CORPORATION
# SPDX-License-Identifier: Apache-2.0
#
# Scenario: heat one mock GPU past its hardware slowdown limit and assert that
# NVSentinel quarantines the node — cordon, then drain.
#
# What is being asserted: nvml-mock reports a signed T.Limit margin (DCGM field
# 153) that goes negative past the slowdown limit, the metadata-collector has
# published that GPU's slowdown offset (NVML field 194), so
# GpuThermalMarginWatch trips, fault-quarantine cordons the node and
# node-drainer evicts the GPU workload onto the healthy worker.
#
# The fault goes in through nvml-mock-ctl rather than `helm upgrade`: the CLI
# writes a node-local override the running mock re-reads on its TTL, so DCGM
# keeps serving the same device and only the reading changes.
#
# Ported from docs/demo/nv-sentinel/run.sh phase 1.

set -euo pipefail

# nvml-mock's namespace (_NAMESPACE in local/nvml_mock.tiltfile) and
# NVSentinel's (_NAMESPACE in local/nv-sentinel/nv_sentinel.tiltfile).
MOKKA_NAMESPACE="mokka"
NVSENTINEL_NAMESPACE="nvsentinel"
# The workload from local/nv-sentinel/gpu-workload.k8s.yaml.
WORKLOAD_NAMESPACE="default"
WORKLOAD_SELECTOR="app=gpu-sample-workload"

TARGET_GPU="${TARGET_GPU:-0}"
# Each phase waits on a full detection round trip: the mock's override TTL, the
# DCGM poll, the health monitor's own interval, then the event's trip through
# MongoDB to fault-quarantine.
POLL_ATTEMPTS="${POLL_ATTEMPTS:-60}"
POLL_INTERVAL_S="${POLL_INTERVAL_S:-5}"
POLL_BUDGET_S=$((POLL_ATTEMPTS * POLL_INTERVAL_S))

fail() { printf 'FAIL: %s\n' "$*" >&2; exit 1; }

# Print what NVSentinel thought, for every failure path below. None of these are
# assertions, so a missing log or an absent condition must not abort the script.
# Takes the nodes worth inspecting as arguments: before the target is chosen
# that is every GPU node, since the interesting one is whichever is cordoned.
diagnose() {
  printf '\n--- GPU node conditions ---\n'
  for node in "$@"; do
    kubectl get node "${node}" -o json 2>/dev/null \
      | jq -r --arg node "${node}" '.status.conditions[] | select(.type | test("Gpu")) | "\($node) \(.type)=\(.status): \(.message)"' || true
  done
  printf '\n--- NVSentinel log lines mentioning the thermal path ---\n'
  kubectl -n "${NVSENTINEL_NAMESPACE}" logs -l app.kubernetes.io/instance=nvsentinel \
    --prefix --tail=300 2>/dev/null \
    | grep -iE 'cordon|quarantin|thermal|slowdown|tlimit|margin' | tail -25 || true
  printf '\n--- nodes ---\n'
  kubectl get nodes || true
}

# --- candidate nodes --------------------------------------------------------
# Only nodes that actually advertise nvidia.com/gpu are candidates. The mock now
# runs on labelled workers only, so this filter and the node set coincide on the
# shared cluster; it stays because the advertised resource is what the workload
# schedules on, and a node with a mock driver but no GPU Operator operand has no
# DCGM to detect anything with.
gpu_nodes=$(kubectl get nodes -o go-template='{{range .items}}{{if index .status.allocatable "nvidia.com/gpu"}}{{.metadata.name}}{{"\n"}}{{end}}{{end}}')
[[ -n "${gpu_nodes}" ]] \
  || fail "no node advertises nvidia.com/gpu; the GPU Operator's device plugin has not registered the mock GPUs yet"

mock_pod_on() {
  kubectl -n "${MOKKA_NAMESPACE}" get pods -l app.kubernetes.io/name=nvml-mock \
    --field-selector "spec.nodeName=$1,status.phase=Running" \
    -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true
}

# One place where the exec form lives: both the fleet-wide reset and this run's
# injection go through it.
mock_ctl_on() {
  local pod="$1"
  shift
  kubectl -n "${MOKKA_NAMESPACE}" exec "${pod}" -- nvml-mock-ctl "$@"
}

# Every GPU node paired with the mock pod that can inject into it. The whole set
# is needed, not only the target: the cleanup below has to reach a pin this
# scenario left on ANOTHER node on a previous run, or that node stays cordoned
# and the drain has nowhere to send the workload.
mock_pods=()
for host in ${gpu_nodes}; do
  pod=$(mock_pod_on "${host}")
  if [[ -n "${pod}" ]]; then
    mock_pods+=("${host}=${pod}")
  fi
done
[[ "${#mock_pods[@]}" -gt 0 ]] \
  || fail "no node has both a running nvml-mock pod and an advertised nvidia.com/gpu"

# A one-worker fleet cannot show a drain: there is nowhere for the evicted
# workload to go, so the reschedule assertion below would fail on a healthy
# cluster.
gpu_node_count=$(grep -c . <<<"${gpu_nodes}")
[[ "${gpu_node_count}" -ge 2 ]] \
  || fail "only ${gpu_node_count} GPU node(s); the drain has no healthy worker to reschedule onto. Recreate the cluster with \`make cluster-create\` (1 control-plane + 2 workers)."

# Name and node of every live Running workload pod, one per line. Read as a pair
# in a single call so the name can never be matched against another pod's node.
#
# Pods with a deletionTimestamp are excluded: an evicted pod keeps
# status.phase=Running until it is actually gone, so a drain still in flight
# would otherwise offer this scenario a doomed pod to target — and its
# replacement, already Running elsewhere, would then satisfy phase 2 without
# this run having evicted anything.
workload_state() {
  kubectl -n "${WORKLOAD_NAMESPACE}" get pods -l "${WORKLOAD_SELECTOR}" \
    --field-selector=status.phase=Running \
    -o go-template='{{range .items}}{{if not .metadata.deletionTimestamp}}{{.metadata.name}} {{.spec.nodeName}}{{"\n"}}{{end}}{{end}}' 2>/dev/null || true
}

# --- start from a genuinely healthy fleet ------------------------------------
# Two assertions below depend on the pre-injection state, and both are defeated
# by a pin left behind: on the target, "NVSentinel cordoned the node" is already
# true before any fault goes in; on the sibling, the drain has no schedulable
# GPU node to move the workload to. Since the target follows the workload it
# alternates between runs, so the previous run's target is typically the sibling
# this run needs healthy — clear every GPU of every mock node, not just this
# run's target.
printf '==> clearing overrides on every GPU of every mock node\n'
for pair in "${mock_pods[@]}"; do
  printf '    %s: ' "${pair%%=*}"
  mock_ctl_on "${pair#*=}" reset --gpu all
done

printf '==> waiting for every GPU node to be schedulable before injecting anything\n'
cordoned_gpu_nodes() {
  kubectl get nodes -o go-template='{{range .items}}{{if index .status.allocatable "nvidia.com/gpu"}}{{if .spec.unschedulable}}{{.metadata.name}}{{"\n"}}{{end}}{{end}}{{end}}'
}
still_cordoned=""
for _ in $(seq 1 "${POLL_ATTEMPTS}"); do
  still_cordoned=$(cordoned_gpu_nodes)
  if [[ -z "${still_cordoned}" ]]; then
    break
  fi
  sleep "${POLL_INTERVAL_S}"
done
if [[ -n "${still_cordoned}" ]]; then
  diagnose ${gpu_nodes}
  fail "GPU node(s) $(tr '\n' ' ' <<<"${still_cordoned}") still cordoned ~${POLL_BUDGET_S}s after clearing every GPU override. If they were cordoned by hand, \`kubectl uncordon\` them; otherwise a health check other than the thermal margin is still failing and this scenario can neither attribute a cordon to its own fault nor drain onto a healthy node."
fi

# --- target selection: the node the workload is on RIGHT NOW -----------------
# Deliberately read here rather than before the reset above. The workload can
# move while the fleet uncordons — a previous interrupted run leaves it Pending
# behind a cordon, or on the very node this run was about to target — and the
# drain assertion is only meaningful if the pod it watches was demonstrably
# Running on the target when the fault went in. Targeting the workload's node
# also makes the drain observable rather than merely logged.
#
# A bounded poll, not a single read: a workload that was Pending behind a cordon
# needs a moment to be scheduled once the fleet is schedulable again.
printf '==> waiting for the GPU workload to be Running where a fault can be injected\n'
workload_pod=""
target_node=""
target_pod=""
workload_count=0
for _ in $(seq 1 "${POLL_ATTEMPTS}"); do
  workload_pods=$(workload_state)
  workload_count=$(grep -c . <<<"${workload_pods}" || true)
  # Exactly one pod, not the first of however many. The selection below takes
  # one line and phase 2 then passes as soon as a workload pod with a different
  # name is Running elsewhere — which a second replica already satisfies before
  # node-drainer evicts anything, turning the drain assertion back into "the
  # workload is somewhere else". The workload ships replicas: 1
  # (gpu-workload.k8s.yaml), so this asserts the invariant the code already
  # depends on rather than adding a new constraint.
  #
  # Polled rather than failed on the spot: a rollout may briefly surge to two
  # Running pods, and that resolves on its own.
  if [[ "${workload_count}" == "1" ]]; then
    read -r workload_pod target_node <<<"${workload_pods}"
    if [[ -n "${target_node}" ]]; then
      target_pod=$(mock_pod_on "${target_node}")
      if [[ -n "${target_pod}" ]]; then
        break
      fi
    fi
  fi
  sleep "${POLL_INTERVAL_S}"
done
if [[ -z "${target_pod}" ]]; then
  diagnose ${gpu_nodes}
  if [[ "${workload_count}" -gt 1 ]]; then
    fail "${workload_count} pods match ${WORKLOAD_SELECTOR} in ${WORKLOAD_NAMESPACE} after ~${POLL_BUDGET_S}s, and this scenario needs exactly one: with a sibling replica already Running on the healthy worker, the eviction assertion in phase 2 is satisfied by that sibling and would pass without node-drainer evicting anything. Scale gpu-sample-workload back to the replicas: 1 that local/nv-sentinel/gpu-workload.k8s.yaml declares."
  elif [[ -n "${target_node}" ]]; then
    fail "the GPU workload (${workload_pod}) is Running on ${target_node}, but no nvml-mock pod is Running there after ~${POLL_BUDGET_S}s, so the fault cannot be injected on the node whose drain this scenario asserts. Inspect \`kubectl -n ${MOKKA_NAMESPACE} get pods -l app.kubernetes.io/name=nvml-mock -o wide\`."
  else
    fail "no Running pod matches ${WORKLOAD_SELECTOR} in ${WORKLOAD_NAMESPACE} after ~${POLL_BUDGET_S}s. Without one there is nothing for node-drainer to evict, so a drain could not be observed even if NVSentinel performed it. Inspect \`kubectl -n ${WORKLOAD_NAMESPACE} get pods -l ${WORKLOAD_SELECTOR} -o wide\`."
  fi
fi

printf '==> target: gpu %s on %s (mock pod %s, workload pod %s), %s GPU nodes total\n' \
  "${TARGET_GPU}" "${target_node}" "${target_pod}" "${workload_pod}" "${gpu_node_count}"

mock_ctl() { mock_ctl_on "${target_pod}" "$@"; }

# --- pick a temperature the active profile will actually slow down at --------
# Read the thresholds out of the profile the mock loaded rather than hardcoding
# them: they differ per --gpu-profile (slowdown 87C on a100/h100, 90C on
# b200/gb200/gb300, 93C on t4/l40s) and shutdown is only 3-5C above. A
# hardcoded 90 silently fails to trip anything on an l40s worker, and anything
# at or above shutdown is clamped by the mock, so the margin never lands where
# this scenario expects.
#
# /etc/nvml-mock/config.yaml is the chart's rendered profile, mounted read-only
# into the pod (MOCK_NVML_CONFIG in the DaemonSet). Each key appears once, under
# device_defaults.thermal.
threshold() {
  kubectl -n "${MOKKA_NAMESPACE}" exec "${target_pod}" -- \
    grep -m1 -E "^[[:space:]]*$1:" /etc/nvml-mock/config.yaml \
    | sed -E 's/.*:[[:space:]]*([0-9]+).*/\1/'
}
# `|| true` so a missing key, a missing config file or a failed exec reaches the
# guard below instead of aborting the script on the assignment under `pipefail`.
# kubectl's own stderr is left alone for the same reason: it is the only thing
# that explains WHICH of those happened.
slowdown_c=$(threshold slowdown_threshold_c || true)
shutdown_c=$(threshold shutdown_threshold_c || true)
[[ "${slowdown_c}" =~ ^[0-9]+$ && "${shutdown_c}" =~ ^[0-9]+$ ]] \
  || fail "could not read the thermal thresholds from /etc/nvml-mock/config.yaml on ${target_pod} (got slowdown='${slowdown_c}' shutdown='${shutdown_c}'); any kubectl error above says why"

# A few degrees past slowdown is enough for a negative margin, and staying
# below shutdown keeps the mock from clamping the reading.
default_hot=$((slowdown_c + 3))
[[ "${default_hot}" -lt "${shutdown_c}" ]] || default_hot=$((shutdown_c - 1))
HOT_TEMP_C="${HOT_TEMP_C:-${default_hot}}"

[[ "${HOT_TEMP_C}" =~ ^[0-9]+$ ]] || fail "HOT_TEMP_C='${HOT_TEMP_C}' is not an integer"
[[ "${HOT_TEMP_C}" -gt "${slowdown_c}" ]] \
  || fail "HOT_TEMP_C=${HOT_TEMP_C} is at or below this profile's slowdown threshold (${slowdown_c}C), so the T.Limit margin stays positive and NVSentinel has nothing to detect"
[[ "${HOT_TEMP_C}" -lt "${shutdown_c}" ]] \
  || fail "HOT_TEMP_C=${HOT_TEMP_C} is at or above this profile's shutdown threshold (${shutdown_c}C); the mock clamps there, so the reading would not be the one asserted on"

printf '    profile thresholds: slowdown %sC, shutdown %sC -> heating to %sC\n' \
  "${slowdown_c}" "${shutdown_c}" "${HOT_TEMP_C}"

# Every failure after the injection leaves the GPU pinned hot, and a pin left
# behind reads later as an unexplained cordon on a cluster nobody touched.
pinned_hot_hint() {
  printf 'gpu %s on %s is LEFT PINNED at %sC by this run: clear it with `kubectl -n %s exec %s -- nvml-mock-ctl reset --gpu %s` (the next run of this scenario also clears it), or the node stays quarantined for reasons a later reader cannot see.' \
    "${TARGET_GPU}" "${target_node}" "${HOT_TEMP_C}" \
    "${MOKKA_NAMESPACE}" "${target_pod}" "${TARGET_GPU}"
}

# --- phase 1: heat, and expect a cordon -------------------------------------
printf '==> heating gpu %s on %s to %sC\n' "${TARGET_GPU}" "${target_node}" "${HOT_TEMP_C}"
mock_ctl temp --gpu "${TARGET_GPU}" "${HOT_TEMP_C}"

printf '==> waiting for NVSentinel to cordon %s\n' "${target_node}"
cordoned=false
for _ in $(seq 1 "${POLL_ATTEMPTS}"); do
  if [[ "$(kubectl get node "${target_node}" -o jsonpath='{.spec.unschedulable}' 2>/dev/null)" == "true" ]]; then
    cordoned=true
    break
  fi
  sleep "${POLL_INTERVAL_S}"
done
if [[ "${cordoned}" != "true" ]]; then
  diagnose "${target_node}"
  fail "${target_node} was not cordoned within ~${POLL_BUDGET_S}s of gpu ${TARGET_GPU} reaching ${HOT_TEMP_C}C. A 'missing slowdown TLIMIT threshold metadata' line above means the metadata-collector never published the offset, so GpuThermalMarginWatch never armed; no thermal line at all means DCGM is not serving field 153 to the health monitor. $(pinned_hot_hint)"
fi
printf 'OK: %s is cordoned by NVSentinel\n' "${target_node}"

# --- phase 2: expect the drain to evict the workload and replace it ---------
# Two conditions, not one. "A workload pod is Running somewhere other than the
# target" is true of a pod that was never on the target and so proves nothing;
# requiring the NAME to change as well asserts what node-drainer actually does —
# ${workload_pod}, which was Running on the target when the fault went in, is
# gone, and its replacement runs elsewhere.
printf '==> waiting for %s to be evicted off %s and replaced on another node\n' \
  "${workload_pod}" "${target_node}"
rescheduled=false
new_pod=""
new_node=""
for _ in $(seq 1 "${POLL_ATTEMPTS}"); do
  while read -r pod_name pod_node; do
    if [[ -n "${pod_name}" && "${pod_name}" != "${workload_pod}" \
          && -n "${pod_node}" && "${pod_node}" != "${target_node}" ]]; then
      rescheduled=true
      new_pod="${pod_name}"
      new_node="${pod_node}"
    fi
  done <<<"$(workload_state)"
  if [[ "${rescheduled}" == "true" ]]; then
    break
  fi
  sleep "${POLL_INTERVAL_S}"
done
if [[ "${rescheduled}" != "true" ]]; then
  diagnose "${target_node}"
  fail "${workload_pod} was not evicted off ${target_node} and replaced on another node within ~${POLL_BUDGET_S}s. node-drainer evicts user-namespace pods only in Immediate mode (node-drainer.userNamespaces in nvsentinel.values.yaml); in the chart's default AllowCompletion mode this pod never completes and so is never evicted. $(pinned_hot_hint)"
fi
printf 'OK: %s was evicted off %s and replaced by %s on %s\n' \
  "${workload_pod}" "${target_node}" "${new_pod}" "${new_node}"

kubectl get nodes || true
printf '\n==> quarantine-node passed: gpu %s on %s crossed its slowdown limit (%sC > %sC), NVSentinel cordoned the node and evicted the GPU workload onto %s.\n' \
  "${TARGET_GPU}" "${target_node}" "${HOT_TEMP_C}" "${slowdown_c}" "${new_node}"
printf '    Run recover-node to cool the GPU and watch the node uncordon itself.\n'
