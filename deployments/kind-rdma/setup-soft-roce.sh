#!/usr/bin/env bash
# Copyright 2026 NVIDIA CORPORATION
# SPDX-License-Identifier: Apache-2.0
#
# Sets up Soft-RoCE (rdma_rxe) on the node this runs inside: loads the host
# rdma_rxe module, puts the RDMA subsystem in exclusive-netns mode, and creates
# an rxe link over a netdev (default eth0). Baked into the kind-rdma node image
# and invoked once per node by the network-operator demo via `docker exec`.
#
# All steps are best-effort and idempotent: the rdma_rxe *kernel module* comes
# from the host kernel (Kind bind-mounts /lib/modules read-only), so this only
# fully succeeds on Linux hosts that provide the module.
#
# Usage: setup-soft-roce.sh [netdev] [link-name]   (defaults: eth0 rxe0)
set -u

NETDEV="${1:-${RXE_NETDEV:-eth0}}"
LINK="${2:-${RXE_LINK:-rxe0}}"

modprobe rdma_rxe 2>/dev/null || echo "WARN: modprobe rdma_rxe failed (host kernel missing the module?)"
rdma system set netns exclusive 2>/dev/null || true
rdma link show "${LINK}" >/dev/null 2>&1 \
  || rdma link add "${LINK}" type rxe netdev "${NETDEV}" \
  || echo "WARN: rdma link add ${LINK} (netdev ${NETDEV}) failed"
rdma link show 2>&1 || true
