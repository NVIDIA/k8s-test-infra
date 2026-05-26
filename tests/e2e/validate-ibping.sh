#!/bin/bash
# Copyright 2026 NVIDIA CORPORATION
# SPDX-License-Identifier: Apache-2.0
#
# Validate ibping against the mock InfiniBand fabric inside nvml-mock.
#
# Usage:
#   validate-ibping.sh <pod-name> <profile> <expected-hcas>
#
# Exercises vendor MAD ping (libibumad) on top of the sysfs mock. Profiles
# with infiniband.enabled=false are skipped.
set -euo pipefail

POD="${1:?Usage: $0 <pod-name> <profile> <expected-hcas>}"
PROFILE="${2:?}"
EXPECTED="${3:?}"

case "$PROFILE" in
  l40s|t4)
    echo "=== ibping validation SKIPPED (IB disabled for $PROFILE) ==="
    exit 0
    ;;
esac

if [ "$EXPECTED" -lt 2 ]; then
  echo "=== ibping cross-HCA test SKIPPED (need >= 2 HCAs, got $EXPECTED) ==="
fi

echo "=== Validating ibping on pod=$POD profile=$PROFILE ==="

# Self-ping on mlx5_0 port 1 (LID 1 in the default render layout).
OUT=$(kubectl exec "$POD" -- ibping -c 1 -C mlx5_0 -P 1 1 2>&1) || {
  echo "FAIL: ibping self-ping exited non-zero"
  printf '%s\n' "$OUT"
  exit 1
}
printf '%s\n' "$OUT"

if ! printf '%s\n' "$OUT" | grep -q "Pong from"; then
  echo "FAIL: ibping self-ping output missing 'Pong from'"
  exit 1
fi
if ! printf '%s\n' "$OUT" | grep -q "1 packets transmitted, 1 received"; then
  echo "FAIL: ibping self-ping did not report 1/1 packets"
  exit 1
fi
echo "PASS: ibping self-ping on mlx5_0 (LID 1)"

if [ "$EXPECTED" -ge 2 ]; then
  # Cross-HCA: server on mlx5_1 (LID 2), client on mlx5_0 pinging remote LID 2.
  # Local LID loopback must not satisfy this path.
  kubectl exec "$POD" -- sh -c '
    set -e
    rm -rf /var/lib/nvml-mock/ib/umad-bus/in/* /var/lib/nvml-mock/ib/umad-bus/out/* 2>/dev/null || true
    ibping -S -C mlx5_1 -P 1 >/tmp/ibping-server.log 2>&1 &
    srv=$!
    sleep 2
    ibping -c 1 -C mlx5_0 -P 1 2 >/tmp/ibping-client.log 2>&1
    kill $srv 2>/dev/null || true
    wait $srv 2>/dev/null || true
    grep -q "Pong from" /tmp/ibping-client.log
    ! grep -qiE "iberror|failed: Failed to open|can'\''t serve" /tmp/ibping-server.log
  '
  echo "PASS: ibping cross-HCA ping mlx5_0 -> LID 2 (server on mlx5_1)"
fi

echo "=== ibping validation PASSED ==="
