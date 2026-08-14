// Copyright (c) 2026, NVIDIA CORPORATION.  All rights reserved.
// Licensed under the Apache License, Version 2.0 (the "License");

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

	controllernodes "github.com/NVIDIA/k8s-test-infra/internal/mokkacontroller/nodecatalog"
	controllerprojection "github.com/NVIDIA/k8s-test-infra/internal/mokkacontroller/projection"
	controllerack "github.com/NVIDIA/k8s-test-infra/internal/mokkacontroller/rack"
	mokkav1alpha1 "github.com/NVIDIA/k8s-test-infra/pkg/apis/mokka/v1alpha1"
	mokkafake "github.com/NVIDIA/k8s-test-infra/pkg/generated/clientset/versioned/fake"
	mokkalisters "github.com/NVIDIA/k8s-test-infra/pkg/generated/listers/mokka/v1alpha1"
	"github.com/NVIDIA/k8s-test-infra/pkg/mokka/allocate"
	"github.com/NVIDIA/k8s-test-infra/pkg/mokka/materialize"
)

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

	profile := acceptanceProfile(2)
	inventory := acceptanceInventory()
	nodes.create(acceptanceNode("node-a", "node-a-v1", 1))
	nodes.create(acceptanceNode("node-b", "node-b-v1", 2))
	_, err = mokka.MokkaV1alpha1().SGPUProfiles().Create(ctx, profile, metav1.CreateOptions{})
	require.NoError(t, err)
	_, err = mokka.MokkaV1alpha1().SGPUInventories().Create(ctx, inventory, metav1.CreateOptions{})
	require.NoError(t, err)

	rackName := materialize.RackName(inventory.Name, inventory.UID, "compute", 0)
	require.Eventually(t, func() bool {
		rack := getAcceptanceRack(t, mokka, rackName)
		return len(rack.Spec.Slots) == 2 &&
			rack.Spec.Slots[0].NodeRef != nil && rack.Spec.Slots[0].NodeRef.UID == "node-a-v1" &&
			rack.Spec.Slots[1].NodeRef != nil && rack.Spec.Slots[1].NodeRef.UID == "node-b-v1" &&
			nodeIsProjected(nodes.snapshot("node-a"), "node-a-v1") &&
			nodeIsProjected(nodes.snapshot("node-b"), "node-b-v1")
	}, 10*time.Second, 20*time.Millisecond)

	require.Eventually(t, func() bool {
		current, err := mokka.MokkaV1alpha1().SGPUInventories().Get(ctx, inventory.Name, metav1.GetOptions{})
		if err != nil {
			return false
		}
		rack := getAcceptanceRack(t, mokka, rackName)
		return current.Status.ObservedGeneration == current.Generation &&
			slices.Contains(current.Finalizers, controllerack.InventoryFinalizer) &&
			current.Status.Capacity.NodeSlots == 2 &&
			current.Status.Usage.AllocatedNodes == 2 &&
			current.Status.Usage.ProjectedNodes == 2 &&
			slices.Contains(rack.Finalizers, controllerack.RackFinalizer) &&
			rack.Status.AssignedSlots == 2 && rack.Status.ProjectedSlots == 2
	}, 10*time.Second, 20*time.Millisecond)

	currentInventory, err := mokka.MokkaV1alpha1().SGPUInventories().Get(ctx, inventory.Name, metav1.GetOptions{})
	require.NoError(t, err)
	currentInventory.Spec.RackGroups[0].Count = 0
	currentInventory.Generation++
	_, err = mokka.MokkaV1alpha1().SGPUInventories().Update(ctx, currentInventory, metav1.UpdateOptions{})
	require.NoError(t, err)
	controller.queues.inventories.Add(inventory.Name)
	require.Eventually(t, func() bool {
		return !nodeHasProjection(nodes.snapshot("node-a")) &&
			!nodeHasProjection(nodes.snapshot("node-b"))
	}, 10*time.Second, 20*time.Millisecond, "capacity shrink must wait for projection cleanup")

	require.Eventually(t, func() bool {
		current, err := mokka.MokkaV1alpha1().SGPUInventories().Get(ctx, inventory.Name, metav1.GetOptions{})
		if err != nil || current.Status.Capacity.NodeSlots != 0 ||
			current.Status.Usage.AllocatedNodes != 0 || current.Status.Usage.ProjectedNodes != 0 {
			return false
		}
		racks, err := mokka.MokkaV1alpha1().SGPURacks().List(ctx, metav1.ListOptions{})
		return err == nil && len(racks.Items) == 0 && current.Status.Capacity.NodeSlots == 0 &&
			current.Status.Usage.AllocatedNodes == 0 && current.Status.Usage.ProjectedNodes == 0
	}, 10*time.Second, 20*time.Millisecond)

	nodes.delete("node-b")
	nodes.replace("node-a", acceptanceNode("node-a", "node-a-v2", 3))
	currentInventory, err = mokka.MokkaV1alpha1().SGPUInventories().Get(ctx, inventory.Name, metav1.GetOptions{})
	require.NoError(t, err)
	currentInventory.Spec.RackGroups[0].Count = 1
	currentInventory.Generation++
	_, err = mokka.MokkaV1alpha1().SGPUInventories().Update(ctx, currentInventory, metav1.UpdateOptions{})
	require.NoError(t, err)
	controller.queues.inventories.Add(inventory.Name)
	require.Eventually(t, func() bool {
		rack := getAcceptanceRack(t, mokka, rackName)
		node := nodes.snapshot("node-a")
		bindings := 0
		projected := false
		for index := range rack.Spec.Slots {
			slot := &rack.Spec.Slots[index]
			if slot.NodeRef != nil && slot.NodeRef.UID == "node-a-v2" {
				bindings++
				projected = projected || controllerprojection.MatchesBinding(node, rack, slot)
			}
		}
		return bindings == 1 && projected
	}, 10*time.Second, 20*time.Millisecond, "same-name replacement must receive a new exact-UID binding")

	currentInventory, err = mokka.MokkaV1alpha1().SGPUInventories().Get(ctx, inventory.Name, metav1.GetOptions{})
	require.NoError(t, err)
	currentInventory.Spec.RackGroups[0].Count = 0
	currentInventory.Generation++
	_, err = mokka.MokkaV1alpha1().SGPUInventories().Update(ctx, currentInventory, metav1.UpdateOptions{})
	require.NoError(t, err)
	controller.queues.inventories.Add(inventory.Name)
	require.Eventually(t, func() bool {
		racks, err := mokka.MokkaV1alpha1().SGPURacks().List(ctx, metav1.ListOptions{})
		return err == nil && len(racks.Items) == 0 && !nodeHasProjection(nodes.snapshot("node-a"))
	}, 10*time.Second, 20*time.Millisecond)

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
	_, err = mokka.MokkaV1alpha1().SGPUProfiles().Create(ctx, profile, metav1.CreateOptions{})
	require.NoError(t, err)
	_, err = mokka.MokkaV1alpha1().SGPUInventories().Create(ctx, inventory, metav1.CreateOptions{})
	require.NoError(t, err)

	rackName := materialize.RackName(inventory.Name, inventory.UID, "compute", 0)
	require.Eventually(t, func() bool {
		rack := getAcceptanceRack(t, mokka, rackName)
		return len(rack.Spec.Slots) == 1 && rack.Spec.Slots[0].NodeRef != nil &&
			rack.Spec.Slots[0].NodeRef.UID == node.UID && nodeIsProjected(nodes.snapshot(node.Name), node.UID)
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
		accepted := findCondition(current.Status.Conditions, mokkav1alpha1.SGPUInventoryConditionAccepted)
		materialized := findCondition(current.Status.Conditions, mokkav1alpha1.SGPUInventoryConditionMaterialized)
		return accepted != nil && accepted.Status == metav1.ConditionFalse && accepted.Message == wantValidationError &&
			materialized != nil && materialized.Status == metav1.ConditionFalse
	}, 10*time.Second, 20*time.Millisecond)

	require.Never(t, func() bool {
		return nodes.patchCalls() != patchesBeforeInvalidEdit
	}, 500*time.Millisecond, 10*time.Millisecond, "an invalid inventory must not clean or reapply the last-good projection")
	retained := getAcceptanceRack(t, mokka, rackName)
	require.NotNil(t, retained.Spec.Slots[0].NodeRef)
	require.Equal(t, node.UID, retained.Spec.Slots[0].NodeRef.UID)
	require.True(t, nodeIsProjected(nodes.snapshot(node.Name), node.UID))
	for _, action := range mokka.Actions() {
		if action.GetResource().Resource == "sgpuracks" {
			require.NotContains(t, []string{"patch", "update", "delete"}, action.GetVerb(),
				"an invalid inventory must not mutate last-good racks")
		}
	}
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
			rack.Spec.Slots[0].NodeRef = &mokkav1alpha1.SGPUNodeReference{Name: oldNode.Name, UID: oldNode.UID}
			assignment, err := controllerprojection.EncodeAssignment(rack, &rack.Spec.Slots[0])
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
				inventory.Spec.RackGroups[0].Count = 0
			}

			mokka := mokkafake.NewSimpleClientset(profile, inventory, rack)
			inventoryIndexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, controllerack.InventoryIndexers())
			profileIndexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
			rackIndexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, controllerack.RackIndexers())
			nodeCatalog := controllernodes.New()
			require.NoError(t, inventoryIndexer.Add(inventory.DeepCopy()))
			require.NoError(t, profileIndexer.Add(profile.DeepCopy()))
			require.NoError(t, rackIndexer.Add(rack.DeepCopy()))
			for _, node := range cachedNodes {
				nodeCatalog.Upsert(node.DeepCopy())
			}
			snapshot := newInformerCache(
				mokkalisters.NewSGPUInventoryLister(inventoryIndexer),
				mokkalisters.NewSGPUProfileLister(profileIndexer),
				rackIndexer,
				nodeCatalog,
				liveNodes,
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
			require.Equal(t, oldNode.UID, stored.Spec.Slots[0].NodeRef.UID)

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
			require.Nil(t, stored.Spec.Slots[0].NodeRef)

			if len(cachedNodes) == 0 {
				return
			}
			require.NoError(t, rackIndexer.Update(stored.DeepCopy()))
			_, err = reconciler.Reconcile(ctx, inventory.Name)
			require.NoError(t, err)
			stored, err = mokka.MokkaV1alpha1().SGPURacks().Get(ctx, rack.Name, metav1.GetOptions{})
			require.NoError(t, err)
			require.Equal(t, types.UID("replacement-uid"), stored.Spec.Slots[0].NodeRef.UID)
		})
	}
}

