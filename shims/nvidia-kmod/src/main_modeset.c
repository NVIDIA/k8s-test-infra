/*
 * Copyright 2026 NVIDIA CORPORATION
 * SPDX-License-Identifier: MIT OR GPL-2.0-only
 *
 * nvidia_modeset.ko -- display modesetting, simulated.
 *
 * Present only to be a holder of nvidia.ko. It registers no character device:
 * the real /dev/nvidia-modeset sits at minor 254 of major 195, which nvidia.ko
 * already claims in full.
 *
 * It earns its place because _unload_driver() in the GPU Operator's
 * nvidia-driver entrypoint reads /sys/module/nvidia_modeset/refcnt and adds
 * nvidia-modeset to the rmmod list, so a simulated node with no such module
 * exercises a different branch than a real one.
 */

#include <linux/init.h>
#include <linux/kernel.h>
#include <linux/module.h>

#include "nvidia_anchor.h"

static int __init mokka_modeset_init(void)
{
	mokka_nvidia_rm_anchor("nvidia_modeset");
	pr_info("nvidia_modeset: Mokka simulated modeset loaded\n");

	return 0;
}

static void __exit mokka_modeset_exit(void)
{
	pr_info("nvidia_modeset: Mokka simulated modeset unloaded\n");
}

module_init(mokka_modeset_init);
module_exit(mokka_modeset_exit);

MODULE_AUTHOR("NVIDIA CORPORATION");
MODULE_DESCRIPTION("Mokka simulated NVIDIA modesetting driver");
MODULE_VERSION(MOKKA_NVRM_VERSION);
MODULE_LICENSE("Dual MIT/GPL");
