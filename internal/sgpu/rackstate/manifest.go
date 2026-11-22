// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package rackstate

import (
	"cmp"
	"errors"
	"fmt"
	"slices"

	api "github.com/NVIDIA/k8s-test-infra/api/v1alpha1"
	"github.com/NVIDIA/k8s-test-infra/internal/sgpu/policy"
	"github.com/NVIDIA/k8s-test-infra/internal/sgpu/rackrender"
)

// DependencyManifest describes the observed inputs and reports whether they
// differ from the rack's saved delivery. The caller owns publication and
// generation increments. The first observation returns generation zero.
func DependencyManifest(rack *api.SGPURack, profile *api.SGPURackProfile, evaluated *policy.Evaluation) (api.RackDelivery, bool, error) {
	if rack == nil || rack.Generation < 1 {
		return api.RackDelivery{}, false, errors.New("rack spec generation is required")
	}
	inventoryRef, err := evaluatedInventory(rack.Spec.InventoryRef, evaluated)
	if err != nil {
		return api.RackDelivery{}, false, err
	}
	profileRef, err := observedProfile(rack.Spec.ProfileRef.Name, profile)
	if err != nil {
		return api.RackDelivery{}, false, err
	}
	if !evaluated.MatchesProfile(rack.Spec.Identity.RackGroup, profileRef.DeliveryObjectReference) {
		return api.RackDelivery{}, false, errors.New("policy evaluation must use the rack profile UID and generation")
	}
	next := api.RackDelivery{
		RackSpecGeneration: rack.Generation,
		InventoryRef:       inventoryRef,
		ProfileRef:         profileRef,
	}
	for p := range evaluated.AcceptedForRack(rack.Spec.Identity.RackGroup, rack.Spec.Identity.RackIndex) {
		if p.UID == "" || p.Generation < 1 {
			return api.RackDelivery{}, false, fmt.Errorf("accepted policy %q has no UID or generation", p.Name)
		}
		next.AcceptedPolicies = append(next.AcceptedPolicies, api.DeliveryObjectReference{UID: p.UID, Generation: p.Generation})
	}
	slices.SortFunc(next.AcceptedPolicies, comparePolicyUID)
	saved := rack.Status.Delivery
	if saved == nil {
		return next, true, nil
	}
	next.Generation = saved.Generation
	return next, inputsChanged(saved, &next), nil
}

func evaluatedInventory(owner api.SGPURackInventoryReference, evaluated *policy.Evaluation) (api.DeliveryObjectReference, error) {
	if evaluated == nil || evaluated.InventoryGeneration < 1 || evaluated.InventoryUID == "" ||
		owner.UID != evaluated.InventoryUID || owner.Name != evaluated.InventoryName {
		return api.DeliveryObjectReference{}, errors.New("policy evaluation must describe the rack's inventory")
	}
	return api.DeliveryObjectReference{UID: evaluated.InventoryUID, Generation: evaluated.InventoryGeneration}, nil
}

func observedProfile(name string, p *api.SGPURackProfile) (api.DeliveryProfileReference, error) {
	if p == nil || p.Name != name || p.UID == "" || p.Generation < 1 {
		return api.DeliveryProfileReference{}, errors.New("expected a versioned observation of the rack's profile")
	}
	revision, err := rackrender.ProfileRevision(p.Spec)
	if err != nil {
		return api.DeliveryProfileReference{}, err
	}
	return api.DeliveryProfileReference{
		DeliveryObjectReference: api.DeliveryObjectReference{UID: p.UID, Generation: p.Generation},
		Revision:                revision,
	}, nil
}

func comparePolicyUID(a, b api.DeliveryObjectReference) int {
	return cmp.Compare(a.UID, b.UID)
}

func inputsChanged(saved, next *api.RackDelivery) bool {
	previous := saved.AcceptedPolicies
	// Kubernetes list-map order carries no meaning. Keep informer objects immutable.
	if !slices.IsSortedFunc(previous, comparePolicyUID) {
		previous = slices.SortedFunc(slices.Values(previous), comparePolicyUID)
	}
	return saved.RackSpecGeneration != next.RackSpecGeneration || saved.InventoryRef != next.InventoryRef ||
		saved.ProfileRef != next.ProfileRef || !slices.Equal(previous, next.AcceptedPolicies)
}
