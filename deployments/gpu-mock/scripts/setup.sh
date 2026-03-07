#!/bin/sh
# Copyright 2026 NVIDIA CORPORATION
# SPDX-License-Identifier: Apache-2.0
#
# Sets up mock GPU environment on the host filesystem.
# Runs as an entrypoint in the gpu-mock DaemonSet container.
#
# Required env vars: GPU_COUNT, DRIVER_VERSION, NODE_NAME
set -e

HOST=/host/var/lib/nvidia-mock
DRIVER_ROOT=$HOST/driver
DEV_ROOT=$HOST/dev
CONFIG_DIR=$HOST/config

# Validate GPU_COUNT does not exceed profile device count
PROFILE_COUNT=$(grep -c "^[[:space:]]*- index:" /config/config.yaml || echo 0)
if [ "$PROFILE_COUNT" -gt 0 ] && [ "$GPU_COUNT" -gt "$PROFILE_COUNT" ]; then
  echo "WARNING: gpu.count ($GPU_COUNT) exceeds profile devices ($PROFILE_COUNT). Capping to $PROFILE_COUNT."
  GPU_COUNT=$PROFILE_COUNT
fi

echo "Setting up mock GPU environment: $GPU_COUNT GPUs, driver $DRIVER_VERSION"

# 1. Create directory structure
mkdir -p "$DRIVER_ROOT/usr/lib64" "$DRIVER_ROOT/usr/bin" "$DRIVER_ROOT/config"
mkdir -p "$DEV_ROOT" "$CONFIG_DIR"

# 2. Copy mock NVML library + create symlinks
#    The .so is built with a fixed version (Makefile LIB_VERSION); rename to match
#    the target DRIVER_VERSION so consumers see a consistent version string.
BUILT_SO=$(ls /usr/local/lib/libnvidia-ml.so.*.*.* 2>/dev/null | head -1)
if [ -z "$BUILT_SO" ]; then
  echo "ERROR: No mock NVML library found in /usr/local/lib/" >&2
  exit 1
fi
cp "$BUILT_SO" "$DRIVER_ROOT/usr/lib64/libnvidia-ml.so.$DRIVER_VERSION"
ln -sf "libnvidia-ml.so.$DRIVER_VERSION" "$DRIVER_ROOT/usr/lib64/libnvidia-ml.so.1"
ln -sf "libnvidia-ml.so.1" "$DRIVER_ROOT/usr/lib64/libnvidia-ml.so"

# 2b. Copy mock CUDA library + create symlinks
BUILT_CUDA_SO=$(ls /usr/local/lib/libcuda.so.*.*.* 2>/dev/null | head -1)
if [ -z "$BUILT_CUDA_SO" ]; then
  echo "WARNING: No mock CUDA library found in /usr/local/lib/, skipping libcuda.so setup"
else
  cp "$BUILT_CUDA_SO" "$DRIVER_ROOT/usr/lib64/libcuda.so.$DRIVER_VERSION"
  ln -sf "libcuda.so.$DRIVER_VERSION" "$DRIVER_ROOT/usr/lib64/libcuda.so.1"
  ln -sf "libcuda.so.1" "$DRIVER_ROOT/usr/lib64/libcuda.so"
  # TODO: properly split driver API (libcuda.so) and runtime API (libcudart.so)
  # For now, our mock exports CUDA Runtime API symbols but is built as libcuda.so.
  # CUDA samples (e.g. vectorAdd) link against libcudart.so, so create a symlink.
  ln -sf "libcuda.so.1" "$DRIVER_ROOT/usr/lib64/libcudart.so.12"
  ln -sf "libcudart.so.12" "$DRIVER_ROOT/usr/lib64/libcudart.so"
fi

# 3. Create char device nodes
#    Major 195 = nvidia, Major 510 = nvidia-uvm (standard NVIDIA major numbers)
for i in $(seq 0 $((GPU_COUNT - 1))); do
  mknod -m 666 "$DEV_ROOT/nvidia$i" c 195 "$i" 2>/dev/null || true
