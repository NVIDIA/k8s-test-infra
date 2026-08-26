#!/bin/sh
# Copyright 2026 NVIDIA CORPORATION
# SPDX-License-Identifier: Apache-2.0
#
# preStop hook for the nvml-mock container. Drops the node label this pod
# caused to exist. Everything else it used to do now belongs to the node agent.
#
# This hook deliberately does NOT remove /host/var/lib/nvml-mock. The node
# agent tears that tree down per-simulator on SIGTERM (Revoke, then Discard),
# so each simulator removes exactly what it created and leaves surfaces owned
# by others — notably gpudriver's files, which sit in directories the
# infiniband simulator also writes to — intact.
#
# A blanket `rm -rf` here defeated that. preStop hooks and SIGTERM are
# dispatched per container, and the node agent has no preStop of its own, so
# the wipe raced the agent's teardown on the same hostPath and usually won:
# Discard then found an empty tree and became a no-op. See MEP-0003, which
# scopes Discard to simulator-created files and leaves the root itself to the
# pod's hostPath lifecycle.
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
