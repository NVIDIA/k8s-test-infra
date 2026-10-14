// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package imex

// Where the node agent stages the IMEX kernel surface, relative to its host
// root. The NRI plugin and the CDI specs resolve the same paths against the
// overlay, so they are named once here.
const (
	// ChannelsDir holds the IMEX channel device nodes: channel0, as on a real
	// node whose driver was loaded with NVreg_CreateImexChannel0=1.
	ChannelsDir = "driver/dev/nvidia-caps-imex-channels"
	// ProcDevicesPath is the substitute /proc/devices the DRA driver's
	// compute-domain kubelet plugin reads through ALT_PROC_DEVICES_PATH.
	ProcDevicesPath = "imex/proc-devices"
	// FabricIMEXMgmtPath is the capability file the plugin reads the fabric
	// IMEX management device minor from.
	FabricIMEXMgmtPath = "driver/proc/driver/nvidia/capabilities/fabric-imex-mgmt"
)
