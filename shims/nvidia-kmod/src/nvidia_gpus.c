/*
 * Copyright 2026 NVIDIA CORPORATION
 * SPDX-License-Identifier: MIT OR GPL-2.0-only
 *
 * The simulated GPUs. mokka.ko registers one platform device per GPU, and this
 * driver binds to them the way the real driver binds to its PCI devices: probe
 * adds a GPU and remove takes it away. Registering the driver probes every GPU
 * already plugged in, so the GPUs are in place by the time insmod returns. The
 * procfs tree and the ioctl surface both read this one table, so they cannot
 * disagree about which GPUs exist.
 *
 * Probe and remove run whenever mokka.ko plugs or unplugs a GPU, so the table
 * is guarded by a mutex. It is kept in minor order, the order in which card
 * info lists the GPUs.
 */

#include <linux/kernel.h>
#include <linux/lockdep.h>
#include <linux/mutex.h>
#include <linux/platform_device.h>
#include <linux/property.h>
#include <linux/slab.h>
#include <linux/string.h>
#include <linux/version.h>

#include "mokka_gpu.h"
#include "nvidia_internal.h"

/*
 * NV_MAX_DEVICES. Clients size their card-info array for this many GPUs, so a
 * GPU past it could never be listed.
 */
#define MOKKA_NV_MAX_GPUS 32

static DEFINE_MUTEX(gpus_lock);
static struct mokka_gpu *gpus[MOKKA_NV_MAX_GPUS];
static unsigned int gpu_count;

/*
 * RM's gpuGenerate32BitId(). Clients hand these back through
 * NV_ESC_ATTACH_GPUS_TO_FD, so they must be derived the way RM derives them.
 */
static u32 mokka_nv_gpu_id(u32 domain, u8 bus, u8 slot)
{
	return ((domain & 0xffff) << 16) | (bus << 8) | slot;
}

/*
 * The PCI location keys everything a client sees -- card info, the GPU ID, the
 * procfs directory -- so a GPU without a valid one is refused rather than
 * probed into a GPU nothing can find.
 */
static int mokka_nv_parse_location(const char *location, struct mokka_gpu *gpu)
{
	unsigned int domain, bus, slot, function;
	int consumed = 0;

	if (sscanf(location, "%x:%x:%x.%x%n", &domain, &bus, &slot, &function, &consumed) != 4 ||
	    location[consumed] != '\0' || bus > 0xff || slot > 0x1f || function > 0x7)
		return -EINVAL;

	snprintf(gpu->bdf, sizeof(gpu->bdf), "%04x:%02x:%02x.%x", domain, bus, slot, function);
	gpu->domain = domain;
	gpu->bus = bus;
	gpu->slot = slot;
	gpu->function = function;
	gpu->gpu_id = mokka_nv_gpu_id(domain, bus, slot);

	return 0;
}

/* Inserts gpu in minor order, refusing a minor or a GPU ID already taken. */
static int mokka_nv_gpu_insert(struct mokka_gpu *gpu)
{
	unsigned int i, at = gpu_count;

	lockdep_assert_held(&gpus_lock);

	if (gpu_count == MOKKA_NV_MAX_GPUS)
		return -ENOSPC;

	for (i = 0; i < gpu_count; i++) {
		if (gpus[i]->minor == gpu->minor || gpus[i]->gpu_id == gpu->gpu_id)
			return -EEXIST;
		if (at == gpu_count && gpus[i]->minor > gpu->minor)
			at = i;
	}

	memmove(&gpus[at + 1], &gpus[at], (gpu_count - at) * sizeof(gpus[0]));
	gpus[at] = gpu;
	gpu_count++;

	return 0;
}

static void mokka_nv_gpu_delete(struct mokka_gpu *gpu)
{
	unsigned int i;

	lockdep_assert_held(&gpus_lock);

	for (i = 0; i < gpu_count; i++) {
		if (gpus[i] != gpu)
			continue;

		memmove(&gpus[i], &gpus[i + 1], (gpu_count - i - 1) * sizeof(gpus[0]));
		gpus[--gpu_count] = NULL;
		return;
	}
}

