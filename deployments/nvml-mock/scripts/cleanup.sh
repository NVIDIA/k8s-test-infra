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
# Remove the CDI specs setup.sh staged. Both live in the same directory setup.sh
# writes to (CDI_DIR at setup.sh:120), and both name device nodes under
# $MOCK_GPU_DIR, which the rm -rf above has just deleted. Leaving either behind
# hands the runtime a spec whose hostPaths no longer exist: containerd fails
# container creation with "failed to stat CDI host device", the kubelet retries
# it forever, and the NRI plugin keeps emitting the reference because its
# staged-spec check (cdiSpecStaged, adjust.go) is a bare stat of the spec file.
for CDI_FILE in /host/var/run/cdi/nvidia.yaml /host/var/run/cdi/nvml-mock-nri.yaml; do
  if [ -f "$CDI_FILE" ]; then
    rm -f "$CDI_FILE"
    echo "CDI spec removed: $CDI_FILE"
  fi
done
if command -v kubectl >/dev/null 2>&1; then
  kubectl label node "$NODE_NAME" nvidia.com/gpu.present- || true
fi

# Mirror of setup.sh step 7: unwind only the write that step made. NFD drops
# the label on its next scan once the file is gone. Gated on the same variable,
# so with the gate off this does nothing — setup.sh never wrote the file on
# this pod and already removed any copy an earlier run left behind.
if [ "$(printf '%s' "${MOCK_NFD_PCI_LABEL:-on}" | tr '[:upper:]' '[:lower:]')" = "on" ]; then
  rm -f /host/etc/kubernetes/node-feature-discovery/features.d/nvml-mock.features
fi
echo "Mock GPU environment cleaned up on $NODE_NAME"
