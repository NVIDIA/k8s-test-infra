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

# 3b. Generate CDI spec for nvidia-container-runtime CDI mode.
#     This allows the toolkit to inject our mock libs into containers without
#     needing libnvidia-container or kernel modules.
CDI_DIR=/host/var/run/cdi
mkdir -p "$CDI_DIR"

cat > "$CDI_DIR/nvidia.yaml" << 'CDI_HEADER'
cdiVersion: "0.6.0"
kind: "nvidia.com/gpu"
containerEdits:
  deviceNodes:
    - path: /dev/nvidiactl
      hostPath: /var/lib/nvidia-mock/dev/nvidiactl
    - path: /dev/nvidia-uvm
      hostPath: /var/lib/nvidia-mock/dev/nvidia-uvm
    - path: /dev/nvidia-uvm-tools
      hostPath: /var/lib/nvidia-mock/dev/nvidia-uvm-tools
  mounts:
    - hostPath: /var/lib/nvidia-mock/driver/usr/lib64/libnvidia-ml.so.1
      containerPath: /usr/lib64/libnvidia-ml.so.1
      options: [ro, nosuid, nodev, bind]
    - hostPath: /var/lib/nvidia-mock/driver/usr/bin/nvidia-smi
      containerPath: /usr/bin/nvidia-smi
      options: [ro, nosuid, nodev, bind]
  hooks:
    - hookName: createContainer
      path: /usr/bin/nvidia-cdi-hook
      args: [nvidia-cdi-hook, update-ldcache, --folder, /usr/lib64]
  env:
    - NVIDIA_VISIBLE_DEVICES=void
devices:
CDI_HEADER

# Per-GPU device entries
for i in $(seq 0 $((GPU_COUNT - 1))); do
  cat >> "$CDI_DIR/nvidia.yaml" << DEVICE_EOF
  - name: "$i"
    containerEdits:
      deviceNodes:
        - path: /dev/nvidia$i
          hostPath: /var/lib/nvidia-mock/dev/nvidia$i
DEVICE_EOF
done

# "all" device — aggregates all GPUs
echo '  - name: "all"' >> "$CDI_DIR/nvidia.yaml"
echo '    containerEdits:' >> "$CDI_DIR/nvidia.yaml"
echo '      deviceNodes:' >> "$CDI_DIR/nvidia.yaml"
for i in $(seq 0 $((GPU_COUNT - 1))); do
  echo "        - path: /dev/nvidia$i" >> "$CDI_DIR/nvidia.yaml"
  echo "          hostPath: /var/lib/nvidia-mock/dev/nvidia$i" >> "$CDI_DIR/nvidia.yaml"
done

echo "CDI spec generated at $CDI_DIR/nvidia.yaml ($GPU_COUNT devices)"

# 4. Install nvidia-smi
#    The DRA driver init container runs nvidia-smi via `env -i` in a distroless
#    container (nvcr.io/nvidia/distroless/cc) which has /bin/bash but NOT /bin/sh.
#    We install a bash shim at the standard path. It delegates to nvidia-smi.real
#    (the real glibc-linked binary) where possible, falling back to basic output
#    for environments where the real binary can't run (missing glibc/libs).
cat > "$DRIVER_ROOT/usr/bin/nvidia-smi" << NVIDIA_SMI_EOF
#!/bin/bash
# Shim: delegates to the real nvidia-smi binary if available (glibc environments),
# otherwise returns basic driver info for lightweight init containers (distroless/musl).
# Uses /bin/bash (not /bin/sh) because the DRA driver's distroless container has
# /bin/bash but not /bin/sh.
SCRIPT_DIR="\$(cd "\$(dirname "\$0")" && pwd)"
if [ -x "\${SCRIPT_DIR}/nvidia-smi.real" ]; then
  LIB_DIR="\${SCRIPT_DIR}/../lib64"
  export LD_LIBRARY_PATH="\${LIB_DIR}\${LD_LIBRARY_PATH:+:\$LD_LIBRARY_PATH}"
  "\${SCRIPT_DIR}/nvidia-smi.real" "\$@" && exit 0
  # Real binary failed (missing glibc/libs); fall through to basic output.
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
