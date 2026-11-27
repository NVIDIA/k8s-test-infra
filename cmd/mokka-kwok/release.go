// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package main

import (
	"context"
	"errors"
	"fmt"
	"slices"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	api "github.com/NVIDIA/k8s-test-infra/api/v1alpha1"
	"github.com/NVIDIA/k8s-test-infra/internal/sgpu/assignment"
	"github.com/NVIDIA/k8s-test-infra/internal/sgpu/metadata"
)

// Release uses only the durable binding and revision. A deleted Node or a
// retiring inventory can make an otherwise correct rack hardware read fail.
func (a *rackAllocator) releaseSlots(ctx context.Context, rack *api.SGPURack) error {
	record, create, err := a.allocationRecord(ctx, rack)
	if err != nil {
		return err
	}
	pending := pendingReleases(rack, record)
	if len(pending) == 0 {
		return nil
	}
	current, err := a.currentRack(ctx, rack)
	if err != nil || !current {
		return err
	}

	record, err = a.dropAllocations(ctx, record, create, pending)
	if err != nil {
		return err
	}
	var cleanupErr error
	changed := false
	for _, slot := range pending {
		ref := slot.NodeRef
		revision := rack.Status.Delivery.ReleaseRevision(ref.UID)
		if err := a.withdrawNode(ctx, ref, revision); err != nil {
			cleanupErr = errors.Join(cleanupErr, err)
			continue
		}
		record.MarkSlotReleased(slot.Index, ref.UID, revision)
		changed = true
	}
	if changed {
		_, err = a.records.Update(ctx, record, metav1.UpdateOptions{})
	}
	return errors.Join(cleanupErr, err)
}

func pendingReleases(rack *api.SGPURack, record *api.SGPURackAllocation) []api.SGPURackNode {
	var pending []api.SGPURackNode

	for _, slot := range rack.Spec.Nodes {
		ref := slot.NodeRef
		if ref == nil {
			continue
		}
		revision := rack.Status.Delivery.ReleaseRevision(ref.UID)
		if revision <= ref.AssignmentRevision || record.SlotReleased(slot.Index, ref.UID, revision) {
			continue
		}
		pending = append(pending, slot)
	}
	return pending
}

func (a *rackAllocator) dropAllocations(ctx context.Context, record *api.SGPURackAllocation, create bool, pending []api.SGPURackNode) (*api.SGPURackAllocation, error) {
	if create {
		return a.records.Create(ctx, record, metav1.CreateOptions{})
	}
	names := make(map[string]bool, len(pending))
	for _, slot := range pending {
		names[slot.NodeRef.Name] = true
	}
	before := len(record.Nodes)
	record.Nodes = slices.DeleteFunc(record.Nodes, func(n api.NodeAllocation) bool { return names[n.Name] })
	if before == len(record.Nodes) {
		return record, nil
	}
	return a.records.Update(ctx, record, metav1.UpdateOptions{})
}

func (a *rackAllocator) withdrawNode(ctx context.Context, ref *api.SGPUNodeReference, revision int64) error {
	nodes := a.core.Nodes()
	node, err := nodes.Get(ctx, ref.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if node.UID != ref.UID {
		return nil
	}
	tombstone, err := assignment.DecodeAssignment(node.Annotations[metadata.AssignmentAnnotation])
	if err != nil || tombstone.Assigned || tombstone.NodeUID != ref.UID || tombstone.AssignmentRevision != revision || node.Labels[metadata.AssignedLabel] == "true" {
		return fmt.Errorf("node %s does not carry release revision %d", ref.Name, revision)
	}
	publisher := labelPublisher{nodes: nodes, recorder: a.recorder}
	return publisher.publishResources(ctx, node, nil)
}
