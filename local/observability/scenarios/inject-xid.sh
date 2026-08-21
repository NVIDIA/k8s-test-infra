#!/usr/bin/env bash
# Copyright 2026 NVIDIA CORPORATION
# SPDX-License-Identifier: Apache-2.0
#
# Fault scenario: raise an uncorrectable-ECC Xid on one mock GPU and assert the
# code reaches Prometheus.
#
# DCGM_FI_DEV_XID_ERRORS has NO series at all on a healthy cluster: the mock
# delivers Xids through the NVML event set, and dcgm-exporter omits field 230
# entirely while it holds no value for it. So a "!= 0" test would be vacuous —
# it compares against a series that does not exist.
#
# DCGM also LATCHES the last Xid it saw per device and never retracts it, not
# even when the mock's failure is cleared. Re-injecting the same code would
# therefore find the assertion already satisfied by the previous run's residue,
# and would equally pass with dcgm-exporter dead, since Prometheus keeps serving
# the last sample for its lookback window. This scenario rotates between two
# codes and injects whichever one is NOT currently reported, so the value being
# waited for cannot be present until this run's injection arrives.
#
# Ported from docs/demo/observability/run.sh phase 2.

set -euo pipefail

# nvml-mock's namespace (_NAMESPACE in local/nvml_mock.tiltfile) and the
# monitoring stack's (_NAMESPACE in local/observability/observability.tiltfile).
MOKKA_NAMESPACE="mokka"
MONITORING_NAMESPACE="monitoring"
# Created by the kube-prometheus-stack release named in observability.tiltfile;
# the chart derives it as <release>-prometheus.
PROM_SVC="kube-prometheus-stack-prometheus"

TARGET_GPU="${TARGET_GPU:-0}"
# The two codes the rotation alternates between. 79 is "GPU has fallen off the
# bus"; 48 is the double-bit-ECC Xid, which stays plausible for the
# ecc_uncorrectable mode being injected. They must differ or the rotation — and
# with it this scenario's only guarantee of a fresh delivery — is vacuous.
XID_CODE="${XID_CODE:-79}"
XID_CODE_ALT="${XID_CODE_ALT:-48}"
# Each poll has to cover the mock's override TTL + dcgm-exporter's collect
# interval + Prometheus' scrape interval. Measured propagation is 25-45s.
POLL_ATTEMPTS="${POLL_ATTEMPTS:-36}"
POLL_INTERVAL_S="${POLL_INTERVAL_S:-5}"

fail() { printf 'FAIL: %s\n' "$*" >&2; exit 1; }

[[ "${XID_CODE}" != "${XID_CODE_ALT}" ]] \
  || fail "XID_CODE and XID_CODE_ALT are both ${XID_CODE}; the rotation needs two distinct codes or it asserts nothing"

# Query Prometheus through the API-server service proxy, the same way
# tests/e2e/go/assertions/dcgm.go reaches dcgm-exporter.
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
  promq "query?query=up" || true
  fail "Prometheus never answered through the service proxy at ${PROM_SVC}"
fi

# Pick the target from the temperature series, which every scraped GPU has,
# rather than from the Xid series, which a healthy fleet does not have at all.
# The control-plane runs a mock pod but no dcgm-exporter, and the two workers
# carry different profiles, so neither "the first mock pod" nor a fixed node
# name would be right.
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

# Read the Xid series for the target GPU, yielding "none" when it does not
# exist — the distinction the rotation below is built on.
#
# The optional err_code argument narrows the match. gpu+Hostname alone is not
# guaranteed to identify one series: DCGM_FI_DEV_XID_ERRORS carries err_code, so
# during the rotation two series can describe the same GPU at once while
# Prometheus is still serving the superseded code's last sample. Returning
# whichever one it happened to list first would make the assertion depend on
# result order, so ambiguity is a hard error instead of a coin flip.
gpu_xid() {
  local want_code="${1:-}" sample
  sample=$(promq "query?query=DCGM_FI_DEV_XID_ERRORS" \
    | jq -r --arg gpu "${TARGET_GPU}" --arg node "${target_node}" --arg code "${want_code}" \
        '[.data.result[]
           | select(.metric.gpu == $gpu and .metric.Hostname == $node)
           | select($code == "" or .metric.err_code == $code)
           | .value[1]]
         | if length == 0 then "none"
           elif length == 1 then .[0]
           else "ambiguous:" + join(",") end')
  [[ "${sample}" != ambiguous:* ]] \
    || fail "DCGM_FI_DEV_XID_ERRORS{gpu=\"${TARGET_GPU}\",Hostname=\"${target_node}\"} matched several series at once (values: ${sample#ambiguous:}); the read must name the err_code that distinguishes them"
  printf '%s\n' "${sample}"
}

