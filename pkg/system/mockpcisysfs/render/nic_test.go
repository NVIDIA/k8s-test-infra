// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package render

import (
	"testing"

	"github.com/stretchr/testify/require"

	mockibconfig "github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/config"
)

func TestSynthesizeNICs_DisabledWhenIBOff(t *testing.T) {
	require.Empty(t, SynthesizeNICs(mockibconfig.Infiniband{Enabled: false}, 8),
		"no NICs when IB disabled")
}

func TestSynthesizeNICs_CountFromGPUs(t *testing.T) {
	ib := mockibconfig.Infiniband{Enabled: true, HCAsPerGPU: 1}.Defaults()
	nics := SynthesizeNICs(ib, 8)
	require.Len(t, nics, 8, "one NIC per GPU")
	require.Equal(t, "0000:e0:00.0", nics[0].BDF)
	require.Equal(t, "0000:e0:07.0", nics[7].BDF)
	require.Equal(t, "pci0000:e0", nics[0].RootComplex)
	require.Equal(t, "0x15b3", nics[0].Attrs.Vendor)
	require.Equal(t, "0x020700", nics[0].Attrs.Class, "InfiniBand class")
}

func TestSynthesizeNICs_OverrideAndEthernet(t *testing.T) {
	ib := mockibconfig.Infiniband{
		Enabled: true, HCACountOverride: 2, LinkLayer: "Ethernet",
	}.Defaults()
	nics := SynthesizeNICs(ib, 8)
	require.Len(t, nics, 2, "hca_count override wins over gpu_count")
	require.Equal(t, "0x020000", nics[0].Attrs.Class, "Ethernet class")
}
