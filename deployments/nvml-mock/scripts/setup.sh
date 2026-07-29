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

# 3a. Mock IMEX capability surface (opt-in via IMEX_MOCK_CHANNELS).
#     The NVIDIA DRA driver's compute-domain kubelet plugin reads a device major
#     for nvidia-caps-imex-channels out of /proc/devices at startup. There is no
#     NVIDIA kernel module here, so that entry does not exist and the plugin
#     aborts. The DRA driver supports a substitute file via its chart's
#     altProcDevices value (env ALT_PROC_DEVICES_PATH); this renders it.
#
#     Pair with, on the DRA driver release that supports it:
#       --set altProcDevices=/var/lib/nvml-mock/imex/proc-devices
#       --set resources.computeDomains.enabled=true
#
#     Bind-mounting over /proc/devices is not an option: runc rejects it
#     ("cannot be mounted because it is inside /proc"), which is why the
#     consumer uses an env-var indirection instead.
if [ "${IMEX_MOCK_CHANNELS:-false}" = "true" ]; then
  IMEX_DIR=$HOST/imex
  IMEX_MAJOR=${IMEX_CHANNEL_MAJOR:-235}
  CAPS_MAJOR=${IMEX_CAPS_MAJOR:-236}
  IMEX_CHANNELS=${IMEX_CHANNEL_COUNT:-2048}

  mkdir -p "$IMEX_DIR"
  # Rendering is idempotent, so a DaemonSet restart re-runs this safely.
  render-imex-procdevices \
    --source /proc/devices \
    --output "$IMEX_DIR/proc-devices" \
    --imex-major "$IMEX_MAJOR" \
    --caps-major "$CAPS_MAJOR"

  # The plugin also reads the fabric IMEX management capability.
  mkdir -p "$DRIVER_ROOT/proc/driver/nvidia/capabilities"
  cat > "$DRIVER_ROOT/proc/driver/nvidia/capabilities/fabric-imex-mgmt" <<CAPS_EOF
DeviceFileMinor: 512
DeviceFileMode: 438
DeviceFileModify: 0
CAPS_EOF

  # Channel device nodes, injected into workload pods by the compute-domain CDI
  # spec. They must exist as character devices for that injection to succeed.
  mkdir -p "$DEV_ROOT/nvidia-caps-imex-channels"
  i=0
  while [ "$i" -lt "$IMEX_CHANNELS" ]; do
    mknod -m 666 "$DEV_ROOT/nvidia-caps-imex-channels/channel$i" c "$IMEX_MAJOR" "$i" 2>/dev/null || true
    i=$((i + 1))
  done

  echo "Mock IMEX surface ready: $IMEX_CHANNELS channels, major $IMEX_MAJOR, proc-devices at $IMEX_DIR/proc-devices"
fi

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
    # the config override via temp-file+rename in this same dir; a directory bind makes
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

