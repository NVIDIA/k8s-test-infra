#!/bin/sh
# Copyright 2026 NVIDIA CORPORATION
# SPDX-License-Identifier: Apache-2.0
#
# PID 1 of smoke.sh's container, standing in for the node: stages what Mokka would, binds the
# driver root as the node agent does, leaves what a killed stand-in would, and reports the binds
# left once the stand-in stops.
set -eu

mkdir -p /var/lib/nvml-mock/driver /var/lib/nvml-mock/proc /run/nvidia/driver
echo stub-root > /var/lib/nvml-mock/driver/stub-root
echo "nvidia 62312448 2 nvidia_uvm,nvidia_modeset, Live 0x0000000000000000" > /var/lib/nvml-mock/proc/modules

mount --bind /var/lib/nvml-mock/driver /run/nvidia/driver
# What the driver DaemonSet mounts in /sys/module: the node's firmware search path.
echo /lib/firmware > /var/lib/nvml-mock/firmware-path
mkdir -p /sys/module/firmware_class/parameters
touch /sys/module/firmware_class/parameters/path
mount --bind /var/lib/nvml-mock/firmware-path /sys/module/firmware_class/parameters/path
# What a stand-in killed before unbinding leaves: its module list bind.
mount --bind /var/lib/nvml-mock/proc/modules /proc/modules

nvidia-driver init &
pid=$!
report() { echo "binds left: $(grep -c ' /run/nvidia/driver ' /proc/self/mountinfo) $(grep -c ' /proc/modules ' /proc/self/mountinfo)"; }
trap 'kill -TERM "$pid"; wait "$pid" || true; report; exit 0' TERM
wait "$pid" || true
report
