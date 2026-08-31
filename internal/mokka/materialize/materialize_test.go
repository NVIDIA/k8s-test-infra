// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package materialize

import (
	"fmt"
	"math"
	"testing"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	mokkav1alpha1 "github.com/NVIDIA/k8s-test-infra/internal/controlplane/api/v1alpha1"
)

func TestValidateProfile(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*mokkav1alpha1.SGPURackProfileSpec)
		wantErr string
	}{
		{name: "valid"},
		{
			name: "slot count differs from GPU count",
			mutate: func(spec *mokkav1alpha1.SGPURackProfileSpec) {
				spec.Node.Topology.GPUSlots = spec.Node.Topology.GPUSlots[:1]
			},
			wantErr: "gpuSlots length",
		},
		{
			name: "slot indexes are not contiguous",
			mutate: func(spec *mokkav1alpha1.SGPURackProfileSpec) {
				spec.Node.Topology.GPUSlots[1].Index = 3
			},
			wantErr: "gpuSlots indexes",
		},
		{
			name: "slot indexes are duplicated",
			mutate: func(spec *mokkav1alpha1.SGPURackProfileSpec) {
				spec.Node.Topology.GPUSlots[1].Index = 0
			},
			wantErr: "gpuSlots indexes",
		},
		{
			name: "PCI addresses are duplicated",
			mutate: func(spec *mokkav1alpha1.SGPURackProfileSpec) {
				spec.Node.Topology.GPUSlots[1].PCIAddress = spec.Node.Topology.GPUSlots[0].PCIAddress
			},
			wantErr: "PCI addresses",
		},
		{
			name: "fabric domain has the wrong GPU count",
			mutate: func(spec *mokkav1alpha1.SGPURackProfileSpec) {
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

func TestValidateProfileDimensionBoundaries(t *testing.T) {
	tests := []struct {
		name         string
		nodesPerRack int32
		gpuCount     int32
		wantErr      string
	}{
		{name: "maximum nodes per rack", nodesPerRack: 1024, gpuCount: 1},
		{name: "too many nodes per rack", nodesPerRack: 1025, gpuCount: 1, wantErr: "rack.nodesPerRack must be at most 1024"},
		{name: "maximum GPUs per node", nodesPerRack: 1, gpuCount: 64},
		{name: "too many GPUs per node", nodesPerRack: 1, gpuCount: 65, wantErr: "node.gpus.count must be at most 64"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := profileSpecWithDimensions(tt.nodesPerRack, tt.gpuCount)
			err := ValidateProfile(spec)
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.EqualError(t, err, tt.wantErr)
		})
	}
}

func TestValidateProfileRejectsExtremeDimensionsBeforeTopologyWork(t *testing.T) {
	spec := validProfile().Spec
	spec.Rack.NodesPerRack = math.MaxInt32
	require.EqualError(t, ValidateProfile(spec), "rack.nodesPerRack must be at most 1024")

	spec = validProfile().Spec
	spec.Node.GPUs.Count = math.MaxInt32
	require.EqualError(t, ValidateProfile(spec), "node.gpus.count must be at most 64")
}

func TestValidateProfileRejectsGPUListBeyondRenderedLimit(t *testing.T) {
	spec := profileSpecWithDimensions(1, 64)
	spec.Node.Topology.GPUSlots = append(spec.Node.Topology.GPUSlots, mokkav1alpha1.GPUSlot{})
	require.EqualError(t, ValidateProfile(spec), "gpuSlots must contain at most 64 entries")
}

func TestRevisionFunctionsRejectProfileWithoutTopology(t *testing.T) {
	profile := validProfile()
	profile.Spec.Node.Topology = nil

	t.Run("canonical profile JSON", func(t *testing.T) {
		_, err := CanonicalProfileJSON(profile.Spec)
		require.EqualError(t, err, "node.topology must be set")
	})

	t.Run("profile revision", func(t *testing.T) {
		_, err := ProfileRevision(profile.Spec)
		require.EqualError(t, err, "marshal canonical profile: node.topology must be set")
	})

	t.Run("precomputed profile revision", func(t *testing.T) {
		_, err := PrecomputeProfileRevision(profile)
		require.EqualError(t, err, "marshal canonical profile: node.topology must be set")
	})
}

func TestProfileRevisionCanonicalVector(t *testing.T) {
	profile := validProfile()
	profile.Spec.Node.GPUs.Capabilities.Attributes = map[string]mokkav1alpha1.CapabilityAttribute{
		"zeta":  {Strings: []string{"two", "one"}},
		"alpha": {Strings: []string{"yes"}},
	}

	revision, err := ProfileRevision(profile.Spec)
	require.NoError(t, err)
	require.Equal(t, "70b035e15002657884cebea9c76251dc1dc9dd31faa3917c9a7e92c7c34fe6ad", revision)

	reordered := profile.Spec
	reordered.Node.Topology.GPUSlots[0], reordered.Node.Topology.GPUSlots[1] =
		reordered.Node.Topology.GPUSlots[1], reordered.Node.Topology.GPUSlots[0]
	reordered.Node.GPUs.Capabilities.Attributes["zeta"] = mokkav1alpha1.CapabilityAttribute{
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
	group := mokkav1alpha1.RackGroup{ID: "compute", Count: 4}

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
		Revision:   "227fe4d76d124cd5f9ddf14866fca0d466d904ab1716f7320bc5c83add9a8de8",
	}, rendered.Spec.ProfileRef)
	require.Equal(t, mokkav1alpha1.SGPURackIdentity{
		RackGroup:  "compute",
		RackIndex:  2,
		FabricUUID: "2c79cf0a-9456-5f4d-aef6-c9dba304466a",
		CliqueID:   0,
	}, rendered.Spec.Identity)
	require.Len(t, rendered.Spec.Nodes, 2)

	require.Equal(t, int32(0), rendered.Spec.Nodes[0].Index)
	require.Nil(t, rendered.Spec.Nodes[0].NodeRef)
	require.Len(t, rendered.Spec.Nodes[0].GPUs, 2)
	require.Equal(t, mokkav1alpha1.SGPURackGPU{
		Index:              0,
		UUID:               "GPU-e1fc2f7b-7e71-5cdd-addf-11103758e19f",
		Serial:             "01184543437958700640",
		MinorNumber:        0,
		PCIAddress:         "0000:01:00.0",
		RootComplex:        "pci0000:00",
		NUMANode:           0,
		HostProcessorIndex: 0,
	}, rendered.Spec.Nodes[0].GPUs[0])
	require.Equal(t, int32(1), rendered.Spec.Nodes[0].GPUs[1].Index)
	require.Equal(t, int32(1), rendered.Spec.Nodes[1].Index)

	// Rendered topology must not alias informer-cache objects passed by callers.
	profileAddress := profile.Spec.Node.Topology.GPUSlots[0].PCIAddress
	rendered.Spec.Nodes[0].GPUs[0].PCIAddress = "changed"
	require.Equal(t, profileAddress, profile.Spec.Node.Topology.GPUSlots[0].PCIAddress)
}

func TestRenderRackWithPrecomputedRevisionBindsProfileObservation(t *testing.T) {
	profile := validProfile()
	input := RackInput{
		InventoryName: "inventory-a",
		InventoryUID:  types.UID("inventory-uid-a"),
		Group:         mokkav1alpha1.RackGroup{ID: "compute", Count: 1},
		Profile:       profile,
	}
	expectedRevision, err := ProfileRevision(profile.Spec)
	require.NoError(t, err)
	precomputed, err := PrecomputeProfileRevision(profile)
	require.NoError(t, err)

	profile.Spec.Software.DriverVersion = "changed-after-resolution"
	rendered, err := RenderRackWithRevision(input, precomputed)
	require.NoError(t, err)
	require.Equal(t, expectedRevision, rendered.Spec.ProfileRef.Revision)
	changedRevision, err := ProfileRevision(profile.Spec)
	require.NoError(t, err)
	require.NotEqual(t, changedRevision, rendered.Spec.ProfileRef.Revision)

	input.Profile = profile.DeepCopy()
	_, err = RenderRackWithRevision(input, precomputed)
	require.EqualError(t, err, "precomputed revision belongs to a different profile observation")
}

func TestRenderRackRejectsInvalidCoordinates(t *testing.T) {
	profile := validProfile()
	base := RackInput{
		InventoryName: "inventory-a",
		InventoryUID:  types.UID("inventory-uid-a"),
		Group:         mokkav1alpha1.RackGroup{ID: "compute", Count: 1},
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
		Group:         mokkav1alpha1.RackGroup{ID: "compute", Count: 1},
		Profile:       profile,
	})
	require.NoError(t, err)
	require.Len(t, rendered.Spec.Nodes, 2)
	require.NotEmpty(t, rendered.Spec.Identity.FabricUUID)
}

