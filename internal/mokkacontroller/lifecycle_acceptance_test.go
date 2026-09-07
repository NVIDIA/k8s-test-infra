// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package mokkacontroller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	k8stesting "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/cache"

	mokkav1alpha1 "github.com/NVIDIA/k8s-test-infra/internal/controlplane/api/v1alpha1"
	"github.com/NVIDIA/k8s-test-infra/internal/mokka/allocate"
	"github.com/NVIDIA/k8s-test-infra/internal/mokka/materialize"
	controllernodes "github.com/NVIDIA/k8s-test-infra/internal/mokkacontroller/nodecatalog"
	controllerprojection "github.com/NVIDIA/k8s-test-infra/internal/mokkacontroller/projection"
	controllerack "github.com/NVIDIA/k8s-test-infra/internal/mokkacontroller/rack"
	controllerstatus "github.com/NVIDIA/k8s-test-infra/internal/mokkacontroller/status"
	mokkafake "github.com/NVIDIA/k8s-test-infra/pkg/generated/clientset/versioned/fake"
	mokkalisters "github.com/NVIDIA/k8s-test-infra/pkg/generated/listers/api/v1alpha1"
)

//nolint:cyclop // One acceptance flow deliberately exercises the complete lifecycle sequence.
func TestControllerLifecycleAcceptance(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	nodes := newAcceptanceNodeClient()
	mokka := mokkafake.NewSimpleClientset()
	installAcceptanceAPIReactors(t, mokka)
	controller, err := newForNodes(nodes, mokka, Options{Workers: 2, StatusDebounce: 0})
	require.NoError(t, err)

	runDone := make(chan error, 1)
	go func() { runDone <- controller.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-runDone:
		case <-time.After(5 * time.Second):
			t.Error("controller did not stop")
		}
	})
	require.Eventually(t, controller.Ready, 5*time.Second, 10*time.Millisecond)

	profile := acceptanceProfile(1)
	inventory := acceptanceInventory()
	inventory.Spec.RackGroups[0].Count = 2
	nodes.create(acceptanceNode("node-a", "node-a-v1", 1))
	nodes.create(acceptanceNode("node-b", "node-b-v1", 2))
	_, err = mokka.MokkaV1alpha1().SGPURackProfiles().Create(ctx, profile, metav1.CreateOptions{})
	require.NoError(t, err)
	_, err = mokka.MokkaV1alpha1().SGPUInventories().Create(ctx, inventory, metav1.CreateOptions{})
	require.NoError(t, err)

	rackName := materialize.RackName(inventory.Name, inventory.UID, "compute", 0)
	secondRackName := materialize.RackName(inventory.Name, inventory.UID, "compute", 1)
	require.Eventually(t, func() bool {
		rack := getAcceptanceRack(ctx, t, mokka, rackName)
		secondRack := getAcceptanceRack(ctx, t, mokka, secondRackName)
		return len(rack.Spec.Nodes) == 1 && len(secondRack.Spec.Nodes) == 1 &&
			rack.Spec.Nodes[0].NodeRef != nil && rack.Spec.Nodes[0].NodeRef.UID == "node-a-v1" &&
			secondRack.Spec.Nodes[0].NodeRef != nil && secondRack.Spec.Nodes[0].NodeRef.UID == "node-b-v1" &&
			nodeIsProjected(nodes.snapshot("node-a"), "node-a-v1") &&
			nodeIsProjected(nodes.snapshot("node-b"), "node-b-v1")
	}, 10*time.Second, 20*time.Millisecond)

	require.Eventually(t, func() bool {
		current, err := mokka.MokkaV1alpha1().SGPUInventories().Get(ctx, inventory.Name, metav1.GetOptions{})
		if err != nil {
			return false
		}
		rack := getAcceptanceRack(ctx, t, mokka, rackName)
		secondRack := getAcceptanceRack(ctx, t, mokka, secondRackName)
		programmed := findCondition(current.Status.Conditions, mokkav1alpha1.InventoryConditionProgrammed)
		ready := findCondition(rack.Status.Conditions, mokkav1alpha1.RackConditionReady)
		secondReady := findCondition(secondRack.Status.Conditions, mokkav1alpha1.RackConditionReady)
		return programmed != nil && programmed.Status == metav1.ConditionTrue &&
			programmed.ObservedGeneration == current.Generation &&
			slices.Contains(current.Finalizers, controllerack.InventoryFinalizer) &&
			current.Status.Capacity.Nodes == 2 &&
			current.Status.Usage.AllocatedNodes == 2 &&
			slices.Contains(rack.Finalizers, controllerack.RackFinalizer) &&
			rack.Status.AssignedNodes == 1 && ready != nil && ready.Status == metav1.ConditionTrue &&
			secondRack.Status.AssignedNodes == 1 && secondReady != nil && secondReady.Status == metav1.ConditionTrue
	}, 10*time.Second, 20*time.Millisecond)

	currentInventory, err := mokka.MokkaV1alpha1().SGPUInventories().Get(ctx, inventory.Name, metav1.GetOptions{})
	require.NoError(t, err)
	currentInventory.Spec.RackGroups[0].Count = 1
	currentInventory.Generation++
	_, err = mokka.MokkaV1alpha1().SGPUInventories().Update(ctx, currentInventory, metav1.UpdateOptions{})
	require.NoError(t, err)
	controller.queues.inventories.Add(inventory.Name)
	require.Eventually(t, func() bool {
		return nodeIsProjected(nodes.snapshot("node-a"), "node-a-v1") &&
			!nodeHasProjection(nodes.snapshot("node-b"))
	}, 10*time.Second, 20*time.Millisecond, "capacity shrink must wait for projection cleanup")

	require.Eventually(t, func() bool {
		current, err := mokka.MokkaV1alpha1().SGPUInventories().Get(ctx, inventory.Name, metav1.GetOptions{})
		if err != nil || current.Status.Capacity.Nodes != 1 || current.Status.Usage.AllocatedNodes != 1 {
			return false
		}
		racks, err := mokka.MokkaV1alpha1().SGPURacks().List(ctx, metav1.ListOptions{})
		return err == nil && len(racks.Items) == 1
	}, 10*time.Second, 20*time.Millisecond)

	nodes.delete("node-b")
	nodes.replace("node-a", acceptanceNode("node-a", "node-a-v2", 3))
	require.Eventually(t, func() bool {
		rack := getAcceptanceRack(ctx, t, mokka, rackName)
		node := nodes.snapshot("node-a")
		bindings := 0
		projected := false
		for index := range rack.Spec.Nodes {
			slot := &rack.Spec.Nodes[index]
			if slot.NodeRef != nil && slot.NodeRef.UID == "node-a-v2" {
				bindings++
				projected = projected || controllerprojection.MatchesBinding(node, rack, slot)
			}
		}
		return bindings == 1 && projected
	}, 10*time.Second, 20*time.Millisecond, "same-name replacement must receive a new exact-UID binding")

	deleting, err := mokka.MokkaV1alpha1().SGPUInventories().Get(ctx, inventory.Name, metav1.GetOptions{})
	require.NoError(t, err)
	now := metav1.Now()
	deleting.DeletionTimestamp = &now
	_, err = mokka.MokkaV1alpha1().SGPUInventories().Update(ctx, deleting, metav1.UpdateOptions{})
	require.NoError(t, err)
	controller.queues.inventories.Add(inventory.Name)
	require.Eventually(t, func() bool {
		current, err := mokka.MokkaV1alpha1().SGPUInventories().Get(ctx, inventory.Name, metav1.GetOptions{})
		if err != nil || len(current.Finalizers) != 0 || nodeHasProjection(nodes.snapshot("node-a")) {
			return false
		}
		racks, err := mokka.MokkaV1alpha1().SGPURacks().List(ctx, metav1.ListOptions{})
		return err == nil && len(racks.Items) == 0
	}, 10*time.Second, 20*time.Millisecond, "inventory deletion must clean Nodes and generated racks before releasing its finalizer")
}

