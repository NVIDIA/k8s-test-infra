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
 * text is kept identical to what writeProcFS produces so that the two cannot
 * disagree while both exist.
 */

#include <linux/kernel.h>
#include <linux/module.h>
#include <linux/proc_fs.h>
#include <linux/seq_file.h>
#include <linux/slab.h>
#include <linux/string.h>
#include <linux/utsname.h>

#include "nvidia_internal.h"

#define MOKKA_NV_PROC_PATH "driver/nvidia"

/*
 * A bound rather than a growable array: the records come from a module
 * parameter, so the list is fixed for the module's lifetime and the largest
 * profile in this repository is an order of magnitude below this.
 */
#define MOKKA_NV_MAX_GPUS 64

struct mokka_gpu {
	const char *bdf;
	const char *uuid;
	const char *model;
	unsigned int minor;
};

static struct proc_dir_entry *nv_proc_root;
static char *gpu_records;
static struct mokka_gpu *gpus;
static unsigned int gpu_count;

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

static int mokka_nv_show_params(struct seq_file *m, void *v)
{
	seq_printf(m, "EnableMSI: %u\n", mokka_nvreg_enable_msi);

	/* No trailing space when unset, matching writeProcFS byte for byte. */
	if (mokka_nvreg_registry_dwords && *mokka_nvreg_registry_dwords)
		seq_printf(m, "NVreg_RegistryDwords: %s\n", mokka_nvreg_registry_dwords);
	else
		seq_puts(m, "NVreg_RegistryDwords:\n");

	seq_printf(m, "NVreg_DeviceFileGID: %u\n", mokka_nvreg_device_file_gid);
	seq_printf(m, "NVreg_DeviceFileMode: %u\n", mokka_nvreg_device_file_mode);
	seq_printf(m, "NVreg_DeviceFileUID: %u\n", mokka_nvreg_device_file_uid);
	seq_printf(m, "NVreg_ModifyDeviceFiles: %u\n", mokka_nvreg_modify_device_files);
	seq_printf(m, "NVreg_PreserveVideoMemoryAllocations: %u\n",
		   mokka_nvreg_preserve_video_memory_allocations);
	seq_printf(m, "NVreg_EnableResizableBar: %u\n", mokka_nvreg_enable_resizable_bar);

	return 0;
}

static int mokka_nv_show_registry(struct seq_file *m, void *v)
{
	if (mokka_nvreg_registry_dwords && *mokka_nvreg_registry_dwords)
		seq_printf(m, "%s\n", mokka_nvreg_registry_dwords);

	return 0;
}

/*
 * The field set is the real file's, because a consumer parsing it looks up
 * fields by name and a short file reads as a malformed one. Model, UUID, bus
 * location and minor come from the mokka_gpus records; the remainder are fixed
 * plausible values, since nothing on the node knows a simulated GPU's IRQ or
 * VBIOS. README.md lists which are which.
 */
static int mokka_nv_show_gpu_information(struct seq_file *m, void *v)
{
	const struct mokka_gpu *gpu = m->private;

	seq_printf(m, "Model: \t\t %s\n", gpu->model);
	seq_puts(m, "IRQ:   \t\t 0\n");
	seq_printf(m, "GPU UUID: \t %s\n", gpu->uuid);
	seq_puts(m, "Video BIOS: \t ??.??.??.??.??\n");
	seq_puts(m, "Bus Type: \t PCIe\n");
	seq_puts(m, "DMA Size: \t 47 bits\n");
	seq_puts(m, "DMA Mask: \t 0x7fffffffffff\n");
	seq_printf(m, "Bus Location: \t %s\n", gpu->bdf);
	seq_printf(m, "Device Minor: \t %u\n", gpu->minor);
	seq_puts(m, "GPU Excluded:\t No\n");

	return 0;
}

/* Tokenises a private copy of mokka_gpus in place; the entries point into it. */
static int mokka_nv_parse_gpus(void)
{
	char *cursor, *record;
	unsigned int i = 0;

	if (!mokka_gpus || !*mokka_gpus)
		return 0;

	gpu_records = kstrdup(mokka_gpus, GFP_KERNEL);
	if (!gpu_records)
		return -ENOMEM;

	gpus = kcalloc(MOKKA_NV_MAX_GPUS, sizeof(*gpus), GFP_KERNEL);
	if (!gpus) {
		kfree(gpu_records);
		gpu_records = NULL;
		return -ENOMEM;
	}

	cursor = gpu_records;
	while ((record = strsep(&cursor, ",")) != NULL) {
		char *fields = record;
		char *bdf, *uuid, *model, *minor;

		if (!*record)
			continue;

		if (i == MOKKA_NV_MAX_GPUS) {
			pr_warn("nvidia: mokka_gpus holds more than %u records; ignoring the rest\n",
				MOKKA_NV_MAX_GPUS);
			break;
		}

		bdf = strsep(&fields, "|");
		uuid = strsep(&fields, "|");
		model = strsep(&fields, "|");
		minor = strsep(&fields, "|");

		gpus[i].bdf = bdf;
		gpus[i].uuid = uuid ? uuid : "";
		gpus[i].model = model ? model : "";
		if (!minor || kstrtouint(minor, 10, &gpus[i].minor))
			gpus[i].minor = i;

		i++;
	}

	gpu_count = i;

	return 0;
}

static void mokka_nv_free_gpus(void)
{
	kfree(gpus);
	kfree(gpu_records);
	gpus = NULL;
	gpu_records = NULL;
	gpu_count = 0;
}

static int mokka_nv_create_gpu_entries(void)
{
	struct proc_dir_entry *gpus_dir;
	unsigned int i;

	gpus_dir = proc_mkdir("gpus", nv_proc_root);
	if (!gpus_dir)
		return -ENOMEM;

	for (i = 0; i < gpu_count; i++) {
		struct proc_dir_entry *dir;

		dir = proc_mkdir(gpus[i].bdf, gpus_dir);
		if (!dir)
			return -ENOMEM;

		if (!proc_create_single_data("information", 0444, dir,
					     mokka_nv_show_gpu_information, &gpus[i]))
			return -ENOMEM;
	}

	return 0;
}

int mokka_nv_procfs_init(void)
{
	int ret;

	ret = mokka_nv_parse_gpus();
	if (ret < 0)
		return ret;

	nv_proc_root = proc_mkdir(MOKKA_NV_PROC_PATH, NULL);
	if (!nv_proc_root) {
		mokka_nv_free_gpus();
		return -ENOMEM;
	}

	ret = -ENOMEM;
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
	mokka_nv_free_gpus();
}
