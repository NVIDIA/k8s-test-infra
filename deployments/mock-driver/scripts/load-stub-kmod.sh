#!/bin/sh
# Copyright 2026 NVIDIA CORPORATION
# SPDX-License-Identifier: Apache-2.0
#
# Loads the stub nvidia kernel module (deployments/mock-driver/kmod/) so the
# NODE and every pod on it get real, kernel-global /proc/driver/nvidia and
# /sys/module/nvidia entries -- the one thing mount-namespace fakes cannot
# provide. Invoked by the nvidia-driver entrypoint when MOCK_KMOD is enabled;
# kept a separate helper (like start-mock-ib.sh) so the entrypoint stays a
# thin dispatcher rather than inlining package-manager and compiler calls.
#
# Acquisition order:
#   1. a prebuilt module at /run/nvidia/mock-kmod/nvidia.ko (built host-side
#      where kernel headers always match the node -- what CI does)
#   2. an in-container build against the node kernel (best effort; only works
#      when this image's distro ships headers for the running kernel)
#
# The loaded module is node-global and persists across driver-pod restarts
# (like a real driver -- the entrypoint deliberately never rmmods it). If it
# is already present, this is a no-op; changing DRIVER_VERSION afterwards
# requires recreating the node, again matching real-driver semantics. Exit
# non-zero if neither path loads the module; the caller decides whether that
# is fatal (MOCK_KMOD=on) or falls back to namespace fakes (auto).
#
# Required env vars: DRIVER_VERSION. Optional: RUN_DIR, KMOD_SRC.
set -eu

RUN_DIR=${RUN_DIR:-/run/nvidia}
KMOD_SRC=${KMOD_SRC:-/kmod}

# Driver versions are digits and dots only (shared guard) -- this both
# rejects typos and makes the value safe to interpolate into the C header.
. /scripts/lib-driver-root.sh
drl_require_version "$DRIVER_VERSION" || exit 1

if [ -e /sys/module/nvidia ]; then
  echo "/sys/module/nvidia already present; not loading the stub module"
  exit 0
fi

# 1. Prebuilt module injected on the host (the conforming, CI-tested path).
if [ -f "$RUN_DIR/mock-kmod/nvidia.ko" ]; then
  if insmod "$RUN_DIR/mock-kmod/nvidia.ko"; then
    echo "Prebuilt stub module loaded (kernel-global /proc and /sys active)"
    exit 0
  fi
  echo "WARNING: prebuilt module failed to load (kernel mismatch?); trying in-container build"
fi

# 2. Best-effort build against the running kernel.
KVER=$(uname -r)
if ! { apt-get update -qq && \
       apt-get install -y -qq "linux-headers-$KVER" build-essential kmod; } > /dev/null 2>&1; then
  echo "ERROR: kernel headers/toolchain unavailable for $KVER" >&2
  exit 1
fi
printf '#define STUB_DRIVER_VERSION "%s"\n' "$DRIVER_VERSION" > "$KMOD_SRC/stub_version.h"
if ! make -s -C "/lib/modules/$KVER/build" M="$KMOD_SRC" modules; then
  echo "ERROR: stub module build failed for $KVER" >&2
  exit 1
fi
if ! insmod "$KMOD_SRC/nvidia.ko"; then
  echo "ERROR: insmod failed" >&2
  exit 1
fi
echo "Stub module built and loaded (kernel-global /proc and /sys active)"
