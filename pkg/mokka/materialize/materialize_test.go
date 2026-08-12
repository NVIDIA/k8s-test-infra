// Copyright (c) 2026, NVIDIA CORPORATION.  All rights reserved.
// Licensed under the Apache License, Version 2.0 (the "License");

package materialize

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	mokkav1alpha1 "github.com/NVIDIA/k8s-test-infra/pkg/apis/mokka/v1alpha1"
)

func TestValidateProfile(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*mokkav1alpha1.SGPUProfileSpec)
		wantErr string
	}{
		{name: "valid"},
		{
			name: "slot count differs from GPU count",
			mutate: func(spec *mokkav1alpha1.SGPUProfileSpec) {
				spec.Node.Topology.GPUSlots = spec.Node.Topology.GPUSlots[:1]
			},
			wantErr: "gpuSlots length",
		},
		{
			name: "slot indexes are not contiguous",
			mutate: func(spec *mokkav1alpha1.SGPUProfileSpec) {
				spec.Node.Topology.GPUSlots[1].Index = 3
			},
			wantErr: "gpuSlots indexes",
		},
		{
			name: "slot indexes are duplicated",
			mutate: func(spec *mokkav1alpha1.SGPUProfileSpec) {
				spec.Node.Topology.GPUSlots[1].Index = 0
			},
			wantErr: "gpuSlots indexes",
		},
		{
			name: "PCI addresses are duplicated",
			mutate: func(spec *mokkav1alpha1.SGPUProfileSpec) {
				spec.Node.Topology.GPUSlots[1].PCIAddress = spec.Node.Topology.GPUSlots[0].PCIAddress
			},
			wantErr: "PCI addresses",
		},
		{
			name: "fabric domain has the wrong GPU count",
			mutate: func(spec *mokkav1alpha1.SGPUProfileSpec) {
				spec.Node.Topology.GPUFabric.Domain.GPUCount++
			},
			wantErr: "gpuFabric.domain.gpuCount",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := validProfile().Spec
			if tt.mutate != nil {
				tt.mutate(&spec)
			}

			err := ValidateProfile(spec)
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, tt.wantErr)
		})
	}
}

func TestProfileRevisionCanonicalVector(t *testing.T) {
	profile := validProfile()
	profile.Spec.Node.GPUs.Capabilities.Attributes = map[string]mokkav1alpha1.GPUCapabilityAttribute{
		"zeta":  {Strings: []string{"two", "one"}},
		"alpha": {Strings: []string{"yes"}},
	}

	revision, err := ProfileRevision(profile.Spec)
	require.NoError(t, err)
	require.Equal(t, "da12f93494230bb62008bc1044efdcb90bb58c5d87a8fab9035f20c558882da2", revision)

	reordered := profile.Spec
	reordered.Node.Topology.GPUSlots[0], reordered.Node.Topology.GPUSlots[1] =
		reordered.Node.Topology.GPUSlots[1], reordered.Node.Topology.GPUSlots[0]
	reordered.Node.GPUs.Capabilities.Attributes["zeta"] = mokkav1alpha1.GPUCapabilityAttribute{
		Strings: []string{"one", "two"},
	}
	reorderedRevision, err := ProfileRevision(reordered)
	require.NoError(t, err)
	require.Equal(t, revision, reorderedRevision)

	reordered.Software.DriverVersion = "different"
	changedRevision, err := ProfileRevision(reordered)
	require.NoError(t, err)
	require.NotEqual(t, revision, changedRevision)
}

