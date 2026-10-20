/*
 * Copyright 2026 NVIDIA CORPORATION
 * SPDX-License-Identifier: MIT OR GPL-2.0-only
 *
 * The ioctls the real driver's kernel interface layer answers on its own,
 * before a request reaches the Resource Manager (RM): the version handshake,
 * GPU enumeration and per-device bookkeeping.
 *
 * That is the whole surface of a client that only enumerates GPUs and holds
 * them open, and nvidia-persistenced is the one that matters. The GPU
 * Operator's driver container starts it right after loading the modules, and
 * it exits unless libnvidia-cfg can list at least one GPU through here.
 *
 * RM escapes -- object allocation and control calls, which NVML and CUDA are
 * built on -- are not simulated: Mokka serves those clients from userspace
 * shims that never reach the kernel. They fail with -ENOTTY, logged, so a
 * client that needs more than this surface shows up in the kernel log.
 */

#include <linux/ctype.h>
#include <linux/fs.h>
#include <linux/kernel.h>
#include <linux/mutex.h>
#include <linux/numa.h>
#include <linux/sched.h>
#include <linux/slab.h>
#include <linux/string.h>
#include <linux/uaccess.h>

#include "nv_ioctl_abi.h"
#include "nvidia_internal.h"

_Static_assert(sizeof(MOKKA_NVRM_VERSION) <= NV_RM_API_VERSION_STRING_LENGTH,
	       "the version handshake returns MOKKA_NVRM_VERSION in a fixed-size field");

/*
 * Bounds an argument wrapped in NV_ESC_IOCTL_XFER_CMD, whose size userspace
 * states itself instead of encoding it in the command. The largest argument
 * handled here is a card-info array of a few kilobytes.
 */
#define MOKKA_NV_MAX_IOCTL_SIZE (16 * 1024)

/* Set by the first NV_ESC_SYS_PARAMS, as the real driver keeps it. */
static DEFINE_MUTEX(sys_params_lock);
static u64 numa_memblock_size;

/* Which node an escape is valid on: upstream's NV_CTL_DEVICE_ONLY and NV_ACTUAL_DEVICE_ONLY. */
enum mokka_nv_node {
	MOKKA_NV_ANY_NODE,
	MOKKA_NV_CONTROL_NODE,
	MOKKA_NV_GPU_NODE,
};

struct mokka_nv_escape {
	unsigned int cmd;
	size_t size;
	bool is_array;
	enum mokka_nv_node node;
	int (*handle)(void *params, size_t size);
};

/* RmSanitizeLogString(): the string comes from the client and goes to the kernel log. */
static void mokka_nv_sanitize(char *s, size_t len)
{
	size_t i;

	for (i = 0; i < len && s[i]; i++) {
		if (!isprint(s[i]))
			s[i] = '?';
	}
}

/*
 * nvidia_read_card_info(). The array must have room for every GPU; entries past
 * the last GPU stay zeroed, which is how a client tells where the list ends.
 */
static int mokka_nv_card_info(void *params, size_t size)
{
	nv_ioctl_card_info_t *ci = params;
	unsigned int i, count = mokka_nv_gpu_count();

	memset(ci, 0, size);
	if (size / sizeof(*ci) < count)
		return -EINVAL;

	for (i = 0; i < count; i++) {
		const struct mokka_gpu *gpu = mokka_nv_gpu(i);

		/*
		 * Upstream leaves the function number out, and
		 * nvidia-persistenced assumes 0 because of it. Interrupt and
		 * BARs stay zero: a simulated GPU has neither.
		 */
		ci[i].valid = 1;
		ci[i].pci_info.domain = gpu->domain;
		ci[i].pci_info.bus = gpu->bus;
		ci[i].pci_info.slot = gpu->slot;
		ci[i].pci_info.vendor_id = MOKKA_NV_PCI_VENDOR_ID;
		ci[i].pci_info.device_id = gpu->device_id;
		ci[i].gpu_id = gpu->gpu_id;
		ci[i].minor_number = gpu->minor;
	}

	return 0;
}

/* Links a GPU fd to a control fd so the two share RM clients. With no RM there is nothing to link. */
static int mokka_nv_register_fd(void *params, size_t size)
{
	return 0;
}

