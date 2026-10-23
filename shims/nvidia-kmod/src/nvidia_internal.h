/*
 * Copyright 2026 NVIDIA CORPORATION
 * SPDX-License-Identifier: MIT OR GPL-2.0-only
 *
 * Internals shared between nvidia.ko's translation units. Not included by the
 * dependent modules, which see only nvidia_anchor.h.
 */

#ifndef MOKKA_NVIDIA_INTERNAL_H
#define MOKKA_NVIDIA_INTERNAL_H

#include <linux/fs.h>
#include <linux/types.h>

/*
 * Character device major for the per-GPU nodes and nvidiactl. Must stay equal
 * to the major charDevsForDevices() mknods in
 * internal/agent/nvml/stage.go; a node whose major nothing has registered
 * cannot be opened at all -- open() returns ENXIO.
 */
#define MOKKA_NV_MAJOR 195

/*
 * The real driver's minor layout: GPUs from 0 up to MOKKA_NV_MINOR_GPU_MAX,
 * then the two device-wide nodes at the top of the major.
 */
#define MOKKA_NV_MINOR_GPU_MAX	247
#define MOKKA_NV_MINOR_MODESET	254
#define MOKKA_NV_MINOR_CONTROL	255

#define MOKKA_NV_PCI_VENDOR_ID 0x10de

/*
 * Frozen so that /proc/driver/nvidia/version agrees with the file
 * gpudriver.writeProcFS stages at driver/proc/driver/nvidia/version. Both can
 * exist at once, and a reader that finds two different build dates for one
 * driver learns nothing good.
 */
#define MOKKA_NVRM_BUILD_DATE "Thu Feb 20 23:41:34 UTC 2026"

/*
 * The NVreg_* module parameters, rendered into /proc/driver/nvidia/params.
 * Values are taken from writeProcFS in internal/agent/gpudriver/stage.go,
 * which is this repository's canonical list.
 */
extern uint mokka_nvreg_enable_msi;
extern char *mokka_nvreg_registry_dwords;
extern uint mokka_nvreg_device_file_gid;
extern uint mokka_nvreg_device_file_mode;
extern uint mokka_nvreg_device_file_uid;
extern uint mokka_nvreg_modify_device_files;
extern uint mokka_nvreg_preserve_video_memory_allocations;
extern uint mokka_nvreg_enable_resizable_bar;

struct proc_dir_entry;

/* A simulated GPU, as mokka.ko describes it; see mokka_gpu.h. */
struct mokka_gpu {
	char bdf[20];	/* canonical lowercase "dddd:bb:ss.f" */
	char uuid[64];
	char model[96];
	unsigned int minor;
	u32 domain;
	u8 bus;
	u8 slot;
	u8 function;
	u16 device_id;
	u32 gpu_id;	/* RM's identifier, derived from the PCI location */
	struct proc_dir_entry *proc_dir;
};

/* Register and unregister the driver for mokka.ko's GPUs; see nvidia_gpus.c. */
int mokka_nv_gpus_init(void);
void mokka_nv_gpus_exit(void);

/*
 * GPUs come and go while the module is loaded, as mokka.ko plugs and unplugs
 * them. A GPU these accessors return stays valid only while the caller holds
 * the GPU lock.
 */
void mokka_nv_gpus_lock(void);
void mokka_nv_gpus_unlock(void);
unsigned int mokka_nv_gpu_count(void);
const struct mokka_gpu *mokka_nv_gpu(unsigned int index);
const struct mokka_gpu *mokka_nv_gpu_by_id(u32 gpu_id);

/* Takes the GPU lock itself. */
bool mokka_nv_gpu_present(unsigned int minor);

int mokka_nv_procfs_init(void);
void mokka_nv_procfs_exit(void);
int mokka_nv_procfs_add_gpu(struct mokka_gpu *gpu);
void mokka_nv_procfs_remove_gpu(struct mokka_gpu *gpu);

long mokka_nv_ioctl(struct file *file, unsigned int cmd, unsigned long arg);

#endif /* MOKKA_NVIDIA_INTERNAL_H */
