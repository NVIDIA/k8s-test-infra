// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package metadata

// Hardware discovery labels published by the KWOK data plane.
const (
	GPUProductLabel  = "nvidia.com/gpu.product"
	GPUCountLabel    = "nvidia.com/gpu.count"
	GPUMemoryLabel   = "nvidia.com/gpu.memory"
	MIGStrategyLabel = "nvidia.com/mig.strategy"
	NvidiaPCILabel   = "feature.node.kubernetes.io/pci-10de.present"
)

// DiscoveryLabelKeys includes optional labels so they can be removed on release.
func DiscoveryLabelKeys() []string {
	return []string{GPUProductLabel, GPUCountLabel, GPUMemoryLabel, MIGStrategyLabel, NvidiaPCILabel}
}
