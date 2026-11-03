/*
 * Copyright 2026 NVIDIA CORPORATION
 * SPDX-License-Identifier: MIT OR GPL-2.0-only
 *
 * mokka.ko -- the node's simulated hardware.
 *
 * A real GPU exists before its driver loads and after it unloads; the driver
 * finds it on the PCI bus when it loads.
 *
 * This module plays that part for the simulated GPUs.
 * The node agent describes them through configfs, and each GPU
 * switched live becomes a platform device that nvidia.ko binds to, so a change
 * reaches the driver as hotplug -- remove, then probe -- and the GPUs survive
 * every driver reload:
 *
 *   /sys/kernel/config/mokka/
 *     generation              bumped by the agent once a description is applied
 *     gpus/<dddd:bb:ss.f>/    one directory per GPU, named by its PCI location
 *       uuid name minor pci_device_id
 *       live                  1 registers the GPU's device, 0 unregisters it
 *
 * The GPUs live here rather than in nvidia.ko because configfs pins the module
 * that owns a directory for as long as the directory exists. In nvidia.ko the
 * pins would count toward the driver's refcount, which the GPU Operator reads
 * to decide whether it can unload the driver. Nothing in the driver's
 * lifecycle unloads this module.
 *
 * The same discipline as gpio-sim, the kernel's own simulator for GPIO chips:
 * a GPU's attributes are read-only while it is live, so every change is an
 * unplug and a plug.
 */

#include <linux/configfs.h>
#include <linux/err.h>
#include <linux/init.h>
#include <linux/kernel.h>
#include <linux/list.h>
#include <linux/module.h>
#include <linux/moduleparam.h>
#include <linux/mutex.h>
#include <linux/platform_device.h>
#include <linux/property.h>
#include <linux/slab.h>
#include <linux/string.h>

#include "mokka_gpu.h"

/* Room for "GPU-" and a 36-character UUID, and for any model name a profile carries. */
#define MOKKA_GPU_UUID_LEN 64
#define MOKKA_GPU_NAME_LEN 96

/*
 * Serializes every change to a GPU: an attribute write, and the live switch
 * that registers or unregisters its device.
 */
static DEFINE_MUTEX(mokka_lock);

/* The live GPUs, so a second GPU claiming a minor is refused by name. */
static LIST_HEAD(mokka_live_gpus);

/*
 * Written by the node agent once it has applied a description, read by the
 * loader before it loads nvidia.ko. Zero until the first description, so a
 * driver loaded right after this module waits for its GPUs instead of finding
 * none. The parameter is a read-only view for the loader, which runs in a
 * container without the configfs mount.
 */
static u64 mokka_generation;
module_param_named(generation, mokka_generation, ullong, 0444);
MODULE_PARM_DESC(generation,
		 "How many descriptions the node agent has applied; written through configfs");

struct mokka_gpu_item {
	struct config_item item;
	char uuid[MOKKA_GPU_UUID_LEN];
	char name[MOKKA_GPU_NAME_LEN];
	u32 minor;
	bool has_minor;
	u16 pci_device_id;
	struct platform_device *pdev;	/* set while live */
	struct list_head live_node;	/* on mokka_live_gpus while live */
};

static struct mokka_gpu_item *to_gpu(struct config_item *item)
{
	return container_of(item, struct mokka_gpu_item, item);
}

/*
 * Registers the GPU's device. The driver bound to MOKKA_GPU_DEVICE, if loaded,
 * probes it before this returns.
 */
