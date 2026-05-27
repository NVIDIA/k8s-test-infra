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

# The mock fabric is a point-to-point topology (no switches): each local CA
# picks ONE outbound neighbor — the lowest-GUID non-self port in the merged
# graph (see fabric.OutboundNeighbor). For a control-plane pod, that ends up
# being whichever worker pod happened to claim the lowest GUID range, which
# may not be $PEER_POD. So instead of asserting "iblinkinfo finds $PEER_POD's
# GUID specifically", we assert the stronger property we actually care about:
# iblinkinfo's fabric scan finds AT LEAST ONE non-local CA (i.e. cross-pod
# visibility works at all).
#
# Local pod's own port GUIDs come from the rendered mock sysfs tree
# (/var/lib/nvml-mock/ib/sys/class/infiniband/*/ports/1/port_guid). Any GUID
# in iblinkinfo's output that is not in this set is, by construction, a
# peer-pod port.
LOCAL_GUIDS=$(kubectl exec "$LOCAL_POD" -- sh -c '
  for f in /var/lib/nvml-mock/ib/sys/class/infiniband/*/ports/1/port_guid; do
    [ -r "$f" ] && cat "$f"
  done' 2>/dev/null | tr -d ':[:space:]' | tr 'A-F' 'a-f' | sort -u)
if [ -z "$LOCAL_GUIDS" ]; then
  echo "FAIL: could not enumerate local port GUIDs from sysfs on $LOCAL_POD"
  exit 1
fi

# Read the announced peer GUID too, so we can log whether the deterministic
# topology happened to land on $PEER_POD (informational, not an assertion).
PEER_GUID=$(kubectl exec "$PEER_POD" -- sh -c 'ibstat mlx5_0 2>/dev/null | grep "Port GUID" | head -1' || true)
PEER_GUID=$(echo "$PEER_GUID" | awk '{print $NF}' | tr -d ':' | tr 'A-F' 'a-f')
PEER_GUID=${PEER_GUID#0x}

OUT=$(kubectl exec "$LOCAL_POD" -- iblinkinfo 2>&1)
echo "--- iblinkinfo ---"
printf '%s\n' "$OUT"

# Extract every 16-hex-digit port GUID iblinkinfo printed (0x prefix, possibly
# uppercase) and lower-case + strip 0x for comparison against $LOCAL_GUIDS.
FOUND_GUIDS=$(printf '%s\n' "$OUT" \
  | grep -oiE '0x[0-9a-f]{16}' \
  | tr 'A-F' 'a-f' \
  | sed 's/^0x//' \
  | sort -u)
if [ -z "$FOUND_GUIDS" ]; then
  echo "FAIL: iblinkinfo on $LOCAL_POD printed no port GUIDs"
  exit 1
fi

CROSS_POD=""
for g in $FOUND_GUIDS; do
  if ! printf '%s\n' "$LOCAL_GUIDS" | grep -qx "$g"; then
    CROSS_POD="$g"
    break
  fi
done

if [ -z "$CROSS_POD" ]; then
  echo "FAIL: iblinkinfo on $LOCAL_POD found only local GUIDs ($FOUND_GUIDS), no cross-pod peer"
  exit 1
fi

if [ "$CROSS_POD" = "$PEER_GUID" ]; then
  echo "OK: iblinkinfo scan from $LOCAL_POD reached requested peer $PEER_POD ($CROSS_POD)"
else
  echo "OK: iblinkinfo scan from $LOCAL_POD reached cross-pod peer $CROSS_POD"
  echo "     (requested $PEER_POD=$PEER_GUID; mock fabric is point-to-point lowest-GUID, "
  echo "      see fabric.OutboundNeighbor — cross-pod visibility is the assertion)"
fi

echo "=== iblinkinfo validation PASSED ==="
