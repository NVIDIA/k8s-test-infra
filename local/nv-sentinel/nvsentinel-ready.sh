#!/usr/bin/env bash
# Copyright 2026 NVIDIA CORPORATION
# SPDX-License-Identifier: Apache-2.0
#
# Finish the NVSentinel install: wait for the collection-setup Job and for the
# DCGM DaemonSet to serve on every node, get the pods that raced them into a
# clean start, then assert the GPU thermal-margin watch actually armed.
#
# Why this is a separate step rather than `helm --wait`: the setup Job that
# creates the MongoDB collections is a Helm post-install hook, and the
# DB-consuming pods (platform-connector, fault-quarantine, node-drainer) cannot
# become Ready until those collections exist. Helm runs post-install hooks AFTER
# --wait is satisfied, so asking for --wait deadlocks the release against its
# own hook. The install therefore does not wait, and this script does.
#
# Those pods start before the Job finishes and land in CrashLoopBackOff. They
# would recover on their own, but only after the exponential backoff expires —
# minutes of a red Tilt resource for a condition that is already resolved — so
# delete them once the collections exist and let them start clean.
#
# Every wait here is an assertion, not narration: this resource is what `tilt
# ci` blocks on, so a green nvsentinel-ready has to mean the pipeline is armed.
# An empty or errored API response is therefore never read as success.

set -euo pipefail

NAMESPACE="nvsentinel"
SETUP_JOB="nvsentinel-external-mongodb-setup"
INSTANCE_SELECTOR="app.kubernetes.io/instance=nvsentinel"
# The GPU Health Monitor DaemonSet pods, the ones that arm GpuThermalMarginWatch.
MONITOR_SELECTOR="app.kubernetes.io/name=gpu-health-monitor"
# The hostengine the GPU Health Monitor polls, published by the standalone DCGM
# DaemonSet local/nv-sentinel/gpu-operator.values.yaml enables.
DCGM_NAMESPACE="gpu-operator"
DCGM_SERVICE="nvidia-dcgm"
DCGM_DAEMONSET="nvidia-dcgm"
# The line the monitor logs once it has set up the DCGM field-153 watch, and the
# one it logs per GPU per poll cycle when it has no slowdown T.Limit threshold
# to compare against. See the armed assertion at the bottom of this script.
ARMED_LOG="Watching DCGM field 153"
DISARMED_LOG="missing slowdown TLIMIT threshold metadata"
POLL_ATTEMPTS="${POLL_ATTEMPTS:-60}"
POLL_INTERVAL_S="${POLL_INTERVAL_S:-5}"
# A separate, much larger budget for the two waits that sit behind a first-time
# container image pull. The standalone DCGM image and the gpu-health-monitor
# image that bundles DCGM 4.x are ~2 GB each, and on a cold cluster every GPU
# worker pulls them at once: measured here at ~14 min from pod creation to
# Ready, against which the 5 min above is not close. The Helm hook Job keeps the
# tighter budget — it only has to appear and run mongosh.
PULL_POLL_ATTEMPTS="${PULL_POLL_ATTEMPTS:-240}"
# Polling is the right response to "not created yet". It is the wrong response
# to an RBAC denial or an API server that is not there, which no amount of
# waiting fixes: those burn the whole 20 min budget above and then report a
# timeout that points at the wrong component. Fail after this many CONSECUTIVE
# API errors instead, which a transient blip does not reach.
API_ERROR_TOLERANCE="${API_ERROR_TOLERANCE:-12}"
# Short: the armed line is printed while the monitor initialises DCGM, so it is
# there within seconds of the pod reporting Ready — but not necessarily in the
# same instant.
WATCH_POLL_ATTEMPTS="${WATCH_POLL_ATTEMPTS:-12}"
# How long to watch a freshly Ready monitor for the disarmed message. Empty
# means "derive it from the poll interval the monitor logs in its armed line",
# which is the only value that cannot go stale if the chart changes it.
WATCH_OBSERVE_S="${WATCH_OBSERVE_S:-}"

fail() { printf 'FAIL: %s\n' "$*" >&2; exit 1; }

# Every one of these is used in arithmetic below, where a non-numeric override
# is a bash error rather than a diagnosis.
for _var in POLL_ATTEMPTS POLL_INTERVAL_S PULL_POLL_ATTEMPTS \
  API_ERROR_TOLERANCE WATCH_POLL_ATTEMPTS; do
  [[ "${!_var}" =~ ^[1-9][0-9]*$ ]] \
    || fail "${_var}='${!_var}' must be a positive integer"
