/*
 * Copyright 2026 NVIDIA CORPORATION
 * SPDX-License-Identifier: MIT OR GPL-2.0-only
 *
 * The slice of the NVIDIA driver's ioctl ABI this module answers, mirrored from
 * kernel-open/common/inc/nv-ioctl.h, nv-ioctl-numbers.h and nv-ioctl-numa.h in
 * NVIDIA's open-gpu-kernel-modules. Names are upstream's, so the two can be
 * diffed side by side.
 *
 * The userspace driver components are compiled against the upstream layout, so
 * a field out of place here would corrupt their memory rather than fail. The
 * assertions pin every size and offset the handlers rely on, as measured from
 * the upstream headers.
 */

#ifndef MOKKA_NV_IOCTL_ABI_H
#define MOKKA_NV_IOCTL_ABI_H

#include <linux/stddef.h>
#include <linux/types.h>

#define NV_IOCTL_BASE 200

#define NV_ESC_CARD_INFO		(NV_IOCTL_BASE + 0)
#define NV_ESC_REGISTER_FD		(NV_IOCTL_BASE + 1)
#define NV_ESC_CHECK_VERSION_STR	(NV_IOCTL_BASE + 10)
#define NV_ESC_IOCTL_XFER_CMD		(NV_IOCTL_BASE + 11)
#define NV_ESC_ATTACH_GPUS_TO_FD	(NV_IOCTL_BASE + 12)
#define NV_ESC_SYS_PARAMS		(NV_IOCTL_BASE + 14)
#define NV_ESC_NUMA_INFO		(NV_IOCTL_BASE + 15)
#define NV_ESC_WAIT_OPEN_COMPLETE	(NV_IOCTL_BASE + 18)

/* NV_STATUS for success, as RM reports it in status fields. */
#define NV_OK 0

typedef struct {
	u32 domain;
	u8 bus;
	u8 slot;
	u8 function;
	u16 vendor_id;
	u16 device_id;
} nv_pci_info_t;

typedef struct nv_ioctl_xfer {
	u32 cmd;
	u32 size;
	u64 ptr __aligned(8);
} nv_ioctl_xfer_t;

typedef struct nv_ioctl_card_info {
	u8 valid;
	nv_pci_info_t pci_info;
	u32 gpu_id;
	u16 interrupt_line;
	u64 reg_address __aligned(8);
	u64 reg_size __aligned(8);
	u64 fb_address __aligned(8);
	u64 fb_size __aligned(8);
	u32 minor_number;
	u8 dev_name[10];
} nv_ioctl_card_info_t;

#define NV_RM_API_VERSION_STRING_LENGTH 64

typedef struct nv_ioctl_rm_api_version {
	u32 cmd;
	u32 reply;
	char versionString[NV_RM_API_VERSION_STRING_LENGTH];
} nv_ioctl_rm_api_version_t;

#define NV_RM_API_VERSION_CMD_STRICT		0
#define NV_RM_API_VERSION_CMD_RELAXED		'1'
#define NV_RM_API_VERSION_CMD_QUERY		'2'

#define NV_RM_API_VERSION_REPLY_UNRECOGNIZED	0
#define NV_RM_API_VERSION_REPLY_RECOGNIZED	1

typedef struct nv_ioctl_sys_params {
	u64 memblock_size __aligned(8);
} nv_ioctl_sys_params_t;

typedef struct nv_ioctl_register_fd {
	int ctl_fd;
} nv_ioctl_register_fd_t;

typedef struct nv_ioctl_wait_open_complete {
	int rc;
	u32 adapterStatus;
} nv_ioctl_wait_open_complete_t;

#define NV_IOCTL_NUMA_INFO_MAX_OFFLINE_ADDRESSES 64

typedef struct offline_addresses {
	u64 addresses[NV_IOCTL_NUMA_INFO_MAX_OFFLINE_ADDRESSES] __aligned(8);
	u32 numEntries;
} nv_offline_addresses_t;

typedef struct nv_ioctl_numa_info {
	s32 nid;
	s32 status;
	u64 memblock_size __aligned(8);
	u64 numa_mem_addr __aligned(8);
	u64 numa_mem_size __aligned(8);
	u8 use_auto_online;
	nv_offline_addresses_t offline_addresses __aligned(8);
} nv_ioctl_numa_info_t;

#define NV_IOCTL_NUMA_STATUS_DISABLED 0

_Static_assert(sizeof(nv_pci_info_t) == 12, "nv_pci_info_t size");
_Static_assert(offsetof(nv_pci_info_t, vendor_id) == 8, "nv_pci_info_t.vendor_id offset");
_Static_assert(sizeof(nv_ioctl_xfer_t) == 16, "nv_ioctl_xfer_t size");
_Static_assert(sizeof(nv_ioctl_card_info_t) == 72, "nv_ioctl_card_info_t size");
_Static_assert(offsetof(nv_ioctl_card_info_t, gpu_id) == 16, "nv_ioctl_card_info_t.gpu_id offset");
_Static_assert(offsetof(nv_ioctl_card_info_t, reg_address) == 24,
	       "nv_ioctl_card_info_t.reg_address offset");
_Static_assert(offsetof(nv_ioctl_card_info_t, minor_number) == 56,
	       "nv_ioctl_card_info_t.minor_number offset");
_Static_assert(sizeof(nv_ioctl_rm_api_version_t) == 72, "nv_ioctl_rm_api_version_t size");
_Static_assert(sizeof(nv_ioctl_sys_params_t) == 8, "nv_ioctl_sys_params_t size");
_Static_assert(sizeof(nv_ioctl_register_fd_t) == 4, "nv_ioctl_register_fd_t size");
_Static_assert(sizeof(nv_ioctl_wait_open_complete_t) == 8, "nv_ioctl_wait_open_complete_t size");
_Static_assert(sizeof(nv_ioctl_numa_info_t) == 560, "nv_ioctl_numa_info_t size");
_Static_assert(offsetof(nv_ioctl_numa_info_t, offline_addresses) == 40,
	       "nv_ioctl_numa_info_t.offline_addresses offset");

#endif /* MOKKA_NV_IOCTL_ABI_H */
