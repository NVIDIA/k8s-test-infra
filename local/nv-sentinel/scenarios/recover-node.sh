#!/usr/bin/env bash
# Copyright 2026 NVIDIA CORPORATION
# SPDX-License-Identifier: Apache-2.0
#
# Scenario: cool the GPU that quarantine-node heated and assert NVSentinel
# uncordons the node on its own.
#
# This is why the demo drives the loop through the thermal margin rather than a
# latched XID or ECC fault. DCGM field 153 is a live gauge: clearing the
# override re-opens the margin above the slowdown offset, the next health-monitor
# poll sees a healthy GPU, and fault-quarantine uncordons — with no DCGM restart
# anywhere. A latched fault would stay latched until nv-hostengine was bounced.
#
# NVSentinel performs the uncordon, so this scenario must never run `kubectl
# uncordon`: that would make its own assertion true. It is also unusually easy to
# pass vacuously, because "the node is schedulable" is equally the state of a
# cluster where nothing ever happened. Everything ahead of the reset below exists
# to establish the opposite — a node fault-quarantine really cordoned, over a
# thermal fault this run really clears.
#
# Ported from docs/demo/nv-sentinel/run.sh phase 2.

set -euo pipefail

# nvml-mock's namespace (_NAMESPACE in local/nvml_mock.tiltfile) and
# NVSentinel's (_NAMESPACE in local/nv-sentinel/nv_sentinel.tiltfile).
MOKKA_NAMESPACE="mokka"
NVSENTINEL_NAMESPACE="nvsentinel"
# The GPU Operator's namespace, home of the standalone DCGM DaemonSet the health
# monitor polls (local/nv-sentinel/gpu-operator.values.yaml enables it).
GPU_OPERATOR_NAMESPACE="gpu-operator"
# The workload from local/nv-sentinel/gpu-workload.k8s.yaml.
WORKLOAD_NAMESPACE="default"
WORKLOAD_SELECTOR="app=gpu-sample-workload"

# The recovery waits on the same round trip as the detection: the mock's override
# TTL, the DCGM poll, the health monitor's own interval, then the healthy event's
# trip through MongoDB to fault-quarantine. Measured at 10-16s on this path, so
# the budget is deliberately generous — it costs nothing on a run whose assertion
# passes.
POLL_ATTEMPTS="${POLL_ATTEMPTS:-60}"
POLL_INTERVAL_S="${POLL_INTERVAL_S:-5}"
POLL_BUDGET_S=$((POLL_ATTEMPTS * POLL_INTERVAL_S))

fail() { printf 'FAIL: %s\n' "$*" >&2; exit 1; }

# Print what NVSentinel thought, for every failure path below. None of these are
# assertions, so a missing log or an absent condition must not abort the script.
# Takes the nodes worth inspecting as arguments.
diagnose() {
  printf '\n--- GPU node conditions ---\n'
  for node in "$@"; do
    kubectl get node "${node}" -o json 2>/dev/null \
      | jq -r --arg node "${node}" '.status.conditions[] | select(.type | test("Gpu")) | "\($node) \(.type)=\(.status): \(.message)"' || true
  done
  printf '\n--- NVSentinel log lines mentioning recovery ---\n'
  kubectl -n "${NVSENTINEL_NAMESPACE}" logs -l app.kubernetes.io/instance=nvsentinel \
    --prefix --tail=300 2>/dev/null \
    | grep -iE 'uncordon|recovered|healthy|quarantin' | tail -25 || true
  printf '\n--- nodes ---\n'
  kubectl get nodes || true
}

# --- reading a node's quarantine state --------------------------------------
# Three facts about one node: whether it is cordoned, whether fault-quarantine
# still records a quarantine on it, and what the thermal check itself says about
# its GPUs. The cordon alone describes both a recovered node and a node nothing
# ever happened to, which is why the other two are read at all.
#
# One call, so the three cannot describe different moments, and a literal in
# place of every absent field, so a kubectl error produces a line matching
# neither the quarantined nor the recovered shape rather than an empty one that
# could be read as either.
_STATE_TPL='{{if .spec.unschedulable}}cordoned{{else}}schedulable{{end}} {{with index .metadata.annotations "quarantineHealthEventIsCordoned"}}{{.}}{{else}}absent{{end}} {{range .status.conditions}}{{if eq .type "GpuThermalMarginWatch"}}{{.status}}{{end}}{{end}}'

