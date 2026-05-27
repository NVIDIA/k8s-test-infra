#!/bin/bash
# Copyright 2026 NVIDIA CORPORATION
# SPDX-License-Identifier: Apache-2.0
#
# Validate that perfquery (libibmad PMA path) returns non-zero counters
# from the mock-ib PMA synthesizer. This proves the full chain:
#   perfquery -> libibmad PMA MAD -> libibumad LD_PRELOAD shim ->
#   mock-ib unix socket -> daemon.IsPMASend + TrySynthesizePMA ->
#   bit-packed PortCounters / PortCountersExtended response.
#
# Usage:
#   validate-perfquery.sh <pod-name> <profile> <expected-hcas>
set -euo pipefail

POD="${1:?Usage: $0 <pod-name> <profile> <expected-hcas>}"
PROFILE="${2:?}"
EXPECTED="${3:?}"

case "$PROFILE" in
  l40s|t4) EXPECTED=0 ;;
esac

echo "=== Validating perfquery on pod=$POD profile=$PROFILE expected=$EXPECTED ==="

if [ "$EXPECTED" -eq 0 ]; then
  echo "SKIP: IB disabled for profile $PROFILE"
  exit 0
fi

# Wait briefly so the writer has produced non-zero values via PMA.
# (sysfs may already show non-zero from render-time seed, but perfquery
# goes through PMA which depends on the writer-shared Epochs.)
sleep 2

# 1) perfquery (legacy PortCounters, 32-bit) on mlx5_0 port 1.
LEGACY=$(kubectl exec "$POD" -- perfquery mlx5_0 1 2>&1 || true)
echo "--- perfquery mlx5_0 1 ---"
printf '%s\n' "$LEGACY"
if printf '%s\n' "$LEGACY" | grep -qiE 'timeout|ibwarn'; then
  echo "FAIL: perfquery legacy output contains error indicator"
  exit 1
fi
LEGACY_XMIT=$(printf '%s\n' "$LEGACY" | awk -F'[ .]+' '/PortXmitData/ {print $NF; exit}')
if [ -z "$LEGACY_XMIT" ] || [ "$LEGACY_XMIT" = "0" ]; then
  echo "FAIL: perfquery PortXmitData = '${LEGACY_XMIT:-<missing>}'; expected non-zero"
  exit 1
fi
echo "PASS: perfquery PortXmitData = $LEGACY_XMIT"

# 2) perfquery -x (PortCountersExtended, 64-bit).
EXT=$(kubectl exec "$POD" -- perfquery -x mlx5_0 1 2>&1 || true)
echo "--- perfquery -x mlx5_0 1 ---"
printf '%s\n' "$EXT"
if printf '%s\n' "$EXT" | grep -qiE 'timeout|ibwarn'; then
  echo "FAIL: perfquery -x output contains error indicator"
  exit 1
fi
EXT_XMIT=$(printf '%s\n' "$EXT" | awk -F'[ .]+' '/PortXmitData/ {print $NF; exit}')
if [ -z "$EXT_XMIT" ] || [ "$EXT_XMIT" = "0" ]; then
  echo "FAIL: perfquery -x PortXmitData = '${EXT_XMIT:-<missing>}'; expected non-zero"
  exit 1
fi
echo "PASS: perfquery -x PortXmitData = $EXT_XMIT"

# 3) perfquery -R -x resets and re-reads; second value must be lower.
PRE=$(kubectl exec "$POD" -- perfquery -x mlx5_0 1 2>&1 | awk -F'[ .]+' '/PortXmitData/ {print $NF; exit}')
kubectl exec "$POD" -- perfquery -R -x mlx5_0 1 >/dev/null 2>&1 || true
sleep 1
POST=$(kubectl exec "$POD" -- perfquery -x mlx5_0 1 2>&1 | awk -F'[ .]+' '/PortXmitData/ {print $NF; exit}')
echo "--- reset: pre=$PRE post=$POST ---"
if [ -z "$PRE" ] || [ -z "$POST" ]; then
  echo "FAIL: pre/post reset PortXmitData missing (pre='$PRE' post='$POST')"
  exit 1
fi
if [ "$POST" -ge "$PRE" ]; then
  echo "FAIL: perfquery -R did not reset counters (pre=$PRE >= post=$POST)"
  exit 1
fi
echo "PASS: perfquery -R reset PortXmitData $PRE -> $POST"

echo "=== perfquery validation PASSED ==="