# 7. Label node (requires RBAC: get+patch on nodes, for gpu.present only)
#
#    nvidia.com/gpu.present is written directly: it is an NVIDIA-namespaced key
#    no NFD source derives.
#
#    feature.node.kubernetes.io/pci-10de.present is NOT written directly. We
#    drop a feature file and let NFD create the label, so that key has exactly
#    one writer and "NFD works" stays distinguishable from "we set it
#    ourselves" (#505).
#
#    Why not NFD's PCI source? As deployed it cannot see our devices.
#    (Upstream facts as of NFD v0.19.0, the version pinned in go.mod; the
#    worker actually deployed comes from GPU Operator and may differ.)
#    source/pci/utils.go resolves hostpath.SysfsDir.Path("bus/pci/devices"),
#    and pkg/utils/hostpath builds SysfsDir from a private `pathPrefix` set
#    only via linker -X. Upstream's container build passes
#    HOSTMOUNT_PREFIX=/host- (Makefile), so the shipped worker reads
#    /host-sys/bus/pci/devices — the real host /sys, hostPath-mounted by the
#    worker DaemonSet. Our tree is at /var/lib/nvml-mock/sys and is reachable
#    only through libpcimocksys.so, whose injection into the worker is inert
#    twice over:
#      a) the shim rewrites only paths starting "/sys/" (k_prefixes[] in
#         pkg/system/mockpcisysfs/c/shim.c), so "/host-sys/..." never matches.
#      b) nfd-worker is built -extldflags=-static, so it has no PT_INTERP and
#         ignores LD_PRELOAD outright.
#    The key itself is right: GPU Operator configures NFD with
#    deviceLabelFields:[vendor] and whitelists class 0302, which is exactly the
#    "pci-<vendor>.present" form, and our rendered devices carry all five
#    attributes NFD treats as mandatory. Only visibility is missing.
#
#    NFD's *local* source has no such limit: source/local/local.go reads
#    featureFilesDir, a plain literal (not host-prefixed), and the worker
#    mounts that host directory at the same path. Each line parses as
#    key[=value]. That is the supported route, and it needs no node RBAC. The
#    GFD mock already writes to that same directory (tests/e2e/gfd-mock.yaml:52).
#
#    MOCK_NFD_PCI_LABEL (chart value `nodeLabels.pciVendorPresent`, default
#    true/on) gates only the feature file. Turn it off on a cluster where
#    something else already supplies the key. This step converges either way:
#    with the gate on it writes the file, with the gate off it removes any
#    file an earlier run left behind. cleanup.sh reads the same variable and
#    unwinds only the write this step makes — with the gate off it has nothing
#    to remove, because the removal already happened here.
PCI_LABEL_MODE=$(printf '%s' "${MOCK_NFD_PCI_LABEL:-on}" | tr '[:upper:]' '[:lower:]')
case "$PCI_LABEL_MODE" in
  off | on) ;;
  *)
    echo "ERROR: MOCK_NFD_PCI_LABEL='$MOCK_NFD_PCI_LABEL' is invalid; expected off or on" >&2
    exit 1
    ;;
esac
if command -v kubectl >/dev/null 2>&1; then
  kubectl label node "$NODE_NAME" nvidia.com/gpu.present=true --overwrite || true
fi

# NFD local source: drop a feature file instead of writing the label
# ourselves. source/local/local.go reads every file in featureFilesDir and
# parses each line as key[=value], so this one line becomes
# feature.node.kubernetes.io/pci-10de.present=true — created by NFD, not by
# us. Requires no node RBAC, unlike the kubectl label call it replaces.
# With no NFD on the cluster the file is inert and the label simply does not
# exist, which is the honest state (#505).
NFD_FEATURES_DIR=/host/etc/kubernetes/node-feature-discovery/features.d
NFD_FEATURE_FILE="$NFD_FEATURES_DIR/nvml-mock.features"
if [ "$PCI_LABEL_MODE" = "on" ]; then
  # Tolerant on purpose — a failure here must not be fatal, as with the `mknod`
  # calls in step 3, the `cp` calls in step 4b and the `kubectl label` above.
  # Those three discard the failure with `|| true`; this one reports it on
  # stderr. It replaced a `kubectl label` call here that carried `|| true`
  # (cleanup.sh carried the matching one for the removal), and under
  # `set -e` (line 9) a bare failure here would abort the entrypoint before
  # step 8's /host/run/nvidia/driver symlink, crash-looping the whole mock for
  # an optional, gated feature. When the write fails no label appears, which is
  # the honest state (#505), and nothing downstream is corrupted — unlike the
  # IB and PCI renders in steps 9 and 10, which are deliberately fatal because
  # a partial tree silently misleads its consumers. A failing `if` CONDITION
  # does not trip `set -e`, so this form warns and continues rather than
  # swallowing the error the way `|| true` would.
  if mkdir -p "$NFD_FEATURES_DIR" && echo "pci-10de.present=true" > "$NFD_FEATURE_FILE"; then
    echo "Wrote $NFD_FEATURE_FILE (NFD derives feature.node.kubernetes.io/pci-10de.present)"
  else
    echo "WARNING: could not write $NFD_FEATURE_FILE — feature.node.kubernetes.io/pci-10de.present will not be created; the mock is otherwise unaffected" >&2
  fi
