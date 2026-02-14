#!/bin/sh
# Copyright 2026 NVIDIA CORPORATION
# SPDX-License-Identifier: Apache-2.0
set -e
/scripts/setup.sh
echo "Mock GPU environment ready. Sleeping..."
exec sleep infinity
