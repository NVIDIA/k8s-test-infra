/*
 * Copyright 2026 NVIDIA CORPORATION
 * SPDX-License-Identifier: MIT OR GPL-2.0-only
 *
 * The symbol every dependent module references so that modpost records a
 * dependency on nvidia.ko.
 *
 * The dependency is the entire point: it is what makes the kernel report a
 * non-zero /sys/module/nvidia/refcnt, populate /sys/module/nvidia/holders/,
 * fill the "Used by" column in /proc/modules, and fail delete_module("nvidia")
 * with -EBUSY until every dependent is gone. None of that is implemented here;
 * it falls out of the reference existing.
 *
 * The name is Mokka-prefixed on purpose. It is visible in /proc/kallsyms and
 * Module.symvers, and a plausible-looking NVIDIA export there would invite
 * someone to mistake these modules for the real driver.
 */

#ifndef MOKKA_NVIDIA_ANCHOR_H
#define MOKKA_NVIDIA_ANCHOR_H

/*
 * Called once from each dependent's init purely to create the link-time
 * reference. Logs the caller so an unexpected holder is traceable in dmesg.
 */
void mokka_nvidia_rm_anchor(const char *client);

#endif /* MOKKA_NVIDIA_ANCHOR_H */