func TestRenderRackRejectsSpecOverOneMiB(t *testing.T) {
	profile := validProfile()
	profile.Spec.Rack.NodesPerRack = 1024
	profile.Spec.Node.GPUs.Count = 8
	profile.Spec.Node.Topology.GPUSlots = make([]mokkav1alpha1.GPUSlot, 8)
	for index := range profile.Spec.Node.Topology.GPUSlots {
		profile.Spec.Node.Topology.GPUSlots[index] = mokkav1alpha1.GPUSlot{
			Index:              int32(index),
			PCIAddress:         fmt.Sprintf("0000:%02x:00.0", index+1),
			RootComplex:        "pci0000:00",
			NumaNode:           int32(index),
			HostProcessorIndex: int32(index),
		}
	}
	profile.Spec.Node.Topology.GPUFabric.Domain.GPUCount = 8192

	_, err := RenderRack(RackInput{
		InventoryName: "inventory-a",
		InventoryUID:  types.UID("inventory-uid-a"),
		Group:         mokkav1alpha1.RackGroup{ID: "compute", Count: 1},
		Profile:       profile,
	})
	require.ErrorContains(t, err, "exceeding the 1048576-byte limit")
}

func TestRenderRackAcceptsRenderedDimensionBoundaries(t *testing.T) {
	tests := []struct {
		name         string
		nodesPerRack int32
		gpuCount     int32
	}{
		{name: "maximum slots", nodesPerRack: 1024, gpuCount: 1},
		{name: "maximum GPUs per slot", nodesPerRack: 1, gpuCount: 64},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			profile := validProfile()
			profile.Spec = profileSpecWithDimensions(tt.nodesPerRack, tt.gpuCount)
			rendered, err := RenderRack(RackInput{
				InventoryName: "inventory-a",
				InventoryUID:  types.UID("inventory-uid-a"),
				Group:         mokkav1alpha1.RackGroup{ID: "compute", Count: 1},
				Profile:       profile,
			})
			require.NoError(t, err)
			require.Len(t, rendered.Spec.Nodes, int(tt.nodesPerRack))
			require.Len(t, rendered.Spec.Nodes[0].GPUs, int(tt.gpuCount))
		})
	}
}

