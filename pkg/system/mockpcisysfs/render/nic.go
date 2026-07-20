// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package render

import (
	"fmt"
	"strings"

	mockibconfig "github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/config"
)

// nicRootComplex is a synthetic PCI root reserved for mock Mellanox NICs. Bus
// 0xe0 is chosen to sit well clear of the GPU BDF ranges profiles declare, so
// NIC entries never collide with the topology-rendered GPU devices.
const nicRootComplex = "pci0000:e0"

// SynthesizeNICs derives mock Mellanox (15b3) NIC PCI devices from a profile's
// InfiniBand block. It returns nil when IB is disabled or the effective count
// is zero. The count mirrors pkg/network/mockib/render: hca_count override, or
// gpu_count * hcas_per_gpu. Every NIC carries vendor/class attribute files so a
// PCI scanner (NFD's pci.device source) matches the operator's nvidia-nics-rules.
func SynthesizeNICs(ib mockibconfig.Infiniband, gpuCount int) []Device {
	if !ib.Enabled {
		return nil
	}
	ib = ib.Defaults()
	count := ib.HCACountOverride
	if count <= 0 {
		count = gpuCount * ib.HCAsPerGPU
	}
	if count <= 0 {
		return nil
	}
	class := "0x020700" // InfiniBand controller (PCI class 0207)
	if strings.EqualFold(ib.LinkLayer, "Ethernet") {
		class = "0x020000" // Ethernet controller (PCI class 0200)
	}
	nics := make([]Device, 0, count)
	for i := 0; i < count; i++ {
		nics = append(nics, Device{
			BDF:         fmt.Sprintf("0000:e0:%02x.0", i),
			RootComplex: nicRootComplex,
			NUMANode:    0,
			Attrs: Attrs{
				Vendor:          "0x15b3",
				Device:          "0x1021",
				Class:           class,
				SubsystemVendor: "0x15b3",
				SubsystemDevice: "0x0000",
			},
		})
	}
	return nics
}
