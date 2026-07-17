#!/bin/sh
# Copyright 2026 NVIDIA CORPORATION
# SPDX-License-Identifier: Apache-2.0
#
# Sets up mock GPU environment on the host filesystem.
# Runs as an entrypoint in the nvml-mock DaemonSet container.
#
# Required env vars: GPU_COUNT, DRIVER_VERSION, NODE_NAME
set -e

HOST=/host/var/lib/nvml-mock
DRIVER_ROOT=$HOST/driver
# Co-locate device nodes under $DRIVER_ROOT so the upstream DRA driver's
# getDevRoot() (cmd/gpu-kubelet-plugin/root.go in NVIDIA/k8s-dra-driver-gpu)
# resolves devRoot to the mock driver root rather than falling back to "/".
DEV_ROOT=$DRIVER_ROOT/dev
CONFIG_DIR=$HOST/config

# Validate GPU_COUNT does not exceed profile device count
PROFILE_COUNT=$(grep -c "^[[:space:]]*- index:" /etc/nvml-mock/config.yaml || echo 0)
if [ "$PROFILE_COUNT" -gt 0 ] && [ "$GPU_COUNT" -gt "$PROFILE_COUNT" ]; then
  echo "WARNING: gpu.count ($GPU_COUNT) exceeds profile devices ($PROFILE_COUNT). Capping to $PROFILE_COUNT."
  GPU_COUNT=$PROFILE_COUNT
fi

echo "Setting up mock GPU environment: $GPU_COUNT GPUs, driver $DRIVER_VERSION"

# 1. Create directory structure
mkdir -p "$DRIVER_ROOT/usr/lib64" "$DRIVER_ROOT/usr/bin" "$DRIVER_ROOT/usr/local/lib" "$DRIVER_ROOT/config"
mkdir -p "$DEV_ROOT" "$CONFIG_DIR"
mkdir -p "$HOST/run"

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

# Resolve fabricmanager enablement once, here, because it influences both the
# CDI spec (below) and the daemon launch (step 11). Validate early so a typo
# fails the pod with a clear message rather than silently disabling the gate.
MOCK_FM_MODE=$(printf '%s' "${MOCK_FABRICMANAGER:-off}" | tr '[:upper:]' '[:lower:]')
case "$MOCK_FM_MODE" in
  off | on) ;;
  *)
    echo "ERROR: MOCK_FABRICMANAGER='$MOCK_FABRICMANAGER' is invalid; expected off or on" >&2
    exit 1
    ;;
esac
FM_STATE_DIR="${MOCK_FABRICMANAGER_STATE_DIR:-/var/lib/nvml-mock/fabric-state}"

cat > "$CDI_DIR/nvidia.yaml" << 'CDI_HEADER'
cdiVersion: "0.6.0"
kind: "nvidia.com/gpu"
containerEdits:
  deviceNodes:
    - path: /dev/nvidiactl
      hostPath: /var/lib/nvml-mock/driver/dev/nvidiactl
    - path: /dev/nvidia-uvm
      hostPath: /var/lib/nvml-mock/driver/dev/nvidia-uvm
    - path: /dev/nvidia-uvm-tools
      hostPath: /var/lib/nvml-mock/driver/dev/nvidia-uvm-tools
  mounts:
    - hostPath: /var/lib/nvml-mock/driver/usr/lib64/libnvidia-ml.so.1
      containerPath: /usr/lib64/libnvidia-ml.so.1
      options: [ro, nosuid, nodev, bind]
    - hostPath: /var/lib/nvml-mock/driver/usr/bin/nvidia-smi
      containerPath: /usr/bin/nvidia-smi
      options: [ro, nosuid, nodev, bind]
    # Bind-mount the GPU profile config DIRECTORY (not just config.yaml) so the
    # mock NVML library finds config.yaml via MOCK_NVML_CONFIG below AND sees
    # overrides.yaml when nvml-mock-ctl writes it at runtime. The CLI creates
    # the overlay via temp-file+rename in this same dir; a directory bind makes
    # that atomic rename observable to CDI-injected consumers (a single-file
    # bind would pin the original inode and hide the replacement). Without the
    # config the mock .so falls back to "no-YAML" defaults — temperature, power
    # and similar metrics surface as N/A in nvidia-smi.
    - hostPath: /var/lib/nvml-mock/driver/config
      containerPath: /etc/nvml-mock
      options: [ro, nosuid, nodev, bind]
