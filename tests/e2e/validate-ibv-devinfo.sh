#!/bin/bash
# Copyright 2026 NVIDIA CORPORATION
# SPDX-License-Identifier: Apache-2.0
#
# Validate that ibv_devinfo (and the broader libibverbs userspace stack)
# can discover the mock InfiniBand HCAs inside an nvml-mock pod.
#
# Discovery uses libibverbs:
#   1. enumerate via /sys/class/infiniband_verbs/* + ibverbs-providers
#      (libmlx5 matches our rendered modalias and claims each mlx5_N).
# Per-port info still comes from libibumad/sysfs via ibstatus, because
# `ibv_devinfo` (full) ends up calling libmlx5's verbs_open_device which
# issues real uverbs ioctl()s on /dev/infiniband/uverbsN that the
# userspace LD_PRELOAD shim does not intercept. The information ibstatus
# prints is the same set of fields ibv_devinfo would surface on real
# hardware (device name, port state, phys state, GID, LID, rate, link
# layer).
#
# Usage:
#   validate-ibv-devinfo.sh <pod-name> <profile> <expected-hcas>
set -euo pipefail

POD="${1:?Usage: $0 <pod-name> <profile> <expected-hcas>}"
PROFILE="${2:?}"
EXPECTED="${3:?}"

case "$PROFILE" in
  l40s|t4) EXPECTED=0 ;;
esac

echo "=== Validating ibv_devinfo on pod=$POD profile=$PROFILE expected=$EXPECTED ==="

if [ "$EXPECTED" -eq 0 ]; then
  echo "SKIP: IB disabled for profile $PROFILE"
  exit 0
fi

# 1) HCA enumeration via libibverbs (ibv_devinfo -l + ibv_devices). This
#    confirms the rendered modalias is in the exact kernel grammar that
#    libmlx5's verbs_match_ent match table fnmatches against, and that
#    ibverbs-providers is installed inside the image.
LIST=$(kubectl exec "$POD" -- ibv_devinfo -l 2>&1)
echo "--- ibv_devinfo -l ---"
printf '%s\n' "$LIST"
ACTUAL=$(printf '%s\n' "$LIST" | awk '/^[[:space:]]+mlx5_/ {print $1}' | wc -l)
if [ "$ACTUAL" -ne "$EXPECTED" ]; then
  echo "FAIL: ibv_devinfo -l reported $ACTUAL devices, expected $EXPECTED"
  echo "=== DEBUG: rendered sysfs tree under MOCK_IB_ROOT ==="
  kubectl exec "$POD" -- sh -c '
    set +e
    echo "--- ls /var/lib/nvml-mock/ib/sys/class/infiniband/ ---"
    ls -la /var/lib/nvml-mock/ib/sys/class/infiniband/ 2>&1
    echo "--- ls /var/lib/nvml-mock/ib/sys/class/infiniband_verbs/ ---"
    ls -la /var/lib/nvml-mock/ib/sys/class/infiniband_verbs/ 2>&1
    echo "--- ls /var/lib/nvml-mock/ib/dev/infiniband/ ---"
    ls -la /var/lib/nvml-mock/ib/dev/infiniband/ 2>&1
    for u in /var/lib/nvml-mock/ib/sys/class/infiniband_verbs/uverbs*; do
      [ -d "$u" ] || continue
      echo "--- $u ---"
      ls "$u"
      for f in ibdev dev abi_version; do
        printf "  %s = " "$f"; cat "$u/$f" 2>&1 || true
      done
    done
    for d in /var/lib/nvml-mock/ib/sys/class/infiniband/mlx5_*; do
      [ -d "$d" ] || continue
      echo "--- $d/{node_type,device/modalias} ---"
      printf "  node_type = "; cat "$d/node_type" 2>&1 || true
      printf "  device/modalias = "; cat "$d/device/modalias" 2>&1 || true
    done
  ' || true
  echo "=== DEBUG: libibverbs verbose enumeration ==="
  kubectl exec "$POD" -- sh -c 'IBV_SHOW_WARNINGS=1 VERBS_LOG_LEVEL=3 ibv_devinfo -l 2>&1' || true
  kubectl exec "$POD" -- sh -c 'IBV_SHOW_WARNINGS=1 VERBS_LOG_LEVEL=3 ibv_devices 2>&1' || true
  echo "=== DEBUG: raw kernel view (MOCK_IB_DISABLE=1, bypasses libibmocksys) ==="
  kubectl exec "$POD" -- sh -c '
    set +e
    echo "--- ls /sys/class/infiniband (real kernel) ---"
    MOCK_IB_DISABLE=1 ls /sys/class/infiniband/ 2>&1
    echo "--- ls /sys/class/infiniband_verbs (real kernel) ---"
    MOCK_IB_DISABLE=1 ls /sys/class/infiniband_verbs/ 2>&1
    echo "--- ls /dev/infiniband (real kernel) ---"
    MOCK_IB_DISABLE=1 ls /dev/infiniband/ 2>&1
    echo "--- MOCK_IB_DISABLE=1 ibv_devinfo -l ---"
    MOCK_IB_DISABLE=1 ibv_devinfo -l 2>&1
  ' || true
  exit 1
fi
echo "PASS: ibv_devinfo -l listed $ACTUAL devices"

DEVICES=$(kubectl exec "$POD" -- ibv_devices 2>&1)
echo "--- ibv_devices ---"
printf '%s\n' "$DEVICES"
if ! printf '%s\n' "$DEVICES" | grep -qE '^[[:space:]]+mlx5_0[[:space:]]+[0-9a-f]{16}'; then
  echo "FAIL: ibv_devices did not list mlx5_0 with a node GUID"
  exit 1
fi

# 2) Per-port info via libibumad/sysfs (state / phys / GID / LID / rate).
FULL=$(kubectl exec "$POD" -- ibstatus 2>&1)
echo "--- ibstatus ---"
printf '%s\n' "$FULL"
ACTIVE_PORTS=$(printf '%s\n' "$FULL" | grep -cE 'state:[[:space:]]+4: ACTIVE' || true)
if [ "$ACTIVE_PORTS" -lt "$EXPECTED" ]; then
  echo "FAIL: ibstatus reports $ACTIVE_PORTS ACTIVE ports, expected at least $EXPECTED"
  exit 1
fi
if ! printf '%s\n' "$FULL" | grep -qE 'phys state:[[:space:]]+5: LinkUp'; then
  echo "FAIL: ibstatus output missing 'phys state: 5: LinkUp'"
  exit 1
fi
echo "PASS: $ACTIVE_PORTS ports ACTIVE / LinkUp"

echo "=== ibv_devinfo validation PASSED ==="
