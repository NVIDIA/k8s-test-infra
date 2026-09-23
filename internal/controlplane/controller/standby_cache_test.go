// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package controller

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8stesting "k8s.io/client-go/testing"

	mokkav1alpha1 "github.com/NVIDIA/k8s-test-infra/api/v1alpha1"
	sgpuinventory "github.com/NVIDIA/k8s-test-infra/internal/sgpu/inventory"
	"github.com/NVIDIA/k8s-test-infra/internal/sgpu/rackrender"
	mokkafake "github.com/NVIDIA/k8s-test-infra/pkg/generated/clientset/versioned/fake"
)

func TestStandbyCachesAssignmentsWithoutRoutingWriteWork(t *testing.T) {
	node := acceptanceNode("node", "node-uid", 1)
	rack := assignmentRack("rack", "rack-uid", mokkav1alpha1.SGPUNodeReference{Name: node.Name, UID: node.UID}, 0)
	mokka := mokkafake.NewSimpleClientset(rack)
	controller, err := newForNodes(newAcceptanceNodeClient(), mokka, Options{Workers: 1})
	require.NoError(t, err)
	startCachesForTest(t, controller)
	mokka.Fake.ClearActions()

	assignments, err := controller.AssignmentsForNode(*rack.Spec.Nodes[0].NodeRef)
	require.NoError(t, err)
	require.Len(t, assignments, 1)
	require.Equal(t, rack.Name, assignments[0].Rack.Name)
	require.False(t, controller.LeaderReady())
	require.Zero(t, controller.queues.inventories.Len())
	require.Zero(t, controller.queues.groups.Len())
	require.Zero(t, controller.queues.projections.Len())
	require.Zero(t, controller.queues.status.Len())
	require.Never(t, func() bool { return hasMutatingAction(mokka.Actions()) }, 100*time.Millisecond, time.Millisecond)
}

func TestWarmPromotionReplaysCachedObjectsBeforeStartingWorkers(t *testing.T) {
	profile := acceptanceProfile(1)
	inventory := acceptanceInventory()
	node := acceptanceNode("node", "node-uid", 1)
	nodes := newAcceptanceNodeClient()
	nodes.create(node)
	mokka := mokkafake.NewSimpleClientset(profile, inventory)
	installAcceptanceAPIReactors(t, mokka)
	controller, err := newForNodes(nodes, mokka, Options{Workers: 1})
	require.NoError(t, err)
	startCachesForTest(t, controller)
	mokka.Fake.ClearActions()

	require.Never(t, func() bool { return hasMutatingAction(mokka.Actions()) }, 100*time.Millisecond, time.Millisecond,
		"cache-only operation must not reconcile pre-existing objects")

	leaderCtx, cancelLeader := context.WithCancel(context.Background())
	leaderDone := make(chan error, 1)
	go func() { leaderDone <- controller.RunLeader(leaderCtx) }()
	require.Eventually(t, controller.LeaderReady, 5*time.Second, 10*time.Millisecond)

	rackName := rackrender.RackName(inventory.Name, inventory.UID, inventory.Spec.RackGroups[0].ID, 0)
	require.Eventually(t, func() bool {
		rack, getErr := mokka.MokkaV1alpha1().SGPURacks().Get(t.Context(), rackName, metav1.GetOptions{})
		return getErr == nil && len(rack.Spec.Nodes) == 1 && rack.Spec.Nodes[0].NodeRef != nil &&
			rack.Spec.Nodes[0].NodeRef.Name == node.Name && rack.Spec.Nodes[0].NodeRef.UID == node.UID
	}, 10*time.Second, 20*time.Millisecond,
		"handler replay must reconcile objects observed before promotion")

	require.Eventually(t, func() bool {
		assignments, lookupErr := controller.AssignmentsForNode(mokkav1alpha1.SGPUNodeReference{
			Name: node.Name, UID: node.UID,
		})
		return lookupErr == nil && len(assignments) == 1 && assignments[0].Rack.Name == rackName
	}, 5*time.Second, 10*time.Millisecond)

	cancelLeader()
	require.NoError(t, <-leaderDone)
	require.False(t, controller.LeaderReady())
	require.True(t, controller.CacheReady(), "leader cancellation must not stop process-lifetime caches")

	stored, err := mokka.MokkaV1alpha1().SGPURacks().Get(t.Context(), rackName, metav1.GetOptions{})
	require.NoError(t, err)
	replacement := mokkav1alpha1.SGPUNodeReference{Name: "replacement", UID: "replacement-uid"}
	stored.Spec.Nodes[0].NodeRef = &replacement
	_, err = mokka.MokkaV1alpha1().SGPURacks().Update(t.Context(), stored, metav1.UpdateOptions{
		FieldManager: sgpuinventory.RackFieldManager,
	})
	require.NoError(t, err)
	mokka.Fake.ClearActions()
	require.Eventually(t, func() bool {
		assignments, lookupErr := controller.AssignmentsForNode(replacement)
		return lookupErr == nil && len(assignments) == 1
	}, 5*time.Second, 10*time.Millisecond,
		"the assignment cache must keep updating after leader workers stop")
	require.Never(t, func() bool { return hasMutatingAction(mokka.Actions()) }, 100*time.Millisecond, time.Millisecond,
		"events after handler shutdown must not trigger writes")
}

