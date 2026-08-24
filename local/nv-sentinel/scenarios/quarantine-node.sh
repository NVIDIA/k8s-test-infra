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
diagnose() {
  printf '\n--- GPU node conditions ---\n'
  kubectl get node "${target_node}" -o json 2>/dev/null \
    | jq -r '.status.conditions[] | select(.type | test("Gpu")) | "\(.type)=\(.status): \(.message)"' || true
  printf '\n--- NVSentinel log lines mentioning the thermal path ---\n'
  kubectl -n "${NVSENTINEL_NAMESPACE}" logs -l app.kubernetes.io/instance=nvsentinel \
    --prefix --tail=300 2>/dev/null \
    | grep -iE 'cordon|quarantin|thermal|slowdown|tlimit|margin' | tail -25 || true
  printf '\n--- nodes ---\n'
  kubectl get nodes || true
}

# --- target selection -------------------------------------------------------
# Only nodes that actually advertise nvidia.com/gpu are candidates: the shared
# cluster also runs an nvml-mock pod on the control-plane, but no GPU Operator
# operand lands there, so it has no DCGM to detect anything with.
gpu_nodes=$(kubectl get nodes -o go-template='{{range .items}}{{if index .status.allocatable "nvidia.com/gpu"}}{{.metadata.name}}{{"\n"}}{{end}}{{end}}')
[[ -n "${gpu_nodes}" ]] \
  || fail "no node advertises nvidia.com/gpu; the GPU Operator's device plugin has not registered the mock GPUs yet"

mock_pod_on() {
  kubectl -n "${MOKKA_NAMESPACE}" get pods -l app.kubernetes.io/name=nvml-mock \
    --field-selector "spec.nodeName=$1,status.phase=Running" \
    -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true
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

# Prefer the node the workload landed on so the drain is observable rather than
# merely logged. Which node that is alternates between runs, since each run
# pushes the workload onto the other worker.
workload_node=$(kubectl -n "${WORKLOAD_NAMESPACE}" get pod -l "${WORKLOAD_SELECTOR}" \
  -o jsonpath='{.items[0].spec.nodeName}' 2>/dev/null || true)

target_node="${mock_pods[0]%%=*}"
target_pod="${mock_pods[0]#*=}"
for pair in "${mock_pods[@]}"; do
  if [[ "${pair%%=*}" == "${workload_node}" ]]; then
    target_node="${workload_node}"
    target_pod="${pair#*=}"
    break
  fi
done

# A one-worker fleet cannot show a drain: there is nowhere for the evicted
# workload to go, so the reschedule assertion below would fail on a healthy
# cluster.
gpu_node_count=$(grep -c . <<<"${gpu_nodes}")
[[ "${gpu_node_count}" -ge 2 ]] \
  || fail "only ${gpu_node_count} GPU node(s); the drain has no healthy worker to reschedule onto. Recreate the cluster with \`make cluster-create\` (1 control-plane + 2 workers)."

printf '==> target: gpu %s on %s (mock pod %s), %s GPU nodes total\n' \
  "${TARGET_GPU}" "${target_node}" "${target_pod}" "${gpu_node_count}"

mock_ctl() { kubectl -n "${MOKKA_NAMESPACE}" exec "${target_pod}" -- nvml-mock-ctl "$@"; }

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
    grep -m1 -E "^[[:space:]]*$1:" /etc/nvml-mock/config.yaml 2>/dev/null \
    | sed -E 's/.*:[[:space:]]*([0-9]+).*/\1/'
}
slowdown_c=$(threshold slowdown_threshold_c)
shutdown_c=$(threshold shutdown_threshold_c)
[[ "${slowdown_c}" =~ ^[0-9]+$ && "${shutdown_c}" =~ ^[0-9]+$ ]] \
  || fail "could not read the thermal thresholds from /etc/nvml-mock/config.yaml on ${target_pod} (got slowdown='${slowdown_c}' shutdown='${shutdown_c}')"

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

# --- start from a genuinely healthy fleet ------------------------------------
# Two assertions below depend on the pre-injection state, and both are defeated
# by a pin left behind: on the target, "NVSentinel cordoned the node" is already
# true before any fault goes in; on the sibling, the drain has no schedulable
# GPU node to move the workload to. Since the target alternates between runs,
# the previous run's target is always the sibling this run needs healthy — so
# clear every GPU of every mock node, not just this run's target.
printf '==> clearing overrides on every GPU of every mock node\n'
for pair in "${mock_pods[@]}"; do
  printf '    %s: ' "${pair%%=*}"
  kubectl -n "${MOKKA_NAMESPACE}" exec "${pair#*=}" -- nvml-mock-ctl reset --gpu all
done

printf '==> waiting for every GPU node to be schedulable before injecting anything\n'
cordoned_gpu_nodes() {
  kubectl get nodes -o go-template='{{range .items}}{{if index .status.allocatable "nvidia.com/gpu"}}{{if .spec.unschedulable}}{{.metadata.name}}{{" "}}{{end}}{{end}}{{end}}'
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
  diagnose
  fail "GPU node(s) ${still_cordoned% } still cordoned ~${POLL_BUDGET_S}s after clearing every GPU override. If they were cordoned by hand, \`kubectl uncordon\` them; otherwise a health check other than the thermal margin is still failing and this scenario can neither attribute a cordon to its own fault nor drain onto a healthy node."
fi

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
  diagnose
  fail "${target_node} was not cordoned within ~${POLL_BUDGET_S}s of gpu ${TARGET_GPU} reaching ${HOT_TEMP_C}C. A 'missing slowdown TLIMIT threshold metadata' line above means the metadata-collector never published the offset, so GpuThermalMarginWatch never armed; no thermal line at all means DCGM is not serving field 153 to the health monitor."
fi
printf 'OK: %s is cordoned by NVSentinel\n' "${target_node}"

# --- phase 2: expect the drain to move the workload -------------------------
printf '==> waiting for the GPU workload to reschedule off %s\n' "${target_node}"
rescheduled=false
new_node=""
for _ in $(seq 1 "${POLL_ATTEMPTS}"); do
  new_node=$(kubectl -n "${WORKLOAD_NAMESPACE}" get pod -l "${WORKLOAD_SELECTOR}" \
    --field-selector=status.phase=Running \
    -o jsonpath='{.items[0].spec.nodeName}' 2>/dev/null || true)
  if [[ -n "${new_node}" && "${new_node}" != "${target_node}" ]]; then
    rescheduled=true
    break
  fi
  sleep "${POLL_INTERVAL_S}"
done
if [[ "${rescheduled}" != "true" ]]; then
  diagnose
  fail "the GPU workload is still on ${target_node} after ~${POLL_BUDGET_S}s. node-drainer evicts user-namespace pods only in Immediate mode (node-drainer.userNamespaces in nvsentinel.values.yaml); in the chart's default AllowCompletion mode this pod never completes and so is never evicted."
fi
printf 'OK: the GPU workload moved from %s to %s\n' "${target_node}" "${new_node}"

kubectl get nodes
printf '\n==> quarantine-node passed: gpu %s on %s crossed its slowdown limit (%sC > %sC), NVSentinel cordoned the node and the workload moved to %s.\n' \
  "${TARGET_GPU}" "${target_node}" "${HOT_TEMP_C}" "${slowdown_c}" "${new_node}"
printf '    Run recover-node to cool the GPU and watch the node uncordon itself.\n'