CDI_HEADER

# When fabricmanager is enabled, bind-mount the node-local readiness marker
# directory into CDI-injected workloads and point the mock NVML library at it.
# Without this, the mock .so loaded inside user pods sees an empty
# MOCK_FABRICMANAGER_STATE_DIR and resolves `fabric.state: auto` straight to
# COMPLETED, silently bypassing the fabricmanager readiness gate (the mock .so
# is loaded by nvidia-smi *inside the workload container*, not by this pod).
if [ "$MOCK_FM_MODE" != "off" ]; then
  cat >> "$CDI_DIR/nvidia.yaml" << FM_MOUNT_EOF
    - hostPath: $FM_STATE_DIR
      containerPath: $FM_STATE_DIR
      options: [ro, nosuid, nodev, bind]
FM_MOUNT_EOF
fi

cat >> "$CDI_DIR/nvidia.yaml" << 'CDI_HOOKS_ENV'
  hooks:
    - hookName: createContainer
      path: /usr/bin/nvidia-cdi-hook
      args: [nvidia-cdi-hook, update-ldcache, --folder, /usr/lib64]
  env:
    - NVIDIA_VISIBLE_DEVICES=void
    - MOCK_NVML_CONFIG=/etc/nvml-mock/config.yaml
    - MOCK_NVML_OVERRIDES=/etc/nvml-mock/overrides.yaml
CDI_HOOKS_ENV

if [ "$MOCK_FM_MODE" != "off" ]; then
  cat >> "$CDI_DIR/nvidia.yaml" << FM_ENV_EOF
    - MOCK_FABRICMANAGER_STATE_DIR=$FM_STATE_DIR
FM_ENV_EOF
fi

cat >> "$CDI_DIR/nvidia.yaml" << 'CDI_DEVICES'
devices:
CDI_DEVICES

# Per-GPU device entries
for i in $(seq 0 $((GPU_COUNT - 1))); do
  cat >> "$CDI_DIR/nvidia.yaml" << DEVICE_EOF
  - name: "$i"
    containerEdits:
      deviceNodes:
        - path: /dev/nvidia$i
          hostPath: /var/lib/nvml-mock/driver/dev/nvidia$i
DEVICE_EOF
done

# "all" device — aggregates all GPUs
echo '  - name: "all"' >> "$CDI_DIR/nvidia.yaml"
echo '    containerEdits:' >> "$CDI_DIR/nvidia.yaml"
echo '      deviceNodes:' >> "$CDI_DIR/nvidia.yaml"
for i in $(seq 0 $((GPU_COUNT - 1))); do
  echo "        - path: /dev/nvidia$i" >> "$CDI_DIR/nvidia.yaml"
  echo "          hostPath: /var/lib/nvml-mock/driver/dev/nvidia$i" >> "$CDI_DIR/nvidia.yaml"
done

echo "CDI spec generated at $CDI_DIR/nvidia.yaml ($GPU_COUNT devices)"