//nolint:cyclop // One acceptance flow asserts recovery and every ownership-safety guard around it.
func TestControllerRecoversDesiredRackAfterForeignBlockerDelete(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	nodes := newAcceptanceNodeClient()
	mokka := mokkafake.NewSimpleClientset()
	installAcceptanceAPIReactors(t, mokka)
	controller, err := newForNodes(nodes, mokka, Options{Workers: 2, StatusDebounce: 0})
	require.NoError(t, err)

	runDone := make(chan error, 1)
	go func() { runDone <- controller.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-runDone:
		case <-time.After(5 * time.Second):
			t.Error("controller did not stop")
		}
	})
	require.Eventually(t, controller.Ready, 5*time.Second, 10*time.Millisecond)

	profile := acceptanceProfile(1)
	inventory := acceptanceInventory()
	rackName := materialize.RackName(inventory.Name, inventory.UID, "compute", 0)
	blocker := &mokkav1alpha1.SGPURack{
		TypeMeta: metav1.TypeMeta{APIVersion: mokkav1alpha1.SchemeGroupVersion.String(), Kind: "SGPURack"},
		ObjectMeta: metav1.ObjectMeta{
			Name: rackName, UID: "foreign-blocker-uid", ResourceVersion: "1",
		},
	}
	_, err = mokka.MokkaV1alpha1().SGPURackProfiles().Create(ctx, profile, metav1.CreateOptions{})
	require.NoError(t, err)
	_, err = mokka.MokkaV1alpha1().SGPURacks().Create(ctx, blocker, metav1.CreateOptions{})
	require.NoError(t, err)
	_, err = mokka.MokkaV1alpha1().SGPUInventories().Create(ctx, inventory, metav1.CreateOptions{})
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		current, getErr := mokka.MokkaV1alpha1().SGPUInventories().Get(ctx, inventory.Name, metav1.GetOptions{})
		if getErr != nil {
			return false
		}
		programmed := findCondition(current.Status.Conditions, mokkav1alpha1.InventoryConditionProgrammed)
		return programmed != nil && programmed.Status == metav1.ConditionFalse &&
			programmed.Reason == controllerstatus.ReasonRackOwnershipConflict
	}, 10*time.Second, 20*time.Millisecond)
	retained, err := mokka.MokkaV1alpha1().SGPURacks().Get(ctx, rackName, metav1.GetOptions{})
	require.NoError(t, err)
	require.Equal(t, blocker.UID, retained.UID)
	require.Empty(t, retained.OwnerReferences)
	require.Empty(t, retained.Spec)

	mokka.Fake.ClearActions()
	require.Never(t, func() bool {
		for _, action := range mokka.Actions() {
			if action.GetResource().Resource == "sgpuracks" &&
				slices.Contains([]string{"patch", "update", "delete"}, action.GetVerb()) {
				return true
			}
		}
		return false
	}, 300*time.Millisecond, 10*time.Millisecond, "a retained blocker must not cause a hot retry or adoption")

	require.NoError(t, mokka.MokkaV1alpha1().SGPURacks().Delete(ctx, rackName, metav1.DeleteOptions{}))
	require.Eventually(t, func() bool {
		recovered, getErr := mokka.MokkaV1alpha1().SGPURacks().Get(ctx, rackName, metav1.GetOptions{})
		return getErr == nil && recovered.UID != blocker.UID && rackOwnedByReference(recovered) &&
			recovered.Spec.InventoryRef.Name == inventory.Name && recovered.Spec.InventoryRef.UID == inventory.UID
	}, 10*time.Second, 20*time.Millisecond, "the blocker delete event must route the name's current claimant")
}

//nolint:cyclop // The acceptance flow asserts both convergence and every last-good-state guard.
func TestControllerRejectsProjectedLabelPlacementWithoutOscillation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	nodes := newAcceptanceNodeClient()
	mokka := mokkafake.NewSimpleClientset()
	installAcceptanceAPIReactors(t, mokka)
	controller, err := newForNodes(nodes, mokka, Options{Workers: 2, StatusDebounce: 0})
	require.NoError(t, err)

	runDone := make(chan error, 1)
	go func() { runDone <- controller.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-runDone:
		case <-time.After(5 * time.Second):
			t.Error("controller did not stop")
		}
	})
	require.Eventually(t, controller.Ready, 5*time.Second, 10*time.Millisecond)

	profile := acceptanceProfile(1)
	inventory := acceptanceInventory()
	node := acceptanceNode("node", "node-uid", 1)
	nodes.create(node)
	_, err = mokka.MokkaV1alpha1().SGPURackProfiles().Create(ctx, profile, metav1.CreateOptions{})
	require.NoError(t, err)
	_, err = mokka.MokkaV1alpha1().SGPUInventories().Create(ctx, inventory, metav1.CreateOptions{})
	require.NoError(t, err)

	rackName := materialize.RackName(inventory.Name, inventory.UID, "compute", 0)
	require.Eventually(t, func() bool {
		rack := getAcceptanceRack(ctx, t, mokka, rackName)
		return len(rack.Spec.Nodes) == 1 && rack.Spec.Nodes[0].NodeRef != nil &&
			rack.Spec.Nodes[0].NodeRef.UID == node.UID && nodeIsProjected(nodes.snapshot(node.Name), node.UID)
	}, 10*time.Second, 20*time.Millisecond)
	require.Eventually(t, func() bool {
		before := nodes.patchCalls()
		time.Sleep(50 * time.Millisecond)
		return nodes.patchCalls() == before
	}, 5*time.Second, 20*time.Millisecond, "projection event handling must settle")

	patchesBeforeInvalidEdit := nodes.patchCalls()
	mokka.Fake.ClearActions()
	invalid, err := mokka.MokkaV1alpha1().SGPUInventories().Get(ctx, inventory.Name, metav1.GetOptions{})
	require.NoError(t, err)
	invalid.Spec.RackGroups[0].Placement.NodeSelector = &metav1.LabelSelector{MatchExpressions: []metav1.LabelSelectorRequirement{{
		Key: controllerprojection.AssignedLabel, Operator: metav1.LabelSelectorOpDoesNotExist,
	}}}
	invalid.Generation++
	_, err = mokka.MokkaV1alpha1().SGPUInventories().Update(ctx, invalid, metav1.UpdateOptions{})
	require.NoError(t, err)
	controller.queues.inventories.Add(inventory.Name)

	wantValidationError := `rack group "compute" selector: selector must not reference controller-owned label "` +
		controllerprojection.AssignedLabel + `"`
	require.Eventually(t, func() bool {
		current, err := mokka.MokkaV1alpha1().SGPUInventories().Get(ctx, inventory.Name, metav1.GetOptions{})
		if err != nil {
			return false
		}
		accepted := findCondition(current.Status.Conditions, mokkav1alpha1.InventoryConditionAccepted)
		materialized := findCondition(current.Status.Conditions, mokkav1alpha1.InventoryConditionProgrammed)
		return accepted != nil && accepted.Status == metav1.ConditionFalse && accepted.Message == wantValidationError &&
			materialized != nil && materialized.Status == metav1.ConditionFalse
	}, 10*time.Second, 20*time.Millisecond)

	require.Never(t, func() bool {
		return nodes.patchCalls() != patchesBeforeInvalidEdit
	}, 500*time.Millisecond, 10*time.Millisecond, "an invalid inventory must not clean or reapply the last-good projection")
	retained := getAcceptanceRack(ctx, t, mokka, rackName)
	require.NotNil(t, retained.Spec.Nodes[0].NodeRef)
	require.Equal(t, node.UID, retained.Spec.Nodes[0].NodeRef.UID)
	require.True(t, nodeIsProjected(nodes.snapshot(node.Name), node.UID))
	for _, action := range mokka.Actions() {
		if action.GetResource().Resource == "sgpuracks" {
			require.NotContains(t, []string{"patch", "update", "delete"}, action.GetVerb(),
				"an invalid inventory must not mutate last-good racks")
		}
	}
}

