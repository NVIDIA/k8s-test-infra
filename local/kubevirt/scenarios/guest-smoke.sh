#!/usr/bin/env bash
# Copyright 2026 NVIDIA CORPORATION
# SPDX-License-Identifier: Apache-2.0
#
# Checks that the VM's guest sees exactly the mock GPU its launcher pod was
# allocated, as passthrough, that libvirt and QEMU run without the shims, and
# that a runtime override set on the node reaches the running guest.
set -euo pipefail

VM="${1:-gpu-vm}"
NS="${NS:-default}"
MOKKA_NS="${MOKKA_NS:-mokka}"
TIMEOUT="${TIMEOUT:-900}"
VMI_TIMEOUT="${VMI_TIMEOUT:-600}"
LIVE_TIMEOUT="${LIVE_TIMEOUT:-60}"
# Must match the NRI plugin's --overlay-mount-path.
OVERLAY_PATH="${OVERLAY_PATH:-/opt/nvml-mock}"

fail() { printf 'FAIL: %s\n' "$*" >&2; exit 1; }

printf '==> waiting for VMI %s to run\n' "${VM}"
# A VMI being deleted stays Running until its launcher exits, and its
# replacement reuses the name, so wait for a running VMI not being deleted.
deadline=$((SECONDS + VMI_TIMEOUT))
while :; do
  read -r uid phase deleting <<<"$(kubectl -n "${NS}" get vmi "${VM}" --ignore-not-found \
    -o jsonpath='{.metadata.uid} {.status.phase} {.metadata.deletionTimestamp}')"
  [[ "${phase}" == Running && -z "${deleting}" ]] && break
  (( SECONDS < deadline )) || fail "VMI ${VM} is not running within ${VMI_TIMEOUT}s"
  sleep 5
done

# Check this VMI's own launcher, never one still shutting down.
pods=$(kubectl -n "${NS}" get pods -l "kubevirt.io=virt-launcher,kubevirt.io/created-by=${uid}" \
  --field-selector status.phase=Running \
  -o go-template='{{range .items}}{{if not .metadata.deletionTimestamp}}{{.metadata.name}}{{"\n"}}{{end}}{{end}}')
npods=$(( $(wc -w <<<"${pods}") ))
(( npods == 1 )) || fail "VMI ${VM} (uid ${uid}) has ${npods} live launcher pods, want 1"
pod="${pods}"
node=$(kubectl -n "${NS}" get pod "${pod}" -o jsonpath='{.spec.nodeName}')
printf '==> launcher %s on node %s\n' "${pod}" "${node}"

# sched: a mock-GPU node, and one GPU from the real device plugin.
[[ -n "$(kubectl get node "${node}" -o jsonpath='{.metadata.labels.mokka\.nvidia\.com/type}')" ]] \
  || fail "node ${node} is not a Mokka GPU node"
limit=$(kubectl -n "${NS}" get pod "${pod}" \
  -o jsonpath='{.spec.containers[?(@.name=="compute")].resources.limits.nvidia\.com/gpu}')
[[ "${limit}" == 1 ]] || fail "compute holds ${limit:-no} nvidia.com/gpu, want 1"

# noharm: the overlay's environment must not reach libvirt and QEMU. Their
# processes are not dumpable, so read the container's environment as an exec
# inherits it: the same process env the NRI plugin would have adjusted.
env=$(kubectl -n "${NS}" exec "${pod}" -c compute -- cat /proc/self/environ | tr '\0' '\n')
grep -qE '^(LD_PRELOAD|MOCK_)' <<<"${env}" && fail "launcher environment carries the mock: $(grep -E '^(LD_PRELOAD|MOCK_)' <<<"${env}")"
# The fifth mountinfo field is the mount point.
overlay=$(kubectl -n "${NS}" exec "${pod}" -c compute -- cat /proc/self/mountinfo \
  | awk -v p="${OVERLAY_PATH}" '$5 == p || index($5, p "/") == 1 {print $5}')