# Rotate to the code that is NOT currently reported. This is the whole
# non-vacuity argument: the value waited for below is provably absent now, so
# its arrival can only be this run's delivery.
xid_before=$(gpu_xid)
xid_want="${XID_CODE}"
if [[ "${xid_before}" == "${XID_CODE}" ]]; then
  xid_want="${XID_CODE_ALT}"
fi
printf '    DCGM_FI_DEV_XID_ERRORS before injection = %s, so this run injects %s\n' \
  "${xid_before}" "${xid_want}"

printf '==> injecting ecc_uncorrectable with Xid %s on gpu %s\n' "${xid_want}" "${TARGET_GPU}"
mock_ctl fail --gpu "${TARGET_GPU}" --mode ecc_uncorrectable --after-calls 1 --xid "${xid_want}"

# An absent series satisfies nothing, so a pipeline that stops delivering the
# metric runs out of attempts rather than passing by default.
printf '==> waiting for Prometheus to carry Xid %s\n' "${xid_want}"
observed="none"
for _ in $(seq 1 "${POLL_ATTEMPTS}"); do
  observed=$(gpu_xid "${xid_want}")
  if [[ "${observed}" == "${xid_want}" ]]; then break; fi
  sleep "${POLL_INTERVAL_S}"
done
[[ "${observed}" == "${xid_want}" ]] \
  || fail "DCGM_FI_DEV_XID_ERRORS{gpu=\"${TARGET_GPU}\",Hostname=\"${target_node}\",err_code=\"${xid_want}\"} never reached Prometheus within ~$((POLL_ATTEMPTS * POLL_INTERVAL_S))s (last read: ${observed})"

if [[ "${xid_before}" == "none" ]]; then
  printf 'OK: DCGM_FI_DEV_XID_ERRORS for gpu %s appeared carrying %s — the series did not exist before, so this run watched an Xid travel the whole path\n' \
    "${TARGET_GPU}" "${observed}"
else
  printf 'OK: DCGM_FI_DEV_XID_ERRORS for gpu %s moved %s -> %s, so this run watched a fresh Xid travel the whole path rather than re-reading a latched value\n' \
    "${TARGET_GPU}" "${xid_before}" "${observed}"
fi

# Same scope argument as the temperature pin, phrased against the injected code
# rather than presence, so a future dcgm-exporter reporting 0 for healthy GPUs
# would still be correctly scoped.
xid_others=$(promq "query?query=DCGM_FI_DEV_XID_ERRORS" \
  | jq -r --arg node "${target_node}" --arg gpu "${TARGET_GPU}" --argjson xid "${xid_want}" \
      '[.data.result[]
         | select(.metric.Hostname == $node and .metric.gpu != $gpu)
         | select((.value[1] | tonumber) == $xid)
         | .metric.gpu] | sort | join(",")')
[[ -z "${xid_others}" ]] \
  || fail "gpu(s) ${xid_others} on ${target_node} also report Xid ${xid_want}; the failure is not scoped to gpu ${TARGET_GPU}"
printf 'OK: the Xid is scoped to gpu %s with labels %s\n' "${TARGET_GPU}" \
  "$(promq "query?query=DCGM_FI_DEV_XID_ERRORS" \
      | jq -c --arg node "${target_node}" --arg gpu "${TARGET_GPU}" --arg xid "${xid_want}" \
          'first(.data.result[]
             | select(.metric.Hostname == $node and .metric.gpu == $gpu and .metric.err_code == $xid)
             | {Hostname: .metric.Hostname, gpu: .metric.gpu, err_code: .metric.err_code, UUID: .metric.UUID, pci_bus_id: .metric.pci_bus_id})')"

printf '\n==> inject-xid passed: gpu %s on %s reports Xid %s and Prometheus recorded it.\n' \
  "${TARGET_GPU}" "${target_node}" "${xid_want}"
printf '    Watch it on the "Last Xid code reported" panel — a code, not a count.\n'
printf '    The next run will inject the other code of the pair by design, because\n'
printf '    DCGM latches the last Xid per device and a repeat would prove nothing.\n'