//nolint:cyclop // The gated worker flow must observe each asynchronous lifecycle boundary explicitly.
func TestControllerCancelsQueuedCleanupWhenSelectorRestoresBinding(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	nodes := newAcceptanceNodeClient()
	mokka := mokkafake.NewSimpleClientset()
	installAcceptanceAPIReactors(t, mokka)
	controller, err := newForNodes(nodes, mokka, Options{Workers: 1, StatusDebounce: 0})
	require.NoError(t, err)

	originalProjection := controller.reconcileProjection
	cleanupStarted := make(chan struct{})
	releaseCleanup := make(chan struct{})
	controller.reconcileProjection = func(ctx context.Context, key projectionKey) error {
		if key.mode == projectionCleanup && key.cleanup.Reason == controllerack.CleanupSelectorMismatch {
			close(cleanupStarted)
			select {
			case <-releaseCleanup:
			case <-ctx.Done():
				return context.Cause(ctx)
			}
		}
		return originalProjection(ctx, key)
	}
	originalGroup := controller.reconcileGroup
	restoring := atomic.Bool{}
	restored := make(chan struct{})
	var restoredOnce sync.Once
	controller.reconcileGroup = func(ctx context.Context, key allocate.GroupKey) error {
		err := originalGroup(ctx, key)
		if err == nil && restoring.Load() {
			restoredOnce.Do(func() { close(restored) })
		}
		return err
	}

	runDone := make(chan error, 1)
	go func() { runDone <- controller.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-runDone:
		case <-time.After(5 * time.Second):
			t.Error("controller did not stop")
		}
	})
	require.Eventually(t, controller.Ready, 5*time.Second, 10*time.Millisecond)

	profile := acceptanceProfile(1)
	inventory := acceptanceInventory()
	node := acceptanceNode("node", "node-uid", 1)
	nodes.create(node)
	_, err = mokka.MokkaV1alpha1().SGPURackProfiles().Create(ctx, profile, metav1.CreateOptions{})
	require.NoError(t, err)
	_, err = mokka.MokkaV1alpha1().SGPUInventories().Create(ctx, inventory, metav1.CreateOptions{})
	require.NoError(t, err)

	rackName := materialize.RackName(inventory.Name, inventory.UID, "compute", 0)
	require.Eventually(t, func() bool {
		rack := getAcceptanceRack(ctx, t, mokka, rackName)
		return len(rack.Spec.Nodes) == 1 && rack.Spec.Nodes[0].NodeRef != nil &&
			rack.Spec.Nodes[0].NodeRef.UID == node.UID && nodeIsProjected(nodes.snapshot(node.Name), node.UID)
	}, 10*time.Second, 20*time.Millisecond)
	require.Eventually(t, func() bool {
		before := nodes.patchCalls()
		time.Sleep(50 * time.Millisecond)
		return nodes.patchCalls() == before
	}, 5*time.Second, 20*time.Millisecond)
	patchesBeforeMismatch := nodes.patchCalls()

	nodes.update(node.Name, func(current *corev1.Node) { current.Labels["pool"] = "other" })
	require.Eventually(t, func() bool {
		select {
		case <-cleanupStarted:
			return true
		default:
			return false
		}
	}, 5*time.Second, 10*time.Millisecond)

	restoring.Store(true)
	nodes.update(node.Name, func(current *corev1.Node) { current.Labels["pool"] = "acceptance" })
	require.Eventually(t, func() bool {
		select {
		case <-restored:
			return true
		default:
			return false
		}
	}, 5*time.Second, 10*time.Millisecond)
	close(releaseCleanup)

	require.Never(t, func() bool {
		return nodes.patchCalls() != patchesBeforeMismatch || !nodeIsProjected(nodes.snapshot(node.Name), node.UID)
	}, 500*time.Millisecond, 10*time.Millisecond,
		"cleanup queued for an obsolete selector decision must not strip the restored binding projection")
}

//nolint:cyclop // The barriers deliberately place the allocation change inside one cleanup attempt.
func TestControllerRestoresBindingWhenAllocationChangesDuringCleanup(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	nodes := newAcceptanceNodeClient()
	mokka := mokkafake.NewSimpleClientset()
	installAcceptanceAPIReactors(t, mokka)
	controller, err := newForNodes(nodes, mokka, Options{Workers: 1, StatusDebounce: 0})
	require.NoError(t, err)

	originalGroup := controller.reconcileGroup
	restoring := atomic.Bool{}
	restoreStarted := make(chan struct{})
	var restoreOnce sync.Once
	controller.reconcileGroup = func(ctx context.Context, key allocate.GroupKey) error {
		if restoring.Load() {
			restoreOnce.Do(func() { close(restoreStarted) })
		}
		return originalGroup(ctx, key)
	}

	runDone := make(chan error, 1)
	go func() { runDone <- controller.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-runDone:
		case <-time.After(5 * time.Second):
			t.Error("controller did not stop")
		}
	})
	require.Eventually(t, controller.Ready, 5*time.Second, 10*time.Millisecond)

	profile := acceptanceProfile(1)
	inventory := acceptanceInventory()
	node := acceptanceNode("node", "node-uid", 1)
	nodes.create(node)
	_, err = mokka.MokkaV1alpha1().SGPURackProfiles().Create(ctx, profile, metav1.CreateOptions{})
	require.NoError(t, err)
	_, err = mokka.MokkaV1alpha1().SGPUInventories().Create(ctx, inventory, metav1.CreateOptions{})
	require.NoError(t, err)

	rackName := materialize.RackName(inventory.Name, inventory.UID, "compute", 0)
	require.Eventually(t, func() bool {
		rack := getAcceptanceRack(ctx, t, mokka, rackName)
		return len(rack.Spec.Nodes) == 1 && rack.Spec.Nodes[0].NodeRef != nil &&
			rack.Spec.Nodes[0].NodeRef.UID == node.UID && nodeIsProjected(nodes.snapshot(node.Name), node.UID)
	}, 10*time.Second, 20*time.Millisecond)
	require.Eventually(t, func() bool {
		before := nodes.patchCalls()
		time.Sleep(50 * time.Millisecond)
		return nodes.patchCalls() == before
	}, 5*time.Second, 20*time.Millisecond)
	patchesBeforeMismatch := nodes.patchCalls()

	cleanupPatchStarted, releaseCleanupPatch := nodes.blockNextPatch()
	t.Cleanup(releaseCleanupPatch)
	nodes.update(node.Name, func(current *corev1.Node) { current.Labels["pool"] = "other" })
	require.Eventually(t, func() bool {
		select {
		case <-cleanupPatchStarted:
			return true
		default:
			return false
		}
	}, 5*time.Second, 10*time.Millisecond)

	restoring.Store(true)
	nodes.update(node.Name, func(current *corev1.Node) { current.Labels["pool"] = "acceptance" })
	require.Eventually(t, func() bool {
		select {
		case <-restoreStarted:
			return true
		default:
			return false
		}
	}, 5*time.Second, 10*time.Millisecond,
		"the restored allocation revision must be visible before cleanup completes")
	releaseCleanupPatch()

	require.Eventually(t, func() bool {
		return nodes.patchCalls() >= patchesBeforeMismatch+2 &&
			nodeIsProjected(nodes.snapshot(node.Name), node.UID)
	}, 10*time.Second, 20*time.Millisecond,
		"stale cleanup must be followed by a fresh projection that clears its acknowledgement")
}

