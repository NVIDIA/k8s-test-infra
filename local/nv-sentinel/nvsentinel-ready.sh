#!/usr/bin/env bash
# Copyright 2026 NVIDIA CORPORATION
# SPDX-License-Identifier: Apache-2.0
#
# Finish the NVSentinel install: wait for the collection-setup Job and for the
# DCGM Service to serve, then get the pods that raced them into a clean start.
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

set -euo pipefail

NAMESPACE="nvsentinel"
SETUP_JOB="nvsentinel-external-mongodb-setup"
INSTANCE_SELECTOR="app.kubernetes.io/instance=nvsentinel"
# The hostengine the GPU Health Monitor polls, published by the standalone DCGM
# DaemonSet local/nv-sentinel/gpu-operator.values.yaml enables.
DCGM_NAMESPACE="gpu-operator"
DCGM_SERVICE="nvidia-dcgm"
POLL_ATTEMPTS="${POLL_ATTEMPTS:-60}"
POLL_INTERVAL_S="${POLL_INTERVAL_S:-5}"
# A separate, much larger budget for the two waits that sit behind a first-time
# container image pull. The standalone DCGM image and the gpu-health-monitor
# image that bundles DCGM 4.x are ~2 GB each, and on a cold cluster every GPU
# worker pulls them at once: measured here at ~14 min from pod creation to
# Ready, against which the 5 min above is not close. The Helm hook Job keeps the
# tighter budget — it only has to appear and run mongosh.
PULL_POLL_ATTEMPTS="${PULL_POLL_ATTEMPTS:-240}"

fail() { printf 'FAIL: %s\n' "$*" >&2; exit 1; }

# A silent quarter-hour local_resource is indistinguishable from a hung one in
# the Tilt UI, so the two long waits report what they are blocked on each minute.
progress() {
  if (( $1 % (60 / POLL_INTERVAL_S) == 0 )); then
    printf '    still waiting after %dm: %s\n' "$(($1 * POLL_INTERVAL_S / 60))" "$2"
  fi
}

# `kubectl wait` errors out if the object does not exist yet, and the hook Job
# is created asynchronously by Helm, so wait for it to appear first.
printf '==> waiting for the %s Job to be created\n' "${SETUP_JOB}"
job_found=false
for _ in $(seq 1 "${POLL_ATTEMPTS}"); do
  if kubectl -n "${NAMESPACE}" get job "${SETUP_JOB}" >/dev/null 2>&1; then
    job_found=true
    break
  fi
  sleep "${POLL_INTERVAL_S}"
done
[[ "${job_found}" == "true" ]] \
  || fail "the Helm post-install hook Job ${SETUP_JOB} never appeared; without it the MongoDB collections are never created and the DB-consuming pods crash-loop forever. Check \`helm -n ${NAMESPACE} history nvsentinel\` and the release's hook events."

printf '==> waiting for %s to create the collections\n' "${SETUP_JOB}"
kubectl -n "${NAMESPACE}" wait --for=condition=complete "job/${SETUP_JOB}" \
  --timeout="$((POLL_ATTEMPTS * POLL_INTERVAL_S))s" \
  || fail "${SETUP_JOB} did not complete; inspect \`kubectl -n ${NAMESPACE} logs job/${SETUP_JOB}\`"

# Ready endpoints, not the gpu-operator Tilt resource: that resource goes green
# when the Operator's Helm release converges, which is well before its ~2 GB
# DCGM image has finished pulling. A GPU Health Monitor that starts with nothing
# behind nvidia-dcgm:5555 reports no GPU health at all, and reports it quietly —
# so wait here, and let the restart below give the monitor a live hostengine on
# its first poll.
printf '==> waiting for the %s Service to have ready endpoints\n' "${DCGM_SERVICE}"
dcgm_ready_endpoints() {
  # EndpointSlices rather than the v1 Endpoints they replace: Endpoints is
  # deprecated from k8s 1.33 and every read prints a warning into the Tilt log.
  kubectl -n "${DCGM_NAMESPACE}" get endpointslices \
    -l "kubernetes.io/service-name=${DCGM_SERVICE}" -o json 2>/dev/null \
    | jq '[.items[].endpoints[]? | select(.conditions.ready)] | length' 2>/dev/null \
    || true
}
dcgm_ready=""
for attempt in $(seq 1 "${PULL_POLL_ATTEMPTS}"); do
  dcgm_ready="$(dcgm_ready_endpoints)"
  if [[ "${dcgm_ready}" =~ ^[0-9]+$ ]] && [[ "${dcgm_ready}" -gt 0 ]]; then
    printf 'OK: %s.%s.svc has %s ready endpoint(s)\n' \
      "${DCGM_SERVICE}" "${DCGM_NAMESPACE}" "${dcgm_ready}"
    break
  fi
  progress "${attempt}" "no ready endpoint behind ${DCGM_SERVICE} yet"
  sleep "${POLL_INTERVAL_S}"
done
[[ "${dcgm_ready}" =~ ^[0-9]+$ ]] && [[ "${dcgm_ready}" -gt 0 ]] \
  || fail "no ready endpoint behind ${DCGM_SERVICE}.${DCGM_NAMESPACE}.svc:5555 after ~$((PULL_POLL_ATTEMPTS * POLL_INTERVAL_S))s; the GPU Health Monitor would come up Ready and report no GPU health. Inspect \`kubectl -n ${DCGM_NAMESPACE} get pods -l app=nvidia-dcgm\`."

# Running only: the Job's own pod is Succeeded and deleting it would make the
# release's hook look un-run to anyone reading afterwards.
printf '==> restarting the pods that started before their dependencies were up\n'
kubectl -n "${NAMESPACE}" delete pod -l "${INSTANCE_SELECTOR}" \
  --field-selector=status.phase=Running --ignore-not-found >/dev/null

printf '==> waiting for every NVSentinel pod to be Ready\n'
for attempt in $(seq 1 "${PULL_POLL_ATTEMPTS}"); do
  # Succeeded pods are excluded: the hook Job's pod never reports Ready and
  # would keep this loop spinning until it timed out.
  not_ready=$(kubectl -n "${NAMESPACE}" get pods \
    --field-selector=status.phase!=Succeeded \
    -o 'jsonpath={range .items[*]}{.metadata.name}{" "}{.status.conditions[?(@.type=="Ready")].status}{"\n"}{end}' \
    2>/dev/null | grep -vE ' True$' || true)
  if [[ -z "${not_ready}" ]]; then
    printf 'OK: all NVSentinel pods are Ready\n'
    kubectl -n "${NAMESPACE}" get pods -o wide
    exit 0
  fi
  progress "${attempt}" "$(tr '\n' ' ' <<<"${not_ready}")"
  sleep "${POLL_INTERVAL_S}"
done

printf 'still not Ready:\n%s\n' "${not_ready}" >&2
kubectl -n "${NAMESPACE}" get pods -o wide >&2
fail "NVSentinel pods did not become Ready within ~$((PULL_POLL_ATTEMPTS * POLL_INTERVAL_S))s"
