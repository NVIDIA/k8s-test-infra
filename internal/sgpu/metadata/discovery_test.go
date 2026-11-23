// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package metadata

import (
	"testing"

	state "github.com/NVIDIA/k8s-test-infra/pkg/rackstate/v1alpha1"
	"github.com/stretchr/testify/require"
)

func TestDiscoveryLabels(t *testing.T) {
	t.Parallel()
	model := state.GPUModel{Memory: "40Gi", ProductName: "NVIDIA A100", Vendor: "NVIDIA"}
	model.PCI.VendorID = "0x10DE"
	got, err := DiscoveryLabels(8, model)
	require.NoError(t, err)
	require.Equal(t, map[string]string{
		"nvidia.com/gpu.product": "NVIDIA-A100", "nvidia.com/gpu.count": "8",
		"nvidia.com/gpu.memory": "40960", "nvidia.com/mig.strategy": "none",
		"feature.node.kubernetes.io/pci-10de.present": "true",
	}, got)
	model.ProductName = ""
	model.PCI.VendorID = "1234"
	got, err = DiscoveryLabels(8, model)
	require.NoError(t, err)
	require.NotContains(t, got, GPUProductLabel)
	require.NotContains(t, got, NvidiaPCILabel)
}
