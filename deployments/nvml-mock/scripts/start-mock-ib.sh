#!/bin/sh
# Copyright 2026 NVIDIA CORPORATION
# SPDX-License-Identifier: Apache-2.0
#
# Start mock-ib when MOCK_IB=1.
set -e

if [ "${MOCK_IB:-0}" != "1" ]; then
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

# Tee daemon output to /tmp/mock-ib.log so tests/e2e/validate-ibping.sh can
# print the last 40 lines on failure (`tail -40 /tmp/mock-ib.log`) without
# needing kubectl-side log scraping. The container's stdout still receives
# every line (so `kubectl logs` continues to work), the file just gives the
# bash validator a stable on-pod location to read from. mock-ib's Go `log`
# package line-flushes by default, so plain tee is sufficient.
#
# Note: `exec cmd | tee file` would replace this shell with the left side of
# the pipeline only in some shells; we just run the pipeline plainly so the
# script keeps a thin wrapper PID. The whole thing is launched as `& ` from
# setup.sh, so blocking here is fine — it lives for the lifetime of the pod.
LOG_FILE="${MOCK_IB_LOG_FILE:-/tmp/mock-ib.log}"
mkdir -p "$(dirname "$LOG_FILE")"
"$@" 2>&1 | tee -a "$LOG_FILE"
