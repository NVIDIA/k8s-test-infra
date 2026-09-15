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
if grep -qiE "Legend|NV# =" <<< "$TOPO"; then
  echo "PASS: topo -m printed a legend"
else
  echo "FAIL: topo -m did not print a legend"
  exit 1
fi

# CPU/NUMA affinity columns must be present in the matrix header.
if grep -qiE "CPU Affinity|NUMA Affinity" <<< "$TOPO"; then
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
  # Truncate for log noise with sed (not head): an 8-GPU x 18-link dump is far
  # longer than 10 lines, and `echo ... | head -10` makes head close the pipe
  # early, so echo takes SIGPIPE -> with `set -o pipefail` the pipeline fails
  # and `set -e` aborts the whole script. sed consumes all input, so echo never
  # hits a broken pipe.
  echo "$NVLINK_S" | sed -n '1,10p'
  # Use a here-string, not `echo ... | grep -q`: grep -q exits on first match
  # and closes the pipe, so echo takes SIGPIPE -> under `set -o pipefail` the
  # pipeline (hence the `if`) reports failure even though the match succeeded.
  if grep -qE "Link[[:space:]]+0" <<< "$NVLINK_S"; then
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
  # sed, not head: see the nvlink -s note above (avoids SIGPIPE under pipefail).
  echo "$NVLINK_C" | sed -n '1,10p'
  # here-string (see nvlink -s note): avoids the grep -q SIGPIPE/pipefail race.
  if grep -qE "Link[[:space:]]+0" <<< "$NVLINK_C"; then
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

# ---------------------------------------------------------------------------
# NVLink Reduced Bandwidth Mode.
#
# Which gate applies depends on which NVML call nvidia-smi actually makes, and
# for -gBwMode/-sBwMode that is the SYSTEM pair (nvmlSystemGet/SetNvlinkBwMode),
# not the per-device trio. The system pair is gated on Hopper and newer, so
# h100 legitimately reports a mode here — the negative control has to be a
# pre-Hopper profile (a100, t4, l40s) or it proves nothing. The per-device
# bandwidth-mode calls are not reachable through this nvidia-smi at all; unit
# tests are their only coverage.
#
# Mode names come from the bundled nvidia-smi's own table, and the engine never
# emits an index above 4 because a higher one would read past it:
#   0=FULL 1=OFF 2=MIN 3=HALF 4=3QUARTER
# ---------------------------------------------------------------------------
case "$GPU_PROFILE" in
  h100 | b200 | gb200 | gb300) EXPECT_BWMODE=1 ;;
  *) EXPECT_BWMODE=0 ;;
esac

# nvlink --info is per-device, so it needs Blackwell AND a real NVLink fabric.
# b200 is standalone: it has no links, so nvidia-smi prints nothing at all for
# it rather than an NVLE row.
if [ "$EXPECT_BWMODE" -eq 1 ] && [ "$EXPECT_NV" -gt 0 ] && [ "$GPU_PROFILE" != "h100" ]; then
  EXPECT_NVLE=1
else
  EXPECT_NVLE=0
fi

echo ""
echo "--- nvidia-smi nvlink -gBwMode (bandwidth mode) ---"
# `|| true`: below the gate nvidia-smi exits non-zero after printing "not
# supported", which is the expected outcome there, so the exit code is not the
# signal we assert on.
BWMODE=$(docker exec "$NODE_CONTAINER" sh -c "$NVIDIA_SMI nvlink -gBwMode" 2>&1 || true)
echo "$BWMODE" | sed -n '1,10p'

if [ "$EXPECT_BWMODE" -eq 1 ]; then
  if grep -qE "bandwidth mode: (FULL|OFF|MIN|HALF|3QUARTER)" <<< "$BWMODE"; then
    echo "PASS: nvlink -gBwMode reported a named bandwidth mode"
  else
    echo "FAIL: profile '$GPU_PROFILE' did not report a bandwidth mode."
    echo "      Check nvmlSystemGetNvlinkBwMode (bridge/nvlink_bw_mode.go) and the"
    echo "      Hopper gate in engine/nvlink_bw_mode.go."
    exit 1
  fi

  # Every name in the table, not just one: this is what pins the index mapping
  # end to end, since nvidia-smi resolves the name to the index it sends.
  echo "--- nvidia-smi nvlink -sBwMode <each named mode> ---"
  for MODE in FULL OFF MIN HALF 3QUARTER; do
    BWSET=$(docker exec "$NODE_CONTAINER" sh -c "$NVIDIA_SMI nvlink -sBwMode $MODE" 2>&1 || true)
    if grep -qiE "Successfully set nvlink bandwidth mode" <<< "$BWSET"; then
      echo "PASS: nvlink -sBwMode $MODE accepted"
    else
      echo "FAIL: profile '$GPU_PROFILE' rejected supported bandwidth mode '$MODE'."
      echo "      Got: $BWSET"
      echo "      Check nvmlSystemSetNvlinkBwMode and the supported list default"
      echo "      (defaultNvlinkBwModes = 0..4 in engine/nvlink_bw_mode.go)."
      exit 1
    fi
  done
  # NOTE: deliberately no set-then-get round trip. Setter state is
  # process-local and nvidia-smi dlopens a fresh mock per invocation, so the
  # new mode is gone by the next call. Cross-process persistence is #849.