//nolint:cyclop // The barriers place restoration after acknowledgement but before its follow-up reconcile.
func TestControllerRestoresBindingWhenAllocationChangesAfterCleanup(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	nodes := newAcceptanceNodeClient()
	mokka := mokkafake.NewSimpleClientset()
	installAcceptanceAPIReactors(t, mokka)
	controller, err := newForNodes(nodes, mokka, Options{Workers: 1, StatusDebounce: 0})
	require.NoError(t, err)

	originalGroup := controller.reconcileGroup
	mismatchStarted := atomic.Bool{}
	var mismatchGroups atomic.Int32
	followupStarted := make(chan struct{})
	releaseFollowup := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseFollowup) }) }
	t.Cleanup(release)
	controller.reconcileGroup = func(ctx context.Context, key allocate.GroupKey) error {
		if mismatchStarted.Load() && mismatchGroups.Add(1) == 2 {
			close(followupStarted)
			select {
			case <-releaseFollowup:
			case <-ctx.Done():
				return context.Cause(ctx)
			}
		}
		return originalGroup(ctx, key)
	}
	originalProjection := controller.reconcileProjection
	restoring := atomic.Bool{}
	restoreObserved := make(chan struct{})
	var restoreOnce sync.Once
	controller.reconcileProjection = func(ctx context.Context, key projectionKey) error {
		if restoring.Load() && key.mode == projectionApply {
			restoreOnce.Do(func() { close(restoreObserved) })
		}
		return originalProjection(ctx, key)
	}

	runDone := make(chan error, 1)
	go func() { runDone <- controller.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-runDone:
		case <-time.After(5 * time.Second):
			t.Error("controller did not stop")
		}
	})
	require.Eventually(t, controller.Ready, 5*time.Second, 10*time.Millisecond)

	profile := acceptanceProfile(1)
	inventory := acceptanceInventory()
	node := acceptanceNode("node", "node-uid", 1)
	nodes.create(node)
	_, err = mokka.MokkaV1alpha1().SGPURackProfiles().Create(ctx, profile, metav1.CreateOptions{})
	require.NoError(t, err)
	_, err = mokka.MokkaV1alpha1().SGPUInventories().Create(ctx, inventory, metav1.CreateOptions{})
	require.NoError(t, err)

	rackName := materialize.RackName(inventory.Name, inventory.UID, "compute", 0)
	require.Eventually(t, func() bool {
		rack := getAcceptanceRack(ctx, t, mokka, rackName)
		return len(rack.Spec.Nodes) == 1 && rack.Spec.Nodes[0].NodeRef != nil &&
			rack.Spec.Nodes[0].NodeRef.UID == node.UID && nodeIsProjected(nodes.snapshot(node.Name), node.UID)
	}, 10*time.Second, 20*time.Millisecond)
	require.Eventually(t, func() bool {
		before := nodes.patchCalls()
		time.Sleep(50 * time.Millisecond)
		return nodes.patchCalls() == before
	}, 5*time.Second, 20*time.Millisecond)
	patchesBeforeMismatch := nodes.patchCalls()

	mismatchStarted.Store(true)
	nodes.update(node.Name, func(current *corev1.Node) { current.Labels["pool"] = "other" })
	require.Eventually(t, func() bool {
		select {
		case <-followupStarted:
			return !nodeHasProjection(nodes.snapshot(node.Name))
		default:
			return false
		}
	}, 5*time.Second, 10*time.Millisecond,
		"cleanup must be acknowledged before its follow-up group reconcile starts")
	require.Eventually(t, func() bool {
		before := nodes.patchCalls()
		time.Sleep(50 * time.Millisecond)
		return nodes.patchCalls() == before && controller.queues.projections.Len() == 0
	}, 5*time.Second, 20*time.Millisecond)

	restoring.Store(true)
	nodes.update(node.Name, func(current *corev1.Node) { current.Labels["pool"] = "acceptance" })
	require.Eventually(t, func() bool {
		select {
		case <-restoreObserved:
			return true
		default:
			return false
		}
	}, 5*time.Second, 10*time.Millisecond,
		"the restored allocation input must be observed while the follow-up reconcile is blocked")
	require.False(t, nodeHasProjection(nodes.snapshot(node.Name)),
		"an ordinary apply demonstrates that the old acknowledgement still suppresses repair")
	release()

	require.Eventually(t, func() bool {
		return nodes.patchCalls() >= patchesBeforeMismatch+2 &&
			nodeIsProjected(nodes.snapshot(node.Name), node.UID)
	}, 10*time.Second, 20*time.Millisecond,
		"a retained binding must revoke its obsolete cleanup acknowledgement and project again")
}

func TestControllerBoundsCleanupWhenForeignCoOwnerPreservesField(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	nodes := newAcceptanceNodeClient()
	mokka := mokkafake.NewSimpleClientset()
	installAcceptanceAPIReactors(t, mokka)
	controller, err := newForNodes(nodes, mokka, Options{Workers: 2, StatusDebounce: 0})
	require.NoError(t, err)

	runDone := make(chan error, 1)
	go func() { runDone <- controller.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-runDone:
		case <-time.After(5 * time.Second):
			t.Error("controller did not stop")
		}
	})
	require.Eventually(t, controller.Ready, 5*time.Second, 10*time.Millisecond)

	profile := acceptanceProfile(1)
	inventory := acceptanceInventory()
	node := acceptanceNode("node", "node-uid", 1)
	nodes.create(node)
	_, err = mokka.MokkaV1alpha1().SGPURackProfiles().Create(ctx, profile, metav1.CreateOptions{})
	require.NoError(t, err)
	_, err = mokka.MokkaV1alpha1().SGPUInventories().Create(ctx, inventory, metav1.CreateOptions{})
	require.NoError(t, err)

	rackName := materialize.RackName(inventory.Name, inventory.UID, "compute", 0)
	require.Eventually(t, func() bool {
		rack := getAcceptanceRack(ctx, t, mokka, rackName)
		return len(rack.Spec.Nodes) == 1 && rack.Spec.Nodes[0].NodeRef != nil &&
			rack.Spec.Nodes[0].NodeRef.UID == node.UID && nodeIsProjected(nodes.snapshot(node.Name), node.UID)
	}, 10*time.Second, 20*time.Millisecond)
	require.Eventually(t, func() bool {
		before := nodes.patchCalls()
		time.Sleep(50 * time.Millisecond)
		return nodes.patchCalls() == before
	}, 5*time.Second, 20*time.Millisecond)

	projected := nodes.snapshot(node.Name)
	nodes.coOwn(node.Name, nil, []string{controllerprojection.AssignmentAnnotation})
	patchesBeforeCleanup := nodes.patchCalls()
	deleting, err := mokka.MokkaV1alpha1().SGPUInventories().Get(ctx, inventory.Name, metav1.GetOptions{})
	require.NoError(t, err)
	now := metav1.Now()
	deleting.DeletionTimestamp = &now
	_, err = mokka.MokkaV1alpha1().SGPUInventories().Update(ctx, deleting, metav1.UpdateOptions{})
	require.NoError(t, err)
	controller.queues.inventories.Add(inventory.Name)

	require.Eventually(t, func() bool {
		current := nodes.snapshot(node.Name)
		return nodes.patchCalls() == patchesBeforeCleanup+1 &&
			current.Labels[controllerprojection.AssignedLabel] == "" &&
			current.Labels[controllerprojection.CliqueLabel] == "" &&
			current.Annotations[controllerprojection.AssignmentAnnotation] ==
				projected.Annotations[controllerprojection.AssignmentAnnotation]
	}, 10*time.Second, 20*time.Millisecond, "cleanup must relinquish sole-owned fields once")

	require.Never(t, func() bool {
		return nodes.patchCalls() != patchesBeforeCleanup+1
	}, 500*time.Millisecond, 10*time.Millisecond, "foreign-only retained metadata must not cause repeated cleanup applies")
	retained := getAcceptanceRack(ctx, t, mokka, rackName)
	require.NotNil(t, retained.Spec.Nodes[0].NodeRef)
	require.Equal(t, node.UID, retained.Spec.Nodes[0].NodeRef.UID)
	require.Contains(t, retained.Finalizers, controllerack.RackFinalizer)
}