# 4. Install nvidia-smi
#    The ELF binary has RPATH=$ORIGIN/../lib64 (set by patchelf in Dockerfile),
#    so it finds libnvidia-ml.so.1 relative to its own location. This works for:
#    - GPU Operator validator:  /run/nvidia/driver/usr/bin/ → ../lib64
#    - CDI injection:           /usr/bin/ → ../lib64 (CDI also mounts libs there)
#    - DRA kubelet-plugin:      /var/lib/nvml-mock/driver/usr/bin/ → ../lib64
#    - Kind node direct:        same path
#
#    We also install a shell fallback (nvidia-smi.sh) for environments without
#    glibc (e.g. Alpine/musl init containers).
if [ -f /usr/local/bin/nvidia-smi ]; then
  cp /usr/local/bin/nvidia-smi "$DRIVER_ROOT/usr/bin/nvidia-smi"
  chmod +x "$DRIVER_ROOT/usr/bin/nvidia-smi"
  echo "Installed nvidia-smi ELF binary (RPATH-enabled)"
else
  echo "WARNING: Real nvidia-smi not found, installing shell fallback only"
fi

# Ensure nvidia-smi exists at the standard path even when the ELF is missing.
# Consumers (e.g. GPU Operator validator) expect /usr/bin/nvidia-smi to exist.
if [ ! -f "$DRIVER_ROOT/usr/bin/nvidia-smi" ]; then
  ln -sf nvidia-smi.sh "$DRIVER_ROOT/usr/bin/nvidia-smi"
  echo "Symlinked nvidia-smi -> nvidia-smi.sh (shell fallback)"
fi

# Shell fallback for non-glibc environments
cat > "$DRIVER_ROOT/usr/bin/nvidia-smi.sh" << NVIDIA_SMI_EOF
#!/bin/sh
echo "NVIDIA-SMI $DRIVER_VERSION"
echo "Driver Version: $DRIVER_VERSION"
echo "CUDA Version: 12.4"
NVIDIA_SMI_EOF
chmod +x "$DRIVER_ROOT/usr/bin/nvidia-smi.sh"

# 4b. Stage InfiniBand tools and preload shims for node-wide NRI injection.
#     The NRI plugin mounts /var/lib/nvml-mock at /opt/nvml-mock in each
#     workload, then prepends driver/usr/bin and driver/usr/lib64 and appends
#     driver/usr/local/lib shims to LD_PRELOAD.
for tool in ibnetdiscover ibstat iblinkinfo ibstatus sminfo ibping ibv_devinfo; do
  if command -v "$tool" >/dev/null 2>&1; then
    cp "$(command -v "$tool")" "$DRIVER_ROOT/usr/bin/$tool"
  fi
done
# Stage the fabric consumer so node-wide NRI-injected pods can verify their
# per-node ComputeDomain identity (nvmlDeviceGetGpuFabricInfo) the same way the
# compute-domain demo does inside the daemon pod. It resolves the mock NVML
# library via the LD_LIBRARY_PATH the NRI plugin injects.
if [ -x /usr/local/bin/check-fabric ]; then
  cp /usr/local/bin/check-fabric "$DRIVER_ROOT/usr/bin/check-fabric"
fi
cp -a /usr/local/lib/libibmock*.so* "$DRIVER_ROOT/usr/local/lib/" 2>/dev/null || true
cp -a /usr/local/lib/libpcimocksys.so* "$DRIVER_ROOT/usr/local/lib/" 2>/dev/null || true

# 4c. Create /proc/driver/nvidia mock files (read by nvidia-smi)
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
cp /etc/nvml-mock/config.yaml "$CONFIG_DIR/config.yaml"
cp /etc/nvml-mock/config.yaml "$DRIVER_ROOT/config/config.yaml"

# 6. Inject num_devices into config so the .so knows GPU count without env vars.
#    This makes the on-host config self-contained — consumers just point at driver root.
sed -i "/^system:/a\\  num_devices: $GPU_COUNT" "$CONFIG_DIR/config.yaml"
sed -i "/^system:/a\\  num_devices: $GPU_COUNT" "$DRIVER_ROOT/config/config.yaml"