else
  if grep -qiE "not supported" <<< "$BWMODE"; then
    echo "PASS: pre-Hopper profile '$GPU_PROFILE' reports bandwidth mode as not supported"
  else
    echo "FAIL: pre-Hopper profile '$GPU_PROFILE' must not claim bandwidth-mode support."
    echo "      The architecture gate in engine/nvlink_bw_mode.go leaked."
    exit 1
  fi
fi

echo ""
echo "--- nvidia-smi nvlink --info (NVLE) ---"
NVINFO=$(docker exec "$NODE_CONTAINER" sh -c "$NVIDIA_SMI nvlink --info" 2>&1 || true)
echo "$NVINFO" | sed -n '1,10p'
if [ "$EXPECT_NVLE" -eq 1 ]; then
  if grep -qE "NVLE:" <<< "$NVINFO"; then
    echo "PASS: nvlink --info reported the NVLE row"
  else
    echo "FAIL: nvlink --info did not report NVLE — check nvmlDeviceGetNvLinkInfo."
    exit 1
  fi
else
  # Below Blackwell this is NVML_ERROR_FUNCTION_NOT_FOUND from the driver-version
  # registry rather than the architecture gate — the profile's driver predates
  # the call. Either way nvidia-smi must not print an NVLE row.
  if grep -qE "NVLE:" <<< "$NVINFO"; then
    echo "FAIL: profile '$GPU_PROFILE' must not report NVLE."
    echo "      The Blackwell gate or the version registry leaked."
    exit 1
  fi
  echo "PASS: profile '$GPU_PROFILE' does not report NVLE"
fi

# Low-power threshold is Hopper+ and per-device, so it needs links too.
if [ "$EXPECT_BWMODE" -eq 1 ] && [ "$EXPECT_NV" -gt 0 ]; then
  echo ""
  echo "--- nvidia-smi nvlink -gLowPwrInfo / -sLowPwrThres ---"
  LOWPWR=$(docker exec "$NODE_CONTAINER" sh -c "$NVIDIA_SMI nvlink -gLowPwrInfo" 2>&1 || true)
  echo "$LOWPWR" | sed -n '1,6p'
  if grep -qiE "Low Power Threshold" <<< "$LOWPWR"; then
    echo "PASS: nvlink -gLowPwrInfo reported the threshold range"
  else
    echo "FAIL: nvlink -gLowPwrInfo did not report the threshold range."
    exit 1
  fi

  LOWSET=$(docker exec "$NODE_CONTAINER" sh -c "$NVIDIA_SMI nvlink -sLowPwrThres 500" 2>&1 || true)
  echo "$LOWSET" | sed -n '1,4p'
  if grep -qiE "Low Power Threshold set to" <<< "$LOWSET"; then
    echo "PASS: nvlink -sLowPwrThres accepted an in-range threshold"
  else
    echo "FAIL: nvlink -sLowPwrThres rejected an in-range threshold."
    echo "      Check SetMockNvLinkLowPowerThreshold (range 1..1023)."
    exit 1
  fi

  # `default` sends NVML_NVLINK_LOW_POWER_THRESHOLD_RESET (0xFFFFFFFF), which
  # must clear the override rather than fail range validation.
  LOWRESET=$(docker exec "$NODE_CONTAINER" sh -c "$NVIDIA_SMI nvlink -sLowPwrThres default" 2>&1 || true)
  echo "$LOWRESET" | sed -n '1,4p'
  if grep -qiE "reset successfully" <<< "$LOWRESET"; then
    echo "PASS: nvlink -sLowPwrThres default reset the threshold"
  else
    echo "FAIL: nvlink -sLowPwrThres default did not reset the threshold."
    echo "      The RESET sentinel (0xFFFFFFFF) must bypass range validation."
    exit 1
  fi
fi

echo ""
echo "=== NVLink validation PASSED ==="
