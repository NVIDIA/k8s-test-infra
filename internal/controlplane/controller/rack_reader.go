// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package controller

import (
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"

	api "github.com/NVIDIA/k8s-test-infra/api/v1alpha1"
	"github.com/NVIDIA/k8s-test-infra/internal/sgpu/assignment"
	"github.com/NVIDIA/k8s-test-infra/internal/sgpu/metadata"
	"github.com/NVIDIA/k8s-test-infra/internal/sgpu/rackstate"
	"github.com/NVIDIA/k8s-test-infra/internal/sgpu/release"
	state "github.com/NVIDIA/k8s-test-infra/pkg/rackstate/v1alpha1"
)

// ReadRacks reads immutable informer objects. Each batch pins its shared inputs.
func (c *Controller) ReadRacks(requests []state.RackRequest) ([]state.RackResult, error) {
	if !c.CacheReady() {
		return nil, ErrCacheNotReady
	}
	reader := rackReader{
		controller:  c,
		profiles:    make(map[string]*api.SGPURackProfile),
		inventories: make(map[string]*api.SGPUInventory),
		policies:    make(map[string]map[types.UID]*api.SGPURuntimePolicy),
	}
	results := make([]state.RackResult, 0, len(requests))
	for _, request := range requests {
		result := reader.resolve(request)
		result.Reference = request.Object
		results = append(results, result)
	}
	return results, nil
}

type rackReader struct {
	controller  *Controller
	profiles    map[string]*api.SGPURackProfile
	inventories map[string]*api.SGPUInventory
	policies    map[string]map[types.UID]*api.SGPURuntimePolicy
}

func (r *rackReader) resolve(request state.RackRequest) state.RackResult {
	c := r.controller
	rack, err := c.snapshot.Rack(request.Name)
	if apierrors.IsNotFound(err) || (err == nil && rack.UID != request.UID) {
		return state.RackResult{Status: state.NotFound}
	}
	if err != nil || !assignment.DeliveryCoversSpec(rack) || rack.Status.Delivery.Generation < request.MinRackDeliveryGeneration {
		return state.RackResult{Status: state.NotReady}
	}
	if !r.inventoryMatches(rack) {
		return state.RackResult{Status: state.NotReady}
	}
	return r.compilePublished(rack)
}

func (r *rackReader) compilePublished(rack *api.SGPURack) state.RackResult {
	profile := r.profile(rack.Spec.ProfileRef.Name)
	cleanups, revisions, ready := r.controller.publishedSlots(rack)
	if !ready {
		return state.RackResult{Status: state.NotReady}
	}
	compiled, err := rackstate.Compile(rack, profile, rackstate.Observations{Policies: r.inventoryPolicies(rack.Spec.InventoryRef.Name), PendingReleases: cleanups})
	if err != nil {
		return state.RackResult{Status: state.NotReady}
	}
	for i := range compiled.Nodes {
		if revision, ok := revisions[compiled.Nodes[i].Index]; ok {
			compiled.Nodes[i].AssignmentRevision = revision
		}
	}
	return state.RackResult{Rack: &compiled}
}

func (r *rackReader) inventoryMatches(rack *api.SGPURack) bool {
	owner := rack.Spec.InventoryRef
	inv, ok := r.inventories[owner.Name]
	if !ok {
		inv, _ = r.controller.snapshot.Inventory(owner.Name)
		r.inventories[owner.Name] = inv
	}
	return inv != nil && inv.Generation > 0 && inv.UID == owner.UID &&
		rack.Status.Delivery.InventoryRef == (api.DeliveryObjectReference{UID: inv.UID, Generation: inv.Generation})
}

func (r *rackReader) profile(name string) *api.SGPURackProfile {
	p, ok := r.profiles[name]
	if !ok {
		p, _ = r.controller.snapshot.Profile(name)
		r.profiles[name] = p
	}
	return p
}

func (r *rackReader) inventoryPolicies(name string) map[types.UID]*api.SGPURuntimePolicy {
	if policies, ok := r.policies[name]; ok {
		return policies
	}
	objects, err := r.controller.runtimePolicies.policies.ByIndex(runtimePolicyByTargetIndex, name)
	if err != nil {
		return nil
	}
	policies := make(map[types.UID]*api.SGPURuntimePolicy, len(objects))
	for _, obj := range objects {
		p := obj.(*api.SGPURuntimePolicy)
		policies[p.UID] = p
	}
	r.policies[name] = policies
	return policies
}

func (c *Controller) publishedSlots(rack *api.SGPURack) ([]release.Cleanup, map[int32]int64, bool) {
	var cleanups []release.Cleanup
	revisions := make(map[int32]int64)
	for i := range rack.Spec.Nodes {
		slot := &rack.Spec.Nodes[i]
		ref := slot.NodeRef
		if ref == nil {
			continue
		}
		revision, ready := c.releaseRevision(rack, slot)
		if !ready {
			return nil, nil, false
		}
		if revision == 0 {
			continue
		}

		// The release is published on the Node while the rack still owns its old slot.
		cleanups = append(cleanups, release.ForSlot(rack, *slot, ""))
		revisions[slot.Index] = revision
	}
	return cleanups, revisions, true
}

// releaseRevision returns zero for a published binding or the newer tombstone
// revision for a published release. Any other observation must catch up first.
func (c *Controller) releaseRevision(rack *api.SGPURack, slot *api.SGPURackNode) (int64, bool) {
	ref := slot.NodeRef
	obj, exists, err := c.publicationNodes.GetByKey(ref.Name)
	if err != nil || !exists {
		return 0, false
	}
	node := obj.(*corev1.Node)
	a, err := assignment.DecodeAssignment(node.Annotations[metadata.AssignmentAnnotation])
	if err != nil || node.UID != ref.UID || a.NodeUID != ref.UID {
		return 0, false
	}
	expected := rack.Status.Delivery.ReleaseRevision(ref.UID)
	if expected == 0 && a.CoversBinding(rack, slot) {
		return 0, true
	}
	return expected, !a.Assigned && expected == a.AssignmentRevision && expected > ref.AssignmentRevision
}
