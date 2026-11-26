// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package declaration

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"sigs.k8s.io/yaml"

	"github.com/NVIDIA/k8s-test-infra/api/v1alpha1"
)

const chartProfiles = "../../../deployments/nvml-mock/helm/nvml-mock/profiles"

// gpuProfileNICs is the NIC part of a chart GPU profile, which file mode
// reads in place of a rack profile, and where the profile puts its GPUs.
type gpuProfileNICs struct {
	Software struct {
		RDMA *v1alpha1.RDMASoftware `json:"rdma"`
	} `json:"software"`
	NICs struct {
		RootPorts  []v1alpha1.RootPort `json:"rootPorts"`
		GPUParents map[int32]string    `json:"gpuParents"`
		Slots      []v1alpha1.NICSlot  `json:"slots"`
	} `json:"nics"`
	// GPUs are the NVML engine's devices and PCIe topology, read leniently.
	GPUs []v1alpha1.GPUSlot `json:"-"`
}

// loadGPUProfileNICs decodes a GPU profile's software and nics keys strictly;
// the rest of the profile is the NVML engine's, of which it reads each GPU's
// address and root complex.
func loadGPUProfileNICs(t *testing.T, name string) gpuProfileNICs {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(chartProfiles, name+".yaml"))
	require.NoError(t, err)
	var doc map[string]any
	require.NoError(t, yaml.Unmarshal(data, &doc))
	part, err := yaml.Marshal(map[string]any{"software": doc["software"], "nics": doc["nics"]})
	require.NoError(t, err)
	var out gpuProfileNICs
	require.NoError(t, yaml.UnmarshalStrict(part, &out), name)

	var engine struct {
		Devices []struct {
			Index int32 `json:"index"`
			PCI   struct {
				BusID string `json:"bus_id"`
			} `json:"pci"`
		} `json:"devices"`
		PCIeTopology struct {
			RootComplexes []struct {
				ID       string   `json:"id"`
				NUMANode int32    `json:"numa_node"`
				Devices  []string `json:"devices"`
			} `json:"root_complexes"`
		} `json:"pcie_topology"`
	}
	require.NoError(t, yaml.Unmarshal(data, &engine))
	for _, d := range engine.Devices {
		gpu := v1alpha1.GPUSlot{Index: d.Index, PCIAddress: strings.ToLower(d.PCI.BusID), Parent: out.NICs.GPUParents[d.Index]}
		for _, rc := range engine.PCIeTopology.RootComplexes {
			if slices.ContainsFunc(rc.Devices, func(a string) bool { return strings.EqualFold(a, d.PCI.BusID) }) {
				gpu.RootComplex, gpu.NumaNode = rc.ID, rc.NUMANode
			}
		}
		out.GPUs = append(out.GPUs, gpu)
	}
	return out
}

// TestGPUProfileSlotsEqualRackFixtures keeps the two copies of a platform's
// PCIe tree equal until built-in rack profiles exist: file mode reads the NIC
// slots, root ports and GPU parents from the chart's GPU profile, and the GPU
// addresses from its NVML part; the rack fixtures stand for the control
// plane's rack profile.
func TestGPUProfileSlotsEqualRackFixtures(t *testing.T) {
	t.Parallel()
	for _, gpuProfile := range []string{"gb300", "vrnvl72"} {
		rack := gpuProfile // the rack fixture has the GPU profile's name
		t.Run(gpuProfile, func(t *testing.T) {
			t.Parallel()
			got := loadGPUProfileNICs(t, gpuProfile)
			data, err := os.ReadFile(filepath.Join(rackFixtures, rack+".yaml"))
			require.NoError(t, err)
			want, err := DecodeRackProfile(data)
			require.NoError(t, err)
			require.NotEmpty(t, got.NICs.Slots)
			require.Equal(t, want.Spec.Node.Topology.NICs, got.NICs.Slots)
			require.Equal(t, want.Spec.Node.Topology.RootPorts, got.NICs.RootPorts)
			require.Equal(t, want.Spec.Node.Topology.GPUSlots, got.GPUs)
			require.Equal(t, want.Spec.Software.RDMA, got.Software.RDMA)
		})
	}
}