# Runtime overrides (written by nvml-mock-ctl) are ephemeral: wipe them on
# every pod start so a restart of this DaemonSet resets simulated GPU state
# back to the pristine profile config.
rm -f "$CONFIG_DIR/overrides.yaml" "$DRIVER_ROOT/config/overrides.yaml"

# 6b. Stage the cluster-level ComputeDomain topology document into the overlay
#     tree so node-wide NRI injection can surface per-node fabric identity.
#     The daemon mounts the topology ConfigMap at /etc/nvml-mock/topology when
#     topology.enabled=true; the NRI plugin bind-mounts $HOST at the container
#     overlay path and injects MOCK_TOPOLOGY_CONFIG pointing here (plus the
#     node's NODE_NAME) so the mock NVML engine's applyTopologyOverlay() rewrites
#     each GPU's clique_id / cluster_uuid. No-op when topology is disabled.
if [ -f /etc/nvml-mock/topology/topology.yaml ]; then
  mkdir -p "$HOST/topology"
  cp /etc/nvml-mock/topology/topology.yaml "$HOST/topology/topology.yaml"
  echo "Staged ComputeDomain topology overlay at $HOST/topology/topology.yaml"
fi

# 7. Label node (requires RBAC: get+patch on nodes)
if command -v kubectl >/dev/null 2>&1; then
  kubectl label node "$NODE_NAME" nvidia.com/gpu.present=true --overwrite || true
  kubectl label node "$NODE_NAME" feature.node.kubernetes.io/pci-10de.present=true --overwrite || true
fi

# 8. Create GPU Operator compatibility symlink.
#    The GPU Operator's validator DaemonSet mounts hostPath /run/nvidia/driver
#    into the driver-validation init container. By symlinking to our mock driver
#    root, the validator finds nvidia-smi and mock NVML at the expected path.
mkdir -p /host/run/nvidia
ln -sfn /var/lib/nvml-mock/driver /host/run/nvidia/driver

# 8b. Write the toolkit-ready marker that GPU Operator operand pods poll for.
#     Operand DaemonSets (device-plugin, gpu-feature-discovery) ship with a
#     hardcoded `toolkit-validation` init container that loops on:
#       until [ -f /run/nvidia/validations/toolkit-ready ]; do sleep 5; done
#     Real nvidia-container-toolkit writes this marker as part of its install.
#     When nvml-mock substitutes for the toolkit, no other component writes
#     it — so we do, here, alongside the existing /run/nvidia/driver setup.
mkdir -p /host/run/nvidia/validations
touch /host/run/nvidia/validations/toolkit-ready

# 9. InfiniBand: render sysfs via mock-ib; optionally run UMAD/fabric daemon.
#    MOCK_IB selects the mock tier (case-insensitive):
#      full  -> sysfs redirection + UMAD/verbs shims + mock-ib daemon
#      sysfs -> sysfs redirection only (ibstat/ibstatus; no daemon)
#      off   -> nothing mocked (default)
#    Any other value is a typo; fail fast so IB isn't silently disabled.
MOCK_IB_MODE=$(printf '%s' "${MOCK_IB:-off}" | tr '[:upper:]' '[:lower:]')
case "$MOCK_IB_MODE" in
  off | sysfs | full) ;;
  *)
    echo "ERROR: MOCK_IB='$MOCK_IB' is invalid; expected off, sysfs, or full" >&2
    exit 1
    ;;
esac

IB_ROOT="$HOST/ib"
mkdir -p "$IB_ROOT"
if [ "$MOCK_IB_MODE" != "off" ] && [ -x /usr/local/bin/mock-ib ]; then
  # Render the sysfs tree synchronously first. This is fatal under `set -e`,
  # so a profile typo fails the pod here with a clear error instead of
  # silently producing an empty tree / zero HCAs. When MOCK_IB=full the serving
  # daemon below re-renders idempotently before it starts listening; we still
  # render here so the fail-fast signal isn't lost to the backgrounded daemon
  # (whose render failure would just exit the `&` child while setup continues).
  /usr/local/bin/mock-ib \
    -config /etc/nvml-mock/config.yaml \
    -gpu-count "$GPU_COUNT" \
    -node-name "$NODE_NAME" \
    -ib-root "$IB_ROOT" \
    -render-only
  if [ "$MOCK_IB_MODE" = "full" ]; then
    /scripts/start-mock-ib.sh &
  fi
