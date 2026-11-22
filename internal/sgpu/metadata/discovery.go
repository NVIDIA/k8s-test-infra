// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package metadata

import (
	"strconv"
	"strings"

	mokkav1 "github.com/NVIDIA/k8s-test-infra/api/v1alpha1"
)

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

// DiscoveryLabels renders the GFD and NFD values for a homogeneous whole-GPU node.
func DiscoveryLabels(gpus mokkav1.SGPUGPUs) map[string]string {
	const bytesPerMiB = 1 << 20
	result := map[string]string{
		GPUCountLabel:    strconv.FormatInt(int64(gpus.Count), 10),
		GPUMemoryLabel:   strconv.FormatInt(gpus.Memory.Capacity.Value()/bytesPerMiB, 10),
		MIGStrategyLabel: "none",
	}
	if name := gpus.Model.ProductName; name != "" {
		result[GPUProductLabel] = strings.ReplaceAll(name, " ", "-")
	}
	vendorID := strings.TrimPrefix(strings.ToLower(gpus.PCI.VendorID), "0x")
	if vendorID == "10de" || vendorID == "" && strings.EqualFold(gpus.Model.Vendor, "NVIDIA") {
		result[NvidiaPCILabel] = "true"
	}
	return result
}