node_state() {
  kubectl get node "$1" -o go-template="${_STATE_TPL}" 2>/dev/null || true
}

# Only cordoned GPU nodes are in scope. Nodes without nvidia.com/gpu are
# excluded for the same reason quarantine-node excludes them: no GPU Operator
# operand lands there, so NVSentinel has nothing to poll and no quarantine of
# its own to release.
cordoned_gpu_nodes() {
  kubectl get nodes -o go-template='{{range .items}}{{if index .status.allocatable "nvidia.com/gpu"}}{{if .spec.unschedulable}}{{.metadata.name}}{{"\n"}}{{end}}{{end}}{{end}}'
}

mock_pod_on() {
  kubectl -n "${MOKKA_NAMESPACE}" get pods -l app.kubernetes.io/name=nvml-mock \
    --field-selector "spec.nodeName=$1,status.phase=Running" \
    -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true
}

mock_ctl_on() {
  local pod="$1"
  shift
  kubectl -n "${MOKKA_NAMESPACE}" exec "${pod}" -- nvml-mock-ctl "$@"
}

# The slowdown limit of the profile the mock on $1 loaded, so a pinned
# temperature can be judged hot rather than merely present. It differs per
# --gpu-profile (87C on h100, 90C on b200/gb200/gb300, 93C on l40s), so it is
# read out of the pod rather than hardcoded. /etc/nvml-mock/config.yaml is the
# chart's rendered profile, mounted read-only into the pod; each key appears
# once, under device_defaults.thermal.
#
# Deliberately a second copy of quarantine-node.sh's reader rather than a shared
# library: scenario scripts in this repo are self-contained, so each one can be
# read, and run, on its own.
threshold() {
  kubectl -n "${MOKKA_NAMESPACE}" exec "$1" -- \
    grep -m1 -E "^[[:space:]]*$2:" /etc/nvml-mock/config.yaml \
    | sed -E 's/.*:[[:space:]]*([0-9]+).*/\1/'
}

# The hottest temperature any GPU on $1's mock is pinned to, or empty if none is.
# Both keys are read because `nvml-mock-ctl temp` writes both — the static
# thermal reading and the zero-variance dynamic block that masks it when
# gpu.dynamicMetrics is on, which this consumer's overlay enables.
hottest_pinned_temp() {
  grep -E '^[[:space:]]*(temperature_gpu_c|base_c):' <<<"$1" \
    | sed -E 's/.*:[[:space:]]*([0-9]+).*/\1/' | sort -n | tail -1
}

# Every failure that turns on the overrides prints them: the guards below are the
# ones an operator is most likely to hit by hand, and the dump is what says why.
dump_overrides() {
  printf '\n--- nvml-mock-ctl status on %s (%s) ---\n%s\n\n' "$1" "$2" "$3"
}

# The standalone DCGM pods, each with its container's restart count. Sampled
# either side of the recovery so the "no DCGM restart" line this script prints at
# the end is an assertion rather than a claim. Pod names are part of the sample:
# a recreated pod is a restart too, and a bare count would reset to 0 with it.
dcgm_restarts() {
  kubectl -n "${GPU_OPERATOR_NAMESPACE}" get pods -l app=nvidia-dcgm \
    -o go-template='{{range .items}}{{.metadata.name}}={{range .status.containerStatuses}}{{.restartCount}}{{end}}{{"\n"}}{{end}}' 2>/dev/null || true
}

# --- the precondition: something to recover ---------------------------------
cordoned_nodes=$(cordoned_gpu_nodes)
if [[ -z "${cordoned_nodes}" ]]; then
  kubectl get nodes || true
  fail "no GPU node is cordoned, so there is nothing to recover. Every assertion below would already hold on this cluster before this run did anything. Run quarantine-node first."
fi
printf '==> cordoned GPU nodes: %s\n' "$(tr '\n' ' ' <<<"${cordoned_nodes}")"

