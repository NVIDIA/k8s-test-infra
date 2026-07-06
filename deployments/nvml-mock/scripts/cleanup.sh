#!/bin/sh
# Copyright 2026 NVIDIA CORPORATION
# SPDX-License-Identifier: Apache-2.0
#
# Cleans up mock GPU environment from host. Runs as preStop hook.
MOCK_GPU_DIR="/host/var/lib/nvml-mock"

# Host driver masquerade cleanup (manifest-driven; must run before the tree
# wipe below removes the manifest). Only files/symlinks nvml-mock itself
# recorded are removed -- plain rm -f, never recursive.
HOST_MANIFEST="$MOCK_GPU_DIR/host-driver-manifest.txt"
if [ -f "$HOST_MANIFEST" ] && [ -d /hostroot ]; then
  while IFS= read -r p; do
    case "$p" in
      /*) rm -f "/hostroot$p" ;;
    esac
  done < "$HOST_MANIFEST"
  chroot /hostroot ldconfig 2>/dev/null || true
  echo "Host driver masquerade removed"
elif [ -f "$HOST_MANIFEST" ]; then
  echo "WARNING: host driver manifest present but /hostroot not mounted; host files not removed"
fi

if [ -d "$MOCK_GPU_DIR" ] && [ "$MOCK_GPU_DIR" = "/host/var/lib/nvml-mock" ]; then
  if [ -f "$HOST_MANIFEST" ] && [ ! -d /hostroot ]; then
    # Host cleanup could not run: keep the manifest -- it is the only record
    # of masquerade files still on the host, and a later hostDriver=true
    # install replays it to self-heal.
    find "$MOCK_GPU_DIR" -mindepth 1 -maxdepth 1 ! -name host-driver-manifest.txt -exec rm -rf {} +
  else
    rm -rf "$MOCK_GPU_DIR"/*
  fi
fi
# Remove GPU Operator compatibility symlink. Unconditional on DRIVER_SYMLINK:
# the -L test alone protects managed-driver mode (a driver-owned directory or
# mountpoint is never a symlink), and removing a symlink regardless of the
# current mode self-heals stale links left by earlier installs.
if [ -L "/host/run/nvidia/driver" ]; then
  rm -f "/host/run/nvidia/driver"
  echo "GPU Operator driver symlink removed"
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