fi

# 10. Render fake PCI sysfs tree (consumed by topology-aware DRA / device
#     plugins that resolve PCIe root complex via a readlink on
#     /sys/bus/pci/devices/<bdf>). The renderer parses the profile's
#     `pcie_topology:` block; profiles without one get a flat default
#     covering every device under a single root complex (`pci0000:00`,
#     NUMA 0). Failures are fatal under `set -e` for the same reason as
#     the IB block above — a topology typo otherwise yields silently
#     malformed sysfs that downstream `dra.k8s.io/pcieRoot` attributes
#     would inherit.
PCI_ROOT="$HOST"
mkdir -p "$PCI_ROOT"
if [ -x /usr/local/bin/render-pci-sysfs ]; then
  /usr/local/bin/render-pci-sysfs \
    --config /etc/nvml-mock/config.yaml \
    --output "$PCI_ROOT"
fi

# 11. Fabric Manager: on NVSwitch platforms (HGX H100 / GB200 / GB300) the
#     real nvidia-fabricmanager registers the GPUs with the NVSwitch fabric
#     before they are usable. When MOCK_FABRICMANAGER is enabled we start the
#     fake daemon, which writes a node-local readiness marker under
#     MOCK_FABRICMANAGER_STATE_DIR. The mock NVML library reads that marker to
#     resolve each GPU's fabric state when the profile sets `fabric.state:
#     auto` (in_progress until ready, completed once ready) — mirroring how a
#     real fabricmanager gates GPU readiness. NVLink counters anchor to
#     /proc/stat btime (stable across nvidia-smi invocations), so no epoch
#     export is needed here for counters to grow.
#
#     MOCK_FM_MODE / FM_STATE_DIR were resolved + validated earlier (near the
#     CDI block). The readiness marker lives on a DirectoryOrCreate hostPath
#     that survives pod restarts, and the daemon re-asserts it every 2s — so a
#     stale marker from a prior pod could make a fresh pod report COMPLETED
#     before its own daemon is ready. Clear it here so every pod starts in a
#     clean IN_PROGRESS state until *this* daemon writes the marker.
if [ "$MOCK_FM_MODE" != "off" ]; then
  if [ -x /usr/bin/nv-fabricmanager ]; then
    mkdir -p "$FM_STATE_DIR"
    # Marker name must match fmcoord.ReadyMarker (pkg/fmcoord/coord.go), which
    # the daemon writes and engine.FabricReadyMarker reads. Keep this literal in
    # sync with that constant — the engine/fmcoord contract test pins the Go
    # side, but this shell path is not covered, so a rename would silently skip
    # this stale-marker cleanup.
    rm -f "$FM_STATE_DIR/fabricmanager.ready"
    echo "Starting fake nvidia-fabricmanager (state dir: $FM_STATE_DIR)"
    /usr/bin/nv-fabricmanager &
  else
    # Hard-fail rather than warn: MOCK_FM_MODE != off means the env is fully
    # wired (a profile with fabric.state: auto). Without the daemon the
    # readiness marker is never written, so those GPUs sit at IN_PROGRESS
    # forever — a confusing failure from the workload side. A missing binary is
    # a broken image, same as the unknown-mode branch validated earlier.
    echo "FATAL: MOCK_FABRICMANAGER='$MOCK_FABRICMANAGER' set but /usr/bin/nv-fabricmanager not found in image" >&2
    exit 1
  fi
fi

echo "Mock GPU environment ready: $GPU_COUNT GPUs at $HOST"
