#!/bin/sh
# Copyright 2026 NVIDIA CORPORATION
# SPDX-License-Identifier: Apache-2.0
#
# Cleans up mock GPU environment from host. Runs as preStop hook.
MOCK_GPU_DIR="/host/var/lib/nvml-mock"

if [ -d "$MOCK_GPU_DIR" ] && [ "$MOCK_GPU_DIR" = "/host/var/lib/nvml-mock" ]; then
  # Also removes the IB char devices under ib/dev/infiniband created by setup.sh.
  rm -rf "$MOCK_GPU_DIR"/*
fi
# Remove GPU Operator compatibility symlink
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
# Remove the NFD local-source feature file (written by setup.sh when
# infiniband.nfd.publishNicLabel is enabled). NFD drops the derived
# feature.node.kubernetes.io/pci-15b3.present label on its next scan. The
# mount only exists when the feature is enabled, so this is best-effort.
NFD_FEATURES_DIR="${MOCK_IB_NFD_FEATURES_DIR:-/host-nfd-features}"
rm -f "$NFD_FEATURES_DIR/nvml-mock-ib.features" 2>/dev/null || true
echo "Mock GPU environment cleaned up on $NODE_NAME"