done
[[ -z "${WATCH_OBSERVE_S}" || "${WATCH_OBSERVE_S}" =~ ^[1-9][0-9]*$ ]] \
  || fail "WATCH_OBSERVE_S='${WATCH_OBSERVE_S}' must be a positive integer or empty"

# A silent quarter-hour local_resource is indistinguishable from a hung one in
# the Tilt UI, so the long waits report what they are blocked on each minute.
# Clamped to 1: with POLL_INTERVAL_S above 60 the stride would be 0 and the
# modulo below would divide by zero.
PROGRESS_EVERY=$((60 / POLL_INTERVAL_S))
((PROGRESS_EVERY > 0)) || PROGRESS_EVERY=1
progress() {
  if (($1 % PROGRESS_EVERY == 0)); then
    printf '    still waiting after %dm: %s\n' "$(($1 * POLL_INTERVAL_S / 60))" "$2"
  fi
}

# kubectl with its stderr kept instead of discarded. Every poll below has to
# distinguish "not there yet" from "the API refused", and a discarded error
# makes a missing RBAC rule look exactly like a slow image pull. Command
# substitution runs in a subshell, so the text goes to a file that kube_err
# reads back in the caller.
_kube_stderr="$(mktemp "${TMPDIR:-/tmp}/nvsentinel-ready.XXXXXX")"
trap 'rm -f "${_kube_stderr}"' EXIT
kube() { kubectl "$@" 2>"${_kube_stderr}"; }
kube_err() { tr '\n' ' ' <"${_kube_stderr}"; }
# The one error class a wait DOES fix: an object the GPU Operator has not
# created yet. Everything else — RBAC, TLS, no API server — is permanent.
kube_err_is_notfound() { grep -qi 'not *found' "${_kube_stderr}"; }

# Helm creates the hook Job asynchronously, and a read of a Job that does not
# exist yet is an error rather than a state, so wait for it to appear before the
# loop below starts reading its conditions.
printf '==> waiting for the %s Job to be created\n' "${SETUP_JOB}"
job_found=false
for _ in $(seq 1 "${POLL_ATTEMPTS}"); do
  if kube -n "${NAMESPACE}" get job "${SETUP_JOB}" >/dev/null; then
    job_found=true
    break
  fi
  sleep "${POLL_INTERVAL_S}"
done
[[ "${job_found}" == "true" ]] \
  || fail "the Helm post-install hook Job ${SETUP_JOB} never appeared (last API error: $(kube_err)); without it the MongoDB collections are never created and the DB-consuming pods crash-loop forever. Check \`helm -n ${NAMESPACE} history nvsentinel\` and the release's hook events."

printf '==> waiting for %s to create the collections\n' "${SETUP_JOB}"
job_complete=false
api_errors=0
for _ in $(seq 1 "${POLL_ATTEMPTS}"); do
  # The Job's own conditions rather than `kubectl wait --for=condition=complete`:
  # wait can only be given one condition, so a Job that has already Failed sits
  # out the entire timeout before this script says anything about it.
  if job_conditions="$(kube -n "${NAMESPACE}" get job "${SETUP_JOB}" \
    -o 'jsonpath={range .status.conditions[?(@.status=="True")]}{.type}{"\n"}{end}')"; then
    api_errors=0
    if grep -qx 'Complete' <<<"${job_conditions}"; then
      job_complete=true
      break
    fi
    # Exact match: k8s also reports a FailureTarget condition on its way here,
    # and only Failed means the backoff limit is spent.
    if grep -qx 'Failed' <<<"${job_conditions}"; then
      fail "${SETUP_JOB} failed, so the MongoDB collections do not exist and the DB-consuming pods will crash-loop forever. Inspect \`kubectl -n ${NAMESPACE} logs job/${SETUP_JOB}\`"
    fi
  else
    api_errors=$((api_errors + 1))
    ((api_errors < API_ERROR_TOLERANCE)) \
      || fail "could not read job/${SETUP_JOB} ${api_errors} times in a row (last error: $(kube_err))"
  fi
  sleep "${POLL_INTERVAL_S}"
done
[[ "${job_complete}" == "true" ]] \
  || fail "${SETUP_JOB} did not complete within ~$((POLL_ATTEMPTS * POLL_INTERVAL_S))s; inspect \`kubectl -n ${NAMESPACE} logs job/${SETUP_JOB}\`"

