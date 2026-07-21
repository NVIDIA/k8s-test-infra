# Copyright 2026 NVIDIA CORPORATION
# SPDX-License-Identifier: Apache-2.0

# A Kind node image with the RDMA userspace stack baked in, for the
# network-operator demo's Soft-RoCE (rdma_rxe) Tier 3 path. It extends the
# pinned kindest/node base so `rdma`, `modprobe`, and the ibverbs rxe provider
# are already present on every node — no runtime apt/apk installs.
#
# The Soft-RoCE setup itself is baked in as a systemd unit (soft-roce.service)
# that runs /usr/local/bin/setup-soft-roce.sh at node boot, so the node image
# configures rdma_rxe on its own — the demo does not `docker exec` into nodes.
#
# NOTE: the rdma_rxe *kernel module* still comes from the host kernel (Kind
# bind-mounts /lib/modules read-only into each node); this image only provides
# the userspace tooling, so the Soft-RoCE path is Linux-only.
ARG BASE_IMAGE=kindest/node:v1.35.0
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

COPY --chmod=0755 setup-soft-roce.sh /usr/local/bin/setup-soft-roce.sh
COPY soft-roce.service /etc/systemd/system/soft-roce.service
# The rdma_rxe module and rxe link can only be set up against a live kernel and
# a running netdev, which do not exist during `docker build` — so instead of a
# RUN, enable the unit to execute the script at every node boot. systemd is not
# running during the build, so create the multi-user.target want symlink
# directly (equivalent to `systemctl enable`).
RUN mkdir -p /etc/systemd/system/multi-user.target.wants \
 && ln -sf /etc/systemd/system/soft-roce.service \
      /etc/systemd/system/multi-user.target.wants/soft-roce.service
