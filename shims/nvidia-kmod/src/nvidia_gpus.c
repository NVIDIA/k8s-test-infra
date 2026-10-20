/*
 * Copyright 2026 NVIDIA CORPORATION
 * SPDX-License-Identifier: MIT OR GPL-2.0-only
 *
 * The simulated GPUs, parsed from the mokka_gpus parameter at load time -- the
 * moment the real driver discovers its GPUs on the PCI bus. The procfs tree and
 * the ioctl surface both read this one table, so they cannot disagree about
 * which GPUs exist.
 *
 * The table is written once in module init, before the character device is
 * registered, and only read afterwards, so it needs no locking.
 */

#include <linux/kernel.h>
#include <linux/slab.h>
#include <linux/string.h>

#include "nvidia_internal.h"

/*
 * A bound rather than a growable array: the records come from a module
 * parameter, so the list is fixed for the module's lifetime and the largest
 * profile in this repository is an order of magnitude below this.
 */
#define MOKKA_NV_MAX_GPUS 64

static char *gpu_records;
static struct mokka_gpu *gpus;
static unsigned int gpu_count;

/*
 * RM's gpuGenerate32BitId(). Clients hand these back through
 * NV_ESC_ATTACH_GPUS_TO_FD, so they must be derived the way RM derives them.
 */
static u32 mokka_nv_gpu_id(u32 domain, u8 bus, u8 slot)
{
	return ((domain & 0xffff) << 16) | (bus << 8) | slot;
}

/* Fills gpu from one record. Its string fields point into the record. */
static int mokka_nv_parse_record(char *record, unsigned int index, struct mokka_gpu *gpu)
{
	char *location = strsep(&record, "|");
	char *uuid = strsep(&record, "|");
	char *model = strsep(&record, "|");
	char *minor = strsep(&record, "|");
	char *device_id = strsep(&record, "|");
	unsigned int domain, bus, slot, function;
	int consumed = 0;

	/*
	 * The PCI location keys everything a client sees -- card info, the GPU
	 * ID, the procfs directory -- so a record without a valid one fails the
	 * load rather than producing a GPU nothing can find.
	 */
	if (sscanf(location, "%x:%x:%x.%x%n", &domain, &bus, &slot, &function, &consumed) != 4 ||
	    location[consumed] != '\0' || bus > 0xff || slot > 0x1f || function > 0x7) {
		pr_err("nvidia: mokka_gpus record %u: invalid PCI location \"%s\"\n",
		       index, location);
		return -EINVAL;
	}

	if (!minor || !*minor) {
		gpu->minor = index;
	} else if (kstrtouint(minor, 10, &gpu->minor) || gpu->minor > MOKKA_NV_MINOR_GPU_MAX) {
		pr_err("nvidia: mokka_gpus record %u: invalid device minor \"%s\"\n",
		       index, minor);
		return -EINVAL;
	}

	if (device_id && *device_id && kstrtou16(device_id, 16, &gpu->device_id)) {
		pr_err("nvidia: mokka_gpus record %u: invalid PCI device ID \"%s\"\n",
		       index, device_id);
		return -EINVAL;
	}

	snprintf(gpu->bdf, sizeof(gpu->bdf), "%04x:%02x:%02x.%x", domain, bus, slot, function);
	gpu->uuid = uuid ? uuid : "";
	gpu->model = model ? model : "";
	gpu->domain = domain;
	gpu->bus = bus;
	gpu->slot = slot;
	gpu->function = function;
	gpu->gpu_id = mokka_nv_gpu_id(domain, bus, slot);

	return 0;
}

int mokka_nv_gpus_init(void)
{
	char *cursor, *record;
	unsigned int i = 0;
	int ret;

	if (!mokka_gpus || !*mokka_gpus)
		return 0;

	gpu_records = kstrdup(mokka_gpus, GFP_KERNEL);
	gpus = kcalloc(MOKKA_NV_MAX_GPUS, sizeof(*gpus), GFP_KERNEL);
	if (!gpu_records || !gpus) {
		mokka_nv_gpus_exit();
		return -ENOMEM;
	}

	cursor = gpu_records;
	while ((record = strsep(&cursor, ",")) != NULL) {
		if (!*record)
			continue;

		if (i == MOKKA_NV_MAX_GPUS) {
			pr_warn("nvidia: mokka_gpus holds more than %u records; ignoring the rest\n",
				MOKKA_NV_MAX_GPUS);
			break;
		}

		ret = mokka_nv_parse_record(record, i, &gpus[i]);
		if (ret < 0) {
			mokka_nv_gpus_exit();
			return ret;
		}

		i++;
	}

	gpu_count = i;

	return 0;
}

void mokka_nv_gpus_exit(void)
{
	kfree(gpus);
	kfree(gpu_records);
	gpus = NULL;
	gpu_records = NULL;
	gpu_count = 0;
}

unsigned int mokka_nv_gpu_count(void)
{
	return gpu_count;
}

const struct mokka_gpu *mokka_nv_gpu(unsigned int index)
{
	return index < gpu_count ? &gpus[index] : NULL;
}

const struct mokka_gpu *mokka_nv_gpu_by_minor(unsigned int minor)
{
	unsigned int i;

	for (i = 0; i < gpu_count; i++) {
		if (gpus[i].minor == minor)
			return &gpus[i];
	}

	return NULL;
}

const struct mokka_gpu *mokka_nv_gpu_by_id(u32 gpu_id)
{
	unsigned int i;

	for (i = 0; i < gpu_count; i++) {
		if (gpus[i].gpu_id == gpu_id)
			return &gpus[i];
	}

	return NULL;
}