func TestValidateRackSpecRejectsImpossibleRenderedDimensions(t *testing.T) {
	validNode := mokkav1alpha1.SGPURackNode{
		Index: 0,
		GPUs:  []mokkav1alpha1.SGPURackGPU{{Index: 0}},
	}
	tests := []struct {
		name    string
		spec    mokkav1alpha1.SGPURackSpec
		wantErr string
	}{
		{name: "no nodes", wantErr: "nodes must contain between 1 and 1024 entries"},
		{
			name:    "too many nodes",
			spec:    mokkav1alpha1.SGPURackSpec{Nodes: make([]mokkav1alpha1.SGPURackNode, 1025)},
			wantErr: "nodes must contain between 1 and 1024 entries",
		},
		{
			name: "too many GPUs",
			spec: mokkav1alpha1.SGPURackSpec{Nodes: []mokkav1alpha1.SGPURackNode{{
				Index: 0, GPUs: make([]mokkav1alpha1.SGPURackGPU, 65),
			}}},
			wantErr: "node 0 GPUs must contain between 1 and 64 entries",
		},
		{
			name: "node index outside schema",
			spec: mokkav1alpha1.SGPURackSpec{Nodes: []mokkav1alpha1.SGPURackNode{{
				Index: 1024, GPUs: validNode.GPUs,
			}}},
			wantErr: "node index 1024 is outside [0,1023]",
		},
		{
			name: "GPU index outside schema",
			spec: mokkav1alpha1.SGPURackSpec{Nodes: []mokkav1alpha1.SGPURackNode{{
				Index: 0, GPUs: []mokkav1alpha1.SGPURackGPU{{Index: 64}},
			}}},
			wantErr: "node 0 GPU index 64 is outside [0,63]",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateRackSpec(tt.spec)
			require.EqualError(t, err, tt.wantErr)
		})
	}
	require.NoError(t, ValidateRackSpec(mokkav1alpha1.SGPURackSpec{Nodes: []mokkav1alpha1.SGPURackNode{validNode}}))
}

