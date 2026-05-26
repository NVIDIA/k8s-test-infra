#!/bin/bash
# Copyright 2026 NVIDIA CORPORATION
# SPDX-License-Identifier: Apache-2.0
#
# Cross-node ibping validation between two nvml-mock DaemonSet pods.
# Requires MOCK_IB_PING / fabric / LD_PRELOAD (always set by the nvml-mock chart).
#
# Usage:
#   validate-ibping.sh <server-pod> <client-pod>
#
# Flow:
#   mock-ib is started by setup.sh (Helm sets
#   POD_IP, MOCK_IB_PING_SERVICE_HOST; registerWithPeersLoop discovers peers).
#   For two separate Helm releases (ibping-multinode CI), we one-shot REGISTER only.
#   1. Read server mlx5_0/ports/1/lid and/or port_guid from sysfs
#   2. Wait for mock-ib socket + fabric registration
#   3. Client: ibping -c 3 <lid> or ibping -G -c 3 <port_guid> (0x hex, no colons)
#
# Set IBPING_E2E_MODE=lid|guid|both (default: both).
set -euo pipefail

SERVER_POD="${1:?Usage: $0 <server-pod> <client-pod>}"
CLIENT_POD="${2:?Usage: $0 <server-pod> <client-pod>}"

MAX_RETRIES="${IBPING_E2E_RETRIES:-3}"
RETRY_SLEEP="${IBPING_E2E_RETRY_SLEEP:-5}"

IB_ROOT='${MOCK_IB_ROOT:-/var/lib/nvml-mock/ib}'
LID_PATH="${IB_ROOT}/sys/class/infiniband/mlx5_0/ports/1/lid"
GUID_PATH="${IB_ROOT}/sys/class/infiniband/mlx5_0/ports/1/port_guid"
MOCK_IBPING_SOCKET="${MOCK_IB_PING_SOCKET:-/run/mock-ib.sock}"
MOCK_IBPING_PORT="${MOCK_IB_PING_PORT:-18515}"
IBPING_E2E_MODE="${IBPING_E2E_MODE:-both}"

echo "=== Validating cross-node ibping server=$SERVER_POD client=$CLIENT_POD ==="

SERVER_IP=$(kubectl get pod "$SERVER_POD" -o jsonpath='{.status.podIP}')
CLIENT_IP=$(kubectl get pod "$CLIENT_POD" -o jsonpath='{.status.podIP}')
if [ -z "$SERVER_IP" ] || [ -z "$CLIENT_IP" ]; then
  echo "FAIL: could not resolve pod IPs (server=$SERVER_IP client=$CLIENT_IP)"
  exit 1
fi
echo "Pod IPs: server=$SERVER_IP client=$CLIENT_IP"

read_lid() {
  local pod=$1
  kubectl exec "$pod" -- sh -c "tr -d '[:space:]' < ${LID_PATH}"
}

read_port_guid() {
  local pod=$1
  kubectl exec "$pod" -- sh -c "tr -d '[:space:]' < ${GUID_PATH}"
}

# ibping accepts decimal or hex; sysfs uses 0xNNNN — strip prefix for portability.
normalize_lid_for_ibping() {
  local raw=$1
  if [[ "$raw" =~ ^[0-9]+$ ]]; then
    echo "$raw"
    return
  fi
  local hex="${raw#0x}"
  hex="${hex#0X}"
  printf '%d' "0x${hex}"
}

# ibdiag parses -G with strtoull; colon-separated sysfs GUIDs must be 0x<hex> without ':'.
normalize_guid_for_ibping() {
  local raw=$1
  local hex="${raw//:/}"
  hex="${hex#0x}"
  hex="${hex#0X}"
  printf '0x%s' "${hex}"
}

# One-shot REGISTER (no TCP bind) for separate Helm releases that do not share
# the same -ibping headless Service (see ibping-multinode CI job).
maybe_register_cross_release_peers() {
  local server_pod=$1 client_pod=$2 server_ip=$3 client_ip=$4
  local s_inst c_inst
  s_inst=$(kubectl get pod "$server_pod" -o jsonpath='{.metadata.labels.app\.kubernetes\.io/instance}' 2>/dev/null || true)
  c_inst=$(kubectl get pod "$client_pod" -o jsonpath='{.metadata.labels.app\.kubernetes\.io/instance}' 2>/dev/null || true)
  if [ -n "$s_inst" ] && [ "$s_inst" = "$c_inst" ]; then
    echo "Same Helm release ($s_inst): peer discovery via MOCK_IB_PING_SERVICE_HOST"
    return 0
  fi
  echo "Separate Helm releases ($s_inst / $c_inst): one-shot REGISTER to peer IPs"
  local pod peers pod_ip attempt
  for pod in "$server_pod" "$client_pod"; do
    if [ "$pod" = "$server_pod" ]; then
      peers=$client_ip
      pod_ip=$server_ip
    else
      peers=$server_ip
      pod_ip=$client_ip
    fi
    for attempt in $(seq 1 5); do
      if kubectl exec "$pod" -- env \
        POD_IP="${pod_ip}" \
        MOCK_IB_PEERS="${peers}" \
        MOCK_IB_ROOT="${MOCK_IB_ROOT:-/var/lib/nvml-mock/ib}" \
        MOCK_IB_PING_PORT="${MOCK_IBPING_PORT}" \
        /usr/local/bin/mock-ib \
          -register-peers \
          -ib-root "${MOCK_IB_ROOT:-/var/lib/nvml-mock/ib}" \
          -port "${MOCK_IBPING_PORT}" \
          -fabric >/dev/null 2>&1; then
        break
      fi
      if [ "$attempt" -eq 5 ]; then
        echo "FAIL: register-peers failed on $pod"
        return 1
      fi
      sleep 2
    done
  done
  return 0
}