func TestControllerReplacementConvergesWhileRestartQueuesInitialize(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	const nodeCount = 50
	profile := acceptanceProfile(nodeCount)
	inventory := acceptanceInventory()
	inventory.Finalizers = []string{controllerack.InventoryFinalizer}
	rendered, err := materialize.RenderRack(materialize.RackInput{
		InventoryName: inventory.Name,
		InventoryUID:  inventory.UID,
		Group:         inventory.Spec.RackGroups[0],
		Profile:       profile,
	})
	require.NoError(t, err)

	controllerRef := true
	rack := &mokkav1alpha1.SGPURack{
		ObjectMeta: metav1.ObjectMeta{
			Name: rendered.Name, UID: "rack-uid", ResourceVersion: "1",
			Finalizers: []string{controllerack.RackFinalizer},
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: mokkav1alpha1.SchemeGroupVersion.String(), Kind: "SGPUInventory",
				Name: inventory.Name, UID: inventory.UID, Controller: &controllerRef,
			}},
		},
		Spec: rendered.Spec,
	}
	nodes := newAcceptanceNodeClient()
	var oldNode *corev1.Node
	for index := range nodeCount {
		node := acceptanceNode(
			fmt.Sprintf("node-%06d", index),
			types.UID(fmt.Sprintf("old-uid-%06d", index)),
			int64(index+1),
		)
		rack.Spec.Nodes[index].NodeRef = &mokkav1alpha1.SGPUNodeReference{Name: node.Name, UID: node.UID}
		assignment, encodeErr := controllerprojection.EncodeAssignment(rack, &rack.Spec.Nodes[index])
		require.NoError(t, encodeErr)
		node.Labels[controllerprojection.AssignedLabel] = "true"
		node.Labels[controllerprojection.CliqueLabel] = rack.Spec.Identity.FabricUUID + ".0"
		node.Annotations = map[string]string{controllerprojection.AssignmentAnnotation: assignment}
		nodes.create(node)
		if index == 0 {
			oldNode = node
		}
	}
	setAcceptanceRackManagedFields(rack)
	mokka := mokkafake.NewSimpleClientset(profile, inventory, rack)
	installAcceptanceAPIReactors(t, mokka)
	controller, err := newForNodes(nodes, mokka, Options{Workers: 16, StatusDebounce: 0})
	require.NoError(t, err)

	runDone := make(chan error, 1)
	go func() { runDone <- controller.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-runDone:
		case <-time.After(5 * time.Second):
			t.Error("controller did not stop")
		}
	})
	require.Eventually(t, controller.Ready, 5*time.Second, 10*time.Millisecond)

	replacement := acceptanceNode(oldNode.Name, "replacement-uid", 2)
	nodes.replace(oldNode.Name, replacement)
	require.Eventually(t, func() bool {
		stored, getErr := mokka.MokkaV1alpha1().SGPURacks().Get(ctx, rack.Name, metav1.GetOptions{})
		if getErr != nil || stored.Spec.Nodes[0].NodeRef == nil || stored.Spec.Nodes[0].NodeRef.UID != replacement.UID {
			return false
		}
		return nodeIsProjected(nodes.snapshot(replacement.Name), replacement.UID)
	}, 10*time.Second, 20*time.Millisecond, "same-name replacement must converge while restart work is still queued")
}

