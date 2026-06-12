#!/bin/bash
# Copyright 2026 NVIDIA CORPORATION
# SPDX-License-Identifier: Apache-2.0
#
# Validate NVLink / NVSwitch topology exposed by mock NVML on a Kind node.
# Mirrors validate-nvidia-smi.sh: runs the host-driver-root nvidia-smi inside
# the Kind node container via `docker exec`.
#
# Usage: validate-nvlink.sh <node-container-name> <gpu-profile> <gpu-count>
#
# Assertions:
#   * `nvidia-smi topo -m` runs and prints the legend + a CPU/NUMA Affinity
#     header (the headline acceptance criterion of #371).
#   * NVSwitch/NVLink profiles show the EXACT expected NV# between every GPU
#     pair (a100 -> NV12, h100/gb200/gb300 -> NV18). A partially-populated
#     matrix, a wrong count (e.g. NV1), or a missing matrix all FAIL — this is
#     the profile-specific acceptance criterion, not just "some NV# present".
#   * Non-NVLink profiles (b200 standalone, t4, l40s) show NO NV# links
#     (negative control — fabric/NVSwitch must not leak).
#   * NVLink profiles MUST enumerate links via `nvidia-smi nvlink -s` and
#     `-c` (Link 0 present). nvidia-smi reads NVML_FI_DEV_NVLINK_LINK_COUNT
#     through nvmlDeviceGetFieldValues to size the list; the mock implements
#     that field set (engine/nvlink_fields.go), so empty output is a FAIL.
#   * Best-effort: NVLink throughput counters are sampled twice via
#     `nvlink -gt d` and asserted non-decreasing IF the bundled nvidia-smi
#     surfaces them; otherwise SKIPPED (the `-gt d` reporting path varies by
#     driver build).
#
# NOTE: the GPU x GPU NV# matrix is derived by nvidia-smi from the per-link
# remote-device-type/state getters plus the NVLink field values the mock now
# implements; on NVSwitch platforms every GPU link terminates at a switch and
# nvidia-smi fans those to all peers. The authoritative, driver-independent
# guards are the engine unit tests (TestNodeFabric_BuiltinProfiles for the NV#
# matrix, TestGetNvLinkFieldValue_* for the field-value surface).
set -euo pipefail

NODE_CONTAINER="${1:?Usage: $0 <node-container> <gpu-profile> <gpu-count>}"
GPU_PROFILE="${2:?}"
GPU_COUNT="${3:?}"

NVIDIA_SMI="/var/lib/nvml-mock/driver/usr/bin/nvidia-smi"

# Expected NV# count per profile (0 == no NVLink fabric; negative control).
# Mirrors engine TestNodeFabric_BuiltinProfiles.
case "$GPU_PROFILE" in
  a100) EXPECT_NV=12 ;;          # DGX A100: 6 NVSwitches -> NV12 all-to-all
  h100 | gb200 | gb300) EXPECT_NV=18 ;; # HGX/NVL: 4 NVSwitches -> NV18
  *) EXPECT_NV=0 ;;              # b200 (standalone), t4, l40s, ...
esac

echo "=== Validating NVLink topology on $NODE_CONTAINER (profile=$GPU_PROFILE, expect_nv=$EXPECT_NV) ==="

echo "--- nvidia-smi topo -m ---"
TOPO=$(docker exec "$NODE_CONTAINER" sh -c "$NVIDIA_SMI topo -m" 2>&1) || {
  echo "FAIL: nvidia-smi topo -m exited with error"
  echo "$TOPO"
  exit 1
}
echo "$TOPO"

# Legend is always printed by topo -m.
if echo "$TOPO" | grep -qiE "Legend|NV# ="; then
  echo "PASS: topo -m printed a legend"
else
  echo "FAIL: topo -m did not print a legend"
  exit 1
fi

# CPU/NUMA affinity columns must be present in the matrix header.
if echo "$TOPO" | grep -qiE "CPU Affinity|NUMA Affinity"; then
  echo "PASS: topo -m shows CPU/NUMA Affinity columns"
else
  echo "FAIL: topo -m missing CPU/NUMA Affinity columns"
  exit 1
fi

