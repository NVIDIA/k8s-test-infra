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
	"errors"
	"fmt"
	"regexp"
	"slices"

	"k8s.io/apimachinery/pkg/types"

	mokkav1alpha1 "github.com/NVIDIA/k8s-test-infra/internal/controlplane/api/v1alpha1"
)

const (
	// MaxRackSpecSize is Kubernetes' accepted materialized rack-spec limit.
	MaxRackSpecSize = 1 << 20
	// MaxNodesPerRack matches the SGPURack node-list schema limit.
	MaxNodesPerRack int32 = 1024
	// MaxGPUsPerNode matches the SGPURack per-node GPU-list schema limit.
	MaxGPUsPerNode int32 = 64
)

var canonicalPCIAddress = regexp.MustCompile(`^[0-9a-f]{4}:[0-9a-f]{2}:[0-9a-f]{2}\.[0-7]$`)
var canonicalRootComplex = regexp.MustCompile("^pci[0-9a-f]{4}:[0-9a-f]{2}$")

// RackInput pins every input needed to render one rack.
type RackInput struct {
	InventoryName string
	InventoryUID  types.UID
	Group         mokkav1alpha1.RackGroup
	RackIndex     int32
	Profile       *mokkav1alpha1.SGPURackProfile
}

// Rack is the pure materialization result consumed by rack reconciliation.
type Rack struct {
	Name string
	Spec mokkav1alpha1.SGPURackSpec
}

// ValidateProfile checks cross-field invariants required for deterministic
// rendering. Admission remains responsible for schema-local validation.
//
//nolint:cyclop // Validation returns field-specific errors for each independent profile contract.
func ValidateProfile(spec mokkav1alpha1.SGPURackProfileSpec) error {
	if spec.Rack.NodesPerRack <= 0 {
		return errors.New("rack.nodesPerRack must be positive")
	}
	if spec.Rack.NodesPerRack > MaxNodesPerRack {
		return fmt.Errorf("rack.nodesPerRack must be at most %d", MaxNodesPerRack)
	}
	if spec.Node.GPUs.Count <= 0 {
		return errors.New("node.gpus.count must be positive")
	}
	if spec.Node.GPUs.Count > MaxGPUsPerNode {
		return fmt.Errorf("node.gpus.count must be at most %d", MaxGPUsPerNode)
	}
	if spec.Node.Topology == nil {
		return errors.New("node.topology must be set")
	}
	if len(spec.Node.Topology.GPUSlots) > int(MaxGPUsPerNode) {
		return fmt.Errorf("gpuSlots must contain at most %d entries", MaxGPUsPerNode)
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
			return errors.New("gpuSlots indexes must be unique and contiguous from zero")
		}
		seenIndexes[slot.Index] = true
		if !canonicalPCIAddress.MatchString(slot.PCIAddress) {
			return fmt.Errorf("gpuSlots PCI address %q is not canonical", slot.PCIAddress)
		}
		if !canonicalRootComplex.MatchString(slot.RootComplex) {
			return fmt.Errorf("gpuSlots root complex %q is not canonical", slot.RootComplex)
		}
		if _, exists := seenPCIAddresses[slot.PCIAddress]; exists {
			return errors.New("gpuSlots PCI addresses must be unique")
		}
		seenPCIAddresses[slot.PCIAddress] = struct{}{}
	}

	if fabric := spec.Node.Topology.GPUFabric; fabric != nil {
		if fabric.Type == "" || fabric.Generation <= 0 || fabric.LinksPerGPU <= 0 ||
			fabric.BandwidthPerLinkMBps <= 0 || fabric.Domain == nil || fabric.Domain.Scope != "Rack" {
			return errors.New("gpuFabric must define a positive rack-scoped topology")
		}
		wantGPUCount := int64(spec.Rack.NodesPerRack) * int64(spec.Node.GPUs.Count)
		if int64(fabric.Domain.GPUCount) != wantGPUCount {
			return fmt.Errorf(
				"gpuFabric.domain.gpuCount %d must equal rack.nodesPerRack * node.gpus.count (%d)",
				fabric.Domain.GPUCount,
				wantGPUCount,
			)
		}
	}
	if network := spec.Node.Topology.Network; network != nil {
		if network.Type == "" || network.AdapterModel == "" || network.FirmwareVersion == "" ||
			network.LinkSpeedGbps <= 0 || network.AdaptersPerGPU <= 0 {
			return errors.New("network must define positive adapter topology")
		}
	}
	return nil
}