func TestRestartCleanupGatesReleasedAndRetiredBindings(t *testing.T) {
	tests := []struct {
		name        string
		retireRack  bool
		liveNode    func(*corev1.Node) *corev1.Node
		cachedNodes func(*corev1.Node) []*corev1.Node
		wantPatch   bool
	}{
		{
			name: "live ineligible release",
			liveNode: func(old *corev1.Node) *corev1.Node {
				delete(old.Labels, allocate.EligibleNodeLabel)
				return old
			},
			wantPatch: true,
		},
		{
			name:       "live ineligible retirement",
			retireRack: true,
			liveNode: func(old *corev1.Node) *corev1.Node {
				delete(old.Labels, allocate.EligibleNodeLabel)
				return old
			},
			wantPatch: true,
		},
		{
			name:     "deleted Node",
			liveNode: func(*corev1.Node) *corev1.Node { return nil },
		},
		{
			name: "same-name replacement",
			liveNode: func(*corev1.Node) *corev1.Node {
				return acceptanceNode("node", "replacement-uid", 2)
			},
			cachedNodes: func(live *corev1.Node) []*corev1.Node { return []*corev1.Node{live} },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			profile := acceptanceProfile(1)
			inventory := acceptanceInventory()
			inventory.Finalizers = []string{controllerack.InventoryFinalizer}
			rendered, err := materialize.RenderRack(materialize.RackInput{
				InventoryName: inventory.Name,
				InventoryUID:  inventory.UID,
				Group:         inventory.Spec.RackGroups[0],
				Profile:       profile,
			})
			require.NoError(t, err)

			oldNode := acceptanceNode("node", "old-uid", 1)
			controller := true
			rack := &mokkav1alpha1.SGPURack{
				ObjectMeta: metav1.ObjectMeta{
					Name: rendered.Name, UID: "rack-uid", ResourceVersion: "1",
					Finalizers: []string{controllerack.RackFinalizer},
					OwnerReferences: []metav1.OwnerReference{{
						APIVersion: mokkav1alpha1.SchemeGroupVersion.String(), Kind: "SGPUInventory",
						Name: inventory.Name, UID: inventory.UID, Controller: &controller,
					}},
				},
				Spec: rendered.Spec,
			}
			rack.Spec.Nodes[0].NodeRef = &mokkav1alpha1.SGPUNodeReference{Name: oldNode.Name, UID: oldNode.UID}
			setAcceptanceRackManagedFields(rack)
			assignment, err := controllerprojection.EncodeAssignment(rack, &rack.Spec.Nodes[0])
			require.NoError(t, err)
			oldNode.Labels[controllerprojection.AssignedLabel] = "true"
			oldNode.Labels[controllerprojection.CliqueLabel] = rack.Spec.Identity.FabricUUID + ".0"
			oldNode.Annotations = map[string]string{controllerprojection.AssignmentAnnotation: assignment}

			liveNode := tt.liveNode(oldNode.DeepCopy())
			liveNodes := newAcceptanceNodeClient()
			if liveNode != nil {
				liveNodes.create(liveNode)
			}
			var cachedNodes []*corev1.Node
			if tt.cachedNodes != nil {
				cachedNodes = tt.cachedNodes(liveNode)
			}
			if tt.retireRack {
				now := metav1.Now()
				rack.DeletionTimestamp = &now
			}

			mokka := mokkafake.NewSimpleClientset(profile, inventory, rack)
			inventoryIndexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, controllerack.InventoryIndexers())
			profileIndexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
			rackIndexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, controllerack.Indexers())
			nodeCatalog := controllernodes.New()
			require.NoError(t, inventoryIndexer.Add(inventory.DeepCopy()))
			require.NoError(t, profileIndexer.Add(profile.DeepCopy()))
			require.NoError(t, rackIndexer.Add(rack.DeepCopy()))
			for _, node := range cachedNodes {
				nodeCatalog.Upsert(node.DeepCopy())
			}
			snapshot := newInformerCache(
				mokkalisters.NewSGPUInventoryLister(inventoryIndexer),
				mokkalisters.NewSGPURackProfileLister(profileIndexer),
				rackIndexer,
				nodeCatalog,
				liveNodes,
				DefaultOptions(),
			)
			projection := controllerprojection.NewController(snapshot, liveNodes)
			reconciler := controllerack.NewReconciler(
				snapshot,
				mokka.MokkaV1alpha1().SGPUInventories(),
				mokka.MokkaV1alpha1().SGPURacks(),
				projection,
			)

			result, err := reconciler.Reconcile(ctx, inventory.Name)
			require.NoError(t, err)
			require.Len(t, result.CleanupNeeded, 1)
			stored, err := mokka.MokkaV1alpha1().SGPURacks().Get(ctx, rack.Name, metav1.GetOptions{})
			require.NoError(t, err, "rack mutation must wait for exact projection cleanup")
			require.Equal(t, oldNode.UID, stored.Spec.Nodes[0].NodeRef.UID)

			_, err = projection.Cleanup(ctx, result.CleanupNeeded[0])
			require.NoError(t, err)
			require.True(t, projection.Ready(result.CleanupNeeded[0]))
			if tt.wantPatch {
				require.False(t, nodeHasProjection(liveNodes.snapshot(oldNode.Name)))
			}

			_, err = reconciler.Reconcile(ctx, inventory.Name)
			require.NoError(t, err)
			require.True(t, projection.Ready(result.CleanupNeeded[0]), "the acknowledgement must cover stale cache reconciles")
			stored, err = mokka.MokkaV1alpha1().SGPURacks().Get(ctx, rack.Name, metav1.GetOptions{})
			if tt.retireRack {
				require.True(t, apierrors.IsNotFound(err))
				return
			}
			require.NoError(t, err)
			require.Nil(t, stored.Spec.Nodes[0].NodeRef)

			if len(cachedNodes) == 0 {
				return
			}
			require.NoError(t, rackIndexer.Update(stored.DeepCopy()))
			_, err = reconciler.Reconcile(ctx, inventory.Name)
			require.NoError(t, err)
			stored, err = mokka.MokkaV1alpha1().SGPURacks().Get(ctx, rack.Name, metav1.GetOptions{})
			require.NoError(t, err)
			require.Equal(t, types.UID("replacement-uid"), stored.Spec.Nodes[0].NodeRef.UID)
		})
	}
}

func installAcceptanceAPIReactors(t *testing.T, client *mokkafake.Clientset) {
	t.Helper()
	var nextRackUID atomic.Int64
	installAcceptanceRackCreateReactor(t, client, &nextRackUID)
	client.PrependReactor("update", "sgpuracks", func(action k8stesting.Action) (bool, runtime.Object, error) {
		if action.GetSubresource() != "" {
			return false, nil, nil
		}
		update := action.(k8stesting.UpdateActionImpl)
		require.Equal(t, controllerack.RackFieldManager, update.GetUpdateOptions().FieldManager)
		desired := update.GetObject().(*mokkav1alpha1.SGPURack).DeepCopy()
		resource := mokkav1alpha1.SchemeGroupVersion.WithResource("sgpuracks")
		stored, err := client.Tracker().Get(resource, "", desired.Name)
		if err != nil {
			return true, nil, err
		}
		current := stored.(*mokkav1alpha1.SGPURack)
		if desired.ResourceVersion != current.ResourceVersion {
			return true, nil, apierrors.NewConflict(
				mokkav1alpha1.Resource("sgpuracks"), desired.Name, errors.New("rack resource version changed"),
			)
		}
		desired.Status = current.Status
		desired.ResourceVersion += "a"
		setAcceptanceRackManagedFields(desired)
		err = client.Tracker().Update(resource, desired, "")
		return true, desired, err
	})
	client.PrependReactor("update", "sgpuracks", func(action k8stesting.Action) (bool, runtime.Object, error) {
		if action.GetSubresource() != "status" {
			return false, nil, nil
		}
		candidate := action.(k8stesting.UpdateAction).GetObject().(*mokkav1alpha1.SGPURack)
		resource := mokkav1alpha1.SchemeGroupVersion.WithResource("sgpuracks")
		stored, err := client.Tracker().Get(resource, "", candidate.Name)
		if err != nil {
			return true, nil, err
		}
		updated := stored.(*mokkav1alpha1.SGPURack).DeepCopy()
		updated.Status = candidate.Status
		err = client.Tracker().Update(resource, updated, "")
		return true, updated, err
	})
	client.PrependReactor("update", "sgpuinventories", func(action k8stesting.Action) (bool, runtime.Object, error) {
		if action.GetSubresource() != "status" {
			return false, nil, nil
		}
		candidate := action.(k8stesting.UpdateAction).GetObject().(*mokkav1alpha1.SGPUInventory)
		resource := mokkav1alpha1.SchemeGroupVersion.WithResource("sgpuinventories")
		stored, err := client.Tracker().Get(resource, "", candidate.Name)
		if err != nil {
			return true, nil, err
		}
		updated := stored.(*mokkav1alpha1.SGPUInventory).DeepCopy()
		updated.Status = candidate.Status
		err = client.Tracker().Update(resource, updated, "")
		return true, updated, err
	})
}

func installAcceptanceRackCreateReactor(
	t *testing.T,
	client *mokkafake.Clientset,
	nextRackUID *atomic.Int64,
) {
	t.Helper()
	client.PrependReactor("create", "sgpuracks", func(action k8stesting.Action) (bool, runtime.Object, error) {
		create := action.(k8stesting.CreateActionImpl)
		if create.GetCreateOptions().FieldManager != controllerack.RackFieldManager {
			return false, nil, nil
		}
		desired := create.GetObject().(*mokkav1alpha1.SGPURack).DeepCopy()
		resource := mokkav1alpha1.SchemeGroupVersion.WithResource("sgpuracks")
		if _, err := client.Tracker().Get(resource, "", desired.Name); err == nil {
			return true, nil, apierrors.NewAlreadyExists(mokkav1alpha1.Resource("sgpuracks"), desired.Name)
		} else if !apierrors.IsNotFound(err) {
			return true, nil, err
		}
		desired.UID = types.UID(fmt.Sprintf("uid-%s-%d", desired.Name, nextRackUID.Add(1)))
		desired.ResourceVersion = "1"
		setAcceptanceRackManagedFields(desired)
		err := client.Tracker().Create(resource, desired, "")
		return true, desired, err
	})
}

