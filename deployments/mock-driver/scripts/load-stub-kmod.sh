#!/bin/sh
# Copyright 2026 NVIDIA CORPORATION
# SPDX-License-Identifier: Apache-2.0
#
# Loads the stub `nvidia` kernel module (deployments/mock-driver/kmod/) so the
# node and every pod on it get real, kernel-global /proc/driver/nvidia and
# /sys/module/nvidia entries -- the one thing mount-namespace fakes cannot
# provide. Invoked by the nvidia-driver entrypoint when MOCK_KMOD is enabled;
# kept a separate helper so the entrypoint stays a thin dispatcher.
#
# PREBUILT ONLY: this helper never runs a package manager or compiler. The
# module MUST already exist at $RUN_DIR/mock-kmod/nvidia.ko when this script
# runs; the caller (typically the Go E2E harness, or an operator on a
# disposable Kind node) is responsible for building it host-side against a
# matching kernel and staging it at that path. Building in-container would
# have to install kernel headers, gcc, and make on every driver-pod restart,
# which is impossibly slow on real nodes and unsupported on distros without a
# packaged linux-headers-$(uname -r).
#
# Loaded module is node-global and persists across driver-pod restarts (like
# a real driver -- the entrypoint deliberately never rmmods it); changing
# DRIVER_VERSION afterwards requires recreating the node. Because
# k8s-driver-manager (v0.11.0+) will `rmmod nvidia` from its uninstall init
# container when it detects the module WITHOUT a matching state file, callers
# who need lifecycle testing must exercise that flow explicitly.
#
# Required env vars: DRIVER_VERSION. Optional: RUN_DIR.
set -eu

RUN_DIR=${RUN_DIR:-/run/nvidia}
STUB_PATH=${STUB_PATH:-$RUN_DIR/mock-kmod/nvidia.ko}

. /scripts/lib-driver-root.sh
drl_require_version "$DRIVER_VERSION" || exit 1

if [ -e /sys/module/nvidia ]; then
  echo "/sys/module/nvidia already present; not reloading the stub module"
  exit 0
fi

if [ ! -f "$STUB_PATH" ]; then
  echo "ERROR: MOCK_KMOD is enabled but no prebuilt module at $STUB_PATH." >&2
  echo "Build it host-side against the node kernel and stage it there before" >&2
  echo "starting the driver container (see docs/mock-driver.md#MOCK_KMOD)." >&2
  exit 1
fi

# Validate module name and embedded version BEFORE we insmod. modinfo is
# cheap and cross-arch; it also refuses to parse an .ko produced by a
# different linker, so a malformed prebuilt fails here rather than during
# insmod (which would hard-fault with a less actionable error).
if ! command -v modinfo >/dev/null 2>&1; then
  echo "ERROR: modinfo missing from mock-driver image (kmod package removed?)" >&2
  exit 1
fi
_stub_name=$(modinfo -F name "$STUB_PATH" 2>/dev/null || true)
if [ "$_stub_name" != "nvidia" ]; then
  echo "ERROR: prebuilt module at $STUB_PATH has name '$_stub_name'; expected 'nvidia'." >&2
  echo "Loading a stub with any other name would not create /sys/module/nvidia." >&2
  exit 1
fi
_stub_version=$(modinfo -F version "$STUB_PATH" 2>/dev/null || true)
if [ -n "$_stub_version" ] && [ "$_stub_version" != "$DRIVER_VERSION" ]; then
  echo "ERROR: prebuilt module version '$_stub_version' does not match" >&2
  echo "DRIVER_VERSION='$DRIVER_VERSION'. Rebuild the module with the profile" >&2
  echo "driver_version so /sys/module/nvidia/version agrees with NVML." >&2
  exit 1
fi

if ! insmod "$STUB_PATH"; then
  echo "ERROR: insmod $STUB_PATH failed (kernel mismatch, taint conflict, or" >&2
  echo "an existing real 'nvidia' module?). Nothing was loaded." >&2
  exit 1
fi
echo "Prebuilt stub module loaded (kernel-global /proc and /sys active)"