wait_for_socket() {
  local pod=$1
  local i
  for i in $(seq 1 30); do
    if kubectl exec "$pod" -- test -S "${MOCK_IBPING_SOCKET}" 2>/dev/null; then
      return 0
    fi
    sleep 1
  done
  echo "FAIL: mock-ib socket not ready on $pod"
  kubectl exec "$pod" -- tail -20 /tmp/mock-ib.log 2>/dev/null || true
  return 1
}

ibping_fail_patterns() {
  local out=$1
  local p
  for p in \
    'client_register for mgmt 3 failed' \
    'iberror:' \
    "can't open UMAD port" \
    'ibwarn:' \
    'mad_rpc' \
    'Resource temporarily unavailable' \
    "can't serve class" \
    '100% packet loss' \
    ', 0 received'; do
    if printf '%s\n' "$out" | grep -Fq "$p"; then
      echo "FAIL: ibping output contains forbidden pattern: $p"
      printf '%s\n' "$out"
      return 1
    fi
  done
  return 0
}

# Require at least one reply in ibping statistics (not merely "packets transmitted").
ibping_success() {
  local out=$1
  if printf '%s\n' "$out" | grep -Eiq '[0-9]+ packets transmitted, [1-9][0-9]* received'; then
    return 0
  fi
  if printf '%s\n' "$out" | grep -Fq '0% packet loss'; then
    return 0
  fi
  return 1
}

LID_RAW=$(read_lid "$SERVER_POD")
GUID_RAW=$(read_port_guid "$SERVER_POD")
if [ -z "$LID_RAW" ]; then
  echo "FAIL: empty LID from $SERVER_POD:$LID_PATH"
  exit 1
fi
if [ -z "$GUID_RAW" ]; then
  echo "FAIL: empty port_guid from $SERVER_POD:$GUID_PATH"
  exit 1
fi
LID=$(normalize_lid_for_ibping "$LID_RAW")
GUID=$(normalize_guid_for_ibping "$GUID_RAW")
echo "Server LID (sysfs=$LID_RAW, ibping=$LID)"
echo "Server port GUID (sysfs=$GUID_RAW, ibping -G=$GUID)"

wait_for_socket "$SERVER_POD"
wait_for_socket "$CLIENT_POD"
maybe_register_cross_release_peers "$SERVER_POD" "$CLIENT_POD" "$SERVER_IP" "$CLIENT_IP"
# Allow registerWithPeersLoop / DNS discovery to populate registries.
sleep 5

run_ibping_case() {
  local label=$1
  local cmd=$2
  local attempt
  local out=""
  echo "=== ibping case: $label ==="
  for attempt in $(seq 1 "$MAX_RETRIES"); do
    echo "--- Client ibping ($label) attempt $attempt/$MAX_RETRIES ---"
    out=$(kubectl exec "$CLIENT_POD" -- sh -c "$cmd" 2>&1) || true
    echo "$out"
    if ibping_fail_patterns "$out" && ibping_success "$out"; then
      echo "=== ibping cross-node validation PASSED ($label) ==="
      return 0
    fi
    if [ "$attempt" -lt "$MAX_RETRIES" ]; then
      echo "Retrying in ${RETRY_SLEEP}s..."
      sleep "$RETRY_SLEEP"
    fi
  done
  echo "FAIL: ibping ($label) did not report success after $MAX_RETRIES attempts"
  return 1
}

FAIL=0
case "$IBPING_E2E_MODE" in
  lid)
    run_ibping_case "LID" "ibping -c 3 ${LID}" || FAIL=1
    ;;
  guid)
    run_ibping_case "GUID" "ibping -G -c 3 ${GUID}" || FAIL=1
    ;;
  both|*)
    run_ibping_case "LID" "ibping -c 3 ${LID}" || FAIL=1
    run_ibping_case "GUID" "ibping -G -c 3 ${GUID}" || FAIL=1
    ;;
esac

if [ "$FAIL" -eq 0 ]; then
  exit 0
fi

echo "FAIL: one or more ibping modes failed (IBPING_E2E_MODE=$IBPING_E2E_MODE)"
echo "=== server pod logs (mock-ib) ==="
kubectl exec "$SERVER_POD" -- tail -40 /tmp/mock-ib.log 2>/dev/null || true
echo "=== client pod logs (mock-ib) ==="
kubectl exec "$CLIENT_POD" -- tail -40 /tmp/mock-ib.log 2>/dev/null || true
exit 1
