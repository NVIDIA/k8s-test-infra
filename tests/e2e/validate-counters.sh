#!/bin/bash
# Copyright 2026 NVIDIA CORPORATION
# SPDX-License-Identifier: Apache-2.0
#
# Validate that the mock InfiniBand counter writer is running inside the
# nvml-mock pod by:
#   1. Reading port_xmit_data_64 once (must be non-zero).
#   2. Sleeping long enough for at least two ticks (counters.tick_seconds
#      default = 5s; profiles may set lower).
#   3. Reading the same counter again (must be strictly greater).
#   4. Confirming the hw_counters/ surface exists with the mlx5-specific
#      files Prometheus RDMA exporters scrape.
#
# Usage:
#   validate-counters.sh <pod-name> <profile> <expected-hcas>
set -euo pipefail

POD="${1:?Usage: $0 <pod-name> <profile> <expected-hcas>}"
PROFILE="${2:?}"
EXPECTED="${3:?}"

case "$PROFILE" in
  l40s|t4) EXPECTED=0 ;;
esac

echo "=== Validating mock IB counters on pod=$POD profile=$PROFILE expected=$EXPECTED ==="

if [ "$EXPECTED" -eq 0 ]; then
  echo "SKIP: IB disabled for profile $PROFILE"
  exit 0
fi

IBROOT=/var/lib/nvml-mock/ib/sys/class/infiniband
PORT_DIR="$IBROOT/mlx5_0/ports/1"
COUNTER_FILE="$PORT_DIR/counters/port_xmit_data_64"

# 1) Counter file must exist and be non-zero.
V1=$(kubectl exec "$POD" -- cat "$COUNTER_FILE" 2>&1 | tr -d '[:space:]')
echo "--- t0 $COUNTER_FILE = $V1 ---"
if ! [[ "$V1" =~ ^[0-9]+$ ]]; then
  echo "FAIL: $COUNTER_FILE did not contain a uint: $V1"
  exit 1
fi
if [ "$V1" -eq 0 ]; then
  echo "FAIL: port_xmit_data_64 is zero on first read"
  exit 1
fi
echo "PASS: port_xmit_data_64 non-zero at t0"

# 2) Sleep one tick + slack, re-read, must be strictly greater.
SLEEP_S=7
echo "--- sleeping ${SLEEP_S}s for writer ticks ---"
sleep "$SLEEP_S"
V2=$(kubectl exec "$POD" -- cat "$COUNTER_FILE" 2>&1 | tr -d '[:space:]')
echo "--- t1 $COUNTER_FILE = $V2 ---"
if ! [[ "$V2" =~ ^[0-9]+$ ]]; then
  echo "FAIL: $COUNTER_FILE not numeric after sleep: $V2"
  exit 1
fi
if [ "$V2" -le "$V1" ]; then
  echo "FAIL: port_xmit_data_64 did not grow ($V1 -> $V2) within ${SLEEP_S}s"
  exit 1
fi
echo "PASS: port_xmit_data_64 grew $V1 -> $V2 in ${SLEEP_S}s"

# 3) hw_counters/ surface exists with mlx5-specific entries.
HW_DIR="$PORT_DIR/hw_counters"
HW_LIST=$(kubectl exec "$POD" -- ls "$HW_DIR" 2>&1)
echo "--- ls $HW_DIR ---"
printf '%s\n' "$HW_LIST"
for f in out_of_buffer np_cnp_sent rx_write_requests; do
  if ! printf '%s\n' "$HW_LIST" | grep -qx "$f"; then
    echo "FAIL: $HW_DIR/$f missing"
    exit 1
  fi
done
echo "PASS: hw_counters/ has expected mlx5 entries"

# 4) Sanity-check that legacy counters/ entries are present + parseable.
for f in port_xmit_data port_rcv_data port_rcv_errors symbol_error; do
  V=$(kubectl exec "$POD" -- cat "$PORT_DIR/counters/$f" 2>&1 | tr -d '[:space:]')
  if ! [[ "$V" =~ ^[0-9]+$ ]]; then
    echo "FAIL: counters/$f not numeric: $V"
    exit 1
  fi
done
echo "PASS: legacy counters/ entries parseable"

echo "=== mock IB counters validation PASSED ==="