static int mokka_nv_gpu_probe(struct platform_device *pdev)
{
	struct device *dev = &pdev->dev;
	const char *location, *uuid, *model = "";
	struct mokka_gpu *gpu;
	u16 device_id = 0;
	u32 minor;
	int ret;

	if (device_property_read_string(dev, MOKKA_GPU_PROP_BUS_ID, &location) ||
	    device_property_read_string(dev, MOKKA_GPU_PROP_UUID, &uuid) ||
	    device_property_read_u32(dev, MOKKA_GPU_PROP_MINOR, &minor))
		return dev_err_probe(dev, -EINVAL, "incomplete GPU description\n");

	/* Optional: without them the model line is empty and the device ID zero. */
	device_property_read_string(dev, MOKKA_GPU_PROP_NAME, &model);
	device_property_read_u16(dev, MOKKA_GPU_PROP_DEVICE_ID, &device_id);

	if (minor > MOKKA_NV_MINOR_GPU_MAX)
		return dev_err_probe(dev, -EINVAL, "device minor %u is past the GPU range\n", minor);

	/* Freed by devres after remove, which has taken the GPU off every list. */
	gpu = devm_kzalloc(dev, sizeof(*gpu), GFP_KERNEL);
	if (!gpu)
		return -ENOMEM;

	ret = mokka_nv_parse_location(location, gpu);
	if (ret)
		return dev_err_probe(dev, ret, "invalid PCI location \"%s\"\n", location);

	strscpy(gpu->uuid, uuid, sizeof(gpu->uuid));
	strscpy(gpu->model, model, sizeof(gpu->model));
	gpu->minor = minor;
	gpu->device_id = device_id;

	mutex_lock(&gpus_lock);
	ret = mokka_nv_gpu_insert(gpu);
	if (!ret) {
		ret = mokka_nv_procfs_add_gpu(gpu);
		if (ret)
			mokka_nv_gpu_delete(gpu);
	}
	mutex_unlock(&gpus_lock);
	if (ret)
		return dev_err_probe(dev, ret, "cannot add GPU %s at minor %u\n", gpu->bdf, minor);

	platform_set_drvdata(pdev, gpu);
	dev_info(dev, "GPU %s (%s) at minor %u\n", gpu->bdf, gpu->uuid, gpu->minor);

	return 0;
}

/*
 * A client holding the GPU's node open keeps its file; its next escape on the
 * node finds the GPU gone, as on a GPU that fell off the bus.
 */
static void mokka_nv_gpu_detach(struct platform_device *pdev)
{
	struct mokka_gpu *gpu = platform_get_drvdata(pdev);

	mutex_lock(&gpus_lock);
	mokka_nv_gpu_delete(gpu);
	mutex_unlock(&gpus_lock);

	/* Waits out the readers of the GPU's procfs files before devres frees it. */
	mokka_nv_procfs_remove_gpu(gpu);

	dev_info(&pdev->dev, "GPU %s removed\n", gpu->bdf);
}

/* Linux 6.11 dropped the return value of a platform driver's remove. */
#if LINUX_VERSION_CODE >= KERNEL_VERSION(6, 11, 0)
static void mokka_nv_gpu_remove(struct platform_device *pdev)
{
	mokka_nv_gpu_detach(pdev);
}
#else
static int mokka_nv_gpu_remove(struct platform_device *pdev)
{
	mokka_nv_gpu_detach(pdev);

	return 0;
}
#endif

static struct platform_driver mokka_nv_gpu_driver = {
	.probe	= mokka_nv_gpu_probe,
	.remove	= mokka_nv_gpu_remove,
	.driver	= {
		.name		= MOKKA_GPU_DEVICE,
		/* The GPUs plugged in at load must be in place when insmod returns. */
		.probe_type	= PROBE_FORCE_SYNCHRONOUS,
	},
};

int mokka_nv_gpus_init(void)
{
	return platform_driver_register(&mokka_nv_gpu_driver);
}

void mokka_nv_gpus_exit(void)
{
	platform_driver_unregister(&mokka_nv_gpu_driver);
}

void mokka_nv_gpus_lock(void)
{
	mutex_lock(&gpus_lock);
}

void mokka_nv_gpus_unlock(void)
{
	mutex_unlock(&gpus_lock);
}

unsigned int mokka_nv_gpu_count(void)
{
	lockdep_assert_held(&gpus_lock);

	return gpu_count;
}

const struct mokka_gpu *mokka_nv_gpu(unsigned int index)
{
	lockdep_assert_held(&gpus_lock);

	return index < gpu_count ? gpus[index] : NULL;
}

const struct mokka_gpu *mokka_nv_gpu_by_id(u32 gpu_id)
{
	unsigned int i;

	lockdep_assert_held(&gpus_lock);

	for (i = 0; i < gpu_count; i++) {
		if (gpus[i]->gpu_id == gpu_id)
			return gpus[i];
	}

	return NULL;
}

bool mokka_nv_gpu_present(unsigned int minor)
{
	bool present = false;
	unsigned int i;

	mutex_lock(&gpus_lock);
	for (i = 0; i < gpu_count && !present; i++)
		present = gpus[i]->minor == minor;
	mutex_unlock(&gpus_lock);

	return present;
}
