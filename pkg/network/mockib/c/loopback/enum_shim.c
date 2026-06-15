/*
 * Copyright 2026 NVIDIA CORPORATION
 * SPDX-License-Identifier: Apache-2.0
 *
 * enum_shim -- TEST-ONLY device enumeration stub for the loopback harness.
 *
 * The production libibmockrdma.so deliberately does NOT interpose
 * ibv_get_device_list: in a real pod the mock sysfs tree + libibmocksys.so
 * already make libibverbs enumerate the mock HCAs. This host loopback test runs
 * in a bare debian container with neither, so this tiny shim fabricates a
 * single "mlx5_0" device just so stock perftest gets past enumeration and into
 * ibv_open_device (which libibmockrdma.so then handles). It is preloaded
 * AFTER libibmockrdma.so so the data-path symbols win; only the enumeration
 * symbols come from here.
 */

#define _GNU_SOURCE
#include <infiniband/verbs.h>
#include <stdio.h>
#include <string.h>

static struct ibv_device g_dev;
static struct ibv_device *g_list[1];

struct ibv_device **ibv_get_device_list(int *n) {
    memset(&g_dev, 0, sizeof(g_dev));
    snprintf(g_dev.name, sizeof(g_dev.name), "mlx5_0");
    snprintf(g_dev.dev_name, sizeof(g_dev.dev_name), "uverbs0");
    g_dev.node_type = IBV_NODE_CA;
    g_dev.transport_type = IBV_TRANSPORT_IB;
    g_list[0] = &g_dev;
    if (n) *n = 1;
    return g_list;
}
void ibv_free_device_list(struct ibv_device **l) { (void)l; }
const char *ibv_get_device_name(struct ibv_device *d) {
    return d ? d->name : "mlx5_0";
}
