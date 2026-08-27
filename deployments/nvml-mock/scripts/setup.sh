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

# 4b. Stage InfiniBand tools and preload shims for node-wide NRI injection.
#     The NRI plugin mounts /var/lib/nvml-mock at /opt/nvml-mock in each
#     workload, then prepends driver/usr/bin and driver/usr/lib64 and appends
#     driver/usr/local/lib shims to LD_PRELOAD.
#
#     The ELF tools arrive pre-staged and pre-patched from the image build
#     (stage-ib-tools.sh): each carries RPATH=$ORIGIN/../lib64 and its shared
#     libraries sit alongside, so they resolve libibmad/libibumad/libibverbs/
#     libnl from the overlay itself.
#
#     Before this, the overlay carried the binaries and none of their
#     libraries, so an injected pod got "error while loading shared
#     libraries" on the first tool it ran — measured on debian:bookworm-slim
#     as well as on distroless, since neither that image nor ubuntu:22.04
#     ships the IB stack. See NVIDIA/k8s-test-infra#438.
if [ -d /usr/local/nvml-mock-ib/bin ]; then
  cp -a /usr/local/nvml-mock-ib/bin/. "$DRIVER_ROOT/usr/bin/"
  cp -a /usr/local/nvml-mock-ib/lib64/. "$DRIVER_ROOT/usr/lib64/"
  echo "Staged IB CLI tools + shared libraries (RPATH-enabled)"
else
  echo "WARNING: no pre-staged IB tools in the image; falling back to unpatched copies" >&2
fi
# Anything the build did not pre-stage. In practice this is ibstatus, which is
# a /bin/sh script rather than an ELF binary: there is no RPATH to give it, and
# it only runs in images that ship a shell. Never overwrite a pre-staged tool —
# the copy under /usr/sbin has no RPATH.
for tool in ibnetdiscover ibstat iblinkinfo ibstatus sminfo ibping ibv_devinfo; do
  if [ ! -e "$DRIVER_ROOT/usr/bin/$tool" ] && command -v "$tool" >/dev/null 2>&1; then
    cp "$(command -v "$tool")" "$DRIVER_ROOT/usr/bin/$tool"
  fi
done
# Stage the ibverbs provider config. libibverbs reads this directory from a
# compile-time absolute path (/etc/libibverbs.d) and offers no environment
# override, so this copy does not redirect an injected container's lookup — the
# mock answers the verbs API through the LD_PRELOAD shim libibmockverbs.so.1
# and never consults it. It is here for consumers that mount the overlay as a
# root, and so the provider set travels with the tools it belongs to.
if [ -d /etc/libibverbs.d ]; then
  mkdir -p "$DRIVER_ROOT/etc/libibverbs.d"
  cp -a /etc/libibverbs.d/. "$DRIVER_ROOT/etc/libibverbs.d/" 2>/dev/null || true
fi
# Stage the fabric consumer so node-wide NRI-injected pods can verify their
# per-node ComputeDomain identity (nvmlDeviceGetGpuFabricInfo) the same way the
# compute-domain demo does inside the daemon pod. It resolves the mock NVML
# library via the LD_LIBRARY_PATH the NRI plugin injects.
if [ -x /usr/local/bin/check-fabric ]; then
  cp /usr/local/bin/check-fabric "$DRIVER_ROOT/usr/bin/check-fabric"
fi
cp -a /usr/local/lib/libibmock*.so* "$DRIVER_ROOT/usr/local/lib/" 2>/dev/null || true

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

# 7. Label node with nvidia.com/gpu.present (requires RBAC: get+patch on nodes).
if command -v kubectl >/dev/null 2>&1; then
  kubectl label node "$NODE_NAME" nvidia.com/gpu.present=true --overwrite || true
fi

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
#     state-dcgm-exporter/0800_daemonset.yaml:76-78 with no type at all — the
#     node-agent sidecar creates that parent. The validator also does os.Mkdir on the
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

# 10. Fabric Manager: on NVSwitch platforms (HGX H100 / GB200 / GB300) the
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
#     The readiness marker lives on a DirectoryOrCreate hostPath
#     that survives pod restarts, and the daemon re-asserts it every 2s — so a
#     stale marker from a prior pod could make a fresh pod report COMPLETED
#     before its own daemon is ready. Clear it here so every pod starts in a
#     clean IN_PROGRESS state until *this* daemon writes the marker.
MOCK_FM_MODE=$(printf '%s' "${MOCK_FABRICMANAGER:-off}" | tr '[:upper:]' '[:lower:]')
case "$MOCK_FM_MODE" in
  off | on) ;;
  *)
    echo "ERROR: MOCK_FABRICMANAGER='$MOCK_FABRICMANAGER' is invalid; expected off or on" >&2
    exit 1
    ;;
esac
FM_STATE_DIR="${MOCK_FABRICMANAGER_STATE_DIR:-/var/lib/nvml-mock/fabric-state}"

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
