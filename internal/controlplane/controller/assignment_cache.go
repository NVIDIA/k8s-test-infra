// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package controller

import (
	"cmp"
	"errors"
	"fmt"
	"slices"

	mokkav1alpha1 "github.com/NVIDIA/k8s-test-infra/api/v1alpha1"
)

// ErrCacheNotReady indicates that the replica has not synchronized its informer stores.
var ErrCacheNotReady = errors.New("controller caches are not ready")

// AssignmentSnapshot is one durable rack-slot assignment observed in the informer cache.
// Node points into Rack, which is a caller-owned deep copy.
type AssignmentSnapshot struct {
	Rack *mokkav1alpha1.SGPURack
	Node *mokkav1alpha1.SGPURackNode
}

// AssignmentsForNode returns every durable assignment matching an exact Node identity.
func (c *Controller) AssignmentsForNode(
	ref mokkav1alpha1.SGPUNodeReference,
) ([]AssignmentSnapshot, error) {
	if ref.Name == "" || ref.UID == "" {
		return nil, errors.New("assignment lookup requires exact Node name and UID")
	}
	if c == nil || !c.CacheReady() || c.snapshot == nil {
		return nil, ErrCacheNotReady
	}
	racks, err := c.snapshot.RacksByNodeUID(ref.UID)
	if err != nil {
		return nil, fmt.Errorf("look up racks for Node UID %q: %w", ref.UID, err)
	}
	result := make([]AssignmentSnapshot, 0, len(racks))
	for _, rack := range racks {
		result = append(result, assignmentsInRack(rack, ref)...)
	}
	slices.SortFunc(result, func(a, b AssignmentSnapshot) int {
		if order := cmp.Compare(a.Rack.Name, b.Rack.Name); order != 0 {
			return order
		}
		if order := cmp.Compare(a.Rack.UID, b.Rack.UID); order != 0 {
			return order
		}
		return cmp.Compare(a.Node.Index, b.Node.Index)
	})
	return result, nil
}

func assignmentsInRack(
	rack *mokkav1alpha1.SGPURack,
	ref mokkav1alpha1.SGPUNodeReference,
) []AssignmentSnapshot {
	result := make([]AssignmentSnapshot, 0)
	for index := range rack.Spec.Nodes {
		nodeRef := rack.Spec.Nodes[index].NodeRef
		if nodeRef == nil || nodeRef.Name != ref.Name || nodeRef.UID != ref.UID {
			continue
		}
		rackCopy := rack.DeepCopy()
		result = append(result, AssignmentSnapshot{
			Rack: rackCopy,
			Node: &rackCopy.Spec.Nodes[index],
		})
	}
	return result
}