func TestRenderRack(t *testing.T) {
	profile := validProfile()
	profile.Spec.Node.Topology.GPUSlots[0], profile.Spec.Node.Topology.GPUSlots[1] =
		profile.Spec.Node.Topology.GPUSlots[1], profile.Spec.Node.Topology.GPUSlots[0]
	group := mokkav1alpha1.SGPURackGroup{ID: "compute", Count: 4}

	rendered, err := RenderRack(RackInput{
		InventoryName: "inventory-a",
		InventoryUID:  types.UID("inventory-uid-a"),
		Group:         group,
		RackIndex:     2,
		Profile:       profile,
	})
	require.NoError(t, err)
	require.Equal(t, "inventory-a-compute-2-152f2cf61b78", rendered.Name)
	require.Equal(t, mokkav1alpha1.SGPURackInventoryReference{
		Name: "inventory-a",
		UID:  types.UID("inventory-uid-a"),
	}, rendered.Spec.InventoryRef)
	require.Equal(t, mokkav1alpha1.SGPURackProfileReference{
		Name:       "profile-a",
		UID:        types.UID("profile-uid-a"),
		Generation: 7,
		Revision:   "22fbd3512cf1ec698f56f7418c15b97db23a214c174495a234afa7de0efba4c0",
	}, rendered.Spec.ProfileRef)
	require.Equal(t, mokkav1alpha1.SGPURackIdentity{
		RackGroup:  "compute",
		RackIndex:  2,
		FabricUUID: "2c79cf0a-9456-5f4d-aef6-c9dba304466a",
		CliqueID:   0,
	}, rendered.Spec.Identity)
	require.Equal(t, profile.Spec.Node.Topology.GPUFabric, rendered.Spec.GPUFabric)
	require.Equal(t, profile.Spec.Node.Topology.Network, rendered.Spec.Network)
	require.Len(t, rendered.Spec.Slots, 2)

	require.Equal(t, int32(0), rendered.Spec.Slots[0].Index)
	require.Nil(t, rendered.Spec.Slots[0].NodeRef)
	require.Len(t, rendered.Spec.Slots[0].GPUs, 2)
	require.Equal(t, mokkav1alpha1.SGPURackGPU{
		Index:              0,
		UUID:               "GPU-e1fc2f7b-7e71-5cdd-addf-11103758e19f",
		Serial:             "01184543437958700640",
		MinorNumber:        0,
		PCIAddress:         "0000:01:00.0",
		RootComplex:        "pci0000:00",
		NUMANode:           0,
		HostProcessorIndex: 0,
	}, rendered.Spec.Slots[0].GPUs[0])
	require.Equal(t, int32(1), rendered.Spec.Slots[0].GPUs[1].Index)
	require.Equal(t, int32(1), rendered.Spec.Slots[1].Index)

	// Rendered topology must not alias informer-cache objects passed by callers.
	rendered.Spec.GPUFabric.Type = "changed"
	rendered.Spec.Network.Type = "changed"
	require.Equal(t, "NVLink", profile.Spec.Node.Topology.GPUFabric.Type)
	require.Equal(t, "InfiniBand", profile.Spec.Node.Topology.Network.Type)
}

func TestRenderRackRejectsInvalidCoordinates(t *testing.T) {
	profile := validProfile()
	base := RackInput{
		InventoryName: "inventory-a",
		InventoryUID:  types.UID("inventory-uid-a"),
		Group:         mokkav1alpha1.SGPURackGroup{ID: "compute", Count: 1},
		Profile:       profile,
	}

	tests := []struct {
		name   string
		mutate func(*RackInput)
	}{
		{name: "empty inventory name", mutate: func(in *RackInput) { in.InventoryName = "" }},
		{name: "empty inventory UID", mutate: func(in *RackInput) { in.InventoryUID = "" }},
		{name: "empty group", mutate: func(in *RackInput) { in.Group.ID = "" }},
		{name: "negative rack index", mutate: func(in *RackInput) { in.RackIndex = -1 }},
		{name: "rack index outside group", mutate: func(in *RackInput) { in.RackIndex = 1 }},
		{name: "empty profile UID", mutate: func(in *RackInput) { in.Profile.UID = "" }},
		{name: "invalid profile", mutate: func(in *RackInput) { in.Profile.Spec.Node.Topology.GPUSlots = nil }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := base
			input.Profile = base.Profile.DeepCopy()
			tt.mutate(&input)
			_, err := RenderRack(input)
			require.Error(t, err)
		})
	}
}

