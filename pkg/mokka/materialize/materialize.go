// Copyright (c) 2026, NVIDIA CORPORATION.  All rights reserved.
// Licensed under the Apache License, Version 2.0 (the "License");

// Package materialize renders immutable rack topology and identities from API
// inputs without Kubernetes clients, mutable global state, or time.
package materialize

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"

	"k8s.io/apimachinery/pkg/types"

	mokkav1alpha1 "github.com/NVIDIA/k8s-test-infra/pkg/apis/mokka/v1alpha1"
)

// MaxRackSpecSize is Kubernetes' accepted materialized rack-spec limit.
const MaxRackSpecSize = 1 << 20

var canonicalPCIAddress = regexp.MustCompile("^[0-9a-f]{4}:[0-9a-f]{2}:[0-9a-f]{2}\\.[0-7]$")

// RackInput pins every input needed to render one rack.
type RackInput struct {
	InventoryName string
	InventoryUID  types.UID
	Group         mokkav1alpha1.SGPURackGroup
	RackIndex     int32
	Profile       *mokkav1alpha1.SGPUProfile
}

// Rack is the pure materialization result consumed by rack reconciliation.
type Rack struct {
	Name string
	Spec mokkav1alpha1.SGPURackSpec
}

// ValidateProfile checks cross-field invariants required for deterministic
// rendering. Admission remains responsible for schema-local validation.
func ValidateProfile(spec mokkav1alpha1.SGPUProfileSpec) error {
	if spec.Rack.NodesPerRack <= 0 {
		return fmt.Errorf("rack.nodesPerRack must be positive")
	}
	if spec.Node.GPUs.Count <= 0 {
		return fmt.Errorf("node.gpus.count must be positive")
	}
	if len(spec.Node.Topology.GPUSlots) != int(spec.Node.GPUs.Count) {
		return fmt.Errorf(
			"gpuSlots length %d must equal gpus.count %d",
			len(spec.Node.Topology.GPUSlots),
			spec.Node.GPUs.Count,
		)
	}

	seenIndexes := make([]bool, spec.Node.GPUs.Count)
	seenPCIAddresses := make(map[string]struct{}, spec.Node.GPUs.Count)
	for _, slot := range spec.Node.Topology.GPUSlots {
		if slot.Index < 0 || slot.Index >= spec.Node.GPUs.Count || seenIndexes[slot.Index] {
			return fmt.Errorf("gpuSlots indexes must be unique and contiguous from zero")
		}
		seenIndexes[slot.Index] = true
		if !canonicalPCIAddress.MatchString(slot.PCIAddress) {
			return fmt.Errorf("gpuSlots PCI address %q is not canonical", slot.PCIAddress)
		}
		if _, exists := seenPCIAddresses[slot.PCIAddress]; exists {
			return fmt.Errorf("gpuSlots PCI addresses must be unique")
		}
		seenPCIAddresses[slot.PCIAddress] = struct{}{}
	}

	if fabric := spec.Node.Topology.GPUFabric; fabric != nil {
		wantGPUCount := int64(spec.Rack.NodesPerRack) * int64(spec.Node.GPUs.Count)
		if int64(fabric.Domain.GPUCount) != wantGPUCount {
			return fmt.Errorf(
				"gpuFabric.domain.gpuCount %d must equal rack.nodesPerRack * node.gpus.count (%d)",
				fabric.Domain.GPUCount,
				wantGPUCount,
			)
		}
	}
	return nil
}

// CanonicalProfileJSON returns the stable JSON content used for revisions.
func CanonicalProfileJSON(spec mokkav1alpha1.SGPUProfileSpec) ([]byte, error) {
	canonical := spec.DeepCopy()
	slices.SortFunc(canonical.Node.Topology.GPUSlots, func(a, b mokkav1alpha1.SGPUGPUSlot) int {
		return cmp.Compare(a.Index, b.Index)
	})
	for i := range canonical.Node.GPUs.Clocks.Supported {
		slices.Sort(canonical.Node.GPUs.Clocks.Supported[i].GraphicsMHz)
	}
	for key, attribute := range canonical.Node.GPUs.Capabilities.Attributes {
		slices.Sort(attribute.Strings)
		canonical.Node.GPUs.Capabilities.Attributes[key] = attribute
	}
	return json.Marshal(canonical)
}

