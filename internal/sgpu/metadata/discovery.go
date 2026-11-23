// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package metadata

import (
	"strconv"
	"strings"

	state "github.com/NVIDIA/k8s-test-infra/pkg/rackstate/v1alpha1"
	"k8s.io/apimachinery/pkg/api/resource"
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
func DiscoveryLabels(count int, model state.GPUModel) (map[string]string, error) {
	memory, err := resource.ParseQuantity(model.Memory)
	if err != nil {
		return nil, err
	}
	const bytesPerMiB = 1 << 20
	result := map[string]string{
		GPUCountLabel:    strconv.Itoa(count),
		GPUMemoryLabel:   strconv.FormatInt(memory.Value()/bytesPerMiB, 10),
		MIGStrategyLabel: "none",
	}
	if name := model.ProductName; name != "" {
		result[GPUProductLabel] = strings.ReplaceAll(name, " ", "-")
	}
	vendorID := strings.TrimPrefix(strings.ToLower(model.PCI.VendorID), "0x")
	if vendorID == "10de" || vendorID == "" && strings.EqualFold(model.Vendor, "NVIDIA") {
		result[NvidiaPCILabel] = "true"
	}
	return result, nil
}