# Ready endpoints, not the gpu-operator Tilt resource: that resource goes green
# when the Operator's Helm release converges, which is well before its ~2 GB
# DCGM image has finished pulling. A GPU Health Monitor that starts with nothing
# behind nvidia-dcgm:5555 reports no GPU health at all, and reports it quietly —
# so wait here, and let the restart below give the monitor a live hostengine on
# its first poll.
printf '==> waiting for every %s endpoint to be ready\n' "${DCGM_SERVICE}"
# EndpointSlices rather than the v1 Endpoints they replace: Endpoints is
# deprecated from k8s 1.33 and every read prints a warning into the Tilt log.
# jsonpath rather than jq: this runs on the path `tilt ci` blocks on, where jq
# is not a dependency of anything else, and a missing jq used to be reported as
# a DCGM timeout 20 minutes later.
dcgm_ready_flags() {
  kube -n "${DCGM_NAMESPACE}" get endpointslices \
    -l "kubernetes.io/service-name=${DCGM_SERVICE}" \
    -o 'jsonpath={range .items[*]}{range .endpoints[*]}{.conditions.ready}{"\n"}{end}{end}'
}
# The DaemonSet's own count of the nodes that must run DCGM. Comparing against
# it is the point: one ready endpoint only proves the Service resolves, but the
# restart below hands EVERY health monitor its hostengine, so a monitor whose
# node is still pulling DCGM gets a dead one — and then comes up Ready anyway,
# leaving that node silently unmonitored.
dcgm_desired_nodes() {
  kube -n "${DCGM_NAMESPACE}" get daemonset "${DCGM_DAEMONSET}" \
    -o 'jsonpath={.status.desiredNumberScheduled}'
}
dcgm_ready=0
dcgm_desired=""
dcgm_fleet_ready=false
api_errors=0
for attempt in $(seq 1 "${PULL_POLL_ATTEMPTS}"); do
  if dcgm_flags="$(dcgm_ready_flags)" && dcgm_desired="$(dcgm_desired_nodes)"; then
    api_errors=0
    dcgm_ready="$(awk '/^true$/ { n++ } END { print n + 0 }' <<<"${dcgm_flags}")"
    # A desired count of 0 is not a satisfied fleet: it is what an unreconciled
    # DaemonSet reports, and 0 >= 0 would wave the gate through before a single
    # DCGM pod exists. >= rather than ==, so an endpoint left behind by a node
    # that has just left the DaemonSet cannot wedge this wait.
    if [[ "${dcgm_desired}" =~ ^[1-9][0-9]*$ ]] && ((dcgm_ready >= dcgm_desired)); then
      printf 'OK: %s.%s.svc has %s ready endpoint(s) for %s scheduled DCGM pod(s)\n' \
        "${DCGM_SERVICE}" "${DCGM_NAMESPACE}" "${dcgm_ready}" "${dcgm_desired}"
      dcgm_fleet_ready=true
      break
    fi
  elif kube_err_is_notfound; then
    # The Operator creates the DaemonSet and the Service, so a NotFound here is
    # exactly what this loop exists to wait through: it must not count against
    # the tolerance.
    api_errors=0
  else
    api_errors=$((api_errors + 1))
    ((api_errors < API_ERROR_TOLERANCE)) \
      || fail "could not read the ${DCGM_SERVICE} EndpointSlices or the ${DCGM_DAEMONSET} DaemonSet in ${DCGM_NAMESPACE} ${api_errors} times in a row (last error: $(kube_err))"
  fi
  progress "${attempt}" \
    "${dcgm_ready} of ${dcgm_desired:-?} scheduled DCGM pod(s) have a ready endpoint"
  sleep "${POLL_INTERVAL_S}"
done
if [[ "${dcgm_fleet_ready}" != "true" ]]; then
  # The last read's error, when there was one: a DaemonSet the Operator never
  # reconciled reports "0 of 0", which on its own names no cause.
  dcgm_hint=""
  [[ -z "$(kube_err)" ]] || dcgm_hint=" (last API error: $(kube_err))"
  fail "only ${dcgm_ready} of ${dcgm_desired:-0} scheduled DCGM pod(s) had a ready endpoint behind ${DCGM_SERVICE}.${DCGM_NAMESPACE}.svc:5555 after ~$((PULL_POLL_ATTEMPTS * POLL_INTERVAL_S))s${dcgm_hint}; the GPU Health Monitor on every node without one would come up Ready and report no GPU health. Inspect \`kubectl -n ${DCGM_NAMESPACE} get pods -l app=nvidia-dcgm\`."
fi

# Running only: the Job's own pod is Succeeded and deleting it would make the
# release's hook look un-run to anyone reading afterwards.
printf '==> restarting the pods that started before their dependencies were up\n'
kube -n "${NAMESPACE}" delete pod -l "${INSTANCE_SELECTOR}" \
  --field-selector=status.phase=Running --ignore-not-found >/dev/null \
  || fail "could not restart the NVSentinel pods: $(kube_err)"

