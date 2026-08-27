#!/bin/sh
# Copyright 2026 NVIDIA CORPORATION
# SPDX-License-Identifier: Apache-2.0
#
# Cleans up mock GPU environment from host. Runs as preStop hook.
MOCK_GPU_DIR="/host/var/lib/nvml-mock"

if [ -d "$MOCK_GPU_DIR" ] && [ "$MOCK_GPU_DIR" = "/host/var/lib/nvml-mock" ]; then
  rm -rf "$MOCK_GPU_DIR"/*
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
if command -v kubectl >/dev/null 2>&1; then
  kubectl label node "$NODE_NAME" nvidia.com/gpu.present- || true
fi

echo "Mock GPU environment cleaned up on $NODE_NAME"
