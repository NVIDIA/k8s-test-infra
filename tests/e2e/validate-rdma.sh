#!/bin/bash
# Copyright 2026 NVIDIA CORPORATION
# SPDX-License-Identifier: Apache-2.0
#
# Cross-node RDMA verbs data-path validation between two nvml-mock DaemonSet
# pods (issue #374). Runs the stock perftest `ib_write_bw` between pods and
# asserts a NON-ZERO bandwidth average.
#
# Requires the chart to be installed with infiniband.rdma.enabled=true (which
# LD_PRELOADs libibmockrdma and sets MOCK_IB_RDMA=1) on top of MOCK_IB=full.
#
# IMPORTANT: the reported bandwidth is a functional artifact of relaying each
# RDMA WRITE over the mock-ib JSON/TCP fabric, NOT a real InfiniBand
# measurement. This test proves the verbs data path is wired end to end
# (QP create -> RTR route resolve -> bytes delivered into the responder MR ->
# completion), not throughput.
#
# Usage:
#   validate-rdma.sh <server-pod> <client-pod>
#
# Flow:
#   1. Wait for the mock-ib socket on both pods and register peers (reusing the
#      same one-shot REGISTER discipline as validate-ibping.sh).
#   2. Start `ib_write_bw` server in the server pod (background, detached).
#   3. Run `ib_write_bw` client in the client pod against the server IP.
#   4. Parse the "BW average" column and require it to be > 0.
#
# perftest's out-of-band TCP sync MUST NOT use mock-ib's fabric port (18515);
# override with RDMA_E2E_PORT (default 18516). This port is exchanged BEFORE
# the QP is created, so it must be reachable between pods: the chart's ibping
# NetworkPolicy admits it via infiniband.rdma.oobPort (kept in sync with this
# default). A mismatch here would be dropped on policy-enforcing CNIs (kindnet
# >= kind v0.24.0, Calico, Cilium) and wedge the handshake before any fabric
# traffic — see deployments/nvml-mock/helm/nvml-mock/templates/network-policy-ibping.yaml.
set -euo pipefail

SERVER_POD="${1:?Usage: $0 <server-pod> <client-pod>}"
CLIENT_POD="${2:?Usage: $0 <server-pod> <client-pod>}"

MAX_RETRIES="${RDMA_E2E_RETRIES:-3}"
RETRY_SLEEP="${RDMA_E2E_RETRY_SLEEP:-5}"
PERFTEST_PORT="${RDMA_E2E_PORT:-18516}"
IB_DEV="${RDMA_E2E_DEV:-mlx5_0}"
MSG_SIZE="${RDMA_E2E_SIZE:-65536}"
ITERS="${RDMA_E2E_ITERS:-1000}"
# Hard cap on each in-pod ib_write_bw run. perftest blocks indefinitely if the
# OOB/QP handshake or a completion never arrives, and `kubectl exec` has no
# timeout of its own -- without this the step hangs until the job's wall clock.
# Bounding it lets a stuck data path fail fast with logs instead of stalling CI.
PERFTEST_TIMEOUT="${RDMA_E2E_TIMEOUT:-30}"
MOCK_IBPING_SOCKET="${MOCK_IB_PING_SOCKET:-/run/mock-ib.sock}"
MOCK_IBPING_PORT="${MOCK_IB_PING_PORT:-18515}"

echo "=== Validating cross-node RDMA ib_write_bw server=$SERVER_POD client=$CLIENT_POD ==="

SERVER_IP=$(kubectl get pod "$SERVER_POD" -o jsonpath='{.status.podIP}')
CLIENT_IP=$(kubectl get pod "$CLIENT_POD" -o jsonpath='{.status.podIP}')
if [ -z "$SERVER_IP" ] || [ -z "$CLIENT_IP" ]; then
  echo "FAIL: could not resolve pod IPs (server=$SERVER_IP client=$CLIENT_IP)"
  exit 1
fi
# The whole point of this test is CROSS-POD fabric traversal: identical IPs mean
# the run never crossed the mock-ib fabric (same pod / scheduling collapse), so
# a BW>0 there proves nothing. Require distinct pod IPs.
if [ "$SERVER_IP" = "$CLIENT_IP" ]; then
  echo "FAIL: server and client share pod IP ($SERVER_IP); not a cross-pod run"
  exit 1
fi
echo "Pod IPs: server=$SERVER_IP client=$CLIENT_IP"

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

# One-shot REGISTER (no TCP bind) for separate Helm releases that do not share
# the same -ibping headless Service. Mirrors validate-ibping.sh so the daemon's
# registry can resolve the peer route at modify_qp(->RTR).
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

# Extract the "BW average[MB/sec]" value from ib_write_bw output. The results
# table row is the line whose first field is the message size (all digits);
# the 4th column is BW average. Returns empty if no data row is found.
parse_bw_average() {
  local out=$1
  printf '%s\n' "$out" \
    | awk '$1 ~ /^[0-9]+$/ && NF>=4 { print $4; exit }'
}

