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

# 3. Create char device nodes
#    Major 195 = nvidia, Major 510 = nvidia-uvm (standard NVIDIA major numbers)
for i in $(seq 0 $((GPU_COUNT - 1))); do
  mknod -m 666 "$DEV_ROOT/nvidia$i" c 195 "$i" 2>/dev/null || true
done
mknod -m 666 "$DEV_ROOT/nvidiactl" c 195 255 2>/dev/null || true
mknod -m 666 "$DEV_ROOT/nvidia-uvm" c 510 0 2>/dev/null || true
mknod -m 666 "$DEV_ROOT/nvidia-uvm-tools" c 510 1 2>/dev/null || true

# 4. Create mock nvidia-smi (required by DRA driver)
#    Uses unquoted heredoc so $DRIVER_VERSION is expanded at setup time.
cat > "$DRIVER_ROOT/usr/bin/nvidia-smi" << NVIDIA_SMI_EOF
#!/bin/bash
# Mock nvidia-smi — returns driver version and basic GPU info.
# Used by consumers (e.g., DRA driver) that probe nvidia-smi at startup.
echo "NVIDIA-SMI $DRIVER_VERSION"
echo "Driver Version: $DRIVER_VERSION"
echo "CUDA Version: 12.4"
exit 0
NVIDIA_SMI_EOF
chmod +x "$DRIVER_ROOT/usr/bin/nvidia-smi"

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
