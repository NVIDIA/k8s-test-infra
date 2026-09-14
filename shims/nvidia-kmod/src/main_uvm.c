/*
 * Copyright 2026 NVIDIA CORPORATION
 * SPDX-License-Identifier: MIT OR GPL-2.0-only
 *
 * nvidia_uvm.ko -- Unified Virtual Memory, simulated.
 *
 * Two jobs. It holds a reference on nvidia.ko, which is what gives that module
 * a non-zero refcnt and makes unloading it fail until this one goes first. And
 * it registers the nvidia-uvm character device major, without which the
 * /dev/nvidia-uvm node gpudriver mknods cannot be opened.
 */

#include <linux/fs.h>
#include <linux/init.h>
#include <linux/kernel.h>
#include <linux/module.h>

#include "nvidia_anchor.h"

/*
 * The real driver takes a dynamically allocated major here. This one is fixed
 * because charDevsForDevices() in internal/agent/gpudriver/stage.go already
 * mknods /dev/nvidia-uvm with major 510; a dynamic major would leave the
 * registration and the device nodes pointing at different numbers. See
 * README.md.
 */
#define MOKKA_NV_UVM_MAJOR 510

static int mokka_uvm_open(struct inode *inode, struct file *file)
{
	return 0;
}

static int mokka_uvm_release(struct inode *inode, struct file *file)
{
	return 0;
}

static long mokka_uvm_ioctl(struct file *file, unsigned int cmd, unsigned long arg)
{
	return -ENOTTY;
}

/*
 * Deliberately not shared with nvidia.ko's identical table: .owner is
 * THIS_MODULE, which resolves per module. Borrowing nvidia.ko's fops would
 * charge every open of /dev/nvidia-uvm to nvidia.ko's refcount instead of this
 * module's, which is the opposite of what the real driver does.
 */
static const struct file_operations mokka_uvm_fops = {
	.owner		= THIS_MODULE,
	.open		= mokka_uvm_open,
	.release	= mokka_uvm_release,
	.unlocked_ioctl	= mokka_uvm_ioctl,
	.llseek		= noop_llseek,
};

static int __init mokka_uvm_init(void)
{
	int ret;

	mokka_nvidia_rm_anchor("nvidia_uvm");

	ret = register_chrdev(MOKKA_NV_UVM_MAJOR, "nvidia-uvm", &mokka_uvm_fops);
	if (ret < 0) {
		pr_err("nvidia_uvm: cannot register major %d: %d\n",
		       MOKKA_NV_UVM_MAJOR, ret);
		return ret;
	}

	pr_info("nvidia_uvm: Mokka simulated UVM loaded\n");

	return 0;
}

static void __exit mokka_uvm_exit(void)
{
	unregister_chrdev(MOKKA_NV_UVM_MAJOR, "nvidia-uvm");
	pr_info("nvidia_uvm: Mokka simulated UVM unloaded\n");
}

module_init(mokka_uvm_init);
module_exit(mokka_uvm_exit);

MODULE_AUTHOR("NVIDIA CORPORATION");
MODULE_DESCRIPTION("Mokka simulated NVIDIA Unified Virtual Memory");
MODULE_VERSION(MOKKA_NVRM_VERSION);
MODULE_LICENSE("Dual MIT/GPL");