done
mknod -m 666 "$DEV_ROOT/nvidiactl" c 195 255 2>/dev/null || true
mknod -m 666 "$DEV_ROOT/nvidia-uvm" c 510 0 2>/dev/null || true
mknod -m 666 "$DEV_ROOT/nvidia-uvm-tools" c 510 1 2>/dev/null || true

# 4. Install nvidia-smi
#    The DRA driver init container runs nvidia-smi via `env -i` in an Alpine-based
#    container. Real nvidia-smi is glibc-linked and can't exec under musl, so we
#    always install a shell script shim at the standard path. The real binary goes
#    to nvidia-smi.real for glibc-based consumers (Kind node, gpu-mock container).
cat > "$DRIVER_ROOT/usr/bin/nvidia-smi" << NVIDIA_SMI_EOF
#!/bin/sh
# Shim: delegates to the real nvidia-smi binary if available (glibc environments),
# otherwise returns basic driver info for lightweight init containers (Alpine/musl).
SCRIPT_DIR="\$(cd "\$(dirname "\$0")" && pwd)"
if [ -x "\${SCRIPT_DIR}/nvidia-smi.real" ]; then
  LIB_DIR="\${SCRIPT_DIR}/../lib64"
  export LD_LIBRARY_PATH="\${LIB_DIR}\${LD_LIBRARY_PATH:+:\$LD_LIBRARY_PATH}"
  exec "\${SCRIPT_DIR}/nvidia-smi.real" "\$@"
fi
echo "NVIDIA-SMI $DRIVER_VERSION"
echo "Driver Version: $DRIVER_VERSION"
echo "CUDA Version: 12.4"
NVIDIA_SMI_EOF
chmod +x "$DRIVER_ROOT/usr/bin/nvidia-smi"
if [ -f /usr/local/bin/nvidia-smi ]; then
  cp /usr/local/bin/nvidia-smi "$DRIVER_ROOT/usr/bin/nvidia-smi.real"
  chmod +x "$DRIVER_ROOT/usr/bin/nvidia-smi.real"
  echo "Installed nvidia-smi shim + real binary"
else
  echo "WARNING: Real nvidia-smi not found, shim will use fallback output"
fi

# 4b. Create /proc/driver/nvidia mock files (read by nvidia-smi)
PROC_DIR="$DRIVER_ROOT/proc/driver/nvidia"
mkdir -p "$PROC_DIR"
cat > "$PROC_DIR/version" << PROC_VERSION_EOF
NVRM version: NVIDIA UNIX x86_64 Kernel Module  $DRIVER_VERSION  Thu Feb 20 23:41:34 UTC 2026
GCC version:  gcc version 12.2.0 (Debian 12.2.0-14)
PROC_VERSION_EOF

cat > "$PROC_DIR/params" << PROC_PARAMS_EOF
EnableMSI: 1
NVreg_RegistryDwords:
NVreg_DeviceFileGID: 0
NVreg_DeviceFileMode: 438
NVreg_DeviceFileUID: 0
NVreg_ModifyDeviceFiles: 1
NVreg_PreserveVideoMemoryAllocations: 0
NVreg_EnableResizableBar: 0
PROC_PARAMS_EOF

# 5. Copy GPU profile config to both locations:
#    - config/config.yaml (canonical, used by device plugin)
#    - driver/config/config.yaml (auto-discovered by .so via /proc/self/maps)
cp /config/config.yaml "$CONFIG_DIR/config.yaml"
cp /config/config.yaml "$DRIVER_ROOT/config/config.yaml"

# 6. Inject num_devices into config so the .so knows GPU count without env vars.
#    This makes the on-host config self-contained — consumers just point at driver root.
sed -i "/^system:/a\\  num_devices: $GPU_COUNT" "$CONFIG_DIR/config.yaml"
sed -i "/^system:/a\\  num_devices: $GPU_COUNT" "$DRIVER_ROOT/config/config.yaml"

# 7. Label node (requires RBAC: get+patch on nodes)
if command -v kubectl >/dev/null 2>&1; then
  kubectl label node "$NODE_NAME" nvidia.com/gpu.present=true --overwrite || true
  kubectl label node "$NODE_NAME" feature.node.kubernetes.io/pci-10de.present=true --overwrite || true
fi

echo "Mock GPU environment ready: $GPU_COUNT GPUs at $HOST"