printf '==> waiting for every NVSentinel pod to be Ready\n'
pods_ready=false
not_ready=""
api_errors=0
for attempt in $(seq 1 "${PULL_POLL_ATTEMPTS}"); do
  # Succeeded pods are excluded: the hook Job's pod never reports Ready and
  # would keep this loop spinning until it timed out.
  if pod_states="$(kube -n "${NAMESPACE}" get pods \
    --field-selector=status.phase!=Succeeded \
    -o 'jsonpath={range .items[*]}{.metadata.name}{" "}{.status.conditions[?(@.type=="Ready")].status}{"\n"}{end}')"; then
    api_errors=0
    # An empty list is NOT "everything is Ready". It is also what a get that
    # lands in the window the delete above opens returns, and what an API server
    # that answered with nothing returns — so keep polling rather than declaring
    # success over an empty namespace.
    if [[ -z "${pod_states//[[:space:]]/}" ]]; then
      not_ready="the API server returned no pods at all"
    else
      not_ready="$(awk 'NF && $NF != "True" { print }' <<<"${pod_states}")"
      if [[ -z "${not_ready}" ]]; then
        pods_ready=true
        break
      fi
    fi
  else
    api_errors=$((api_errors + 1))
    ((api_errors < API_ERROR_TOLERANCE)) \
      || fail "could not list the pods in ${NAMESPACE} ${api_errors} times in a row (last error: $(kube_err))"
    not_ready="API error: $(kube_err)"
  fi
  progress "${attempt}" "$(tr '\n' ' ' <<<"${not_ready}")"
  sleep "${POLL_INTERVAL_S}"
done
if [[ "${pods_ready}" != "true" ]]; then
  printf 'still not Ready:\n%s\n' "${not_ready}" >&2
  # Plain kubectl and tolerated failure: this is the diagnostic dump, so its own
  # errors belong on the terminal and must not pre-empt the message below.
  kubectl -n "${NAMESPACE}" get pods -o wide >&2 || true
  fail "NVSentinel pods did not become Ready within ~$((PULL_POLL_ATTEMPTS * POLL_INTERVAL_S))s"
fi
printf 'OK: all NVSentinel pods are Ready\n'
kubectl -n "${NAMESPACE}" get pods -o wide || true

# "Every pod is Ready" is not "the watch is armed", and armed is the property
# this consumer exists to demonstrate. The GPU Health Monitor reports Ready
# either way: with a pre-Ada --gpu-profile, with a metadata-collector that has
# not written slowdown_tlimit_c for every GPU, or with the chart's
# gpuTempLimitMonitoringEnabled flipped off, it comes up Ready and then logs
# that it has no slowdown T.Limit threshold to watch. That was the exact state
# of a green `tilt ci` on the a100 profile, so assert against it here.
printf '==> asserting the GPU thermal-margin watch armed on every health monitor\n'
# Pods with a deletionTimestamp are excluded. A pod keeps status.phase=Running
# while it terminates, so the restart above leaves doomed pods matching the field
# selector for a few seconds — and one of them was selected here, went away
# mid-observation, and failed the run on a NotFound instead of on anything about
# the watch. Skipping them makes the DaemonSet's replacement the pod that is
# checked: until it is Running the count below stays short of desired and this
# waits, which is the correct response to a restart still in flight.
monitor_pods() {
  kube -n "${NAMESPACE}" get pods -l "${MONITOR_SELECTOR}" \
    --field-selector=status.phase=Running \
    -o go-template='{{range .items}}{{if not .metadata.deletionTimestamp}}{{.metadata.name}}{{"\n"}}{{end}}{{end}}'
}
# Summed over both monitor DaemonSets — the chart ships a dcgm-3.x and a
# dcgm-4.x variant and node labels decide which one schedules — so a fleet
# that is missing a monitor entirely cannot pass by having no pod to check.
monitor_desired() {
  kube -n "${NAMESPACE}" get daemonsets -l "${MONITOR_SELECTOR}" \
    -o 'jsonpath={range .items[*]}{.status.desiredNumberScheduled}{"\n"}{end}' \
    | awk '{ n += $1 } END { print n + 0 }'
}
monitors=""
monitor_log=""
monitors_armed=false
api_errors=0
for attempt in $(seq 1 "${WATCH_POLL_ATTEMPTS}"); do
  if monitors="$(monitor_pods)" && desired_monitors="$(monitor_desired)"; then
    api_errors=0
  else
    api_errors=$((api_errors + 1))
    ((api_errors < API_ERROR_TOLERANCE)) \
      || fail "could not list the ${MONITOR_SELECTOR} pods or DaemonSets in ${NAMESPACE} ${api_errors} times in a row (last error: $(kube_err))"
    sleep "${POLL_INTERVAL_S}"
    continue
  fi
  running_monitors="$(awk 'NF { n++ } END { print n + 0 }' <<<"${monitors}")"
  if ((desired_monitors > 0)) && ((running_monitors >= desired_monitors)); then
    unarmed=""
    for pod in ${monitors}; do
      # The full log, not a tail: the armed line is printed during DCGM init, at
      # the very top. The delete above means every monitor log is seconds old.
      if ! monitor_log="$(kube -n "${NAMESPACE}" logs "${pod}")"; then
        unarmed="${unarmed}${pod} (log unreadable: $(kube_err)) "
      elif ! grep -qF "${ARMED_LOG}" <<<"${monitor_log}"; then
        unarmed="${unarmed}${pod} "
      fi
    done
    if [[ -z "${unarmed}" ]]; then
      monitors_armed=true
      break
    fi
  else
    unarmed="only ${running_monitors} of ${desired_monitors} monitor pod(s) are running"
  fi
  progress "${attempt}" "no \"${ARMED_LOG}\" from: ${unarmed}"
  sleep "${POLL_INTERVAL_S}"