# Checked for every node before anything is cleared, so a run that cannot prove
# its recovery does not first destroy the evidence it would have needed. Each
# guard rejects a node whose eventual state says nothing about this run: one
# NVSentinel is not holding, one held by a check no temperature override
# governs, or one with no GPU pinned hot enough to be the fault.
mock_pods=()
for node in ${cordoned_nodes}; do
  read -r cordon marker margin <<<"$(node_state "${node}")"
  if [[ "${cordon}" != "cordoned" ]]; then
    fail "could not read ${node}'s state back (got '${cordon} ${marker} ${margin}'); refusing to clear a fault whose recovery could then not be attributed to this run"
  fi
  if [[ "${marker}" != "True" ]]; then
    fail "${node} is cordoned but fault-quarantine's quarantineHealthEventIsCordoned annotation reads '${marker}', so NVSentinel is not holding this cordon and cooling a GPU will not lift it. If the node was cordoned by hand, \`kubectl uncordon ${node}\` is what releases it."
  fi
  if [[ "${margin}" != "True" ]]; then
    fail "${node} is quarantined by NVSentinel but its GpuThermalMarginWatch condition reads '${margin}', so the check holding the quarantine is not the thermal margin and clearing a temperature override is not what would recover it. \`kubectl describe node ${node}\` names the Gpu* condition that is True."
  fi
  pod=$(mock_pod_on "${node}")
  if [[ -z "${pod}" ]]; then
    fail "no Running nvml-mock pod on the cordoned node ${node}, so its GPU overrides cannot be cleared. node-drainer skips DaemonSets, so one should have survived the drain: \`kubectl -n ${MOKKA_NAMESPACE} get pods -l app.kubernetes.io/name=nvml-mock -o wide\`."
  fi
  # A GPU pinned past its slowdown limit is the fault this run clears. Without
  # one the node would uncordon on its own timing and "NVSentinel uncordoned it"
  # would be true of a run that did nothing — the exact vacuous pass this
  # scenario is most exposed to. The value is compared, not merely found: a cold
  # pin from a by-hand session is an override this run can clear but not a fault
  # GpuThermalMarginWatch can be failing on, so accepting it would pass the
  # guard on a node whose recovery is somebody else's.
  #
  # `|| true` keeps a failed exec out of `set -e` so the guards, and kubectl's
  # own stderr, get to explain it.
  overrides=$(mock_ctl_on "${pod}" status || true)
  slowdown_c=$(threshold "${pod}" slowdown_threshold_c || true)
  hottest_c=$(hottest_pinned_temp "${overrides}" || true)
  if [[ ! "${slowdown_c}" =~ ^[0-9]+$ ]]; then
    fail "could not read slowdown_threshold_c from /etc/nvml-mock/config.yaml on ${pod} (got '${slowdown_c}'); any kubectl error above says why. Without it a pinned temperature cannot be judged hot, and clearing one proves nothing."
  fi
  if [[ ! "${hottest_c}" =~ ^[0-9]+$ ]]; then
    dump_overrides "${node}" "${pod}" "${overrides}"
    fail "no temperature override is pinned on ${node} (its override dump is above), so this run has no thermal fault to clear and the uncordon it would go on to assert would happen with or without it. Run quarantine-node first."
  fi
  if [[ "${hottest_c}" -lt "${slowdown_c}" ]]; then
    dump_overrides "${node}" "${pod}" "${overrides}"
    fail "the hottest GPU pinned on ${node} is at ${hottest_c}C, below this profile's slowdown threshold of ${slowdown_c}C (its override dump is above), so nothing there is hot enough for GpuThermalMarginWatch to be failing on it and clearing the pin is not what would recover the node. A cold pin left behind by a by-hand session reads exactly like this. Run quarantine-node first."
  fi
  mock_pods+=("${node}=${pod}")
  printf '    %s: quarantined by fault-quarantine over GpuThermalMarginWatch, hottest GPU pinned at %sC against a %sC slowdown limit via %s\n' \
    "${node}" "${hottest_c}" "${slowdown_c}" "${pod}"
done

dcgm_before=$(dcgm_restarts)
if [[ -z "${dcgm_before}" ]]; then
  fail "no pods match app=nvidia-dcgm in ${GPU_OPERATOR_NAMESPACE}. Without the standalone DCGM DaemonSet nothing serves field 153 to the health monitor, so neither the recovery nor the no-restart claim below could be checked."
fi

# Clear every override on each cordoned node rather than only the GPU
# quarantine-node targeted: the point is a node whose checks all clear, and one
# forgotten pin from a by-hand session keeps it quarantined forever.
printf '==> cooling every GPU on the cordoned node(s)\n'
for pair in "${mock_pods[@]}"; do
  printf '    %s: ' "${pair%%=*}"
  mock_ctl_on "${pair#*=}" reset --gpu all
