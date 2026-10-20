/*
 * Copyright 2026 NVIDIA CORPORATION
 * SPDX-License-Identifier: MIT OR GPL-2.0-only
 *
 * /proc/driver/nvidia/, served by the module rather than staged as files.
 *
 * gpudriver.writeProcFS renders the same content into the node overlay at
 * driver/proc/driver/nvidia/ and the NRI plugin mounts it into containers. The
 * module's copy is strictly better where it applies: it appears at the real
 * path, needs no mount, and vanishes exactly when the driver is unloaded. The
 * params and per-GPU text are kept identical to what writeProcFS produces, so
 * the two cannot disagree while both exist.
 */

#include <linux/kernel.h>
#include <linux/module.h>
#include <linux/proc_fs.h>
#include <linux/seq_file.h>
#include <linux/utsname.h>

#include "nvidia_internal.h"

#define MOKKA_NV_PROC_PATH "driver/nvidia"

static struct proc_dir_entry *nv_proc_root;

#ifndef CONFIG_CC_VERSION_TEXT
#define CONFIG_CC_VERSION_TEXT "unknown"
#endif

static int mokka_nv_show_version(struct seq_file *m, void *v)
{
	/*
	 * The architecture is read from the running kernel rather than frozen.
	 * writeProcFS hardcodes x86_64, which is wrong on the arm64 nodes this
	 * repository also targets; see README.md.
	 */
	seq_printf(m, "NVRM version: NVIDIA UNIX %s Kernel Module  %s  %s\n",
		   init_utsname()->machine, MOKKA_NVRM_VERSION, MOKKA_NVRM_BUILD_DATE);
	seq_printf(m, "GCC version:  %s\n", CONFIG_CC_VERSION_TEXT);

	return 0;
}

/*
 * The driver prints its registry table under the bare key names, then each
 * string key with its value quoted; NVreg_ is only the module-parameter
 * spelling. nvidia-modprobe's device-node code, which the driver libraries
 * embed, looks the keys up by those bare names.
 */
static int mokka_nv_show_params(struct seq_file *m, void *v)
{
	seq_printf(m, "ModifyDeviceFiles: %u\n", mokka_nvreg_modify_device_files);
	seq_printf(m, "DeviceFileUID: %u\n", mokka_nvreg_device_file_uid);
	seq_printf(m, "DeviceFileGID: %u\n", mokka_nvreg_device_file_gid);
	seq_printf(m, "DeviceFileMode: %u\n", mokka_nvreg_device_file_mode);
	seq_printf(m, "EnableMSI: %u\n", mokka_nvreg_enable_msi);
	seq_printf(m, "PreserveVideoMemoryAllocations: %u\n",
		   mokka_nvreg_preserve_video_memory_allocations);
	seq_printf(m, "EnableResizableBar: %u\n", mokka_nvreg_enable_resizable_bar);
	seq_printf(m, "RegistryDwords: \"%s\"\n",
		   mokka_nvreg_registry_dwords ? mokka_nvreg_registry_dwords : "");

	return 0;
}

static int mokka_nv_show_registry(struct seq_file *m, void *v)
{
	if (mokka_nvreg_registry_dwords && *mokka_nvreg_registry_dwords)
		seq_printf(m, "%s\n", mokka_nvreg_registry_dwords);

	return 0;
}

/*
 * Only the lines a mokka_gpus record backs, as writeGPUInformation writes
 * them: a consumer that needs the IRQ or VBIOS gets nothing rather than a value
 * no profile declares. Consumers look fields up by name, so the names and the
 * tab layout are the real file's.
 */
static int mokka_nv_show_gpu_information(struct seq_file *m, void *v)
{
	const struct mokka_gpu *gpu = m->private;

	seq_printf(m, "Model: \t\t %s\n", gpu->model);
	seq_printf(m, "GPU UUID: \t %s\n", gpu->uuid);
	seq_puts(m, "Bus Type: \t PCIe\n");
	seq_printf(m, "Bus Location: \t %s\n", gpu->bdf);
	seq_printf(m, "Device Minor: \t %u\n", gpu->minor);
	seq_puts(m, "GPU Excluded:\t No\n");

	return 0;
}

static int mokka_nv_create_gpu_entries(void)
{
	struct proc_dir_entry *gpus_dir;
	unsigned int i;

	gpus_dir = proc_mkdir("gpus", nv_proc_root);
	if (!gpus_dir)
		return -ENOMEM;

	for (i = 0; i < mokka_nv_gpu_count(); i++) {
		const struct mokka_gpu *gpu = mokka_nv_gpu(i);
		struct proc_dir_entry *dir;

		dir = proc_mkdir(gpu->bdf, gpus_dir);
		if (!dir)
			return -ENOMEM;

		/* The cast drops const only to fit the seq_file private pointer. */
		if (!proc_create_single_data("information", 0444, dir,
					     mokka_nv_show_gpu_information, (void *)gpu))
			return -ENOMEM;
	}

	return 0;
}

int mokka_nv_procfs_init(void)
{
	int ret = -ENOMEM;

	nv_proc_root = proc_mkdir(MOKKA_NV_PROC_PATH, NULL);
	if (!nv_proc_root)
		return -ENOMEM;

	if (!proc_create_single("version", 0444, nv_proc_root, mokka_nv_show_version))
		goto unwind;
	if (!proc_create_single("params", 0444, nv_proc_root, mokka_nv_show_params))
		goto unwind;
	if (!proc_create_single("registry", 0444, nv_proc_root, mokka_nv_show_registry))
		goto unwind;

	ret = mokka_nv_create_gpu_entries();
	if (ret < 0)
		goto unwind;

	return 0;

unwind:
	mokka_nv_procfs_exit();

	return ret;
}

void mokka_nv_procfs_exit(void)
{
	/* Recursive, so the per-GPU subtree needs no separate bookkeeping. */
	proc_remove(nv_proc_root);
	nv_proc_root = NULL;
}