func TestRenderRackWithoutOptionalTopology(t *testing.T) {
	profile := validProfile()
	profile.Spec.Node.Topology.GPUFabric = nil
	profile.Spec.Node.Topology.Network = nil

	rendered, err := RenderRack(RackInput{
		InventoryName: "inventory-a",
		InventoryUID:  types.UID("inventory-uid-a"),
		Group:         mokkav1alpha1.SGPURackGroup{ID: "compute", Count: 1},
		Profile:       profile,
	})
	require.NoError(t, err)
	require.Nil(t, rendered.Spec.GPUFabric)
	require.Nil(t, rendered.Spec.Network)
	require.NotEmpty(t, rendered.Spec.Identity.FabricUUID)
}

func TestRenderRackRejectsSpecOverOneMiB(t *testing.T) {
	profile := validProfile()
	profile.Spec.Rack.NodesPerRack = 1024
	profile.Spec.Node.GPUs.Count = 8
	profile.Spec.Node.Topology.GPUSlots = make([]mokkav1alpha1.SGPUGPUSlot, 8)
	for index := range profile.Spec.Node.Topology.GPUSlots {
		profile.Spec.Node.Topology.GPUSlots[index] = mokkav1alpha1.SGPUGPUSlot{
			Index:              int32(index),
			PCIAddress:         fmt.Sprintf("0000:%02x:00.0", index+1),
			RootComplex:        "pci0000:00",
			NUMANode:           int32(index),
			HostProcessorIndex: int32(index),
		}
	}
	profile.Spec.Node.Topology.GPUFabric.Domain.GPUCount = 8192

	_, err := RenderRack(RackInput{
		InventoryName: "inventory-a",
		InventoryUID:  types.UID("inventory-uid-a"),
		Group:         mokkav1alpha1.SGPURackGroup{ID: "compute", Count: 1},
		Profile:       profile,
	})
	require.ErrorContains(t, err, "exceeding the 1048576-byte limit")
}

func validProfile() *mokkav1alpha1.SGPUProfile {
	return &mokkav1alpha1.SGPUProfile{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "profile-a",
			UID:        types.UID("profile-uid-a"),
			Generation: 7,
		},
		Spec: mokkav1alpha1.SGPUProfileSpec{
			Rack: mokkav1alpha1.SGPUProfileRack{NodesPerRack: 2},
			Node: mokkav1alpha1.SGPUProfileNode{
				GPUs: mokkav1alpha1.SGPUHardware{Count: 2},
				Topology: mokkav1alpha1.SGPUNodeTopology{
					GPUSlots: []mokkav1alpha1.SGPUGPUSlot{
						{Index: 0, PCIAddress: "0000:01:00.0", RootComplex: "pci0000:00", NUMANode: 0, HostProcessorIndex: 0},
						{Index: 1, PCIAddress: "0000:02:00.0", RootComplex: "pci0000:00", NUMANode: 1, HostProcessorIndex: 1},
					},
					GPUFabric: &mokkav1alpha1.SGPUGPUFabric{
						Type:                 "NVLink",
						Generation:           5,
						LinksPerGPU:          18,
						BandwidthPerLinkMBps: 50000,
						Domain: mokkav1alpha1.SGPUGPUFabricDomain{
							Scope:    "Rack",
							GPUCount: 4,
						},
					},
					Network: &mokkav1alpha1.SGPUNetwork{
						Type:            "InfiniBand",
						AdapterModel:    "ConnectX",
						FirmwareVersion: "1.0",
						LinkSpeedGbps:   400,
						AdaptersPerGPU:  1,
					},
				},
			},
			Software: mokkav1alpha1.SGPUSoftware{
				DriverVersion: "580.1",
				NVMLVersion:   "13",
				CUDAVersion:   "13.1",
			},
		},
	}
}