/*
 * RmPerformVersionCheck(). Every client starts with this handshake and treats a
 * mismatch as fatal, so the module must be built with the exact version of the
 * userspace components installed beside it -- the loader sees to that.
 */
static int mokka_nv_check_version(void *params, size_t size)
{
	nv_ioctl_rm_api_version_t *p = params;
	const char *rm = MOKKA_NVRM_VERSION;
	bool relaxed = p->cmd == NV_RM_API_VERSION_CMD_RELAXED;
	char comm[TASK_COMM_LEN];
	size_t i;

	p->reply = NV_RM_API_VERSION_REPLY_RECOGNIZED;

	if (p->cmd == NV_RM_API_VERSION_CMD_QUERY) {
		strscpy(p->versionString, rm, sizeof(p->versionString));
		return 0;
	}

	/* Stops at rm's terminator at the latest, so it never reads past it. */
	for (i = 0; i < sizeof(p->versionString) && p->versionString[i] == rm[i]; i++) {
		if (rm[i] == '\0' || (relaxed && rm[i] == '.'))
			return 0;
	}

	p->versionString[sizeof(p->versionString) - 1] = '\0';
	mokka_nv_sanitize(p->versionString, sizeof(p->versionString));
	get_task_comm(comm, current);
	mokka_nv_sanitize(comm, sizeof(comm));

	/* Upstream's wording, which is what an operator searching the kernel log looks for. */
	pr_err("NVRM: API mismatch: the client '%s' (pid %d)\n"
	       "NVRM: has the version %s, but this kernel module has\n"
	       "NVRM: the version %s.  Please make sure that this\n"
	       "NVRM: kernel module and all NVIDIA driver components\n"
	       "NVRM: have the same version.\n",
	       comm, task_pid_nr(current), p->versionString, rm);

	strscpy(p->versionString, rm, sizeof(p->versionString));

	return -EINVAL;
}

/* Fails for an ID that names no GPU, as the real driver's nvidia_dev_get() does. */
static int mokka_nv_attach_gpus(void *params, size_t size)
{
	const u32 *ids = params;
	size_t i;

	for (i = 0; i < size / sizeof(*ids); i++) {
		if (ids[i] != 0 && !mokka_nv_gpu_by_id(ids[i]))
			return -EINVAL;
	}

	return 0;
}

/* The first caller fixes the block size; a later one may only repeat it. */
static int mokka_nv_sys_params(void *params, size_t size)
{
	const nv_ioctl_sys_params_t *p = params;
	int ret = 0;

	mutex_lock(&sys_params_lock);
	if (numa_memblock_size == 0)
		numa_memblock_size = p->memblock_size;
	else if (numa_memblock_size != p->memblock_size)
		ret = -EBUSY;
	mutex_unlock(&sys_params_lock);

	return ret;
}

/*
 * Simulated GPU memory is never onlined as a NUMA node. On coherent platforms
 * that is also what the GPU Operator asks for, through
 * NVreg_CoherentGPUMemoryMode=driver. DISABLED is what tells
 * nvidia-persistenced there is nothing to online.
 */
static int mokka_nv_numa_info(void *params, size_t size)
{
	nv_ioctl_numa_info_t *p = params;

	memset(p, 0, sizeof(*p));
	p->nid = NUMA_NO_NODE;
	p->status = NV_IOCTL_NUMA_STATUS_DISABLED;

	mutex_lock(&sys_params_lock);
	p->memblock_size = numa_memblock_size;
	mutex_unlock(&sys_params_lock);

	return 0;
}

/* The open completed synchronously: mokka_nv_open() has already refused unknown minors. */
static int mokka_nv_wait_open_complete(void *params, size_t size)
{
	nv_ioctl_wait_open_complete_t *p = params;

	p->rc = 0;
	p->adapterStatus = NV_OK;

	return 0;
}

/*
 * Argument sizes are checked the way nv_validate_ioctl_data() checks them:
 * exactly one struct, or a non-empty whole number of elements for the two
 * array arguments. A size mismatch is how a layout drift between this module
 * and the userspace components would surface, so it is never relaxed.
 */