# NV# cells in the GPU x GPU submatrix (e.g. NV12, NV18). topo -m can also
# list NIC<n> columns: the Kind node inherits the runner host's RDMA devices,
# and the 580 nvidia-smi fans a GPU's NVSwitch-connected link count across its
# whole row, so a GPU-NIC cell can read NV# too. Those NIC columns are an
# environmental artifact, not part of the GPU NVLink fabric under test, so we
# restrict counting to the first GPU_COUNT data columns. In a "GPU<n>" row,
# awk field $1 is the row label and $2..$(1+GPU_COUNT) are the GPU columns
# (the diagonal is "X"); NIC and CPU/NUMA Affinity columns come after.
NV_TOKENS=$(echo "$TOPO" | awk -v n="$GPU_COUNT" '
  /^GPU[0-9]/ { for (i = 2; i <= 1 + n; i++) if ($i ~ /^NV[0-9]+$/) print $i }')
NV_DISTINCT=$(echo "$NV_TOKENS" | sed '/^$/d' | sort -u | tr '\n' ' ' | sed 's/ *$//')
NV_CELL_COUNT=$(echo "$NV_TOKENS" | sed '/^$/d' | grep -c . || true)
EXPECTED_OFFDIAG=$((GPU_COUNT * (GPU_COUNT - 1)))

if [ "$EXPECT_NV" -gt 0 ]; then
  WANT_TOKEN="NV$EXPECT_NV"
  if [ -z "$NV_DISTINCT" ]; then
    echo "FAIL: profile '$GPU_PROFILE' expected $WANT_TOKEN between every GPU pair, found NO NV# in topo -m."
    echo "      The 580 nvidia-smi builds the NV# matrix from"
    echo "      NVML_FI_DEV_NVSWITCH_CONNECTED_LINK_COUNT (field 147) per GPU; if that"
    echo "      returns NOT_SUPPORTED the matrix collapses to PIX. Check"
    echo "      engine/nvlink_fields.go (fiNvswitchConnectedLinkCount) and the fabric's"
    echo "      switch-attached link expansion."
    exit 1
  fi
  if [ "$NV_DISTINCT" != "$WANT_TOKEN" ]; then
    echo "FAIL: profile '$GPU_PROFILE' expected uniform $WANT_TOKEN, got distinct values: $NV_DISTINCT"
    exit 1
  fi
  if [ "$NV_CELL_COUNT" -ne "$EXPECTED_OFFDIAG" ]; then
    echo "FAIL: profile '$GPU_PROFILE' expected $EXPECTED_OFFDIAG off-diagonal $WANT_TOKEN cells (full matrix), got $NV_CELL_COUNT"
    exit 1
  fi
  echo "PASS: every GPU pair shows $WANT_TOKEN ($NV_CELL_COUNT/$EXPECTED_OFFDIAG cells)"
else
  if [ -n "$NV_DISTINCT" ]; then
    echo "FAIL: non-NVLink profile '$GPU_PROFILE' leaked NV# links: $NV_DISTINCT"
    exit 1
  fi
  echo "PASS: non-NVLink profile '$GPU_PROFILE' shows no NV# links (negative control)"
fi

# ---------------------------------------------------------------------------
# Per-link status / capabilities. nvidia-smi enumerates NVLinks by reading
# NVML_FI_DEV_NVLINK_LINK_COUNT via nvmlDeviceGetFieldValues first; if that is
# unimplemented it concludes the GPU has 0 links and prints nothing. The mock
# now implements that field (engine/nvlink_fields.go), so for NVLink profiles
# `nvlink -s` and `-c` MUST enumerate links — empty output is the regression
# this asserts against.
# ---------------------------------------------------------------------------
if [ "$EXPECT_NV" -gt 0 ]; then
  echo ""
  echo "--- nvidia-smi nvlink -s (status) ---"
  NVLINK_S=$(docker exec "$NODE_CONTAINER" sh -c "$NVIDIA_SMI nvlink -s" 2>&1) || {
    echo "FAIL: nvidia-smi nvlink -s exited with error"; echo "$NVLINK_S"; exit 1; }
  echo "$NVLINK_S" | head -10
  if echo "$NVLINK_S" | grep -qE "Link[[:space:]]+0"; then
    echo "PASS: nvlink -s enumerated links (Link 0 present)"
  else
    echo "FAIL: nvlink -s printed no links for NVLink profile '$GPU_PROFILE'."
    echo "      nvidia-smi could not read NVML_FI_DEV_NVLINK_LINK_COUNT — check"
    echo "      nvmlDeviceGetFieldValues (bridge/fieldvalues.go, engine/nvlink_fields.go)."
    exit 1
  fi

  echo "--- nvidia-smi nvlink -c (capabilities) ---"
  NVLINK_C=$(docker exec "$NODE_CONTAINER" sh -c "$NVIDIA_SMI nvlink -c" 2>&1) || {
    echo "FAIL: nvidia-smi nvlink -c exited with error"; echo "$NVLINK_C"; exit 1; }
  echo "$NVLINK_C" | head -10
  if echo "$NVLINK_C" | grep -qE "Link[[:space:]]+0"; then
    echo "PASS: nvlink -c reported per-link capabilities (Link 0 present)"
  else
    echo "FAIL: nvlink -c printed no capabilities for NVLink profile '$GPU_PROFILE'."
    echo "      See nvmlDeviceGetFieldValues (NVML_FI_DEV_NVLINK_LINK_COUNT)."
    exit 1
  fi

  # Counter growth: sample throughput counters twice, ~1s apart, and assert
  # the total is non-decreasing IF any counters are surfaced.
  echo ""
  echo "--- NVLink counter growth (diagnostic) ---"
  sum_counters() {
    docker exec "$NODE_CONTAINER" sh -c "$NVIDIA_SMI nvlink -gt d" 2>/dev/null |
      grep -oE "[0-9]+" | awk '{s+=$1} END {print s+0}'
  }
  S1=$(sum_counters || echo 0)
  sleep 1
  S2=$(sum_counters || echo 0)
  echo "counter sums: t0=$S1 t1=$S2"
  if [ "$S1" = "0" ] && [ "$S2" = "0" ]; then
    echo "SKIP: bundled nvidia-smi did not surface NVLink throughput counters via 'nvlink -gt d' (reporting path varies by driver build)"
  elif [ "$S2" -ge "$S1" ]; then
    echo "PASS: NVLink counters are non-decreasing ($S1 -> $S2)"
  else
    echo "FAIL: NVLink counters decreased ($S1 -> $S2) — not monotonic"
    exit 1
  fi
fi

echo ""
echo "=== NVLink validation PASSED ==="
