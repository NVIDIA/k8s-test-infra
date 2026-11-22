// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package metadata

import (
	"testing"

	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/api/resource"

	mokkav1 "github.com/NVIDIA/k8s-test-infra/api/v1alpha1"
)

func TestDiscoveryLabels(t *testing.T) {
	t.Parallel()
	gpus := mokkav1.SGPUGPUs{Count: 8}
	gpus.Model.ProductName = "NVIDIA A100"
	gpus.Memory.Capacity = resource.MustParse("40Gi")
	reserved := resource.MustParse("1Gi")
	gpus.Memory.Reserved = &reserved
	gpus.PCI.VendorID = "0x10DE"
	require.Equal(t, map[string]string{
		"nvidia.com/gpu.product": "NVIDIA-A100", "nvidia.com/gpu.count": "8",
		"nvidia.com/gpu.memory": "40960", "nvidia.com/mig.strategy": "none",
		"feature.node.kubernetes.io/pci-10de.present": "true",
	}, DiscoveryLabels(gpus))
	gpus.Model.ProductName = ""
	gpus.PCI.VendorID = "1234"
	got := DiscoveryLabels(gpus)
	require.NotContains(t, got, GPUProductLabel)
	require.NotContains(t, got, NvidiaPCILabel)
}
