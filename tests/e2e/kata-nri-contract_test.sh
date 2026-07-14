#!/usr/bin/env bash
# Copyright 2026 NVIDIA CORPORATION
# SPDX-License-Identifier: Apache-2.0
set -euo pipefail
ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
WORKFLOW="$ROOT/.github/workflows/nvml-mock-e2e.yaml"
KIND_CONFIG="$ROOT/tests/e2e/kind-kata-config.yaml"
RUNNER="$ROOT/tests/e2e/run-kata-nri.sh"
fail() { echo "FAIL: $*" >&2; exit 1; }
contains() { grep -Fq -- "$2" "$1" || fail "$1 does not contain: $2"; }
not_contains() { ! grep -Fq -- "$2" "$1" || fail "$1 still contains: $2"; }
contains "$KIND_CONFIG" '[plugins."io.containerd.nri.v1.nri"]'
contains "$KIND_CONFIG" 'socket_path = "/var/run/nri/nri.sock"'
contains "$WORKFLOW" '--set nri.enabled=true'
contains "$WORKFLOW" '--namespace "$NVML_MOCK_NAMESPACE"'
contains "$WORKFLOW" './tests/e2e/run-kata-nri.sh'
contains "$WORKFLOW" 'configured by runtime'
not_contains "$WORKFLOW" 'device-plugin-kata'
test ! -e "$ROOT/tests/e2e/device-plugin-kata.yaml" || fail "obsolete device-plugin-kata.yaml still exists"
test -x "$RUNNER" || fail "$RUNNER is missing or not executable"
contains "$RUNNER" 'runtimeClassName: kata-qemu'
contains "$RUNNER" 'nvml-mock.nvidia.com/devices: "true"'
contains "$RUNNER" 'nvml-mock.nvidia.com/inject: "false"'
not_contains "$RUNNER" 'nvidia.com/gpu'
echo "PASS: Kata NRI repository contracts"