func profileSpecWithDimensions(nodesPerRack, gpuCount int32) mokkav1alpha1.SGPURackProfileSpec {
	spec := validProfile().Spec
	spec.Rack.NodesPerRack = nodesPerRack
	spec.Node.GPUs.Count = gpuCount
	spec.Node.Topology.GPUSlots = make([]mokkav1alpha1.GPUSlot, gpuCount)
	for index := range spec.Node.Topology.GPUSlots {
		spec.Node.Topology.GPUSlots[index] = mokkav1alpha1.GPUSlot{
			Index:              int32(index),
			PCIAddress:         fmt.Sprintf("0000:%02x:00.0", index+1),
			RootComplex:        "pci0000:00",
			NumaNode:           int32(index),
			HostProcessorIndex: int32(index),
		}
	}
	spec.Node.Topology.GPUFabric.Domain.GPUCount = nodesPerRack * gpuCount
	return spec
}

func validProfile() *mokkav1alpha1.SGPURackProfile {
	return &mokkav1alpha1.SGPURackProfile{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "profile-a",
			UID:        types.UID("profile-uid-a"),
			Generation: 7,
		},
		Spec: mokkav1alpha1.SGPURackProfileSpec{
			Rack: mokkav1alpha1.SGPURackShape{NodesPerRack: 2},
			Node: mokkav1alpha1.SGPUNode{
				GPUs: mokkav1alpha1.SGPUGPUs{
					Count:        2,
					Capabilities: &mokkav1alpha1.GPUCapabilities{},
				},
				Topology: &mokkav1alpha1.SGPUTopology{
					GPUSlots: []mokkav1alpha1.GPUSlot{
						{Index: 0, PCIAddress: "0000:01:00.0", RootComplex: "pci0000:00", NumaNode: 0, HostProcessorIndex: 0},
						{Index: 1, PCIAddress: "0000:02:00.0", RootComplex: "pci0000:00", NumaNode: 1, HostProcessorIndex: 1},
					},
					GPUFabric: &mokkav1alpha1.GPUFabric{
						Type:                 "NVLink",
						Generation:           5,
						LinksPerGPU:          18,
						BandwidthPerLinkMBps: 50000,
						Domain: &mokkav1alpha1.FabricDomain{
							Scope:    "Rack",
							GPUCount: 4,
						},
					},
					Network: &mokkav1alpha1.NetworkTopology{
						Type:            "InfiniBand",
						AdapterModel:    "ConnectX",
						FirmwareVersion: "1.0",
						LinkSpeedGbps:   400,
						AdaptersPerGPU:  1,
					},
				},
			},
			Software: &mokkav1alpha1.SGPUSoftware{
				DriverVersion: "580.1",
				NVMLVersion:   "13",
				CUDAVersion:   "13.1",
			},
		},
	}
}
