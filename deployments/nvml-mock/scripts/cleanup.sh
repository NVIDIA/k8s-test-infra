#!/bin/sh
# Copyright 2026 NVIDIA CORPORATION
# SPDX-License-Identifier: Apache-2.0
#
# Cleans up mock GPU environment from host. Runs as preStop hook.
MOCK_GPU_DIR="/host/var/lib/nvml-mock"

if [ -d "$MOCK_GPU_DIR" ] && [ "$MOCK_GPU_DIR" = "/host/var/lib/nvml-mock" ]; then
  rm -rf "$MOCK_GPU_DIR"/*
fi
# Remove GPU Operator compatibility symlink
if [ -L "/host/run/nvidia/driver" ]; then
  rm -f "/host/run/nvidia/driver"
  echo "GPU Operator driver symlink removed"
fi
# NOTE: /run/nvidia/validations/toolkit-ready is deliberately NOT removed here.
# setup.sh no longer creates it (see setup.sh step 8b); its owner is GPU
# Operator's nvidia-validator. This hook is nvml-mock's preStop, so removing the
# marker would delete another component's state from a hook we own. In the
# GPU-Operator e2e that was probably benign in practice: dropping the
# nvidia.com/gpu.present label below recycles the operator-validator alongside
# us, and it rewrites the marker within seconds (observed live). The rm is dead
# code either way, and the hazard is real wherever the validator does not
# happen to restart.
# Remove CDI spec
CDI_FILE="/host/var/run/cdi/nvidia.yaml"
if [ -f "$CDI_FILE" ]; then
  rm -f "$CDI_FILE"
  echo "CDI spec removed"
fi
if command -v kubectl >/dev/null 2>&1; then
  kubectl label node "$NODE_NAME" nvidia.com/gpu.present- || true
  # Mirror of setup.sh step 7: remove the NFD PCI label only when this pod was
  # the one that wrote it. Chart value nodeLabels.pciVendorPresent maps to
  # MOCK_NFD_PCI_LABEL; with the gate off a real NFD owns that key, and an
  # unconditional delete here would strip a label the mock never created.
  if [ "$(printf '%s' "${MOCK_NFD_PCI_LABEL:-on}" | tr '[:upper:]' '[:lower:]')" = "on" ]; then
    kubectl label node "$NODE_NAME" feature.node.kubernetes.io/pci-10de.present- || true
  fi
fi
echo "Mock GPU environment cleaned up on $NODE_NAME"
