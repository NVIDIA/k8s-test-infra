/*
 * Copyright 2026 NVIDIA CORPORATION
 * SPDX-License-Identifier: MIT OR GPL-2.0-only
 *
 * The contract between mokka.ko, which holds the node's simulated GPUs, and
 * nvidia.ko, which drives them. Neither module calls the other. mokka.ko
 * registers one platform device per GPU under MOKKA_GPU_DEVICE, carrying the
 * GPU's description as named properties, and the driver core hands each such
 * device to the driver registered under that name, as the PCI core hands a
 * real GPU to the real driver.
 *
 * The properties are looked up by name, so a mokka.ko built from older
 * sources -- it stays loaded across driver upgrades -- still serves a newer
 * nvidia.ko: a property one side does not know is ignored, and one it misses
 * takes its default.
 */

#ifndef MOKKA_GPU_H
#define MOKKA_GPU_H

#define MOKKA_GPU_DEVICE "nvidia-gpu"

/* The PCI location, canonical lowercase "dddd:bb:ss.f". */
#define MOKKA_GPU_PROP_BUS_ID		"pci-bus-id"
/* The GPU UUID, "GPU-" and 36 characters. */
#define MOKKA_GPU_PROP_UUID		"uuid"
/* The model, as /proc/driver/nvidia/gpus/<bdf>/information shows it. */
#define MOKKA_GPU_PROP_NAME		"name"
/* u32: the GPU's /dev/nvidia<minor>. */
#define MOKKA_GPU_PROP_MINOR		"minor"
/* u16: the PCI device ID, without the vendor half. */
#define MOKKA_GPU_PROP_DEVICE_ID	"pci-device-id"

#endif /* MOKKA_GPU_H */
