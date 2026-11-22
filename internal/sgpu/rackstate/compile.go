// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

// Package rackstate compiles the hardware and runtime view of a delivered rack.
package rackstate

import (
	"cmp"
	"errors"
	"fmt"
	"slices"

	"k8s.io/apimachinery/pkg/types"

	api "github.com/NVIDIA/k8s-test-infra/api/v1alpha1"
	"github.com/NVIDIA/k8s-test-infra/internal/sgpu/policy"
	"github.com/NVIDIA/k8s-test-infra/internal/sgpu/rackrender"
	"github.com/NVIDIA/k8s-test-infra/internal/sgpu/release"
	state "github.com/NVIDIA/k8s-test-infra/pkg/rackstate/v1alpha1"
)

// Observations are the cached policies and pending binding cleanups available to a reader.
type Observations struct {
	Policies        map[types.UID]*api.SGPURuntimePolicy
	PendingReleases []release.Cleanup
}

// Compile uses only the supplied observations. A reader must retry if it
// hasn't observed the rack spec, profile, and policies pinned by the delivery record.
func Compile(rack *api.SGPURack, profile *api.SGPURackProfile, observed Observations) (state.Rack, error) {
	if err := checkDelivery(rack, profile); err != nil {
		return state.Rack{}, err
	}

	policies, err := policy.DeliveredPolicies(rack.Status.Delivery.AcceptedPolicies, observed.Policies)
	if err != nil {
		return state.Rack{}, err
	}
	var defaults *api.RuntimeState
	if profile.Spec.Defaults != nil {
		defaults = profile.Spec.Defaults.Runtime
	}
	nodes, err := boundSlots(rack, defaults, policies, observed.PendingReleases)
	if err != nil {
		return state.Rack{}, err
	}
	return state.Rack{Hardware: rackHardware(rack, profile), Nodes: nodes}, nil
}

func checkDelivery(rack *api.SGPURack, profile *api.SGPURackProfile) error {
	if rack == nil || rack.Name == "" || rack.UID == "" || rack.Status.Delivery == nil {
		return errors.New("rack identity and delivery are required")
	}
	delivery := rack.Status.Delivery
	if delivery.Generation < 1 || rack.Generation < 1 || rack.Generation != delivery.RackSpecGeneration {
		return errors.New("rack spec does not match its delivery generation")
	}
	return checkProfile(rack.Spec.ProfileRef, delivery.ProfileRef, profile)
}

func rackHardware(rack *api.SGPURack, profile *api.SGPURackProfile) state.Hardware {
	hardware := state.Hardware{
		Rack:                   state.Object{Name: rack.Name, UID: rack.UID},
		RackDeliveryGeneration: rack.Status.Delivery.Generation,
		Software:               profile.Spec.Software.DeepCopy(),
		GPUModel:               gpuModel(&profile.Spec.Node.GPUs),
	}
	if fabric := profile.Spec.Node.Topology.GPUFabric; fabric != nil {
		hardware.Fabric = &state.Fabric{
			FabricUUID: rack.Spec.Identity.FabricUUID,
			CliqueID:   rack.Spec.Identity.CliqueID,
			GPUFabric:  *fabric.DeepCopy(),
		}
	}
	return hardware
}

func boundSlots(rack *api.SGPURack, defaults *api.RuntimeState, policies policy.RackPolicies, cleanups []release.Cleanup) ([]state.Slot, error) {
	releasing := make(map[int32]bool)
	for _, cleanup := range cleanups {
		if cleanup.MatchesRack(rack) {
			releasing[cleanup.Binding.Coordinate.NodeIndex] = true
		}
	}
	slots := rack.Spec.Nodes
	nodes := make([]state.Slot, 0, len(slots))
	for _, slot := range slots {
		ref := slot.NodeRef
		if ref == nil {
			continue
		}
		if ref.AssignmentRevision < 1 || ref.UID == "" || ref.Name == "" {
			return nil, fmt.Errorf("rack slot %d has an incomplete binding", slot.Index)
		}
		node := state.Slot{
			Index:              slot.Index,
			Node:               state.Object{Name: ref.Name, UID: ref.UID},
			AssignmentRevision: ref.AssignmentRevision,
			Releasing:          releasing[slot.Index],
		}
		if !node.Releasing {
			at := policy.Coordinate{RackGroup: rack.Spec.Identity.RackGroup, RackIndex: rack.Spec.Identity.RackIndex, NodeIndex: slot.Index}
			gpus, err := compileGPUs(slot.GPUs, defaults, policies, at)
			if err != nil {
				return nil, err
			}
			node.GPUs = gpus
		}
		nodes = append(nodes, node)
	}
	slices.SortFunc(nodes, func(a, b state.Slot) int { return cmp.Compare(a.Index, b.Index) })
	return nodes, nil
}

func compileGPUs(rendered []api.SGPURackGPU, defaults *api.RuntimeState, policies policy.RackPolicies, at policy.Coordinate) ([]state.GPU, error) {
	gpus := make([]state.GPU, 0, len(rendered))
	for _, gpu := range rendered {
		at.GPUIndex = gpu.Index
		runtime, err := policies.Runtime(defaults, at)
		if err != nil {
			return nil, fmt.Errorf("slot %d GPU %d runtime: %w", at.NodeIndex, at.GPUIndex, err)
		}
		gpus = append(gpus, state.GPU{SGPURackGPU: gpu, Runtime: runtime})
	}
	slices.SortFunc(gpus, func(a, b state.GPU) int { return cmp.Compare(a.Index, b.Index) })
	return gpus, nil
}

func checkProfile(rendered api.SGPURackProfileReference, pinned api.DeliveryProfileReference, profile *api.SGPURackProfile) error {
	if profile == nil || profile.UID == "" || profile.Generation < 1 ||
		profile.UID != pinned.UID || profile.Generation != pinned.Generation {
		return errors.New("profile identity or generation does not match rack delivery")
	}
	revision, err := rackrender.ProfileRevision(profile.Spec)
	if err != nil {
		return err
	}
	if revision != pinned.Revision || profile.Reference(revision) != rendered {
		return errors.New("profile content does not match the delivered and rendered rack")
	}
	return nil
}

func gpuModel(gpus *api.SGPUGPUs) state.GPUModel {
	m := &gpus.Model
	memory := gpus.Memory.Capacity.DeepCopy()
	result := state.GPUModel{
		Vendor: m.Vendor, Product: m.Product, ProductName: m.ProductName, Architecture: m.Architecture,
		Memory: memory.String(), PCI: *gpus.PCI.DeepCopy(), Cores: m.Cores.DeepCopy(),
		Board: m.Board.DeepCopy(), Firmware: m.Firmware.DeepCopy(),
	}
	if cc := m.ComputeCapability; cc != nil {
		result.ComputeCapability = fmt.Sprintf("%d.%d", cc.Major, cc.Minor)
	}
	return result
}
