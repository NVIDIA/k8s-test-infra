#!/bin/bash
# Copyright 2026 NVIDIA CORPORATION
# SPDX-License-Identifier: Apache-2.0
#
# Spike: run real DCGM (nv-hostengine + dcgmi) and dcgm-exporter against the
# mock NVML library, capturing every [NVML-STUB] hit so we know exactly which
# NVML entry points DCGM needs beyond what the mock implements.
# Usage: cd <repo-root> && ./tests/e2e/spike-dcgm.sh
#
# The mock library is built natively for the host docker architecture (both
# the DCGM and dcgm-exporter images are multi-arch amd64/arm64), so no QEMU
# emulation is needed. libdcgm dlopen()s "libnvidia-ml.so.1", which honors
# LD_LIBRARY_PATH, so the mock is injected by mounting a lib dir and setting
# the env var — same recipe as docs/examples.md "Testing DCGM".
set -euo pipefail

# DCGM 3.3.9 matches the dcgm-exporter bundled with GPU Operator v24.9.x
# (the version pinned in tests/e2e/VERSION-MATRIX.md).
DCGM_IMAGE=${DCGM_IMAGE:-nvcr.io/nvidia/cloud-native/dcgm:3.3.9-1-ubuntu22.04}
EXPORTER_IMAGE=${EXPORTER_IMAGE:-nvcr.io/nvidia/k8s/dcgm-exporter:3.3.9-3.6.1-ubuntu22.04}
PROFILE=${PROFILE:-deployments/nvml-mock/helm/nvml-mock/profiles/h100.yaml}

REPO_ROOT=$(git rev-parse --show-toplevel)
cd "$REPO_ROOT"

case "$PROFILE" in
  /*) PROFILE_PATH="$PROFILE" ;;
  *) PROFILE_PATH="$REPO_ROOT/$PROFILE" ;;
esac
GPU_COUNT=$(awk '/^devices:/{f=1;next} /^[^[:space:]]/{f=0} f&&/^[[:space:]]*-[[:space:]]*index:/{c++} END{print c+0}' "$PROFILE_PATH")
if [ -z "$GPU_COUNT" ] || [ "$GPU_COUNT" -lt 1 ]; then
  echo "FAIL: could not derive GPU count from $PROFILE_PATH"
  exit 1
fi
PROFILE_NAME=$(basename "$PROFILE_PATH" .yaml)
case "$PROFILE_NAME" in
  h100|b200|gb200|gb300) HOPPER_PLUS=1 ;;
  *) HOPPER_PLUS=0 ;;
esac

# Keep artifacts inside the repo: on Docker Desktop (macOS) only shared paths
# (/Users, ...) are bind-mountable; /tmp mounts silently arrive empty.
OUT_DIR=${OUT_DIR:-$REPO_ROOT/.spike-dcgm}

LIB_DIR="$OUT_DIR/lib"
mkdir -p "$OUT_DIR" "$LIB_DIR"

if [ "${SKIP_BUILD:-0}" != "1" ]; then
  echo "=== Building mock NVML + CUDA libraries (native arch, in container) ==="
  make -C pkg/gpu/mocknvml docker-build
  # dcgmi diag's nvvs plugins dlopen libcuda.so.1; reuse the builder image.
  docker run --rm \
    -e GOCACHE=/tmp/.cache/go -e GOMODCACHE=/tmp/.cache/gomod \
    -e GOFLAGS=-buildvcs=false \
    -v "$REPO_ROOT:/work" -w /work/pkg/gpu/mockcuda \
    --user "$(id -u):$(id -g)" \
    mock-nvml-builder make
fi

cp pkg/gpu/mocknvml/libnvidia-ml.so.550.163.01 "$LIB_DIR/"
ln -sf libnvidia-ml.so.550.163.01 "$LIB_DIR/libnvidia-ml.so.1"
ln -sf libnvidia-ml.so.1 "$LIB_DIR/libnvidia-ml.so"
if [ -f pkg/gpu/mockcuda/libcuda.so.550.163.01 ]; then
  cp pkg/gpu/mockcuda/libcuda.so.550.163.01 "$LIB_DIR/"
  ln -sf libcuda.so.550.163.01 "$LIB_DIR/libcuda.so.1"
  ln -sf libcuda.so.1 "$LIB_DIR/libcuda.so"
fi

EXPORTER_PROFILE_PATH="$PROFILE_PATH"
if [ "$HOPPER_PLUS" = "1" ]; then
  EXPORTER_PROFILE_PATH="$OUT_DIR/positive-gpm-profile.yaml"
  awk '
    /^  utilization:/ { in_util = 1; print; next }
    in_util && !done && /^    gpu:/ {
      sub(/gpu:.*/, "gpu: 50")
      done = 1
    }
    in_util && /^[^[:space:]]/ { in_util = 0 }
    { print }
    END { exit done ? 0 : 1 }
  ' "$PROFILE_PATH" >"$EXPORTER_PROFILE_PATH" \
    || { echo "FAIL: could not create positive GPM profile from $PROFILE_PATH"; exit 1; }
