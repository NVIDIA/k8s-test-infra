#!/usr/bin/env bash
# Copyright 2026 NVIDIA CORPORATION
# SPDX-License-Identifier: Apache-2.0
#
# Fault scenario: heat one mock GPU and assert the step change reaches
# Prometheus.
#
# Resets every GPU on the target node, waits for the reading to DIFFER from the
# value about to be injected, pins the target GPU, then asserts Prometheus
# serves that exact value. The reset plus the "differs first" wait are what stop
# a re-run passing on the pin the previous run left behind.
#
# Injection goes through nvml-mock-ctl rather than `helm upgrade`: an upgrade
# recycles dcgm-exporter and tears a hole in the series this exists to render.
# The CLI writes a node-local overlay the running exporter re-reads on the mock
# engine's TTL, so the series stays continuous and shows a step change.
#
# Ported from docs/demo/observability/run.sh phase 1.

set -euo pipefail

# nvml-mock's namespace (_NAMESPACE in local/nvml_mock.tiltfile) and the
# monitoring stack's (_NAMESPACE in local/observability/observability.tiltfile).
MOKKA_NAMESPACE="mokka"
MONITORING_NAMESPACE="monitoring"
# Created by the kube-prometheus-stack release named in observability.tiltfile;
# the chart derives it as <release>-prometheus.
PROM_SVC="kube-prometheus-stack-prometheus"

TARGET_GPU="${TARGET_GPU:-0}"
# 90C sits in the one band that works for every --gpu-profile:
#   - ABOVE the dynamic simulator's ceiling of 73C (base 55C + 15C ramp + 3C
#     variance), so a sibling GPU cannot wander onto the injected value and make
#     the scope check below accuse this run of leaking an override it scoped
#     correctly.
#   - AT OR BELOW the LOWEST thermal shutdown threshold of any profile (92C on
#     a100 and h100; 95C on b200/gb200/gb300; 96C on t4/l40s). The mock clamps
#     an injected temperature to that threshold, and the wait below is for the
#     exact value, so a higher default would burn the whole poll budget and then
#     report "never became == 95" without ever mentioning the clamp.
HOT_TEMP_C="${HOT_TEMP_C:-90}"
# Each poll has to cover the mock's override TTL + dcgm-exporter's collect
# interval + Prometheus' scrape interval. Measured propagation is 25-45s.
POLL_ATTEMPTS="${POLL_ATTEMPTS:-36}"
POLL_INTERVAL_S="${POLL_INTERVAL_S:-5}"

fail() { printf 'FAIL: %s\n' "$*" >&2; exit 1; }

# Reject an out-of-band override up front. Both failure modes are real and both
# report something other than their cause, so the guard has to name them here.
[[ "${HOT_TEMP_C}" =~ ^[0-9]+$ ]] || fail "HOT_TEMP_C='${HOT_TEMP_C}' is not an integer"
[[ "${HOT_TEMP_C}" -gt 73 ]] \
  || fail "HOT_TEMP_C=${HOT_TEMP_C} is inside the simulator's own 52-73C band, so a sibling GPU will read it too and the scope check will report a leak that did not happen"
[[ "${HOT_TEMP_C}" -le 92 ]] \
  || fail "HOT_TEMP_C=${HOT_TEMP_C} exceeds the lowest profile shutdown threshold (92C on a100/h100); the mock would clamp it and the exact-value wait below could never pass"

# Query Prometheus through the API-server service proxy, the same way
# tests/e2e/go/assertions/dcgm.go reaches dcgm-exporter. Keeps the scenario
# independent of the Grafana port-forward and of any host port mapping.
promq() {
  kubectl get --raw \
    "/api/v1/namespaces/${MONITORING_NAMESPACE}/services/${PROM_SVC}:9090/proxy/api/v1/$1"
}

printf '==> waiting for the Prometheus API to answer\n'
prom_ready=false
for _ in $(seq 1 60); do
  if promq "query?query=up" >/dev/null 2>&1; then prom_ready=true; break; fi
  sleep 5
done
if [[ "${prom_ready}" != "true" ]]; then
  # Replay once with the error visible: the poll discards stderr on all 60
  # attempts, so a name that does not resolve looks exactly like a slow start.
  promq "query?query=up" || true
  fail "Prometheus never answered through the service proxy at ${PROM_SVC}"
fi

# Pick the target from what Prometheus actually serves, not from "the first mock
# pod": the fleet is heterogeneous (a100 and t4 workers carry different GPU
# counts) and the control-plane runs a mock pod but no dcgm-exporter, so not
# every mock node has scraped GPUs.
target_node=""
target_pod=""
for host in $(promq "query?query=DCGM_FI_DEV_GPU_TEMP" \
    | jq -r --arg gpu "${TARGET_GPU}" \
        '[.data.result[] | select(.metric.gpu == $gpu) | .metric.Hostname] | unique | .[]'); do
  pod=$(kubectl -n "${MOKKA_NAMESPACE}" get pods -l app.kubernetes.io/name=nvml-mock \
    --field-selector "spec.nodeName=${host},status.phase=Running" \
    -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true)
  if [[ -n "${pod}" ]]; then
    target_node="${host}"
    target_pod="${pod}"
    break
  fi
done
[[ -n "${target_node}" ]] \
  || fail "no node has both a running nvml-mock pod and a scraped DCGM_FI_DEV_GPU_TEMP series for gpu ${TARGET_GPU}; check that dcgm-exporter is up and that its ServiceMonitor carries release=kube-prometheus-stack"
printf '    target: gpu %s on %s (mock pod %s)\n' "${TARGET_GPU}" "${target_node}" "${target_pod}"

mock_ctl() { kubectl -n "${MOKKA_NAMESPACE}" exec "${target_pod}" -- nvml-mock-ctl "$@"; }