done
cooled_at="${SECONDS}"

printf '==> waiting for NVSentinel to uncordon\n'
for node in ${cordoned_nodes}; do
  recovered=false
  for _ in $(seq 1 "${POLL_ATTEMPTS}"); do
    read -r cordon marker margin <<<"$(node_state "${node}")"
    # Two positive conditions carry this test, so an empty or errored read
    # satisfies neither and the loop keeps polling instead of passing: the node
    # is schedulable AND the thermal check reports healthy.
    #
    # The second is what makes this NVSentinel's recovery rather than anyone's
    # uncordon, and it is not redundant with the first. Measured: `kubectl
    # uncordon` on a node whose GPU was still pinned hot left the state
    # `schedulable absent True` — the cordon went, fault-quarantine's annotation
    # went with it, and only GpuThermalMarginWatch stayed True until the GPU
    # actually cooled. So the annotation alone would have accepted that node;
    # the health check is the fact a by-hand uncordon cannot produce.
    if [[ "${cordon}" == "schedulable" && "${margin}" == "False" && "${marker}" != "True" ]]; then
      recovered=true
      break
    fi
    sleep "${POLL_INTERVAL_S}"
  done
  if [[ "${recovered}" != "true" ]]; then
    diagnose "${node}"
    fail "${node} did not come back ~${POLL_BUDGET_S}s after its GPUs cooled (last read: '${cordon} ${marker} ${margin}' for cordon/quarantine-annotation/GpuThermalMarginWatch). Its overrides ARE cleared, so the fault is gone and what remains is NVSentinel not acting on it: a check other than the thermal margin is still failing — the mock's baseline NVLink effective-BER breach is the usual one, suppressed via gpu-health-monitor.dcgmHealthCheck.suppressedErrorCodes in nvsentinel.values.yaml — or fault-quarantine's circuit breaker tripped and halted all event processing (circuitBreaker.enabled must stay false on a two-worker cluster). The node is LEFT CORDONED; \`kubectl uncordon ${node}\` releases it by hand once you know why."
  fi
  printf 'OK: %s is schedulable again and GpuThermalMarginWatch reports it healthy (%ss after cooling)\n' \
    "${node}" "$((SECONDS - cooled_at))"
done

# A node cordoned while this ran is not covered by the loop above, so without
# this the summary would claim a fully recovered fleet over a partly quarantined
# one. A pin left on a node that was still schedulable at the start looks exactly
# like this one detection cycle later.
still_cordoned=$(cordoned_gpu_nodes)
if [[ -n "${still_cordoned}" ]]; then
  diagnose ${still_cordoned}
  fail "GPU node(s) $(tr '\n' ' ' <<<"${still_cordoned}") are cordoned now, so a fault this run never cleared is being detected — most likely a temperature override on a node that was schedulable when this run started, though a node it did recover being re-quarantined since would look the same. \`kubectl -n ${MOKKA_NAMESPACE} exec <that node's mock pod> -- nvml-mock-ctl reset --gpu all\` clears it; quarantine-node also clears every pin fleet-wide before it injects."
fi

dcgm_after=$(dcgm_restarts)
if [[ "${dcgm_after}" != "${dcgm_before}" ]]; then
  printf '\n--- app=nvidia-dcgm restart counts before ---\n%s--- after ---\n%s\n' \
    "${dcgm_before}" "${dcgm_after}"
  fail "the standalone DCGM pods changed across the recovery (above), so this run cannot claim the node came back without a DCGM restart — and that is the property separating field 153's live gauge from a latched XID or ECC fault. The uncordon itself did happen; what is unproven is that it happened without bouncing nv-hostengine."
fi

# Not asserted on, deliberately: a Deployment does not move a Running pod back,
# so the workload stays on the node quarantine-node drained it onto. The node
# coming back means it can be scheduled there again, not that anything migrates.
printf "\n--- the GPU workload's placement ---\n"
kubectl -n "${WORKLOAD_NAMESPACE}" get pods -l "${WORKLOAD_SELECTOR}" -o wide || true
kubectl get nodes || true
printf '\n==> recover-node passed: the thermal margin re-opened and NVSentinel uncordoned every quarantined node.\n'
printf '    No DCGM restart was involved — asserted above, and the reason field 153 is a live\n'
printf '    gauge: this loop self-clears where a latched XID or ECC fault would not.\n'