static int mokka_gpu_plug(struct mokka_gpu_item *gpu)
{
	const struct property_entry props[] = {
		PROPERTY_ENTRY_STRING(MOKKA_GPU_PROP_BUS_ID, config_item_name(&gpu->item)),
		PROPERTY_ENTRY_STRING(MOKKA_GPU_PROP_UUID, gpu->uuid),
		PROPERTY_ENTRY_STRING(MOKKA_GPU_PROP_NAME, gpu->name),
		PROPERTY_ENTRY_U32(MOKKA_GPU_PROP_MINOR, gpu->minor),
		PROPERTY_ENTRY_U16(MOKKA_GPU_PROP_DEVICE_ID, gpu->pci_device_id),
		{ }
	};
	/* Named after the minor, so /sys/bus/platform/devices reads like /dev. */
	const struct platform_device_info info = {
		.name = MOKKA_GPU_DEVICE,
		.id = gpu->minor,
		.properties = props,
	};
	struct mokka_gpu_item *other;
	struct platform_device *pdev;

	if (!gpu->uuid[0] || !gpu->has_minor) {
		pr_err("mokka: GPU %s needs a uuid and a minor before it can go live\n",
		       config_item_name(&gpu->item));
		return -EINVAL;
	}

	list_for_each_entry(other, &mokka_live_gpus, live_node) {
		if (other->minor == gpu->minor) {
			pr_err("mokka: GPU %s claims minor %u, which GPU %s holds\n",
			       config_item_name(&gpu->item), gpu->minor,
			       config_item_name(&other->item));
			return -EEXIST;
		}
	}

	pdev = platform_device_register_full(&info);
	if (IS_ERR(pdev))
		return PTR_ERR(pdev);

	gpu->pdev = pdev;
	list_add_tail(&gpu->live_node, &mokka_live_gpus);

	return 0;
}

/* Unregisters the GPU's device; the bound driver, if any, removes it first. */
static void mokka_gpu_unplug(struct mokka_gpu_item *gpu)
{
	list_del_init(&gpu->live_node);
	platform_device_unregister(gpu->pdev);
	gpu->pdev = NULL;
}

/* Copies a string attribute, dropping the newline echo appends. */
static ssize_t mokka_gpu_store_string(struct mokka_gpu_item *gpu, char *dst, size_t size,
				      const char *page, size_t count)
{
	size_t len = count;
	ssize_t ret = count;

	if (len && page[len - 1] == '\n')
		len--;
	if (len >= size)
		return -EINVAL;

	mutex_lock(&mokka_lock);
	if (gpu->pdev) {
		ret = -EBUSY;
	} else {
		memcpy(dst, page, len);
		dst[len] = '\0';
	}
	mutex_unlock(&mokka_lock);

	return ret;
}

static ssize_t mokka_gpu_show_string(const char *src, char *page)
{
	ssize_t ret;

	mutex_lock(&mokka_lock);
	ret = scnprintf(page, PAGE_SIZE, "%s\n", src);
	mutex_unlock(&mokka_lock);

	return ret;
}

static ssize_t mokka_gpu_uuid_show(struct config_item *item, char *page)
{
	return mokka_gpu_show_string(to_gpu(item)->uuid, page);
}

static ssize_t mokka_gpu_uuid_store(struct config_item *item, const char *page, size_t count)
{
	struct mokka_gpu_item *gpu = to_gpu(item);

	return mokka_gpu_store_string(gpu, gpu->uuid, sizeof(gpu->uuid), page, count);
}

static ssize_t mokka_gpu_name_show(struct config_item *item, char *page)
{
	return mokka_gpu_show_string(to_gpu(item)->name, page);
}

static ssize_t mokka_gpu_name_store(struct config_item *item, const char *page, size_t count)
{
	struct mokka_gpu_item *gpu = to_gpu(item);

	return mokka_gpu_store_string(gpu, gpu->name, sizeof(gpu->name), page, count);
}

/* Empty until written: minor 0 is a valid minor, so it cannot mean "unset". */
static ssize_t mokka_gpu_minor_show(struct config_item *item, char *page)
{
	struct mokka_gpu_item *gpu = to_gpu(item);
	ssize_t ret;

	mutex_lock(&mokka_lock);
	if (gpu->has_minor)
		ret = scnprintf(page, PAGE_SIZE, "%u\n", gpu->minor);
	else
		ret = scnprintf(page, PAGE_SIZE, "\n");
	mutex_unlock(&mokka_lock);

	return ret;
}

