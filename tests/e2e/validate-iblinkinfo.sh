#!/bin/bash
# Copyright 2026 NVIDIA CORPORATION
# SPDX-License-Identifier: Apache-2.0
#
# Validate iblinkinfo fabric scan (requires mock-ib fabric ready + peer pods).
#
# Usage:
#   validate-iblinkinfo.sh <local-pod> <peer-pod> <profile> <expected-hcas>
set -euo pipefail

LOCAL_POD="${1:?Usage: $0 <local-pod> <peer-pod> <profile> <expected-hcas>}"
PEER_POD="${2:?}"
PROFILE="${3:?}"
EXPECTED="${4:?}"

case "$PROFILE" in
  l40s|t4) EXPECTED=0 ;;
esac

echo "=== Validating iblinkinfo local=$LOCAL_POD peer=$PEER_POD profile=$PROFILE ==="

if [ "$EXPECTED" -eq 0 ]; then
  echo "SKIP: IB disabled for profile $PROFILE"
  exit 0
fi

for P in "$LOCAL_POD" "$PEER_POD"; do
  if ! kubectl logs "$P" 2>/dev/null | grep -q 'fabric ready'; then
    echo "WARN: pod $P logs do not show 'fabric ready' yet"
  fi
done

PEER_GUID=$(kubectl exec "$PEER_POD" -- sh -c 'ibstat mlx5_0 2>/dev/null | grep "Port GUID" | head -1' || true)
PEER_GUID=$(echo "$PEER_GUID" | awk '{print $NF}' | tr -d ':')
PEER_GUID=${PEER_GUID#0x}
if [ -z "$PEER_GUID" ]; then
  echo "FAIL: could not read peer port GUID from ibstat"
  exit 1
fi

OUT=$(kubectl exec "$LOCAL_POD" -- iblinkinfo 2>&1)
echo "--- iblinkinfo ---"
printf '%s\n' "$OUT"
if ! printf '%s\n' "$OUT" | grep -qi "$PEER_GUID"; then
  echo "FAIL: iblinkinfo on $LOCAL_POD did not show peer GUID $PEER_GUID"
  exit 1
fi

echo "=== iblinkinfo validation PASSED ==="