func setAcceptanceRackManagedFields(rack *mokkav1alpha1.SGPURack) {
	rack.ManagedFields = []metav1.ManagedFieldsEntry{{
		Manager: controllerack.RackFieldManager, Operation: metav1.ManagedFieldsOperationUpdate,
		APIVersion: mokkav1alpha1.SchemeGroupVersion.String(), FieldsType: "FieldsV1",
		FieldsV1: metav1.NewFieldsV1(`{"f:spec":{}}`),
	}}
}

func acceptanceProfile(nodesPerRack int32) *mokkav1alpha1.SGPURackProfile {
	return &mokkav1alpha1.SGPURackProfile{
		TypeMeta: metav1.TypeMeta{APIVersion: mokkav1alpha1.SchemeGroupVersion.String(), Kind: "SGPURackProfile"},
		ObjectMeta: metav1.ObjectMeta{
			Name: "acceptance-profile", UID: "acceptance-profile-uid", Generation: 1, ResourceVersion: "1",
		},
		Spec: mokkav1alpha1.SGPURackProfileSpec{
			Rack: mokkav1alpha1.SGPURackShape{NodesPerRack: nodesPerRack},
			Node: mokkav1alpha1.SGPUNode{
				GPUs: mokkav1alpha1.SGPUGPUs{Count: 1},
				Topology: &mokkav1alpha1.SGPUTopology{
					GPUSlots: []mokkav1alpha1.GPUSlot{{Index: 0, PCIAddress: "0000:01:00.0", RootComplex: "pci0000:00"}},
					GPUFabric: &mokkav1alpha1.GPUFabric{
						Type: "NVLink", Generation: 5, LinksPerGPU: 18, BandwidthPerLinkMBps: 50000,
						Domain: &mokkav1alpha1.FabricDomain{Scope: "Rack", GPUCount: nodesPerRack},
					},
				},
			},
			Software: &mokkav1alpha1.SGPUSoftware{DriverVersion: "580.1", NVMLVersion: "13", CUDAVersion: "13.1"},
		},
	}
}

func acceptanceInventory() *mokkav1alpha1.SGPUInventory {
	return &mokkav1alpha1.SGPUInventory{
		TypeMeta: metav1.TypeMeta{APIVersion: mokkav1alpha1.SchemeGroupVersion.String(), Kind: "SGPUInventory"},
		ObjectMeta: metav1.ObjectMeta{
			Name: "acceptance", UID: "acceptance-inventory-uid", Generation: 1, ResourceVersion: "1",
		},
		Spec: mokkav1alpha1.SGPUInventorySpec{RackGroups: []mokkav1alpha1.RackGroup{{
			ID: "compute", Count: 1,
			ProfileRef: mokkav1alpha1.ProfileReference{Name: "acceptance-profile"},
			Placement: &mokkav1alpha1.RackPlacement{NodeSelector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"pool": "acceptance"},
			}},
		}}},
	}
}

func acceptanceNode(name string, uid types.UID, created int64) *corev1.Node {
	return &corev1.Node{ObjectMeta: metav1.ObjectMeta{
		Name: name, UID: uid, ResourceVersion: "1",
		CreationTimestamp: metav1.NewTime(time.Unix(created, 0)),
		Labels:            map[string]string{allocate.EligibleNodeLabel: "true", "pool": "acceptance"},
	}}
}

func getAcceptanceRack(ctx context.Context, t *testing.T, client *mokkafake.Clientset, name string) *mokkav1alpha1.SGPURack {
	t.Helper()
	rack, err := client.MokkaV1alpha1().SGPURacks().Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return &mokkav1alpha1.SGPURack{}
	}
	require.NoError(t, err)
	return rack
}

func nodeHasProjection(node *corev1.Node) bool {
	return node != nil && (node.Labels[controllerprojection.AssignedLabel] != "" ||
		node.Labels[controllerprojection.CliqueLabel] != "" ||
		node.Annotations[controllerprojection.AssignmentAnnotation] != "")
}

func nodeIsProjected(node *corev1.Node, uid types.UID) bool {
	if node == nil || node.Labels[controllerprojection.AssignedLabel] != "true" ||
		node.Annotations[controllerprojection.AssignmentAnnotation] == "" {
		return false
	}
	assignment, err := controllerprojection.DecodeAssignment(node.Annotations[controllerprojection.AssignmentAnnotation])
	return err == nil && assignment.NodeUID == uid
}

type acceptanceNodeClient struct {
	corev1client.NodeInterface
	mu                 sync.Mutex
	nodes              map[string]*corev1.Node
	ownedLabels        map[string]map[string]struct{}
	ownedAnnotations   map[string]map[string]struct{}
	foreignLabels      map[string]map[string]struct{}
	foreignAnnotations map[string]map[string]struct{}
	watcher            *watch.RaceFreeFakeWatcher
	nextRV             int64
	patches            int
	nextPatchBarrier   *acceptancePatchBarrier
}

type acceptancePatchBarrier struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func newAcceptanceNodeClient() *acceptanceNodeClient {
	return &acceptanceNodeClient{
		nodes:              make(map[string]*corev1.Node),
		ownedLabels:        make(map[string]map[string]struct{}),
		ownedAnnotations:   make(map[string]map[string]struct{}),
		foreignLabels:      make(map[string]map[string]struct{}),
		foreignAnnotations: make(map[string]map[string]struct{}),
		nextRV:             1,
	}
}

func (c *acceptanceNodeClient) List(context.Context, metav1.ListOptions) (*corev1.NodeList, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	list := &corev1.NodeList{}
	for _, node := range c.nodes {
		if node.Labels[allocate.EligibleNodeLabel] == "true" {
			list.Items = append(list.Items, *node.DeepCopy())
		}
	}
	return list, nil
}

func (c *acceptanceNodeClient) Watch(_ context.Context, options metav1.ListOptions) (watch.Interface, error) {
	c.mu.Lock()
	if c.watcher == nil {
		c.watcher = watch.NewRaceFreeFake()
	}
	watcher := c.watcher
	initial := make([]*corev1.Node, 0, len(c.nodes))
	if options.SendInitialEvents != nil && *options.SendInitialEvents {
		for _, node := range c.nodes {
			if node.Labels[allocate.EligibleNodeLabel] == "true" {
				initial = append(initial, node.DeepCopy())
			}
		}
	}
	c.mu.Unlock()
	if options.SendInitialEvents != nil && *options.SendInitialEvents {
		go func() {
			for _, node := range initial {
				watcher.Add(node)
			}
			watcher.Action(watch.Bookmark, &corev1.Node{ObjectMeta: metav1.ObjectMeta{
				ResourceVersion: "1", Annotations: map[string]string{metav1.InitialEventsAnnotationKey: "true"},
			}})
		}()
	}
	return watcher, nil
}

func (c *acceptanceNodeClient) Get(_ context.Context, name string, _ metav1.GetOptions) (*corev1.Node, error) {
	node := c.snapshot(name)
	if node == nil {
		return nil, apierrors.NewNotFound(corev1.Resource("nodes"), name)
	}
	return node, nil
}