done
[[ "${monitors_armed}" == "true" ]] \
  || fail "the GPU health monitors never logged \"${ARMED_LOG}\" within ~$((WATCH_POLL_ATTEMPTS * POLL_INTERVAL_S))s (${unarmed:-no monitor pod was checked}), so the DCGM field-153 watch was never set up and a heated GPU would be detected by nothing. Check gpu-health-monitor.dcgmFieldsMonitoring in local/nv-sentinel/nvsentinel.values.yaml and \`kubectl -n ${NAMESPACE} logs -l ${MONITOR_SELECTOR}\`."

# The armed line alone is not proof: it is printed when the field watch is set
# up, before any GPU's metadata is read, so it appears on a pre-Ada profile too.
# The disarmed message is what discriminates, and the monitor re-logs it per GPU
# on every poll cycle — so watch a whole cycle of FRESH log. Fresh matters twice
# over: a pod can report Ready a few seconds before its first evaluation runs,
# and a monitor that started before the metadata-collector wrote
# gpu_metadata.json logs the message once and then arms itself (the reader
# treats a missing file as transient and reloads), which must not fail here.
if [[ -n "${WATCH_OBSERVE_S}" ]]; then
  observe_s="${WATCH_OBSERVE_S}"
else
  # The monitor prints its own interval in the armed line ("... at 15s
  # interval"), so this tracks a chart that changes it instead of quietly
  # shrinking to less than a cycle and asserting nothing.
  watch_interval_s="$(sed -n 's/.*at \([0-9][0-9]*\)s interval.*/\1/p' <<<"${monitor_log}" | head -1)"
  [[ "${watch_interval_s}" =~ ^[1-9][0-9]*$ ]] || watch_interval_s=15
  observe_s=$((watch_interval_s + POLL_INTERVAL_S))
fi
printf '    watching %ss of fresh log for "%s"\n' "${observe_s}" "${DISARMED_LOG}"
observed_s=0
while ((observed_s < observe_s)); do
  sleep "${POLL_INTERVAL_S}"
  observed_s=$((observed_s + POLL_INTERVAL_S))
  for pod in ${monitors}; do
    if ! monitor_log="$(kube -n "${NAMESPACE}" logs "${pod}" --since="${observed_s}s")"; then
      fail "could not read the ${pod} log to check whether its thermal-margin watch armed: $(kube_err)"
    fi
    if grep -qF "${DISARMED_LOG}" <<<"${monitor_log}"; then
      # Substitution rather than a pipeline: head closing the pipe on grep would
      # be a pipefail non-zero, which set -e turns into a silent exit here.
      printf '%s\n' "$(grep -F "${DISARMED_LOG}" <<<"${monitor_log}" | head -3)" >&2
      fail "${pod} is still logging \"${DISARMED_LOG}\" ${observed_s}s after going Ready: GpuThermalMarginWatch is NOT active on that node, so no heated GPU there can ever be detected. Usual causes: a pre-Ada --gpu-profile (a100 and t4 report no T.Limit offset), or a metadata-collector that wrote no slowdown_tlimit_c for those GPUs."
    fi
  done
done

printf 'OK: GpuThermalMarginWatch is armed on all %s health monitor(s)\n' \
  "${running_monitors}"
