# Copyright 2026 NVIDIA CORPORATION
# SPDX-License-Identifier: Apache-2.0

# Driver stand-in: runs in the GPU Operator's driver DaemonSet on Mokka nodes
# and installs nothing (docs/components/driver-stub.md). Build it with
# `make driver-stub-image`, one image per driver version. Not published.
FROM alpine:3.22

# Required: the operator's driver.version.
ARG DRIVER_VERSION

RUN test -n "$DRIVER_VERSION"

LABEL org.opencontainers.image.title="mokka-driver-stub" \
      org.opencontainers.image.description="Stand-in for the NVIDIA driver container on Mokka nodes: installs no driver and loads no kernel module" \
      org.opencontainers.image.version="${DRIVER_VERSION}"

ENV DRIVER_VERSION=${DRIVER_VERSION} \
    STUB_INSTALL_SECONDS=0 \
    STUB_DRIVER_ROOT=/var/lib/nvml-mock/driver \
    STUB_PROC_MODULES=/var/lib/nvml-mock/proc/modules

COPY nvidia-driver nvidia-smi /usr/local/bin/
RUN chmod 0755 /usr/local/bin/nvidia-driver /usr/local/bin/nvidia-smi