static ssize_t mokka_gpu_minor_store(struct config_item *item, const char *page, size_t count)
{
	struct mokka_gpu_item *gpu = to_gpu(item);
	ssize_t ret = count;
	u32 minor;
	int err;

	err = kstrtou32(page, 10, &minor);
	if (err)
		return err;

	mutex_lock(&mokka_lock);
	if (gpu->pdev) {
		ret = -EBUSY;
	} else {
		gpu->minor = minor;
		gpu->has_minor = true;
	}
	mutex_unlock(&mokka_lock);

	return ret;
}

/* Hexadecimal, as lspci and card info show it. */
static ssize_t mokka_gpu_pci_device_id_show(struct config_item *item, char *page)
{
	struct mokka_gpu_item *gpu = to_gpu(item);
	ssize_t ret;

	mutex_lock(&mokka_lock);
	ret = scnprintf(page, PAGE_SIZE, "%04x\n", gpu->pci_device_id);
	mutex_unlock(&mokka_lock);

	return ret;
}

static ssize_t mokka_gpu_pci_device_id_store(struct config_item *item, const char *page,
					     size_t count)
{
	struct mokka_gpu_item *gpu = to_gpu(item);
	ssize_t ret = count;
	u16 id;
	int err;

	err = kstrtou16(page, 16, &id);
	if (err)
		return err;

	mutex_lock(&mokka_lock);
	if (gpu->pdev)
		ret = -EBUSY;
	else
		gpu->pci_device_id = id;
	mutex_unlock(&mokka_lock);

	return ret;
}

static ssize_t mokka_gpu_live_show(struct config_item *item, char *page)
{
	struct mokka_gpu_item *gpu = to_gpu(item);
	ssize_t ret;

	mutex_lock(&mokka_lock);
	ret = scnprintf(page, PAGE_SIZE, "%d\n", gpu->pdev ? 1 : 0);
	mutex_unlock(&mokka_lock);

	return ret;
}

static ssize_t mokka_gpu_live_store(struct config_item *item, const char *page, size_t count)
{
	struct mokka_gpu_item *gpu = to_gpu(item);
	bool live;
	int err;

	err = kstrtobool(page, &live);
	if (err)
		return err;

	mutex_lock(&mokka_lock);
	if (live && !gpu->pdev)
		err = mokka_gpu_plug(gpu);
	else if (!live && gpu->pdev)
		mokka_gpu_unplug(gpu);
	mutex_unlock(&mokka_lock);

	return err ? err : count;
}

CONFIGFS_ATTR(mokka_gpu_, uuid);
CONFIGFS_ATTR(mokka_gpu_, name);
CONFIGFS_ATTR(mokka_gpu_, minor);
CONFIGFS_ATTR(mokka_gpu_, pci_device_id);
CONFIGFS_ATTR(mokka_gpu_, live);

static struct configfs_attribute *mokka_gpu_attrs[] = {
	&mokka_gpu_attr_uuid,
	&mokka_gpu_attr_name,
	&mokka_gpu_attr_minor,
	&mokka_gpu_attr_pci_device_id,
	&mokka_gpu_attr_live,
	NULL,
};

static void mokka_gpu_release(struct config_item *item)
{
	kfree(to_gpu(item));
}

static struct configfs_item_operations mokka_gpu_item_ops = {
	.release = mokka_gpu_release,
};

static const struct config_item_type mokka_gpu_type = {
	.ct_item_ops	= &mokka_gpu_item_ops,
	.ct_attrs	= mokka_gpu_attrs,
	.ct_owner	= THIS_MODULE,
};

/*
 * A GPU's directory is named by its PCI location, canonically spelled, so that
 * a location has exactly one directory and the agent can compare names.
 */
static struct config_item *mokka_gpus_make_item(struct config_group *group, const char *name)
{
	unsigned int domain, bus, slot, function;
	struct mokka_gpu_item *gpu;
	char canonical[16];
	int consumed = 0;