func installAcceptanceAPIReactors(t *testing.T, client *mokkafake.Clientset) {
	t.Helper()
	var nextRackUID atomic.Int64
	client.PrependReactor("patch", "sgpuracks", func(action k8stesting.Action) (bool, runtime.Object, error) {
		patch := action.(k8stesting.PatchActionImpl)
		require.Equal(t, types.ApplyPatchType, patch.GetPatchType())
		require.Equal(t, controllerack.RackFieldManager, patch.GetPatchOptions().FieldManager)
		require.NotNil(t, patch.GetPatchOptions().Force)
		require.False(t, *patch.GetPatchOptions().Force)
		desired := &mokkav1alpha1.SGPURack{}
		require.NoError(t, json.Unmarshal(patch.GetPatch(), desired))
		resource := mokkav1alpha1.SchemeGroupVersion.WithResource("sgpuracks")
		stored, err := client.Tracker().Get(resource, "", desired.Name)
		if apierrors.IsNotFound(err) {
			desired.UID = types.UID(fmt.Sprintf("uid-%s-%d", desired.Name, nextRackUID.Add(1)))
			desired.ResourceVersion = "1"
			err = client.Tracker().Create(resource, desired, "")
			return true, desired, err
		}
		if err != nil {
			return true, nil, err
		}
		updated := stored.(*mokkav1alpha1.SGPURack).DeepCopy()
		if desired.ResourceVersion != updated.ResourceVersion {
			return true, nil, apierrors.NewConflict(
				mokkav1alpha1.Resource("sgpuracks"), desired.Name, errors.New("rack resource version changed"),
			)
		}
		updated.Spec = desired.Spec
		updated.Labels = desired.Labels
		updated.Annotations = desired.Annotations
		updated.Finalizers = desired.Finalizers
		updated.OwnerReferences = desired.OwnerReferences
		updated.ResourceVersion += "a"
		err = client.Tracker().Update(resource, updated, "")
		return true, updated, err
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

func acceptanceProfile(nodesPerRack int32) *mokkav1alpha1.SGPUProfile {
	return &mokkav1alpha1.SGPUProfile{
		TypeMeta: metav1.TypeMeta{APIVersion: mokkav1alpha1.SchemeGroupVersion.String(), Kind: "SGPUProfile"},
		ObjectMeta: metav1.ObjectMeta{
			Name: "acceptance-profile", UID: "acceptance-profile-uid", Generation: 1, ResourceVersion: "1",
		},
		Spec: mokkav1alpha1.SGPUProfileSpec{
			Rack: mokkav1alpha1.SGPUProfileRack{NodesPerRack: nodesPerRack},
			Node: mokkav1alpha1.SGPUProfileNode{
				GPUs: mokkav1alpha1.SGPUHardware{Count: 1},
				Topology: mokkav1alpha1.SGPUNodeTopology{
					GPUSlots: []mokkav1alpha1.SGPUGPUSlot{{Index: 0, PCIAddress: "0000:01:00.0", RootComplex: "pci0000:00"}},
					GPUFabric: &mokkav1alpha1.SGPUGPUFabric{
						Type: "NVLink", Domain: mokkav1alpha1.SGPUGPUFabricDomain{Scope: "Rack", GPUCount: nodesPerRack},
					},
				},
			},
			Software: mokkav1alpha1.SGPUSoftware{DriverVersion: "580.1", NVMLVersion: "13", CUDAVersion: "13.1"},
		},
	}
}

func acceptanceInventory() *mokkav1alpha1.SGPUInventory {
	return &mokkav1alpha1.SGPUInventory{
		TypeMeta: metav1.TypeMeta{APIVersion: mokkav1alpha1.SchemeGroupVersion.String(), Kind: "SGPUInventory"},
		ObjectMeta: metav1.ObjectMeta{
			Name: "acceptance", UID: "acceptance-inventory-uid", Generation: 1, ResourceVersion: "1",
		},
		Spec: mokkav1alpha1.SGPUInventorySpec{RackGroups: []mokkav1alpha1.SGPURackGroup{{
			ID: "compute", Count: 1,
			ProfileRef: corev1.LocalObjectReference{Name: "acceptance-profile"},
			Placement: &mokkav1alpha1.SGPUPlacement{NodeSelector: &metav1.LabelSelector{
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

func getAcceptanceRack(t *testing.T, client *mokkafake.Clientset, name string) *mokkav1alpha1.SGPURack {
	t.Helper()
	rack, err := client.MokkaV1alpha1().SGPURacks().Get(context.Background(), name, metav1.GetOptions{})
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
	mu               sync.Mutex
	nodes            map[string]*corev1.Node
	ownedLabels      map[string]map[string]struct{}
	ownedAnnotations map[string]map[string]struct{}
	watcher          *watch.RaceFreeFakeWatcher
	nextRV           int64
	patches          int
}

func newAcceptanceNodeClient() *acceptanceNodeClient {
	return &acceptanceNodeClient{
		nodes:       make(map[string]*corev1.Node),
		ownedLabels: make(map[string]map[string]struct{}), ownedAnnotations: make(map[string]map[string]struct{}),
		nextRV: 1,
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
	updated.Labels, c.ownedLabels[name] = applyStringMap(updated.Labels, payload.Metadata.Labels, c.ownedLabels[name])
	updated.Annotations, c.ownedAnnotations[name] = applyStringMap(
		updated.Annotations, payload.Metadata.Annotations, c.ownedAnnotations[name],
	)
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
	if node.Labels[controllerprojection.AssignedLabel] == "true" {
		c.ownedLabels[node.Name] = map[string]struct{}{controllerprojection.AssignedLabel: {}}
		if node.Labels[controllerprojection.CliqueLabel] != "" {
			c.ownedLabels[node.Name][controllerprojection.CliqueLabel] = struct{}{}
		}
	}
	if node.Annotations[controllerprojection.AssignmentAnnotation] != "" {
		c.ownedAnnotations[node.Name] = map[string]struct{}{controllerprojection.AssignmentAnnotation: {}}
	}
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

func (c *acceptanceNodeClient) delete(name string) {
	c.mu.Lock()
	old := c.nodes[name]
	delete(c.nodes, name)
	delete(c.ownedLabels, name)
	delete(c.ownedAnnotations, name)
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
) (map[string]string, map[string]struct{}) {
	if current == nil {
		current = make(map[string]string)
	}
	for key := range owned {
		if _, retained := desired[key]; !retained {
			delete(current, key)
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

var _ corev1client.NodeInterface = (*acceptanceNodeClient)(nil)