// CanonicalProfileJSON returns the stable JSON content used for revisions.
func CanonicalProfileJSON(spec mokkav1alpha1.SGPURackProfileSpec) ([]byte, error) {
	canonical := spec.DeepCopy()
	slices.SortFunc(canonical.Node.Topology.GPUSlots, func(a, b mokkav1alpha1.GPUSlot) int {
		return cmp.Compare(a.Index, b.Index)
	})
	if canonical.Node.GPUs.Clocks != nil {
		for i := range canonical.Node.GPUs.Clocks.Supported {
			slices.Sort(canonical.Node.GPUs.Clocks.Supported[i].GraphicsMHz)
		}
	}
	if canonical.Node.GPUs.Capabilities != nil {
		for key, attribute := range canonical.Node.GPUs.Capabilities.Attributes {
			slices.Sort(attribute.Strings)
			canonical.Node.GPUs.Capabilities.Attributes[key] = attribute
		}
	}
	return json.Marshal(canonical)
}

// ProfileRevision returns the SHA-256 of a profile's canonical spec JSON.
func ProfileRevision(spec mokkav1alpha1.SGPURackProfileSpec) (string, error) {
	content, err := CanonicalProfileJSON(spec)
	if err != nil {
		return "", fmt.Errorf("marshal canonical profile: %w", err)
	}
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:]), nil
}

// RenderRack materializes every logical Node and GPU identity.
//
//nolint:cyclop // Rendering validates each identity and topology input before materialization.
func RenderRack(input RackInput) (Rack, error) {
	if input.InventoryName == "" {
		return Rack{}, errors.New("inventory name must not be empty")
	}
	if input.InventoryUID == "" {
		return Rack{}, errors.New("inventory UID must not be empty")
	}
	if input.Group.ID == "" {
		return Rack{}, errors.New("rack group ID must not be empty")
	}
	if input.RackIndex < 0 || input.RackIndex >= input.Group.Count {
		return Rack{}, fmt.Errorf(
			"rack index %d is outside group count %d",
			input.RackIndex,
			input.Group.Count,
		)
	}
	if input.Profile == nil {
		return Rack{}, errors.New("profile must not be nil")
	}
	if input.Profile.Name == "" || input.Profile.UID == "" || input.Profile.Generation < 1 {
		return Rack{}, errors.New("profile reference must include name, UID, and generation")
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
		Nodes: make([]mokkav1alpha1.SGPURackNode, input.Profile.Spec.Rack.NodesPerRack),
	}

	gpuSlots := slices.Clone(input.Profile.Spec.Node.Topology.GPUSlots)
	slices.SortFunc(gpuSlots, func(a, b mokkav1alpha1.GPUSlot) int {
		return cmp.Compare(a.Index, b.Index)
	})
	for nodeIndex := range spec.Nodes {
		node := &spec.Nodes[nodeIndex]
		node.Index = int32(nodeIndex)
		node.GPUs = make([]mokkav1alpha1.SGPURackGPU, len(gpuSlots))
		for gpuIndex, profileSlot := range gpuSlots {
			node.GPUs[gpuIndex] = mokkav1alpha1.SGPURackGPU{
				Index: profileSlot.Index,
				UUID: GPUUUID(
					input.InventoryUID,
					input.Group.ID,
					input.RackIndex,
					int32(nodeIndex),
					profileSlot.Index,
				),
				Serial: GPUSerial(
					input.InventoryUID,
					input.Group.ID,
					input.RackIndex,
					int32(nodeIndex),
					profileSlot.Index,
				),
				MinorNumber:        profileSlot.Index,
				PCIAddress:         profileSlot.PCIAddress,
				RootComplex:        profileSlot.RootComplex,
				NUMANode:           profileSlot.NumaNode,
				HostProcessorIndex: profileSlot.HostProcessorIndex,
			}
		}
	}
	if err := ValidateRackSpec(spec); err != nil {
		return Rack{}, fmt.Errorf("validate rendered rack spec: %w", err)
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

// ValidateRackSpec checks the list dimensions materialization owns against the
// admission limits of the SGPURack it will submit.
func ValidateRackSpec(spec mokkav1alpha1.SGPURackSpec) error {
	if len(spec.Nodes) < 1 || len(spec.Nodes) > int(MaxNodesPerRack) {
		return fmt.Errorf("nodes must contain between 1 and %d entries", MaxNodesPerRack)
	}
	for _, node := range spec.Nodes {
		if err := validateRackNode(node); err != nil {
			return err
		}
	}
	return nil
}

func validateRackNode(node mokkav1alpha1.SGPURackNode) error {
	if node.Index < 0 || node.Index >= MaxNodesPerRack {
		return fmt.Errorf("node index %d is outside [0,%d]", node.Index, MaxNodesPerRack-1)
	}
	if len(node.GPUs) < 1 || len(node.GPUs) > int(MaxGPUsPerNode) {
		return fmt.Errorf("node %d GPUs must contain between 1 and %d entries", node.Index, MaxGPUsPerNode)
	}
	for _, gpu := range node.GPUs {
		if gpu.Index < 0 || gpu.Index >= MaxGPUsPerNode {
			return fmt.Errorf("node %d GPU index %d is outside [0,%d]", node.Index, gpu.Index, MaxGPUsPerNode-1)
		}
	}
	return nil
}
