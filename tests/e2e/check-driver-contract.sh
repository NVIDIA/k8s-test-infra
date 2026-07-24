#!/bin/bash
# Copyright 2026 NVIDIA CORPORATION
# SPDX-License-Identifier: Apache-2.0
#
# Driver-container contract drift check.
#
# The mock-driver image (deployments/mock-driver/) satisfies a contract the
# GPU Operator imposes on the driver DaemonSet it renders: the startup-probe
# script (refcnt existence + nvidia-smi) and the DaemonSet shape (fixed
# command "nvidia-driver init", hostPath mounts, preStop hook). That contract
# is reverse-engineered, not documented, and has changed between operator
# releases (v25.x used an inline nvidia-smi probe; v26.x switched to the
# refcnt-checking ConfigMap script).
#
# This script re-fetches the contract assets from the pinned operator tag and
# diffs them against the vendored copies in tests/e2e/contract/<tag>/, so a
# contract change fails CI loudly instead of silently breaking the mock.
#
# Usage: check-driver-contract.sh [tag]
#   tag defaults to the newest vendored version directory.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
CONTRACT_ROOT="$REPO_ROOT/tests/e2e/contract"
ASSETS="0400_configmap.yaml 0500_daemonset.yaml"
UPSTREAM=https://raw.githubusercontent.com/NVIDIA/gpu-operator

# Default to the newest vendored version (only version-shaped directories --
# stray files or backups in contract/ must not win the sort). "|| true" keeps
# a missing contract dir from silently aborting under set -euo pipefail; the
# friendly vendor-help error below handles that case.
DEFAULT_TAG=$(find "$CONTRACT_ROOT" -mindepth 1 -maxdepth 1 -type d -name 'v*' 2>/dev/null \
  | sed 's|.*/||' | sort -V | tail -1 || true)
TAG=${1:-"$DEFAULT_TAG"}
if [ -z "$TAG" ]; then
  echo "ERROR: no version argument given and no vendored v* directory under $CONTRACT_ROOT" >&2
  exit 1
fi
VENDORED_DIR="$CONTRACT_ROOT/$TAG"

if [ ! -d "$VENDORED_DIR" ]; then
  echo "ERROR: no vendored contract at $VENDORED_DIR" >&2
  echo "Vendor it with:" >&2
  echo "  mkdir -p $VENDORED_DIR" >&2
  for asset in $ASSETS; do
    echo "  curl -fsSL $UPSTREAM/$TAG/assets/state-driver/$asset -o $VENDORED_DIR/$asset" >&2
  done
  exit 1
fi

TMP_DIR=$(mktemp -d)
trap 'rm -rf "$TMP_DIR"' EXIT

RC=0
for asset in $ASSETS; do
  URL="$UPSTREAM/$TAG/assets/state-driver/$asset"
  if ! curl -fsSL "$URL" -o "$TMP_DIR/$asset"; then
    echo "ERROR: failed to fetch $URL" >&2
    RC=1
    continue
  fi
  if diff -u "$VENDORED_DIR/$asset" "$TMP_DIR/$asset"; then
    echo "PASS: $TAG/$asset matches upstream"
  else
    echo "FAIL: $TAG/$asset drifted from upstream -- the driver-container" >&2
    echo "contract changed. Review deployments/mock-driver/scripts/nvidia-driver" >&2
    echo "against the diff above, then re-vendor tests/e2e/contract/$TAG/." >&2
    RC=1
  fi
done

exit "$RC"
