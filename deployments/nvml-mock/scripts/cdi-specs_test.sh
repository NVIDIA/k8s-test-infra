#!/bin/bash
# Copyright 2026 NVIDIA CORPORATION
# SPDX-License-Identifier: Apache-2.0
set -euo pipefail
cd "$(dirname "$0")"
. ./generate-cdi-specs.sh
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT
write_nvml_mock_cdi_spec "$TMP" nvml 0 0
write_nvml_mock_cdi_spec "$TMP" ib 1 0
write_nvml_mock_cdi_spec "$TMP" full 1 1
grep -q 'kind: "nvml-mock.nvidia.com/mock"' "$TMP/nvml-mock-ib.yaml"
grep -q 'MOCK_IB=full' "$TMP/nvml-mock-ib.yaml"
grep -q 'MOCK_PCI_ROOT' "$TMP/nvml-mock-full.yaml"
grep -q 'libpcimocksys' "$TMP/nvml-mock-full.yaml"
echo "PASS"