[[ -z "${overlay}" ]] || fail "compute mounts the overlay: ${overlay}"

minors=$(kubectl -n "${NS}" exec "${pod}" -c compute -- sh -c 'ls /dev | sed -n "s/^nvidia\([0-9][0-9]*\)$/\1/p"')
# Arithmetic, because BSD wc pads its count with spaces.
(( $(wc -w <<<"${minors}") == 1 )) || fail "compute sees minors '${minors}', want exactly one"
printf '==> allocated minor %s\n' "${minors}"

agent=$(kubectl -n "${MOKKA_NS}" get pods -l app.kubernetes.io/name=nvml-mock \
  --field-selector "spec.nodeName=${node}" -o jsonpath='{.items[*].metadata.name}')
agent="${agent%% *}"
[[ -n "${agent}" ]] || fail "no nvml-mock pod in ${MOKKA_NS} on node ${node}"
# read fails on empty input; the check below reports it with context.
read -r want_name want_uuid < <(kubectl -n "${MOKKA_NS}" exec "${agent}" -- nvidia-smi -q | awk -v minor="${minors}" '
  /Product Name/ {name=$0; sub(/.*: /, "", name)}
  /GPU UUID/     {uuid=$0; sub(/.*: /, "", uuid)}
  /Minor Number/ {m=$0; sub(/.*: /, "", m); if (m == minor) {gsub(/ /, "_", name); print name, uuid; exit}}') || true
[[ -n "${want_uuid:-}" ]] || fail "host nvidia-smi has no GPU with minor ${minors}"
want_name="${want_name//_/ }"

printf '==> waiting up to %ss for the guest report\n' "${TIMEOUT}"
deadline=$((SECONDS + TIMEOUT))
until report=$(kubectl -n "${NS}" logs "${pod}" -c guest-console-log 2>/dev/null \
    | sed -n '/MOKKA-GUEST BEGIN/,/MOKKA-GUEST END/p') && grep -q 'MOKKA-GUEST END' <<<"${report}"; do
  (( SECONDS < deadline )) || fail "guest did not report within ${TIMEOUT}s"
  sleep 10
done
printf '%s\n' "${report}"

# smi, uuid, virt
gpus=$(grep -c '^GPU [0-9]*:' <<<"${report}" || true)
[[ "${gpus}" == 1 ]] || fail "guest lists ${gpus} GPUs, want 1"
grep -qF "${want_name} (UUID: ${want_uuid})" <<<"${report}" \
  || fail "guest GPU is not '${want_name}' with UUID ${want_uuid}"
grep -q 'Virtualization Mode *: Pass-Through' <<<"${report}" || fail "guest does not report Pass-Through"

# live: a runtime change on the node reaches the running guest, which logs the
# SHA-256 of every override document it applies.
printf '==> pinning %s to 87C on the node\n' "${want_uuid}"
trap 'kubectl -n "${MOKKA_NS}" exec "${agent}" -- nvml-mock-ctl reset --gpu "${want_uuid}" >/dev/null || true' EXIT
kubectl -n "${MOKKA_NS}" exec "${agent}" -- nvml-mock-ctl temp --gpu "${want_uuid}" 87 >/dev/null
sum=$(kubectl -n "${MOKKA_NS}" exec "${agent}" -- sha256sum /var/lib/nvml-mock/driver/config/overrides.yaml)
sum="${sum%% *}"
deadline=$((SECONDS + LIVE_TIMEOUT))
until kubectl -n "${NS}" logs "${pod}" -c guest-console-log 2>/dev/null | grep -q "applied host overrides.*${sum}"; do
  (( SECONDS < deadline )) || fail "guest did not apply the node's overrides (sha256 ${sum}) within ${LIVE_TIMEOUT}s"
  sleep 2
done
printf '==> guest applied overrides %s\n' "${sum}"

printf 'PASS: %s\n' "${VM}"
