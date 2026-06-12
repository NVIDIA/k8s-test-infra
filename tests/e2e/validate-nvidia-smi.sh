#!/bin/bash
# Copyright 2026 NVIDIA CORPORATION
# SPDX-License-Identifier: Apache-2.0
#
# Validate nvidia-smi output against mock NVML on a Kind node.
# Usage: validate-nvidia-smi.sh <node-container-name> <expected-gpu-name> <expected-gpu-count>
set -euo pipefail

NODE_CONTAINER="${1:?Usage: $0 <node-container> <gpu-name> <gpu-count>}"
GPU_NAME="${2:?}"
GPU_COUNT="${3:?}"

echo "=== Validating nvidia-smi on $NODE_CONTAINER ==="

# Run nvidia-smi inside the Kind node container.
# The ELF binary has RPATH=$ORIGIN/../lib64, so it finds libs automatically.
NVIDIA_SMI_CMD="/var/lib/nvml-mock/driver/usr/bin/nvidia-smi"

echo "--- nvidia-smi default output ---"
OUTPUT=$(docker exec "$NODE_CONTAINER" sh -c "$NVIDIA_SMI_CMD" 2>&1) || {
  echo "FAIL: nvidia-smi exited with error"
  echo "$OUTPUT"
  exit 1
}
echo "$OUTPUT"

# Validate GPU name and count from `nvidia-smi -L`. The expected name is the
# profile's device_defaults.name (the CI passes it straight through), which is
# the full product string, e.g. "NVIDIA A100-SXM4-40GB". We match against -L
# rather than the default table because that table truncates the Name column,
# so the full string would not appear there verbatim.
echo ""
echo "--- nvidia-smi -L ---"
LIST_OUTPUT=$(docker exec "$NODE_CONTAINER" sh -c "$NVIDIA_SMI_CMD -L" 2>&1) || {
  echo "FAIL: nvidia-smi -L exited with error"
  echo "$LIST_OUTPUT"
  exit 1
}
echo "$LIST_OUTPUT"

# here-string, not `echo | grep -q`: grep -q exits on first match and closes
# the pipe, so echo takes SIGPIPE and `set -o pipefail` would fail the `if`.
if grep -qF -- "$GPU_NAME" <<< "$LIST_OUTPUT"; then
  echo "PASS: GPU name '$GPU_NAME' found in nvidia-smi -L output"
else
  echo "FAIL: GPU name '$GPU_NAME' not found in nvidia-smi -L output"
  exit 1
fi

ACTUAL_COUNT=$(echo "$LIST_OUTPUT" | grep -c "^GPU")
if [ "$ACTUAL_COUNT" -eq "$GPU_COUNT" ]; then
  echo "PASS: Found $ACTUAL_COUNT GPUs (expected $GPU_COUNT)"
else
  echo "FAIL: Found $ACTUAL_COUNT GPUs (expected $GPU_COUNT)"
  exit 1
fi

echo ""
echo "=== nvidia-smi validation PASSED ==="
