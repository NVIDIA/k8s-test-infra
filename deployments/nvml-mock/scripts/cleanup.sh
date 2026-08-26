#!/bin/sh
# Copyright 2026 NVIDIA CORPORATION
# SPDX-License-Identifier: Apache-2.0
#
# preStop hook for the nvml-mock container. Drops the node label this pod
# caused to exist.
#
# It deliberately does not touch /host/var/lib/nvml-mock. The node agent tears
# that tree down per-simulator on SIGTERM so each removes only what it created;
# a blanket rm here raced that teardown and won, since hooks and SIGTERM are
# dispatched per container and the agent has no preStop of its own.
#
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
