// Copyright (c) 2026, NVIDIA CORPORATION.  All rights reserved.
// Licensed under the Apache License, Version 2.0 (the "License");

package rack

import (
	"errors"
	"fmt"
	"math"

	mokkav1alpha1 "github.com/NVIDIA/k8s-test-infra/internal/controlplane/api/v1alpha1"
)

const (
	// MaxInventoryNodeSlots is the largest topology supported by one Inventory.
	MaxInventoryNodeSlots int64 = 100_000
	// ReasonCapacityExceeded identifies declarations outside the supported topology envelope.
	ReasonCapacityExceeded = "CapacityExceeded"
)

// DeclaredCapacity holds checked capacity values before conversion to API status types.
type DeclaredCapacity struct {
	Racks     int64
	NodeSlots int64
	GPUs      int64
}

// CapacityForGroup computes one group's declared capacity with checked intermediates.
func CapacityForGroup(group mokkav1alpha1.RackGroup, profile *mokkav1alpha1.SGPUProfile) (DeclaredCapacity, error) {
	if profile == nil {
		return DeclaredCapacity{}, fmt.Errorf("rack group %q profile must not be nil", group.ID)
	}
	racks := int64(group.Count)
	nodeSlots, ok := checkedMultiply(racks, int64(profile.Spec.Rack.NodesPerRack))
	if !ok {
		return DeclaredCapacity{}, fmt.Errorf("rack group %q node-slot capacity overflows int64", group.ID)
	}
	gpus, ok := checkedMultiply(nodeSlots, int64(profile.Spec.Node.GPUs.Count))
	if !ok {
		return DeclaredCapacity{}, fmt.Errorf("rack group %q GPU capacity overflows int64", group.ID)
	}
	return DeclaredCapacity{Racks: racks, NodeSlots: nodeSlots, GPUs: gpus}, nil
}

// AddCapacity combines checked group or inventory capacity values.
func AddCapacity(a, b DeclaredCapacity) (DeclaredCapacity, error) {
	racks, ok := checkedAdd(a.Racks, b.Racks)
	if !ok {
		return DeclaredCapacity{}, errors.New("aggregate rack capacity overflows int64")
	}
	nodeSlots, ok := checkedAdd(a.NodeSlots, b.NodeSlots)
	if !ok {
		return DeclaredCapacity{}, errors.New("aggregate node-slot capacity overflows int64")
	}
	gpus, ok := checkedAdd(a.GPUs, b.GPUs)
	if !ok {
		return DeclaredCapacity{}, errors.New("aggregate GPU capacity overflows int64")
	}
	return DeclaredCapacity{Racks: racks, NodeSlots: nodeSlots, GPUs: gpus}, nil
}

// ValidateSupportedCapacity enforces the controller scale contract and status bounds.
func ValidateSupportedCapacity(capacity DeclaredCapacity) error {
	if capacity.Racks < 0 || capacity.NodeSlots < 0 || capacity.GPUs < 0 {
		return errors.New("declared capacity must not be negative")
	}
	if capacity.NodeSlots > MaxInventoryNodeSlots {
		return fmt.Errorf(
			"desired node slots %d exceed supported maximum %d",
			capacity.NodeSlots,
			MaxInventoryNodeSlots,
		)
	}
	if capacity.Racks > MaxInventoryNodeSlots {
		return fmt.Errorf("desired racks %d exceed supported maximum %d", capacity.Racks, MaxInventoryNodeSlots)
	}
	if capacity.Racks > math.MaxInt32 || capacity.NodeSlots > math.MaxInt32 || capacity.GPUs > math.MaxInt32 {
		return errors.New("declared capacity exceeds int32 status bounds")
	}
	return nil
}

// StatusCapacity converts capacity only after the supported bounds are established.
func StatusCapacity(capacity DeclaredCapacity) (mokkav1alpha1.InventoryCapacity, error) {
	if err := ValidateSupportedCapacity(capacity); err != nil {
		return mokkav1alpha1.InventoryCapacity{}, err
	}
	return mokkav1alpha1.InventoryCapacity{
		Racks: int32(capacity.Racks),
		Nodes: int32(capacity.NodeSlots),
		GPUs:  int32(capacity.GPUs),
	}, nil
}

func checkedMultiply(a, b int64) (int64, bool) {
	if a < 0 || b < 0 {
		return 0, false
	}
	if a != 0 && b > math.MaxInt64/a {
		return 0, false
	}
	return a * b, true
}

func checkedAdd(a, b int64) (int64, bool) {
	if a < 0 || b < 0 || b > math.MaxInt64-a {
		return 0, false
	}
	return a + b, true
}