static const struct mokka_nv_escape mokka_nv_escapes[] = {
	{ NV_ESC_CARD_INFO, sizeof(nv_ioctl_card_info_t), true,
	  MOKKA_NV_CONTROL_NODE, mokka_nv_card_info },
	{ NV_ESC_REGISTER_FD, sizeof(nv_ioctl_register_fd_t), false,
	  MOKKA_NV_ANY_NODE, mokka_nv_register_fd },
	{ NV_ESC_CHECK_VERSION_STR, sizeof(nv_ioctl_rm_api_version_t), false,
	  MOKKA_NV_CONTROL_NODE, mokka_nv_check_version },
	{ NV_ESC_ATTACH_GPUS_TO_FD, sizeof(u32), true,
	  MOKKA_NV_CONTROL_NODE, mokka_nv_attach_gpus },
	{ NV_ESC_SYS_PARAMS, sizeof(nv_ioctl_sys_params_t), false,
	  MOKKA_NV_CONTROL_NODE, mokka_nv_sys_params },
	{ NV_ESC_NUMA_INFO, sizeof(nv_ioctl_numa_info_t), false,
	  MOKKA_NV_GPU_NODE, mokka_nv_numa_info },
	{ NV_ESC_WAIT_OPEN_COMPLETE, sizeof(nv_ioctl_wait_open_complete_t), false,
	  MOKKA_NV_ANY_NODE, mokka_nv_wait_open_complete },
};

static const struct mokka_nv_escape *mokka_nv_find_escape(unsigned int cmd)
{
	size_t i;

	for (i = 0; i < ARRAY_SIZE(mokka_nv_escapes); i++) {
		if (mokka_nv_escapes[i].cmd == cmd)
			return &mokka_nv_escapes[i];
	}

	return NULL;
}

static bool mokka_nv_valid_size(const struct mokka_nv_escape *esc, size_t size)
{
	if (size > MOKKA_NV_MAX_IOCTL_SIZE)
		return false;

	if (esc->is_array)
		return size != 0 && size % esc->size == 0;

	return size == esc->size;
}

static bool mokka_nv_valid_node(const struct mokka_nv_escape *esc, bool control)
{
	switch (esc->node) {
	case MOKKA_NV_CONTROL_NODE:
		return control;
	case MOKKA_NV_GPU_NODE:
		return !control;
	default:
		return true;
	}
}

long mokka_nv_ioctl(struct file *file, unsigned int cmd, unsigned long arg)
{
	bool control = iminor(file_inode(file)) == MOKKA_NV_MINOR_CONTROL;
	void __user *uparams = (void __user *)arg;
	unsigned int escape = _IOC_NR(cmd);
	size_t size = _IOC_SIZE(cmd);
	const struct mokka_nv_escape *esc;
	void *params;
	int ret;

	/*
	 * An argument too large for the command's 14-bit size field travels
	 * indirectly: the command names XFER, and the real escape, its size and
	 * a pointer to it ride in XFER's argument.
	 */
	if (escape == NV_ESC_IOCTL_XFER_CMD) {
		nv_ioctl_xfer_t xfer;

		if (size != sizeof(xfer))
			return -EINVAL;
		if (copy_from_user(&xfer, uparams, sizeof(xfer)))
			return -EFAULT;

		escape = xfer.cmd;
		size = xfer.size;
		uparams = u64_to_user_ptr(xfer.ptr);
	}

	esc = mokka_nv_find_escape(escape);
	if (!esc) {
		pr_warn_ratelimited("nvidia: ioctl 0x%02x (%zu bytes) from %s[%d] is not simulated\n",
				    escape, size, current->comm, task_pid_nr(current));
		return -ENOTTY;
	}

	if (!mokka_nv_valid_size(esc, size) || !mokka_nv_valid_node(esc, control))
		return -EINVAL;

	params = memdup_user(uparams, size);
	if (IS_ERR(params))
		return PTR_ERR(params);

	ret = esc->handle(params, size);

	/*
	 * Copied back even when the escape failed, as the real driver does: a
	 * version mismatch fails with -EINVAL and reports the module's version
	 * through the argument.
	 */
	if (copy_to_user(uparams, params, size))
		ret = -EFAULT;

	kfree(params);

	return ret;
}
