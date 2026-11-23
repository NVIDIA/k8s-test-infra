// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package controller

import (
	"github.com/NVIDIA/k8s-test-infra/internal/sgpu/release"
	"testing"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	api "github.com/NVIDIA/k8s-test-infra/api/v1alpha1"
	"github.com/NVIDIA/k8s-test-infra/internal/sgpu/allocate"
	"github.com/NVIDIA/k8s-test-infra/internal/sgpu/assignment"
	"github.com/NVIDIA/k8s-test-infra/internal/sgpu/metadata"
	"github.com/NVIDIA/k8s-test-infra/internal/sgpu/rackrender"
	mokkafake "github.com/NVIDIA/k8s-test-infra/pkg/generated/clientset/versioned/fake"
	state "github.com/NVIDIA/k8s-test-infra/pkg/rackstate/v1alpha1"
)

func TestStandbyReadsOnlyPublishedRackState(t *testing.T) {
	profile, inventory := acceptanceProfile(1), acceptanceInventory()
	n := acceptanceNode("worker", "worker-uid", 1)
	rendered, err := rackrender.RenderRack(rackrender.RackInput{InventoryName: inventory.Name, InventoryUID: inventory.UID, Profile: profile, Group: inventory.Spec.RackGroups[0]})
	require.NoError(t, err)
	rack := &api.SGPURack{ObjectMeta: metav1.ObjectMeta{Name: rendered.Name, UID: "rack-uid", Generation: 6}, Spec: rendered.Spec}
	rack.Spec.Nodes[0].NodeRef = &api.SGPUNodeReference{Name: n.Name, UID: n.UID, AssignmentRevision: 7}
	rack.Status.Delivery = &api.RackDelivery{Generation: 10, RackSpecGeneration: 6, InventoryRef: api.DeliveryObjectReference{UID: inventory.UID, Generation: inventory.Generation}, ProfileRef: api.DeliveryProfileReference{DeliveryObjectReference: api.DeliveryObjectReference{UID: profile.UID, Generation: profile.Generation}, Revision: rack.Spec.ProfileRef.Revision}}
	raw, err := assignment.EncodeAssignment(rack, &rack.Spec.Nodes[0])
	require.NoError(t, err)
	n.Annotations = map[string]string{metadata.AssignmentAnnotation: raw}
	// Start a standby after the Node has lost its eligibility label.
	delete(n.Labels, allocate.EligibleNodeLabel)
	nodes := newAcceptanceNodeClient()
	nodes.create(n)
	client := mokkafake.NewSimpleClientset(rack, profile, inventory)
	c, err := newForNodes(nodes, client, Options{Workers: 1})
	require.NoError(t, err)
	req := state.RackRequest{Object: state.Object{Name: rack.Name, UID: rack.UID}, MinRackDeliveryGeneration: 10}
	_, err = c.ReadRacks([]state.RackRequest{req})
	require.ErrorIs(t, err, ErrCacheNotReady)
	startCachesForTest(t, c)
	newer, other := req, req
	newer.MinRackDeliveryGeneration++
	other.UID = "recreated"
	got, err := c.ReadRacks([]state.RackRequest{newer, req, other})
	require.NoError(t, err)
	require.Equal(t, state.NotReady, got[0].Status)
	require.NotNil(t, got[1].Rack)
	require.Len(t, got[1].Nodes[0].GPUs, 1)
	require.Equal(t, state.NotFound, got[2].Status)

	read := func() state.RackResult {
		entries, e := c.ReadRacks([]state.RackRequest{req})
		require.NoError(t, e)
		return entries[0]
	}
	changed := rack.DeepCopy()
	changed.Status.Delivery.AcceptedPolicies = []api.DeliveryObjectReference{{UID: "missing", Generation: 1}}
	require.NoError(t, c.snapshot.racks.Update(changed))
	require.Equal(t, state.NotReady, read().Status)
	changed = rack.DeepCopy()
	changed.Generation++
	require.NoError(t, c.snapshot.racks.Update(changed))
	require.Equal(t, state.NotReady, read().Status)
	changed = rack.DeepCopy()
	changed.Status.Delivery.InventoryRef.Generation++
	require.NoError(t, c.snapshot.racks.Update(changed))
	require.Equal(t, state.NotReady, read().Status)
	changed = rack.DeepCopy()
	changed.Spec.Nodes[0].NodeRef.AssignmentRevision++
	require.NoError(t, c.snapshot.racks.Update(changed))
	require.Equal(t, state.NotReady, read().Status)
	require.NoError(t, c.snapshot.racks.Update(rack))
	tombstone, err := assignment.EncodePublication(n.UID, 8, nil)
	require.NoError(t, err)
	released := n.DeepCopy()
	released.Annotations[metadata.AssignmentAnnotation] = tombstone
	require.NoError(t, c.publicationNodes.Update(released))
	require.Equal(t, state.NotReady, read().Status, "a Node tombstone alone must not change the old delivery")
	// Use separate stores so either watch can reach the standby first.
	leaderAPI := mokkafake.NewSimpleClientset(rack.DeepCopy()).MokkaV1alpha1().SGPURacks()
	leader := informerCache{liveRacks: leaderAPI, liveTimeout: defaultLiveNodeGetTimeout}
	cleanup := release.ForSlot(rack, rack.Spec.Nodes[0], release.NodeIneligible)
	require.NoError(t, leader.PublishRelease(t.Context(), cleanup, 8))
	require.NoError(t, leader.PublishRelease(t.Context(), cleanup, 8), "retry after a leader restart")
	delivered, err := leaderAPI.Get(t.Context(), rack.Name, metav1.GetOptions{})
	require.NoError(t, err)
	require.EqualValues(t, 11, delivered.Status.Delivery.Generation)
	require.EqualValues(t, 8, delivered.Status.Delivery.ReleaseRevisions[n.UID])
	require.NoError(t, c.snapshot.racks.Update(delivered))
	require.NoError(t, c.publicationNodes.Update(n))
	require.Equal(t, state.NotReady, read().Status, "a replica must also see the matching Node release")
	require.NoError(t, c.publicationNodes.Update(released))
	result := read()
	require.NotNil(t, result.Rack)
	require.Greater(t, result.RackDeliveryGeneration, got[1].RackDeliveryGeneration)
	require.True(t, result.Nodes[0].Releasing)
	require.Empty(t, result.Nodes[0].GPUs)
	require.EqualValues(t, 8, result.Nodes[0].AssignmentRevision)
}