	if (sscanf(name, "%x:%x:%x.%x%n", &domain, &bus, &slot, &function, &consumed) != 4 ||
	    name[consumed] != '\0' || domain > 0xffff || bus > 0xff || slot > 0x1f ||
	    function > 0x7)
		return ERR_PTR(-EINVAL);

	snprintf(canonical, sizeof(canonical), "%04x:%02x:%02x.%x", domain, bus, slot, function);
	if (strcmp(name, canonical))
		return ERR_PTR(-EINVAL);

	gpu = kzalloc(sizeof(*gpu), GFP_KERNEL);
	if (!gpu)
		return ERR_PTR(-ENOMEM);

	INIT_LIST_HEAD(&gpu->live_node);
	config_item_init_type_name(&gpu->item, name, &mokka_gpu_type);

	return &gpu->item;
}

/* rmdir of a live GPU unplugs it first, as pulling a card does. */
static void mokka_gpus_drop_item(struct config_group *group, struct config_item *item)
{
	struct mokka_gpu_item *gpu = to_gpu(item);

	mutex_lock(&mokka_lock);
	if (gpu->pdev)
		mokka_gpu_unplug(gpu);
	mutex_unlock(&mokka_lock);

	config_item_put(item);
}

static struct configfs_group_operations mokka_gpus_group_ops = {
	.make_item	= mokka_gpus_make_item,
	.drop_item	= mokka_gpus_drop_item,
};

static const struct config_item_type mokka_gpus_type = {
	.ct_group_ops	= &mokka_gpus_group_ops,
	.ct_owner	= THIS_MODULE,
};

static ssize_t mokka_root_generation_show(struct config_item *item, char *page)
{
	return scnprintf(page, PAGE_SIZE, "%llu\n", READ_ONCE(mokka_generation));
}

static ssize_t mokka_root_generation_store(struct config_item *item, const char *page,
					   size_t count)
{
	u64 generation;
	int err;

	err = kstrtou64(page, 10, &generation);
	if (err)
		return err;

	WRITE_ONCE(mokka_generation, generation);

	return count;
}

CONFIGFS_ATTR(mokka_root_, generation);

static struct configfs_attribute *mokka_root_attrs[] = {
	&mokka_root_attr_generation,
	NULL,
};

static const struct config_item_type mokka_root_type = {
	.ct_attrs	= mokka_root_attrs,
	.ct_owner	= THIS_MODULE,
};

static struct config_group mokka_gpus_group;

static struct configfs_subsystem mokka_subsys = {
	.su_group = {
		.cg_item = {
			.ci_namebuf	= "mokka",
			.ci_type	= &mokka_root_type,
		},
	},
};

static int __init mokka_init(void)
{
	int ret;

	config_group_init(&mokka_subsys.su_group);
	mutex_init(&mokka_subsys.su_mutex);

	config_group_init_type_name(&mokka_gpus_group, "gpus", &mokka_gpus_type);
	configfs_add_default_group(&mokka_gpus_group, &mokka_subsys.su_group);

	ret = configfs_register_subsystem(&mokka_subsys);
	if (ret) {
		pr_err("mokka: cannot register the configfs subsystem: %d\n", ret);
		return ret;
	}

	pr_info("mokka: simulated hardware loaded; GPUs are described under configfs/mokka/gpus\n");

	return 0;
}

/* Runs only once every GPU directory is gone: each one pins the module. */
static void __exit mokka_exit(void)
{
	configfs_unregister_subsystem(&mokka_subsys);
	pr_info("mokka: simulated hardware unloaded\n");
}

module_init(mokka_init);
module_exit(mokka_exit);

MODULE_AUTHOR("NVIDIA CORPORATION");
MODULE_DESCRIPTION("Mokka simulated node hardware: GPUs for the simulated NVIDIA driver");
MODULE_LICENSE("Dual MIT/GPL");