bw_is_nonzero() {
  local bw=$1
  # Accept any strictly-positive decimal.
  printf '%s\n' "$bw" | awk '{ exit !($1+0 > 0) }'
}

# Assert the verbs data path actually crossed the mock-ib fabric, not just that
# perftest's local completion accounting was satisfied (BW>0 is generated even
# if every egress op is dropped). handleVerbsQPConnect logs a resolved route
# "verbs qp_connect ... -> peer=<ip>" UNCONDITIONALLY, and an inbound op shows
# up as a "verbs ..." routing line; require at least one. Returns 0 if traversal
# evidence is present in the server's mock-ib.log.
fabric_was_traversed() {
  local server_pod=$1 log
  log=$(kubectl exec "$server_pod" -- cat /tmp/mock-ib.log 2>/dev/null || true)
  printf '%s\n' "$log" | grep -Eq 'verbs qp_connect .*-> peer=' && return 0
  printf '%s\n' "$log" | grep -Eq 'verbs (op|local deliver|fabric)' && return 0
  return 1
}

wait_for_socket "$SERVER_POD"
wait_for_socket "$CLIENT_POD"
maybe_register_cross_release_peers "$SERVER_POD" "$CLIENT_POD" "$SERVER_IP" "$CLIENT_IP"
# Allow registerWithPeersLoop / DNS discovery to populate registries.
sleep 5

run_rdma_case() {
  local attempt out_client server_log
  for attempt in $(seq 1 "$MAX_RETRIES"); do
    echo "--- ib_write_bw attempt $attempt/$MAX_RETRIES (port $PERFTEST_PORT, dev $IB_DEV) ---"
    # Start the server detached so the exec returns; nohup keeps it alive after
    # the wrapper sh exits. It serves exactly one client run then exits.
    # -F suppresses the cpufreq-governor warning that otherwise aborts the run
    # on nodes without exposed scaling governors (matches the proven loopback).
    # `timeout` bounds the server too: a blocked run must not survive into the
    # next attempt holding the OOB port, which would wedge the retry's bind.
    kubectl exec "$SERVER_POD" -- sh -c \
      "rm -f /tmp/ib_write_bw.server.log; \
       nohup timeout ${PERFTEST_TIMEOUT} ib_write_bw -d ${IB_DEV} -p ${PERFTEST_PORT} -s ${MSG_SIZE} -n ${ITERS} -F \
         > /tmp/ib_write_bw.server.log 2>&1 & echo started" >/dev/null 2>&1 || true
    # Give the server time to bind its OOB listener.
    sleep 3
    out_client=$(kubectl exec "$CLIENT_POD" -- sh -c \
      "timeout ${PERFTEST_TIMEOUT} ib_write_bw -d ${IB_DEV} -p ${PERFTEST_PORT} -s ${MSG_SIZE} -n ${ITERS} -F ${SERVER_IP}" 2>&1) || true
    echo "=== client ib_write_bw output ==="
    echo "$out_client"
    server_log=$(kubectl exec "$SERVER_POD" -- cat /tmp/ib_write_bw.server.log 2>/dev/null || true)
    echo "=== server ib_write_bw output ==="
    echo "$server_log"

    local bw
    bw=$(parse_bw_average "$out_client")
    if [ -z "$bw" ]; then
      # Fall back to the server's table if the client print was truncated.
      bw=$(parse_bw_average "$server_log")
    fi
    if [ -n "$bw" ] && bw_is_nonzero "$bw"; then
      # BW>0 alone is a weak signal (completions are generated locally). Gate on
      # genuine cross-pod fabric traversal as well: the server daemon must have
      # resolved the peer route / seen an inbound op.
      if ! fabric_was_traversed "$SERVER_POD"; then
        echo "FAIL: BW=${bw} but no fabric traversal in server mock-ib.log"
        echo "      (route never resolved -> egress dropped; likely a bogus"
        echo "       LID/GID exchange). Dumping server mock-ib.log:"
        kubectl exec "$SERVER_POD" -- tail -60 /tmp/mock-ib.log 2>/dev/null || true
        return 1
      fi
      echo "=== RDMA cross-node validation PASSED (BW average=${bw} MB/sec, fabric traversed) ==="
      echo "NOTE: bandwidth is a JSON/TCP relay artifact, not an IB measurement."
      return 0
    fi
    echo "No non-zero BW average yet (parsed='${bw:-}')."
    if [ "$attempt" -lt "$MAX_RETRIES" ]; then
      echo "Retrying in ${RETRY_SLEEP}s..."
      sleep "$RETRY_SLEEP"
    fi
  done
  return 1
}

if run_rdma_case; then
  exit 0
fi

echo "FAIL: ib_write_bw did not report non-zero bandwidth after $MAX_RETRIES attempts"
echo "=== server pod logs (mock-ib) ==="
kubectl exec "$SERVER_POD" -- tail -40 /tmp/mock-ib.log 2>/dev/null || true
echo "=== client pod logs (mock-ib) ==="
kubectl exec "$CLIENT_POD" -- tail -40 /tmp/mock-ib.log 2>/dev/null || true
exit 1
