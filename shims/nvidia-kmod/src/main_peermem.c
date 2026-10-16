/*
 * Copyright 2026 NVIDIA CORPORATION
 * SPDX-License-Identifier: MIT OR GPL-2.0-only
 *
 * nvidia_peermem.ko -- GPUDirect RDMA peer memory, simulated.
 *
 * A holder of nvidia.ko, like nvidia_modeset. It matters for the
 * GPU_DIRECT_RDMA_ENABLED path: k8s-driver-manager lists nvidia_peermem among
 * the modules it unloads, and the driver entrypoint waits on peermem readiness
 * when RDMA is enabled, so its presence changes which branch those take.
 */

#include <linux/init.h>
#include <linux/kernel.h>
#include <linux/module.h>

#include "nvidia_anchor.h"

static int __init mokka_peermem_init(void)
{
	mokka_nvidia_rm_anchor("nvidia_peermem");
	pr_info("nvidia_peermem: Mokka simulated peer memory loaded\n");

	return 0;
}

static void __exit mokka_peermem_exit(void)
{
	pr_info("nvidia_peermem: Mokka simulated peer memory unloaded\n");
}

module_init(mokka_peermem_init);
module_exit(mokka_peermem_exit);

MODULE_AUTHOR("NVIDIA CORPORATION");
MODULE_DESCRIPTION("Mokka simulated NVIDIA GPUDirect RDMA peer memory");
MODULE_VERSION(MOKKA_NVRM_VERSION);
MODULE_LICENSE("Dual MIT/GPL");
