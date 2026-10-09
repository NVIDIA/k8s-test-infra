/*
 * Copyright 2026 NVIDIA CORPORATION
 * SPDX-License-Identifier: MIT OR GPL-2.0-only
 *
 * nvidia.ko -- the anchor of Mokka's simulated NVIDIA kernel module family.
 *
 * Mokka simulates a GPU node's driver footprint with rendered file trees and
 * LD_PRELOAD shims. That strategy stops at the kernel boundary: the consumers
 * that decide whether a driver is installed -- k8s-driver-manager, the GPU
 * Operator driver container, the operator-validator -- answer the question by
 * reading /sys/module/nvidia/refcnt and by calling delete_module(2). Those are
 * raw syscalls from Go binaries, so there is no libc symbol for an LD_PRELOAD
 * shim to interpose on, and no file tree that can produce the right answer.
 *
 * This module exists so the kernel produces that surface itself. It drives no
 * hardware and moves no data; loading it is the entire behaviour.
 */

#include <linux/fs.h>
#include <linux/init.h>
#include <linux/kernel.h>
#include <linux/module.h>
#include <linux/moduleparam.h>

#include "nvidia_anchor.h"
#include "nvidia_internal.h"

uint mokka_nvreg_enable_msi = 1;
module_param_named(NVreg_EnableMSI, mokka_nvreg_enable_msi, uint, 0444);
MODULE_PARM_DESC(NVreg_EnableMSI, "Whether MSI interrupts are enabled");

char *mokka_nvreg_registry_dwords;
module_param_named(NVreg_RegistryDwords, mokka_nvreg_registry_dwords, charp, 0444);
MODULE_PARM_DESC(NVreg_RegistryDwords, "Registry key overrides, semicolon separated");

uint mokka_nvreg_device_file_gid;
module_param_named(NVreg_DeviceFileGID, mokka_nvreg_device_file_gid, uint, 0444);
MODULE_PARM_DESC(NVreg_DeviceFileGID, "Group owning the /dev/nvidia* nodes");

uint mokka_nvreg_device_file_mode = 438;
module_param_named(NVreg_DeviceFileMode, mokka_nvreg_device_file_mode, uint, 0444);
MODULE_PARM_DESC(NVreg_DeviceFileMode, "Mode bits for the /dev/nvidia* nodes");

uint mokka_nvreg_device_file_uid;
module_param_named(NVreg_DeviceFileUID, mokka_nvreg_device_file_uid, uint, 0444);
MODULE_PARM_DESC(NVreg_DeviceFileUID, "User owning the /dev/nvidia* nodes");

uint mokka_nvreg_modify_device_files = 1;
module_param_named(NVreg_ModifyDeviceFiles, mokka_nvreg_modify_device_files, uint, 0444);
MODULE_PARM_DESC(NVreg_ModifyDeviceFiles, "Whether the driver maintains the /dev/nvidia* nodes");

uint mokka_nvreg_preserve_video_memory_allocations;
module_param_named(NVreg_PreserveVideoMemoryAllocations,
		   mokka_nvreg_preserve_video_memory_allocations, uint, 0444);
MODULE_PARM_DESC(NVreg_PreserveVideoMemoryAllocations,
		 "Whether video memory survives suspend");

uint mokka_nvreg_enable_resizable_bar;
module_param_named(NVreg_EnableResizableBar, mokka_nvreg_enable_resizable_bar, uint, 0444);
MODULE_PARM_DESC(NVreg_EnableResizableBar, "Whether resizable BAR is enabled");

/*
 * Mokka-prefixed because it is a simulation control rather than a simulated
 * NVreg_* surface. Anyone listing /sys/module/nvidia/parameters/ should be able
 * to tell the two apart at a glance -- a node running this module is meant to
 * be identifiable as simulated by anyone who looks.
 *
 * GPU identity is node state, not driver state: the real driver discovers it
 * from PCI at load time. The driver version, by contrast, is a property of the
 * build and is fixed at compile time -- see MOKKA_NVRM_VERSION.
 */
char *mokka_gpus;
module_param_named(mokka_gpus, mokka_gpus, charp, 0444);
MODULE_PARM_DESC(mokka_gpus,
		 "Comma-separated GPU records \"<bdf>|<uuid>|<model>|<minor>\", rendered "
		 "under /proc/driver/nvidia/gpus/");

/*
 * Referenced by nvidia_uvm, nvidia_modeset and nvidia_peermem. noinline so the
 * reference survives whatever the compiler would otherwise do to an empty
 * function: if it were elided, the module dependency would vanish with it and
 * refcnt would silently stay at zero.
 */
noinline void mokka_nvidia_rm_anchor(const char *client)
{
	pr_debug("nvidia: rm anchor taken by %s\n", client);
}
EXPORT_SYMBOL(mokka_nvidia_rm_anchor);

/*
 * Minimal enough to make open() succeed and no more. Mokka's NVML and CUDA
 * simulation lives in userspace shims that never ioctl the device, so a real
 * ioctl surface would have no caller; -ENOTTY is what an unimplemented ioctl is
 * supposed to return, and it keeps the failure honest for anything that tries.
 */
static int mokka_nv_open(struct inode *inode, struct file *file)
{
	return 0;
}

static int mokka_nv_release(struct inode *inode, struct file *file)
{
	return 0;
}

static long mokka_nv_ioctl(struct file *file, unsigned int cmd, unsigned long arg)
{
	return -ENOTTY;
}

static const struct file_operations mokka_nv_fops = {
	.owner		= THIS_MODULE,
	.open		= mokka_nv_open,
	.release	= mokka_nv_release,
	.unlocked_ioctl	= mokka_nv_ioctl,
	.llseek		= noop_llseek,
};

static int __init mokka_nvidia_init(void)
{
	int ret;

	/*
	 * Claims the whole major, which covers the per-GPU minors, nvidiactl at
	 * 255 and nvidia-modeset at 254 in one call -- the same span the real
	 * driver serves. Until something registers it, the nodes gpudriver
	 * mknods are unopenable, and /proc/devices carries no nvidia line for
	 * nvidia-container-cli to find.
	 */
	ret = register_chrdev(MOKKA_NV_MAJOR, "nvidia", &mokka_nv_fops);
	if (ret < 0) {
		pr_err("nvidia: cannot register major %d: %d\n", MOKKA_NV_MAJOR, ret);
		return ret;
	}

	ret = mokka_nv_procfs_init();
	if (ret < 0) {
		unregister_chrdev(MOKKA_NV_MAJOR, "nvidia");
		return ret;
	}

	pr_info("nvidia: Mokka simulated NVRM version %s loaded\n", MOKKA_NVRM_VERSION);
	return 0;
}

static void __exit mokka_nvidia_exit(void)
{
	mokka_nv_procfs_exit();
	unregister_chrdev(MOKKA_NV_MAJOR, "nvidia");
	pr_info("nvidia: Mokka simulated NVRM unloaded\n");
}

module_init(mokka_nvidia_init);
module_exit(mokka_nvidia_exit);

MODULE_AUTHOR("NVIDIA CORPORATION");
MODULE_DESCRIPTION("Mokka simulated NVIDIA GPU driver (no hardware, no data path)");
MODULE_VERSION(MOKKA_NVRM_VERSION);
/*
 * "Dual MIT/GPL" is what NVIDIA's open kernel modules declare, so modinfo
 * matches the real thing and the kernel is not tainted. The repository's
 * Apache-2.0 licence is not GPLv2-compatible and is not a string the kernel
 * recognises, so these sources are separately licensed -- see REUSE.toml.
 */
MODULE_LICENSE("Dual MIT/GPL");