# Read one DCGM series for the target GPU. The PromQL stays label-free so it
# needs no URL encoding; jq matches instead. An absent series yields the literal
# "none" so a missing metric can never be mistaken for a reading.
gpu_temp() {
  promq "query?query=DCGM_FI_DEV_GPU_TEMP" \
    | jq -r --arg gpu "${TARGET_GPU}" --arg node "${target_node}" \
        '[.data.result[] | select(.metric.gpu == $gpu and .metric.Hostname == $node) | .value[1]]
         | if length == 1 then .[0] else "none" end'
}

# Poll until the reading satisfies op ("==" or "!=") against want, leaving it in
# OBSERVED. Injection is instant but Prometheus scrapes on an interval, so a
# query fired straight afterwards legitimately still serves the pre-injection
# sample. An absent series satisfies neither test, so a pipeline that stops
# delivering the metric runs out of attempts instead of passing by default.
OBSERVED=""
await_temp() {
  local op="$1" want="$2" cur=""
  for _ in $(seq 1 "${POLL_ATTEMPTS}"); do
    cur=$(gpu_temp)
    if [[ "${cur}" != "none" ]] \
      && awk -v v="${cur}" -v w="${want}" -v o="${op}" \
           'BEGIN { exit !(o == "==" ? v == w : v != w) }'; then
      OBSERVED="${cur}"
      return 0
    fi
    sleep "${POLL_INTERVAL_S}"
  done
  local hint=""
  # The clamp is the failure this message has to name: it reads back as a lower,
  # perfectly plausible temperature, so without this the reader sees only a
  # timeout on a value the mock was never going to report.
  if [[ "${op}" == "==" && "${cur}" != "none" ]] \
    && awk -v v="${cur}" -v w="${want}" 'BEGIN { exit !(v < w) }'; then
    hint=" — the reading settled BELOW the injected value, which is what the mock's clamp to the profile's thermal shutdown_threshold_c looks like; lower HOT_TEMP_C"
  fi
  fail "DCGM_FI_DEV_GPU_TEMP{gpu=\"${TARGET_GPU}\",Hostname=\"${target_node}\"} never became ${op} ${want} within ~$((POLL_ATTEMPTS * POLL_INTERVAL_S))s (last read: ${cur})${hint}"
}

# Clear anything left by an earlier run or a by-hand session. Without this a
# re-run starts already pinned at HOT_TEMP_C and "the target reads HOT_TEMP_C"
# is true before anything is injected — an assertion that cannot fail.
#
# Every GPU on the node, not just the target: a pin left on a sibling would
# otherwise survive every re-run and the scope check below would read it as this
# run's override leaking across the node.
printf '==> clearing overrides on every GPU of %s\n' "${target_node}"
mock_ctl reset --gpu all

printf '==> waiting for gpu %s to report a simulator-driven temperature again\n' "${TARGET_GPU}"
await_temp '!=' "${HOT_TEMP_C}"
baseline="${OBSERVED}"
printf '    baseline DCGM_FI_DEV_GPU_TEMP = %sC\n' "${baseline}"

printf '==> heating gpu %s to %sC\n' "${TARGET_GPU}" "${HOT_TEMP_C}"
mock_ctl temp --gpu "${TARGET_GPU}" "${HOT_TEMP_C}"

# Equality, not >=: `temp` pins the reading with zero variance, so the injected
# value is the only correct answer. A >= test would also accept a GPU that
# merely happens to run hot.
printf '==> waiting for the heat to reach Prometheus\n'
await_temp '==' "${HOT_TEMP_C}"
printf 'OK: DCGM_FI_DEV_GPU_TEMP for gpu %s stepped %sC -> %sC in Prometheus\n' \
  "${TARGET_GPU}" "${baseline}" "${OBSERVED}"

# A pin that moved every GPU on the node is indistinguishable from a
# `temp --gpu all` mistake and would make the dashboard's per-GPU story a lie.
# Prove the siblings kept their own readings — and that there WERE siblings, so
# an empty result cannot pass as a clean scope.
siblings=$(promq "query?query=DCGM_FI_DEV_GPU_TEMP" \
  | jq --arg node "${target_node}" --arg gpu "${TARGET_GPU}" \
      '[.data.result[] | select(.metric.Hostname == $node and .metric.gpu != $gpu)]')
sibling_count=$(jq 'length' <<<"${siblings}")
[[ "${sibling_count}" -gt 0 ]] \
  || fail "no sibling GPU series on ${target_node} to compare against, so the scope check would be vacuous"
hot_siblings=$(jq -r --argjson hot "${HOT_TEMP_C}" \
  '[.[] | select((.value[1] | tonumber) == $hot) | .metric.gpu] | sort | join(",")' <<<"${siblings}")
[[ -z "${hot_siblings}" ]] \
  || fail "gpu(s) ${hot_siblings} on ${target_node} also read ${HOT_TEMP_C}C; the override is not scoped to gpu ${TARGET_GPU}"
printf 'OK: the other %s GPUs on %s kept simulator-driven temperatures (%s)\n' \
  "${sibling_count}" "${target_node}" \
  "$(jq -r '[.[] | "gpu" + .metric.gpu + "=" + .value[1] + "C"] | sort | join(" ")' <<<"${siblings}")"

printf '\n==> inject-thermal passed: gpu %s on %s stepped %sC -> %sC and Prometheus recorded it.\n' \
  "${TARGET_GPU}" "${target_node}" "${baseline}" "${HOT_TEMP_C}"
printf '    Watch it on the "GPU temperature" panel: one line steps up and goes flat.\n'
printf '    Re-running resets the pin first, so the step is genuine every time.\n'
