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
# The ELF binary needs LD_LIBRARY_PATH to find mock libnvidia-ml.so.1 via dlopen.
NVIDIA_SMI_CMD="LD_LIBRARY_PATH=/var/lib/nvidia-mock/driver/usr/lib64 /var/lib/nvidia-mock/driver/usr/bin/nvidia-smi"

echo "--- nvidia-smi default output ---"
OUTPUT=$(docker exec "$NODE_CONTAINER" sh -c "$NVIDIA_SMI_CMD" 2>&1) || {
  echo "FAIL: nvidia-smi exited with error"
  echo "$OUTPUT"
  exit 1
}
echo "$OUTPUT"

# Validate GPU name appears in output
if echo "$OUTPUT" | grep -qF -- "$GPU_NAME"; then
  echo "PASS: GPU name '$GPU_NAME' found in output"
else
  echo "FAIL: GPU name '$GPU_NAME' not found in output"
  exit 1
fi

# Validate GPU count (nvidia-smi -L lists one line per GPU)
echo ""
echo "--- nvidia-smi -L ---"
LIST_OUTPUT=$(docker exec "$NODE_CONTAINER" sh -c "$NVIDIA_SMI_CMD -L" 2>&1) || {
  echo "FAIL: nvidia-smi -L exited with error"
  echo "$LIST_OUTPUT"
  exit 1
}
echo "$LIST_OUTPUT"

ACTUAL_COUNT=$(echo "$LIST_OUTPUT" | grep -c "^GPU")
if [ "$ACTUAL_COUNT" -eq "$GPU_COUNT" ]; then
  echo "PASS: Found $ACTUAL_COUNT GPUs (expected $GPU_COUNT)"
else
  echo "FAIL: Found $ACTUAL_COUNT GPUs (expected $GPU_COUNT)"
  exit 1
fi

echo ""
echo "=== nvidia-smi validation PASSED ==="