fi

RUN_ARGS=(
  --rm
  -e MOCK_NVML_CONFIG=/config/config.yaml
  -e MOCK_NVML_DEBUG=1
  -e MOCK_GPU_COUNT="$GPU_COUNT"
  -e LD_LIBRARY_PATH=/mock/lib
  -v "$LIB_DIR:/mock/lib:ro"
  -v "$PROFILE_PATH:/config/config.yaml:ro"
  # diag -r 1 open()s the /dev/nvidia* nodes the container script mknods; the
  # default device cgroup denies unlisted char devices. In the real
  # deployment CDI injects the nodes with proper cgroup rules.
  --device-cgroup-rule='c 195:* rmw'
)

echo ""
echo "=== 1. nv-hostengine + dcgmi against mock NVML ($DCGM_IMAGE) ==="
# All dcgmi invocations are best-effort: the point of the spike is the
# captured output (including failures and [NVML-STUB] lines), not exit codes.
docker run "${RUN_ARGS[@]}" --entrypoint bash "$DCGM_IMAGE" -c '
  set +e
  # diag -r 1 cross-checks the NVML device count against /dev/nvidia*; in the
  # real deployment CDI injects these nodes, here we mknod them from the
  # selected profile.
  for i in $(seq 0 "$((MOCK_GPU_COUNT - 1))"); do mknod "/dev/nvidia$i" c 195 "$i" 2>/dev/null; done
  mknod /dev/nvidiactl c 195 255 2>/dev/null

  nv-hostengine -n --log-level ERROR -f /tmp/hostengine.log 2>/tmp/hostengine.stderr &
  HE_PID=$!
  sleep 3

  run() { echo ""; echo "--- $* ---"; "$@" 2>&1; }

  run dcgmi discovery -l
  run dcgmi dmon -e 150,155,203,204,254,1004 -c 3 -d 1000
  # PROF fields: 1001 gr_engine, 1002 sm_active, 1003 sm_occupancy,
  # 1005 dram_active, 1009 pcie_tx, 1010 pcie_rx
  run dcgmi dmon -e 1001,1002,1003,1005,1009,1010 -c 3 -d 1000
  run dcgmi health -g 0 -s a
  run dcgmi health -g 0 -c
  run dcgmi diag -r 1

  kill $HE_PID 2>/dev/null; wait $HE_PID 2>/dev/null
  echo ""
  echo "=== hostengine stderr (mock debug + stub hits) ==="
  cat /tmp/hostengine.stderr
' >"$OUT_DIR/dcgmi.log" 2>&1 || true
tail -40 "$OUT_DIR/dcgmi.log"

echo ""
echo "=== 2. dcgm-exporter (embedded hostengine) against mock NVML ($EXPORTER_IMAGE) ==="
docker rm -f spike-dcgm-exporter >/dev/null 2>&1 || true
docker run -d --name spike-dcgm-exporter \
  -e MOCK_NVML_CONFIG=/config/config.yaml \
  -e MOCK_NVML_DEBUG=1 \
  -e LD_LIBRARY_PATH=/mock/lib \
  -e DCGM_EXPORTER_LISTEN=:9400 \
  -e DCGM_EXPORTER_COLLECT_INTERVAL=5000 \
  -v "$LIB_DIR:/mock/lib:ro" \
  -v "$EXPORTER_PROFILE_PATH:/config/config.yaml:ro" \
  -p 127.0.0.1:9400:9400 \
  --cap-add SYS_ADMIN \
  "$EXPORTER_IMAGE" >/dev/null || true