// ProfileRevision returns the SHA-256 of a profile's canonical spec JSON.
func ProfileRevision(spec mokkav1alpha1.SGPUProfileSpec) (string, error) {
	content, err := CanonicalProfileJSON(spec)
	if err != nil {
		return "", fmt.Errorf("marshal canonical profile: %w", err)
	}
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:]), nil
}

// RenderRack materializes every rack slot and GPU identity.
func RenderRack(input RackInput) (Rack, error) {
	if input.InventoryName == "" {
		return Rack{}, fmt.Errorf("inventory name must not be empty")
	}
	if input.InventoryUID == "" {
		return Rack{}, fmt.Errorf("inventory UID must not be empty")
	}
	if input.Group.ID == "" {
		return Rack{}, fmt.Errorf("rack group ID must not be empty")
	}
	if input.RackIndex < 0 || input.RackIndex >= input.Group.Count {
		return Rack{}, fmt.Errorf(
			"rack index %d is outside group count %d",
			input.RackIndex,
			input.Group.Count,
		)
	}
	if input.Profile == nil {
		return Rack{}, fmt.Errorf("profile must not be nil")
	}
	if input.Profile.Name == "" || input.Profile.UID == "" || input.Profile.Generation < 1 {
		return Rack{}, fmt.Errorf("profile reference must include name, UID, and generation")
	}
	if err := ValidateProfile(input.Profile.Spec); err != nil {
		return Rack{}, fmt.Errorf("validate profile: %w", err)
	}

	revision, err := ProfileRevision(input.Profile.Spec)
	if err != nil {
		return Rack{}, err
	}
	spec := mokkav1alpha1.SGPURackSpec{
		InventoryRef: mokkav1alpha1.SGPURackInventoryReference{
			Name: input.InventoryName,
			UID:  input.InventoryUID,
		},
		ProfileRef: mokkav1alpha1.SGPURackProfileReference{
			Name:       input.Profile.Name,
			UID:        input.Profile.UID,
			Generation: input.Profile.Generation,
			Revision:   revision,
		},
		Identity: mokkav1alpha1.SGPURackIdentity{
			RackGroup: input.Group.ID,
			RackIndex: input.RackIndex,
			FabricUUID: FabricUUID(
				input.InventoryUID,
				input.Group.ID,
				input.RackIndex,
			),
			CliqueID: 0,
		},
		GPUFabric: input.Profile.Spec.Node.Topology.GPUFabric.DeepCopy(),
		Network:   input.Profile.Spec.Node.Topology.Network.DeepCopy(),
		Slots:     make([]mokkav1alpha1.SGPURackSlot, input.Profile.Spec.Rack.NodesPerRack),
	}

	gpuSlots := slices.Clone(input.Profile.Spec.Node.Topology.GPUSlots)
	slices.SortFunc(gpuSlots, func(a, b mokkav1alpha1.SGPUGPUSlot) int {
		return cmp.Compare(a.Index, b.Index)
	})
	for slotIndex := range spec.Slots {
		slot := &spec.Slots[slotIndex]
		slot.Index = int32(slotIndex)
		slot.GPUs = make([]mokkav1alpha1.SGPURackGPU, len(gpuSlots))
		for gpuIndex, profileSlot := range gpuSlots {
			slot.GPUs[gpuIndex] = mokkav1alpha1.SGPURackGPU{
				Index: profileSlot.Index,
				UUID: GPUUUID(
					input.InventoryUID,
					input.Group.ID,
					input.RackIndex,
					int32(slotIndex),
					profileSlot.Index,
				),
				Serial: GPUSerial(
					input.InventoryUID,
					input.Group.ID,
					input.RackIndex,
					int32(slotIndex),
					profileSlot.Index,
				),
				MinorNumber:        profileSlot.Index,
				PCIAddress:         profileSlot.PCIAddress,
				RootComplex:        profileSlot.RootComplex,
				NUMANode:           profileSlot.NUMANode,
				HostProcessorIndex: profileSlot.HostProcessorIndex,
			}
		}
	}

	content, err := json.Marshal(spec)
	if err != nil {
		return Rack{}, fmt.Errorf("marshal rack spec: %w", err)
	}
	if len(content) > MaxRackSpecSize {
		return Rack{}, fmt.Errorf(
			"rendered rack spec is %d bytes, exceeding the %d-byte limit",
			len(content),
			MaxRackSpecSize,
		)
	}

	return Rack{
		Name: RackName(input.InventoryName, input.InventoryUID, input.Group.ID, input.RackIndex),
		Spec: spec,
	}, nil
}
