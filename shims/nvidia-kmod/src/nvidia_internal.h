/*
 * Copyright 2026 NVIDIA CORPORATION
 * SPDX-License-Identifier: MIT OR GPL-2.0-only
 *
 * Internals shared between nvidia.ko's translation units. Not included by the
 * dependent modules, which see only nvidia_anchor.h.
 */

#ifndef MOKKA_NVIDIA_INTERNAL_H
#define MOKKA_NVIDIA_INTERNAL_H

#include <linux/types.h>

/*
 * Character device major for the per-GPU nodes and nvidiactl. Must stay equal
 * to the major charDevsForDevices() mknods in
 * internal/agent/gpudriver/stage.go; a node whose major nothing has registered
 * cannot be opened at all -- open() returns ENXIO.
 */
#define MOKKA_NV_MAJOR 195

/*
 * Frozen so that /proc/driver/nvidia/version agrees with the file
 * gpudriver.writeProcFS stages at driver/proc/driver/nvidia/version. Both can
 * exist at once, and a reader that finds two different build dates for one
 * driver learns nothing good.
 */
#define MOKKA_NVRM_BUILD_DATE "Thu Feb 20 23:41:34 UTC 2026"

/*
 * The NVreg_* module parameters, rendered into /proc/driver/nvidia/params.
 * Values and spelling are taken from writeProcFS in
 * internal/agent/gpudriver/stage.go, which is this repository's canonical list.
 */
extern uint mokka_nvreg_enable_msi;
extern char *mokka_nvreg_registry_dwords;
extern uint mokka_nvreg_device_file_gid;
extern uint mokka_nvreg_device_file_mode;
extern uint mokka_nvreg_device_file_uid;
extern uint mokka_nvreg_modify_device_files;
extern uint mokka_nvreg_preserve_video_memory_allocations;
extern uint mokka_nvreg_enable_resizable_bar;

/* Comma-separated GPU records: "<bdf>|<uuid>|<model>|<minor>". */
extern char *mokka_gpus;

int mokka_nv_procfs_init(void);
void mokka_nv_procfs_exit(void);

#endif /* MOKKA_NVIDIA_INTERNAL_H */
