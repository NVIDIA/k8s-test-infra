// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

// Package rackstate compiles the static hardware view of a delivered rack.
package rackstate

import (
	"cmp"
	"errors"
	"fmt"
	"slices"

	api "github.com/NVIDIA/k8s-test-infra/api/v1alpha1"
	"github.com/NVIDIA/k8s-test-infra/internal/sgpu/rackrender"
	state "github.com/NVIDIA/k8s-test-infra/pkg/rackstate/v1alpha1"
)

// Compile uses only the supplied observations. A reader must retry if it
// hasn't observed the rack spec and profile pinned by the delivery record.
func Compile(rack *api.SGPURack, profile *api.SGPURackProfile) (state.Rack, error) {
	if rack == nil || rack.Name == "" || rack.UID == "" || rack.Status.Delivery == nil {
		return state.Rack{}, errors.New("rack identity and delivery are required")
	}
	delivery := rack.Status.Delivery
	if delivery.Generation < 1 || rack.Generation < 1 || rack.Generation != delivery.RackSpecGeneration {
		return state.Rack{}, errors.New("rack spec does not match its delivery generation")
	}
	if err := checkProfile(rack.Spec.ProfileRef, delivery.ProfileRef, profile); err != nil {
		return state.Rack{}, err
	}

	result := state.Rack{
		Hardware: state.Hardware{
			Rack:                   state.Object{Name: rack.Name, UID: rack.UID},
			RackDeliveryGeneration: delivery.Generation,
			Software:               profile.Spec.Software.DeepCopy(),
			GPUModel:               gpuModel(&profile.Spec.Node.GPUs),
		},
	}
	if fabric := profile.Spec.Node.Topology.GPUFabric; fabric != nil {
		result.Fabric = &state.Fabric{FabricUUID: rack.Spec.Identity.FabricUUID,
			CliqueID: rack.Spec.Identity.CliqueID, GPUFabric: *fabric.DeepCopy()}
	}
	nodes, err := boundSlots(rack.Spec.Nodes)
	if err != nil {
		return state.Rack{}, err
	}
	result.Nodes = nodes
	return result, nil
}

func boundSlots(slots []api.SGPURackNode) ([]state.Slot, error) {
	nodes := make([]state.Slot, 0, len(slots))
	for _, slot := range slots {
		ref := slot.NodeRef
		if ref == nil {
			continue
		}
		if ref.AssignmentRevision < 1 || ref.UID == "" || ref.Name == "" {
			return nil, fmt.Errorf("rack slot %d has an incomplete binding", slot.Index)
		}
		gpus := slices.Clone(slot.GPUs)
		slices.SortFunc(gpus, func(a, b api.SGPURackGPU) int { return cmp.Compare(a.Index, b.Index) })
		nodes = append(nodes, state.Slot{
			Index: slot.Index, Node: state.Object{Name: ref.Name, UID: ref.UID},
			AssignmentRevision: ref.AssignmentRevision, GPUs: gpus,
		})
	}
	slices.SortFunc(nodes, func(a, b state.Slot) int { return cmp.Compare(a.Index, b.Index) })
	return nodes, nil
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
