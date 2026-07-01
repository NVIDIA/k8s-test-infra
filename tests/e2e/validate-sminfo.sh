#!/bin/bash
# Copyright 2026 NVIDIA CORPORATION
# SPDX-License-Identifier: Apache-2.0
#
# Validate `sminfo` against the mock-ib master subnet manager.
#
# sminfo reads its local PortInfo (MasterSMLID=1), then issues a SubnGet(SMInfo)
# to that SM LID. The mock-ib daemon answers from the elected master SM (lowest
# port GUID in the fabric graph) with SMState=MASTER(3). This needs the UMAD
# daemon (MOCK_IB=full), so it is skipped for IB-disabled profiles.
#
# With an optional <peer-pod>, also assert that both pods report the SAME master
# SM GUID — the cross-fabric "consistent SM identity" property.
#
# Usage:
#   validate-sminfo.sh <pod> <profile> <expected-hcas> [peer-pod]
set -euo pipefail

POD="${1:?Usage: $0 <pod> <profile> <expected-hcas> [peer-pod]}"
PROFILE="${2:?}"
EXPECTED="${3:?}"
PEER_POD="${4:-}"

case "$PROFILE" in
  l40s|t4) EXPECTED=0 ;;
esac

MOCK_IBPING_SOCKET="${MOCK_IB_PING_SOCKET:-/var/lib/nvml-mock/run/mock-ib.sock}"
MAX_RETRIES="${SMINFO_E2E_RETRIES:-3}"
RETRY_SLEEP="${SMINFO_E2E_RETRY_SLEEP:-5}"

echo "=== Validating sminfo pod=$POD profile=$PROFILE peer=${PEER_POD:-<none>} ==="

if [ "$EXPECTED" -eq 0 ]; then
  echo "SKIP: IB disabled for profile $PROFILE"
  exit 0
fi

# sminfo needs the mock-ib UMAD daemon; under MOCK_IB=sysfs (no socket) there is
# nothing to answer the SubnGet, so skip rather than fail.
wait_for_socket() {
  local pod=$1 i
  for i in $(seq 1 30); do
    if kubectl exec "$pod" -- test -S "${MOCK_IBPING_SOCKET}" 2>/dev/null; then
      return 0
    fi
    sleep 1
  done
  return 1
}

if ! wait_for_socket "$POD"; then
  echo "SKIP: mock-ib socket ${MOCK_IBPING_SOCKET} not present on $POD (UMAD daemon not running)"
  exit 0
fi

sminfo_fail_patterns() {
  local out=$1 p
  for p in \
    'iberror:' \
    "can't open UMAD port" \
    'ibwarn:' \
    'mad_rpc' \
    'umad_get_mad' \
    'Resource temporarily unavailable' \
    'query failed'; do
    if printf '%s\n' "$out" | grep -Fq "$p"; then
      echo "FAIL: sminfo output contains forbidden pattern: $p"
      return 1
    fi
  done
  return 0
}

# A believable master SM: SMState MASTER (printed as "state 3 SMINFO_MASTER")
# and a non-zero SM GUID.
sminfo_success() {
  local out=$1
  if ! printf '%s\n' "$out" | grep -Eiq 'SMINFO_MASTER|state 3'; then
    echo "FAIL: sminfo did not report a master SM (no SMINFO_MASTER / state 3)"
    return 1
  fi
  if ! printf '%s\n' "$out" | grep -Eiq 'sm guid 0x0*[1-9a-f][0-9a-f]*'; then
    echo "FAIL: sminfo did not report a non-zero SM GUID"
    return 1
  fi
  return 0
}

sm_guid() {
  printf '%s\n' "$1" | grep -oiE 'sm guid 0x[0-9a-f]+' | head -1 | awk '{print $NF}' | tr 'A-F' 'a-f'
}

# run_sminfo PODNAME -> echoes the validated sminfo output (or exits non-zero).
run_sminfo() {
  local pod=$1 attempt out
  for attempt in $(seq 1 "$MAX_RETRIES"); do
    echo "--- sminfo on $pod attempt $attempt/$MAX_RETRIES ---" >&2
    out=$(kubectl exec "$pod" -- sminfo 2>&1) || true
    printf '%s\n' "$out" >&2
    if sminfo_fail_patterns "$out" && sminfo_success "$out"; then
      printf '%s' "$out"
      return 0
    fi
    if [ "$attempt" -lt "$MAX_RETRIES" ]; then
      echo "Retrying in ${RETRY_SLEEP}s..." >&2
      sleep "$RETRY_SLEEP"
    fi
  done
  return 1
}

if ! OUT=$(run_sminfo "$POD"); then
  echo "FAIL: sminfo did not report a master SM on $POD after $MAX_RETRIES attempts"
  kubectl exec "$POD" -- tail -40 /tmp/mock-ib.log 2>/dev/null || true
  exit 1
fi
GUID=$(sm_guid "$OUT")
echo "OK: $POD reports master SM guid=$GUID state=MASTER"

if [ -n "$PEER_POD" ]; then
  if ! wait_for_socket "$PEER_POD"; then
    echo "SKIP cross-pod check: mock-ib socket not present on peer $PEER_POD"
    echo "=== sminfo validation PASSED ==="
    exit 0
  fi
  if ! PEER_OUT=$(run_sminfo "$PEER_POD"); then
    echo "FAIL: sminfo did not report a master SM on peer $PEER_POD"
    kubectl exec "$PEER_POD" -- tail -40 /tmp/mock-ib.log 2>/dev/null || true
    exit 1
  fi
  PEER_GUID=$(sm_guid "$PEER_OUT")
  echo "OK: $PEER_POD reports master SM guid=$PEER_GUID state=MASTER"
  if [ "$GUID" != "$PEER_GUID" ]; then
    echo "FAIL: pods disagree on the master SM GUID ($POD=$GUID, $PEER_POD=$PEER_GUID)"
    exit 1
  fi
  echo "OK: both pods agree on a single master SM ($GUID)"
fi

echo "=== sminfo validation PASSED ==="