else
  # Mirror of the tolerant write above: the removal is the same optional,
  # gated feature on its other arm, so an EROFS or unwritable directory must
  # not abort the entrypoint before step 8 either.
  if rm -f "$NFD_FEATURE_FILE"; then
    echo "Skipping NFD pci-10de feature file (nodeLabels.pciVendorPresent=false)"
  else
    echo "WARNING: could not remove $NFD_FEATURE_FILE — a stale feature file may keep feature.node.kubernetes.io/pci-10de.present alive; the mock is otherwise unaffected" >&2
  fi
fi

# 8. Create GPU Operator compatibility symlink.
#    The GPU Operator's validator DaemonSet mounts hostPath /run/nvidia/driver
#    into the driver-validation init container. By symlinking to our mock driver
#    root, the validator finds nvidia-smi and mock NVML at the expected path.
mkdir -p /host/run/nvidia
ln -sfn /var/lib/nvml-mock/driver /host/run/nvidia/driver

# 8b. Deliberately NOT written here: /run/nvidia/validations/toolkit-ready.
#     Six GPU Operator operand DaemonSets ship an unconditional
#     `toolkit-validation` init container that loops on:
#       until [ -f /run/nvidia/validations/toolkit-ready ]; do sleep 5; done
#     (gpu-operator v26.3.0: assets/state-device-plugin/0500_daemonset.yaml:29,31;
#     gpu-feature-discovery/0500_daemonset.yaml:29,32;
#     state-dcgm-exporter/0800_daemonset.yaml:28,31; state-dcgm/0400_dcgm.yml:28,31;
#     state-mps-control-daemon/0400_daemonset.yaml:31,33;
#     state-mig-manager/0600_daemonset.yaml:28,31.)
#
#     That gate is real, but nvml-mock is not its satisfier and must not
#     pre-empt it. The marker's writer is GPU Operator's own nvidia-validator,
#     running as the operator-validator DaemonSet's `toolkit-validation` init
#     container with COMPONENT=toolkit
#     (assets/state-operator-validation/0500_daemonset.yaml:59-69); that
#     DaemonSet is deployed unconditionally. Toolkit.validate() DELETES the
#     marker (cmd/nvidia-validator/main.go:1134), runs nvidia-smi against our
#     mock driver, and re-creates it only on success (main.go:1153).
#
#     Writing it here was never durable — the validator deletes it on every run
#     and its preStop hook removes every *-ready file on shutdown
#     (state-operator-validation/0500_daemonset.yaml:133-136) — and it let
#     operands clear the gate before any toolkit check had run, turning an
#     ordering barrier into a no-op and making a green operand look like a
#     validated one. See #504.
#
#     The directory needs no mkdir either. The DaemonSets that mount the
#     validations dir directly — device-plugin
#     (state-device-plugin/0500_daemonset.yaml:146-149), mig-manager
#     (state-mig-manager/0600_daemonset.yaml:96-99) and the validator
#     (state-operator-validation/0500_daemonset.yaml:142-145) — all declare
#     hostPath type DirectoryOrCreate. The other four gated operands mount the
#     parent /run/nvidia instead: gpu-feature-discovery/0500_daemonset.yaml:129-132,
#     state-dcgm/0400_dcgm.yml:55-58 and
#     state-mps-control-daemon/0400_daemonset.yaml:125-128 as type Directory
#     (which requires the parent to pre-exist), and
#     state-dcgm-exporter/0800_daemonset.yaml:76-78 with no type at all — step 8
#     above still creates that parent. The validator also does os.Mkdir on the
#     status dir itself (main.go:524).
#
#     Residual hazard, as a debugging pointer: nvidia-validator has a cleanup-all
#     flag (main.go:302-309, env CLEANUP_ALL, default false) that os.RemoveAll's
#     the output dir before recreating it (main.go:513-527), wiping every marker;
#     because the validator's own /run/nvidia/validations is a bind mount
#     (state-operator-validation/0500_daemonset.yaml:54-55, 73-74, 96-97,
#     123-124, 138-139, over the hostPath volume cited above), the RemoveAll then
#     fails EBUSY on the mount point and the validator exits before recreating
#     anything (main.go:516-520 returns ahead of the os.Mkdir at :524), so
#     already-gated pods block on a marker that never returns while the validator
#     itself sits in CrashLoopBackOff. Nothing in assets/ or controllers/ sets
#     CLEANUP_ALL, so this is unreachable by default — but if an operand ever
#     hangs here, check this first.

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