func TestLeaderAndStandbyObserveDurableAssignmentUpdates(t *testing.T) {
	original := mokkav1alpha1.SGPUNodeReference{Name: "node", UID: "node-uid"}
	rack := assignmentRack("rack", "rack-uid", original, 0)
	mokka := mokkafake.NewSimpleClientset(rack)
	first, err := newForNodes(newAcceptanceNodeClient(), mokka, Options{Workers: 1})
	require.NoError(t, err)
	second, err := newForNodes(newAcceptanceNodeClient(), mokka, Options{Workers: 1})
	require.NoError(t, err)
	startCachesForTest(t, first)
	startCachesForTest(t, second)

	firstAssignments, err := first.AssignmentsForNode(original)
	require.NoError(t, err)
	secondAssignments, err := second.AssignmentsForNode(original)
	require.NoError(t, err)
	require.Equal(t, firstAssignments, secondAssignments)
	require.Len(t, firstAssignments, 1)

	leaderCtx, cancelLeader := context.WithCancel(context.Background())
	leaderDone := make(chan error, 1)
	go func() { leaderDone <- first.RunLeader(leaderCtx) }()
	require.Eventually(t, first.LeaderReady, 5*time.Second, 10*time.Millisecond)
	require.False(t, second.LeaderReady())
	t.Cleanup(func() {
		cancelLeader()
		require.NoError(t, <-leaderDone)
	})

	stored, err := mokka.MokkaV1alpha1().SGPURacks().Get(t.Context(), rack.Name, metav1.GetOptions{})
	require.NoError(t, err)
	replacement := mokkav1alpha1.SGPUNodeReference{Name: "replacement", UID: "replacement-uid"}
	stored.Spec.Nodes[0].NodeRef = &replacement
	_, err = mokka.MokkaV1alpha1().SGPURacks().Update(t.Context(), stored, metav1.UpdateOptions{})
	require.NoError(t, err)

	for _, controller := range []*Controller{first, second} {
		require.Eventually(t, func() bool {
			assignments, lookupErr := controller.AssignmentsForNode(replacement)
			return lookupErr == nil && len(assignments) == 1 && assignments[0].Rack.Name == rack.Name
		}, 5*time.Second, 10*time.Millisecond)
	}
	firstAssignments, err = first.AssignmentsForNode(replacement)
	require.NoError(t, err)
	secondAssignments, err = second.AssignmentsForNode(replacement)
	require.NoError(t, err)
	require.Equal(t, firstAssignments, secondAssignments)
	require.Zero(t, second.queues.inventories.Len())
	require.Zero(t, second.queues.groups.Len())
	require.Zero(t, second.queues.projections.Len())
	require.Zero(t, second.queues.status.Len())
}

func startCachesForTest(t *testing.T, controller *Controller) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- controller.RunCaches(ctx) }()
	select {
	case <-controller.CachesSynced():
	case err := <-done:
		require.NoError(t, err)
		t.Fatal("cache lifecycle stopped before synchronization")
	case <-time.After(5 * time.Second):
		t.Fatal("controller caches did not synchronize")
	}
	require.True(t, controller.CacheReady())
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			require.NoError(t, err)
		case <-time.After(5 * time.Second):
			t.Error("controller caches did not stop")
		}
	})
}

func hasMutatingAction(actions []k8stesting.Action) bool {
	for _, action := range actions {
		if slices.Contains([]string{"create", "update", "patch", "delete", "delete-collection"}, action.GetVerb()) {
			return true
		}
	}
	return false
}
