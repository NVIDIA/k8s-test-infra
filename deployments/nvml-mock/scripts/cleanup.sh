#!/bin/sh
# Copyright 2026 NVIDIA CORPORATION
# SPDX-License-Identifier: Apache-2.0
#
# Cleans up mock GPU environment from host. Runs as preStop hook.

. /scripts/lib-host-driver.sh

MOCK_GPU_DIR="/host/var/lib/nvml-mock"
NVML_MOCK_SYMLINK_TARGET=/var/lib/nvml-mock/driver

# Host driver masquerade cleanup (manifest-driven; must run before the tree
# wipe below removes the manifest). Only files/symlinks/devices whose
# recorded shape still matches the actual host state are removed -- a
# tampered or foreign entry aborts cleanup and preserves the manifest so a
# human can reconcile it.
HOST_MANIFEST=$(hdrl_manifest_path "$MOCK_GPU_DIR")
HOST_MANIFEST_KEEP=false
if [ -f "$HOST_MANIFEST" ] && [ -d /hostroot ]; then
  if hdrl_verify_manifest /hostroot "$HOST_MANIFEST"; then
    hdrl_remove_verified_manifest /hostroot "$HOST_MANIFEST"
    chroot /hostroot ldconfig 2>/dev/null || true
    echo "Host driver masquerade removed"
  else
    echo "ERROR: host driver manifest at $HOST_MANIFEST no longer matches actual" >&2
    echo "host state (see errors above). Refusing to remove anything and keeping" >&2
    echo "the manifest so an operator can reconcile it." >&2
    HOST_MANIFEST_KEEP=true
  fi
elif [ -f "$HOST_MANIFEST" ]; then
  echo "WARNING: host driver manifest present but /hostroot not mounted; host files not removed"
  HOST_MANIFEST_KEEP=true
fi

if [ -d "$MOCK_GPU_DIR" ] && [ "$MOCK_GPU_DIR" = "/host/var/lib/nvml-mock" ]; then
  if [ -f "$HOST_MANIFEST" ] && [ "$HOST_MANIFEST_KEEP" = "true" ]; then
    # Host cleanup either could not run (no /hostroot) or bailed out on a
    # verification mismatch: keep the manifest -- it is the only record of
    # masquerade files still on the host, and a later hostDriver=true install
    # replays it to self-heal (or an operator inspects it manually).
    find "$MOCK_GPU_DIR" -mindepth 1 -maxdepth 1 ! -name host-driver-manifest.txt -exec rm -rf {} +
  else
    # ${MOCK_GPU_DIR:?} guards against an empty expansion to /* even though the
    # enclosing test already pins it to the literal state dir.
    rm -rf "${MOCK_GPU_DIR:?}"/*
  fi
fi
# Remove the GPU Operator compatibility symlink ONLY when it is ours. A
# non-symlink at this path is never nvml-mock's -- an operator-managed
# driver DaemonSet owns the directory (with a Bidirectional rbind), so
# touching it here could detach a running driver.
if [ -L "/host/run/nvidia/driver" ]; then
  _cur_target=$(readlink "/host/run/nvidia/driver")
  if [ "$_cur_target" = "$NVML_MOCK_SYMLINK_TARGET" ]; then
    rm -f "/host/run/nvidia/driver"
    echo "GPU Operator driver symlink removed"
  else
    echo "WARNING: /run/nvidia/driver is a symlink to $_cur_target; not ours, leaving it alone"
  fi
fi
# Remove GPU Operator toolkit-ready marker (counterpart to setup.sh:8b)
rm -f "/host/run/nvidia/validations/toolkit-ready"
# Remove CDI spec
CDI_FILE="/host/var/run/cdi/nvidia.yaml"
if [ -f "$CDI_FILE" ]; then
  rm -f "$CDI_FILE"
  echo "CDI spec removed"
fi
if command -v kubectl >/dev/null 2>&1; then
  kubectl label node "$NODE_NAME" nvidia.com/gpu.present- || true
  kubectl label node "$NODE_NAME" feature.node.kubernetes.io/pci-10de.present- || true
fi
echo "Mock GPU environment cleaned up on $NODE_NAME"
