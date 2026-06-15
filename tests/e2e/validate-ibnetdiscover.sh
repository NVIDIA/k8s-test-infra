#!/bin/bash
# Copyright 2026 NVIDIA CORPORATION
# SPDX-License-Identifier: Apache-2.0
#
# Validate ibnetdiscover whole-fabric scan (requires mock-ib fabric ready + peer
# pods). ibnetdiscover runs the same libibnetdisc directed-route walk as
# iblinkinfo over the NODE_INFO/NODE_DESC/PORT_INFO synthesis the daemon already
# serves.
#
# The mock fabric is point-to-point (no switches): each local CA picks ONE
# outbound neighbor (lowest-GUID non-self port; see fabric.PeerAtOutbound), so a
# scan from one pod cannot enumerate every port of a >2-node fabric. As with
# validate-iblinkinfo.sh, the assertion is the property we actually care about:
# ibnetdiscover's scan reaches AT LEAST ONE non-local CA (cross-pod visibility).
#
# Usage:
#   validate-ibnetdiscover.sh <local-pod> <peer-pod> <profile> <expected-hcas>
set -euo pipefail

LOCAL_POD="${1:?Usage: $0 <local-pod> <peer-pod> <profile> <expected-hcas>}"
PEER_POD="${2:?}"
PROFILE="${3:?}"
EXPECTED="${4:?}"

case "$PROFILE" in
  l40s|t4) EXPECTED=0 ;;
esac

MOCK_IBPING_SOCKET="${MOCK_IB_PING_SOCKET:-/run/mock-ib.sock}"
MAX_RETRIES="${IBNETDISCOVER_E2E_RETRIES:-3}"
RETRY_SLEEP="${IBNETDISCOVER_E2E_RETRY_SLEEP:-5}"

echo "=== Validating ibnetdiscover local=$LOCAL_POD peer=$PEER_POD profile=$PROFILE ==="

if [ "$EXPECTED" -eq 0 ]; then
  echo "SKIP: IB disabled for profile $PROFILE"
  exit 0
fi

# ibnetdiscover needs the mock-ib UMAD daemon; under MOCK_IB=sysfs (no socket)
# there is nothing to answer the DR walk, so skip rather than hard-fail.
if ! kubectl exec "$LOCAL_POD" -- test -S "${MOCK_IBPING_SOCKET}" 2>/dev/null; then
  for i in $(seq 1 30); do
    if kubectl exec "$LOCAL_POD" -- test -S "${MOCK_IBPING_SOCKET}" 2>/dev/null; then
      break
    fi
    sleep 1
  done
fi
if ! kubectl exec "$LOCAL_POD" -- test -S "${MOCK_IBPING_SOCKET}" 2>/dev/null; then
  echo "SKIP: mock-ib socket ${MOCK_IBPING_SOCKET} not present on $LOCAL_POD (UMAD daemon not running)"
  exit 0
fi

for P in "$LOCAL_POD" "$PEER_POD"; do
  if ! kubectl logs "$P" 2>/dev/null | grep -q 'fabric ready'; then
    echo "WARN: pod $P logs do not show 'fabric ready' yet"
  fi
done

# Local pod's own port GUIDs come from the rendered mock sysfs tree. Any GUID in
# ibnetdiscover's output that is not in this set is, by construction, a peer-pod
# port (same technique as validate-iblinkinfo.sh).
LOCAL_GUIDS=$(kubectl exec "$LOCAL_POD" -- sh -c '
  for f in /var/lib/nvml-mock/ib/sys/class/infiniband/*/ports/1/port_guid; do
    [ -r "$f" ] && cat "$f"
  done' 2>/dev/null | tr -d ':[:space:]' | tr 'A-F' 'a-f' | sort -u)
if [ -z "$LOCAL_GUIDS" ]; then
  echo "FAIL: could not enumerate local port GUIDs from sysfs on $LOCAL_POD"
  exit 1
fi

attempt_discover() {
  local out found_guids cross_pod g p
  out=$(kubectl exec "$LOCAL_POD" -- ibnetdiscover 2>&1) || true
  echo "--- ibnetdiscover ---"
  printf '%s\n' "$out"

  for p in 'iberror:' "can't open UMAD port" 'mad_rpc' 'Resource temporarily unavailable'; do
    if printf '%s\n' "$out" | grep -Fq "$p"; then
      echo "ibnetdiscover output contains forbidden pattern: $p"
      return 1
    fi
  done

  # ibnetdiscover prints a Ca record per discovered HCA (caguid=...).
  if ! printf '%s\n' "$out" | grep -qiE '^caguid='; then
    echo "ibnetdiscover printed no Ca records (caguid=)"
    return 1
  fi

  found_guids=$(printf '%s\n' "$out" \
    | grep -oiE '0x[0-9a-f]{16}' \
    | tr 'A-F' 'a-f' \
    | sed 's/^0x//' \
    | sort -u)
  if [ -z "$found_guids" ]; then
    echo "ibnetdiscover on $LOCAL_POD printed no port GUIDs"
    return 1
  fi

  cross_pod=""
  for g in $found_guids; do
    if ! printf '%s\n' "$LOCAL_GUIDS" | grep -qx "$g"; then
      cross_pod="$g"
      break
    fi
  done
  if [ -z "$cross_pod" ]; then
    echo "ibnetdiscover on $LOCAL_POD found only local GUIDs ($found_guids), no cross-pod peer"
    return 1
  fi

  echo "OK: ibnetdiscover scan from $LOCAL_POD reached cross-pod peer $cross_pod"
  echo "    (mock fabric is point-to-point lowest-GUID, see fabric.PeerAtOutbound —"
  echo "     cross-pod visibility is the assertion)"
  return 0
}

for attempt in $(seq 1 "$MAX_RETRIES"); do
  echo "--- ibnetdiscover attempt $attempt/$MAX_RETRIES ---"
  if attempt_discover; then
    echo "=== ibnetdiscover validation PASSED ==="
    exit 0
  fi
  if [ "$attempt" -lt "$MAX_RETRIES" ]; then
    echo "Retrying in ${RETRY_SLEEP}s..."
    sleep "$RETRY_SLEEP"
  fi
done

echo "FAIL: ibnetdiscover did not reach a cross-pod peer after $MAX_RETRIES attempts"
kubectl exec "$LOCAL_POD" -- tail -40 /tmp/mock-ib.log 2>/dev/null || true
exit 1