func (c *acceptanceNodeClient) Patch(
	_ context.Context,
	name string,
	patchType types.PatchType,
	data []byte,
	_ metav1.PatchOptions,
	_ ...string,
) (*corev1.Node, error) {
	if patchType != types.ApplyPatchType {
		return nil, fmt.Errorf("unexpected patch type %q", patchType)
	}
	var payload struct {
		Metadata struct {
			UID         types.UID          `json:"uid"`
			Labels      map[string]*string `json:"labels"`
			Annotations map[string]*string `json:"annotations"`
		} `json:"metadata"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, err
	}
	c.mu.Lock()
	barrier := c.nextPatchBarrier
	c.nextPatchBarrier = nil
	c.mu.Unlock()
	if barrier != nil {
		close(barrier.started)
		<-barrier.release
	}
	c.mu.Lock()
	node := c.nodes[name]
	if node == nil {
		c.mu.Unlock()
		return nil, apierrors.NewNotFound(corev1.Resource("nodes"), name)
	}
	if payload.Metadata.UID != node.UID {
		c.mu.Unlock()
		return nil, apierrors.NewConflict(corev1.Resource("nodes"), name, fmt.Errorf(
			"Node UID changed from %q to %q", payload.Metadata.UID, node.UID,
		))
	}
	updated := node.DeepCopy()
	updated.Labels, c.ownedLabels[name] = applyStringMap(
		updated.Labels, payload.Metadata.Labels, c.ownedLabels[name], c.foreignLabels[name],
	)
	updated.Annotations, c.ownedAnnotations[name] = applyStringMap(
		updated.Annotations, payload.Metadata.Annotations, c.ownedAnnotations[name], c.foreignAnnotations[name],
	)
	setAcceptanceManagedFields(updated, c.ownedLabels[name], c.ownedAnnotations[name])
	c.nextRV++
	c.patches++
	updated.ResourceVersion = strconv.FormatInt(c.nextRV, 10)
	c.nodes[name] = updated
	watcher := c.watcher
	c.mu.Unlock()
	if watcher != nil {
		watcher.Modify(updated.DeepCopy())
	}
	return updated.DeepCopy(), nil
}

func (c *acceptanceNodeClient) create(node *corev1.Node) {
	c.mu.Lock()
	c.nodes[node.Name] = node.DeepCopy()
	delete(c.ownedLabels, node.Name)
	delete(c.ownedAnnotations, node.Name)
	delete(c.foreignLabels, node.Name)
	delete(c.foreignAnnotations, node.Name)
	if node.Labels[controllerprojection.AssignedLabel] == "true" {
		c.ownedLabels[node.Name] = map[string]struct{}{controllerprojection.AssignedLabel: {}}
		if node.Labels[controllerprojection.CliqueLabel] != "" {
			c.ownedLabels[node.Name][controllerprojection.CliqueLabel] = struct{}{}
		}
	}
	if node.Annotations[controllerprojection.AssignmentAnnotation] != "" {
		c.ownedAnnotations[node.Name] = map[string]struct{}{controllerprojection.AssignmentAnnotation: {}}
	}
	setAcceptanceManagedFields(c.nodes[node.Name], c.ownedLabels[node.Name], c.ownedAnnotations[node.Name])
	watcher := c.watcher
	c.mu.Unlock()
	if watcher != nil && node.Labels[allocate.EligibleNodeLabel] == "true" {
		watcher.Add(node.DeepCopy())
	}
}

func (c *acceptanceNodeClient) replace(name string, replacement *corev1.Node) {
	c.mu.Lock()
	old := c.nodes[name]
	delete(c.nodes, name)
	watcher := c.watcher
	c.mu.Unlock()
	if watcher != nil && old != nil && old.Labels[allocate.EligibleNodeLabel] == "true" {
		watcher.Delete(old.DeepCopy())
	}
	c.create(replacement)
}

func (c *acceptanceNodeClient) update(name string, mutate func(*corev1.Node)) {
	c.mu.Lock()
	node := c.nodes[name]
	if node == nil {
		c.mu.Unlock()
		return
	}
	updated := node.DeepCopy()
	mutate(updated)
	c.nextRV++
	updated.ResourceVersion = strconv.FormatInt(c.nextRV, 10)
	c.nodes[name] = updated
	watcher := c.watcher
	c.mu.Unlock()
	if watcher != nil {
		watcher.Modify(updated.DeepCopy())
	}
}

func (c *acceptanceNodeClient) delete(name string) {
	c.mu.Lock()
	old := c.nodes[name]
	delete(c.nodes, name)
	delete(c.ownedLabels, name)
	delete(c.ownedAnnotations, name)
	delete(c.foreignLabels, name)
	delete(c.foreignAnnotations, name)
	watcher := c.watcher
	c.mu.Unlock()
	if watcher != nil && old != nil && old.Labels[allocate.EligibleNodeLabel] == "true" {
		watcher.Delete(old.DeepCopy())
	}
}

func (c *acceptanceNodeClient) snapshot(name string) *corev1.Node {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.nodes[name] == nil {
		return nil
	}
	return c.nodes[name].DeepCopy()
}

func (c *acceptanceNodeClient) patchCalls() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.patches
}

func (c *acceptanceNodeClient) blockNextPatch() (<-chan struct{}, func()) {
	c.mu.Lock()
	defer c.mu.Unlock()
	barrier := &acceptancePatchBarrier{started: make(chan struct{}), release: make(chan struct{})}
	c.nextPatchBarrier = barrier
	return barrier.started, func() { barrier.once.Do(func() { close(barrier.release) }) }
}

func (c *acceptanceNodeClient) coOwn(name string, labelKeys, annotationKeys []string) {
	c.mu.Lock()
	node := c.nodes[name]
	if node == nil {
		c.mu.Unlock()
		return
	}
	updated := node.DeepCopy()
	c.foreignLabels[name] = keySet(labelKeys)
	c.foreignAnnotations[name] = keySet(annotationKeys)
	setNodeManagedFields(updated, "foreign-controller", labelKeys, annotationKeys)
	c.nextRV++
	updated.ResourceVersion = strconv.FormatInt(c.nextRV, 10)
	c.nodes[name] = updated
	watcher := c.watcher
	c.mu.Unlock()
	if watcher != nil {
		watcher.Modify(updated.DeepCopy())
	}
}

func findCondition(conditions []metav1.Condition, conditionType string) *metav1.Condition {
	for i := range conditions {
		if conditions[i].Type == conditionType {
			return &conditions[i]
		}
	}
	return nil
}

func applyStringMap(
	current map[string]string,
	desired map[string]*string,
	owned map[string]struct{},
	foreign map[string]struct{},
) (map[string]string, map[string]struct{}) {
	if current == nil {
		current = make(map[string]string)
	}
	for key := range owned {
		if _, retained := desired[key]; !retained {
			if _, shared := foreign[key]; !shared {
				delete(current, key)
			}
		}
	}
	nextOwned := make(map[string]struct{}, len(desired))
	for key, value := range desired {
		nextOwned[key] = struct{}{}
		if value == nil {
			current[key] = ""
			continue
		}
		current[key] = *value
	}
	return current, nextOwned
}

func keySet(keys []string) map[string]struct{} {
	set := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		set[key] = struct{}{}
	}
	return set
}

func setAcceptanceManagedFields(node *corev1.Node, ownedLabels, ownedAnnotations map[string]struct{}) {
	managedFields := node.ManagedFields[:0]
	for _, entry := range node.ManagedFields {
		if entry.Manager != controllerprojection.FieldManager {
			managedFields = append(managedFields, entry)
		}
	}
	node.ManagedFields = managedFields
	labels := make([]string, 0, len(ownedLabels))
	for key := range ownedLabels {
		labels = append(labels, key)
	}
	annotations := make([]string, 0, len(ownedAnnotations))
	for key := range ownedAnnotations {
		annotations = append(annotations, key)
	}
	if len(labels)+len(annotations) > 0 {
		setNodeManagedFields(node, controllerprojection.FieldManager, labels, annotations)
	}
}

var _ corev1client.NodeInterface = (*acceptanceNodeClient)(nil)
