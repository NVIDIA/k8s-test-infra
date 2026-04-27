#!/bin/bash
# Copyright 2026 NVIDIA CORPORATION
# SPDX-License-Identifier: Apache-2.0
#
# Validate the InfiniBand sysfs mock by running real ibstat / ibstatus
# inside the nvml-mock DaemonSet pod and asserting on the output.
#
# Usage:
#   validate-ibstat.sh <pod-name> <profile> <expected-hcas>
#
# Profiles that ship with infiniband.enabled=false (l40s, t4) are expected
# to report zero HCAs regardless of <expected-hcas>.
set -euo pipefail

POD="${1:?Usage: $0 <pod-name> <profile> <expected-hcas>}"
PROFILE="${2:?}"
EXPECTED="${3:?}"

# Profiles where the bundled defaults disable IB. Keep in sync with
# deployments/nvml-mock/helm/nvml-mock/profiles/*.yaml.
case "$PROFILE" in
  l40s|t4) EXPECTED=0 ;;
esac

echo "=== Validating ibstat on pod=$POD profile=$PROFILE expected=$EXPECTED ==="

# 1. ibstat -l must list one mlx5_<n> per HCA.
LIST=$(kubectl exec "$POD" -- ibstat -l 2>&1 || true)
echo "--- ibstat -l ---"
printf '%s\n' "$LIST"
ACTUAL=$(printf '%s\n' "$LIST" | grep -c '^mlx' || true)
if [ "$ACTUAL" -ne "$EXPECTED" ]; then
  echo "FAIL: ibstat -l reported $ACTUAL HCAs, expected $EXPECTED"
  exit 1
fi
echo "PASS: $ACTUAL HCAs listed by ibstat -l"

if [ "$EXPECTED" -eq 0 ]; then
  echo "=== ibstat validation PASSED (IB disabled for $PROFILE) ==="
  exit 0
fi

# 2. ibstatus must show every port in state ACTIVE.
STATUS=$(kubectl exec "$POD" -- ibstatus 2>&1)
echo "--- ibstatus ---"
printf '%s\n' "$STATUS"
ACTIVE=$(printf '%s\n' "$STATUS" | grep -Ec 'state:[[:space:]]+[0-9]+:[[:space:]]+ACTIVE' || true)
if [ "$ACTIVE" -ne "$EXPECTED" ]; then
  echo "FAIL: ibstatus shows $ACTIVE ACTIVE ports, expected $EXPECTED"
  exit 1
fi
echo "PASS: $ACTIVE ports ACTIVE"

# 3. ibstat (no args) must contain a CA section per HCA.
FULL=$(kubectl exec "$POD" -- ibstat 2>&1)
CAS=$(printf '%s\n' "$FULL" | grep -c "^CA '" || true)
if [ "$CAS" -ne "$EXPECTED" ]; then
  echo "FAIL: ibstat reported $CAS CA sections, expected $EXPECTED"
  printf '%s\n' "$FULL"
  exit 1
fi
echo "PASS: $CAS CA sections in ibstat output"

echo "=== ibstat validation PASSED ==="
