# Copyright 2026 NVIDIA CORPORATION
# SPDX-License-Identifier: Apache-2.0

# A Kind node image with the RDMA userspace stack baked in, for the
# network-operator demo's Soft-RoCE (rdma_rxe) Tier 3 path. It extends the
# pinned kindest/node base so `rdma`, `modprobe`, and the ibverbs rxe provider
# are already present on every node — no runtime apt/apk installs. It also ships
# /usr/local/bin/setup-soft-roce.sh, which the demo runs once per node via
# `docker exec` to load rdma_rxe and create the rxe link.
#
# NOTE: the rdma_rxe *kernel module* still comes from the host kernel (Kind
# bind-mounts /lib/modules read-only into each node); this image only provides
# the userspace tooling, so the Soft-RoCE path is Linux-only.
ARG BASE_IMAGE=kindest/node:v1.32.2
FROM ${BASE_IMAGE}

RUN apt-get update \
 && apt-get install -y --no-install-recommends \
      iproute2 \
      rdma-core \
      ibverbs-providers \
      ibverbs-utils \
      libibverbs1 \
      infiniband-diags \
      perftest \
      kmod \
 && rm -rf /var/lib/apt/lists/*

COPY setup-soft-roce.sh /usr/local/bin/setup-soft-roce.sh
RUN chmod 0755 /usr/local/bin/setup-soft-roce.sh
