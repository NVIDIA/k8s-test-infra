#!/bin/sh
# SPDX-FileCopyrightText: Copyright (c) NVIDIA CORPORATION & AFFILIATES. All rights reserved.
# SPDX-License-Identifier: Apache-2.0
#
# Copy of the driver startup probe from GPU Operator v26.7.0, assets/state-driver/0400_configmap.yaml,
# under the GPU Operator's license, the same as this repository's.
set -eu

VALIDATIONS_DIR="/run/nvidia/validations"
READY_FILE="${VALIDATIONS_DIR}/.driver-ctr-ready"
DAEMONS_STATUS_FILE="${VALIDATIONS_DIR}/.driver-daemons-status"

mkdir -p "${VALIDATIONS_DIR}"

if [ ! -f /sys/module/nvidia/refcnt ]; then
  echo "NVIDIA kernel module not loaded"
  exit 1
fi

if [ -f "$DAEMONS_STATUS_FILE" ]; then
  daemons_status=$(cat "$DAEMONS_STATUS_FILE")
  if [ "$daemons_status" != "Ready" ]; then
    echo "NVIDIA driver daemons are not ready; Current status: $daemons_status"
    exit 1
  fi
fi

if ! nvidia-smi; then
  echo "nvidia-smi failed"
  exit 1
fi

GPU_DIRECT_RDMA_ENABLED="${GPU_DIRECT_RDMA_ENABLED:-false}"
GDS_ENABLED="${GDS_ENABLED:-false}"
GDRCOPY_ENABLED="${GDRCOPY_ENABLED:-false}"

TMP_FILE="${READY_FILE}.tmp"

{
  echo "GDRCOPY_ENABLED: ${GDRCOPY_ENABLED}"
  echo "GDS_ENABLED: ${GDS_ENABLED}"
  echo "GPU_DIRECT_RDMA_ENABLED: ${GPU_DIRECT_RDMA_ENABLED}"
} > "$TMP_FILE"

mv "$TMP_FILE" "$READY_FILE"