# DEV samples appear one collect interval after the watches are set up; the
# PROF (GPM) fields warm up a few intervals later — poll for those so the
# captured metrics.txt shows the full surface.
: >"$OUT_DIR/metrics.txt"
for _ in $(seq 1 45); do
  curl -sf http://127.0.0.1:9400/metrics >"$OUT_DIR/metrics.txt" 2>/dev/null || true
  if [ "$HOPPER_PLUS" = "1" ]; then
    awk '/^DCGM_FI_PROF_PCIE_TX_BYTES\{/ { seen = 1; if ($NF + 0 > 0) positive = 1 } END { exit (seen && positive) ? 0 : 1 }' "$OUT_DIR/metrics.txt" && break
  else
    grep -q '^DCGM_' "$OUT_DIR/metrics.txt" && break
  fi
  sleep 2
done
echo "metrics endpoint: $(grep -c '^DCGM_' "$OUT_DIR/metrics.txt" || true) DCGM_* samples," \
  "$(grep -c '^DCGM_FI_PROF' "$OUT_DIR/metrics.txt" || true) PROF samples"
if [ "$HOPPER_PLUS" = "1" ]; then
  if ! awk '/^DCGM_FI_PROF_PCIE_TX_BYTES\{/ { seen = 1; if ($NF + 0 <= 0) bad = 1 } END { exit (seen && !bad) ? 0 : 1 }' "$OUT_DIR/metrics.txt"; then
    echo "FAIL: DCGM_FI_PROF_PCIE_TX_BYTES missing or non-positive"
    docker logs spike-dcgm-exporter >"$OUT_DIR/exporter.log" 2>&1 || true
    docker rm -f spike-dcgm-exporter >/dev/null 2>&1 || true
    exit 1
  fi
  echo "PASS: DCGM_FI_PROF_PCIE_TX_BYTES positive under $EXPORTER_PROFILE_PATH"
fi
docker logs spike-dcgm-exporter >"$OUT_DIR/exporter.log" 2>&1 || true
docker rm -f spike-dcgm-exporter >/dev/null 2>&1 || true
tail -30 "$OUT_DIR/exporter.log"

echo ""
echo "=== 3. DCGM health with failure injection (ECC uncorrectable + Xid 79) ==="
# A minimal config: healthy telemetry plus a tripped ECC/Xid failure. dcgmi
# health watches must go Unhealthy and report the Xid.
cat >"$OUT_DIR/failure-config.yaml" <<'EOF'
version: "1.0"
system:
  driver_version: "550.163.01"
  nvml_version: "12.550.163.01"
  num_devices: 2
device_defaults:
  name: "NVIDIA H100 80GB HBM3"
  architecture: "hopper"
  ecc:
    mode_current: "enabled"
  thermal:
    temperature_gpu_c: 34
  power:
    current_draw_mw: 95000
  utilization:
    gpu: 0
  failure:
    mode: "ecc_uncorrectable"
    xid:
      code: 79
EOF
docker run --rm \
  -e MOCK_NVML_CONFIG=/config/config.yaml \
  -e MOCK_NVML_DEBUG=1 \
  -e LD_LIBRARY_PATH=/mock/lib \
  -v "$LIB_DIR:/mock/lib:ro" \
  -v "$OUT_DIR/failure-config.yaml:/config/config.yaml:ro" \
  --entrypoint bash "$DCGM_IMAGE" -c '
  set +e
  nv-hostengine -n --log-level ERROR -f /tmp/hostengine.log 2>/dev/null &
  sleep 3
  dcgmi health -g 0 -s a
  echo "--- letting health watches poll (15s) ---"
  sleep 15
  dcgmi health -g 0 -c 2>&1
  dcgmi dmon -e 230 -c 2 -d 1000 2>&1   # 230 = DCGM_FI_DEV_XID_ERRORS
' >"$OUT_DIR/health.log" 2>&1 || true
grep -A20 'Health Monitor Report' "$OUT_DIR/health.log" || tail -25 "$OUT_DIR/health.log"

echo ""
echo "=== 4. Stub hits (deduplicated) — the implementation worklist ==="
grep -h 'NVML-STUB' "$OUT_DIR"/dcgmi.log "$OUT_DIR"/exporter.log "$OUT_DIR"/health.log 2>/dev/null \
  | sed 's/.*\[NVML-STUB\] //' | sort | uniq -c | sort -rn \
  | tee "$OUT_DIR/stub-hits.txt" || echo "(none captured)"

echo ""
echo "Artifacts in $OUT_DIR: dcgmi.log exporter.log metrics.txt stub-hits.txt"
