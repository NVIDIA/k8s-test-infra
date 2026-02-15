#!/bin/sh
# Copyright 2026 NVIDIA CORPORATION
# SPDX-License-Identifier: Apache-2.0
#
# Cleans up mock GPU environment from host. Runs as preStop hook.
MOCK_GPU_DIR="/host/var/lib/nvidia-mock"

if [ -d "$MOCK_GPU_DIR" ] && [ "$MOCK_GPU_DIR" = "/host/var/lib/nvidia-mock" ]; then
  rm -rf "$MOCK_GPU_DIR"/*
fi
if command -v kubectl >/dev/null 2>&1; then
  kubectl label node "$NODE_NAME" nvidia.com/gpu.present- || true
  kubectl label node "$NODE_NAME" feature.node.kubernetes.io/pci-10de.present- || true
fi
echo "Mock GPU environment cleaned up on $NODE_NAME"
