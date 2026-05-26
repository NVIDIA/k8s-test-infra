#!/bin/sh
# Copyright 2026 NVIDIA CORPORATION
# SPDX-License-Identifier: Apache-2.0
#
# Start mock-ib when MOCK_IB_PING=1.
set -e

if [ "${MOCK_IB_PING:-0}" != "1" ]; then
  exit 0
fi

SOCKET="${MOCK_IB_PING_SOCKET:-/run/mock-ib.sock}"
IB_ROOT="${MOCK_IB_ROOT:-/var/lib/nvml-mock/ib}"
PORT="${MOCK_IB_PING_PORT:-18515}"

set -- /usr/local/bin/mock-ib \
  -config "${MOCK_IB_CONFIG:-/etc/nvml-mock/config.yaml}" \
  -gpu-count "${GPU_COUNT:-0}" \
  -node-name "${NODE_NAME:-}" \
  -socket "$SOCKET" \
  -ib-root "$IB_ROOT" \
  -port "$PORT"
if [ "${MOCK_IB_PING_FABRIC:-0}" = "1" ]; then
  set -- "$@" -fabric
fi
exec "$@"
