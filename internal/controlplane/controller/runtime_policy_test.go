// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package controller

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	k8stesting "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/cache"
	"k8s.io/utils/ptr"

	mokkav1alpha1 "github.com/NVIDIA/k8s-test-infra/api/v1alpha1"
	sgpuinventory "github.com/NVIDIA/k8s-test-infra/internal/sgpu/inventory"
	"github.com/NVIDIA/k8s-test-infra/internal/sgpu/nodecatalog"
	sgpupolicy "github.com/NVIDIA/k8s-test-infra/internal/sgpu/policy"
	sgpustatus "github.com/NVIDIA/k8s-test-infra/internal/sgpu/status"
	mokkafake "github.com/NVIDIA/k8s-test-infra/pkg/generated/clientset/versioned/fake"
	mokkalisters "github.com/NVIDIA/k8s-test-infra/pkg/generated/listers/api/v1alpha1"
)

func TestRuntimePolicyEventsRouteTheirTargetInventory(t *testing.T) {
	t.Parallel()

	inventories := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
	racks := cache.NewIndexer(cache.MetaNamespaceKeyFunc, sgpuinventory.Indexers())
	queues := newQueues(0)
	t.Cleanup(queues.shutdown)
	router := newEventRouter(inventories, racks, nodecatalog.New(), newPlacementRegistry(), queues)
	policy := testRuntimePolicy("hot", "inventory")
	policiesOf := func(inventory string) statusKey { return statusKey{kind: statusRuntimePolicies, name: inventory} }

	router.runtimePolicyAdd(policy)
	require.Equal(t, []statusKey{policiesOf("inventory")}, drainQueue(queues.status))

	edited := policy.DeepCopy()
	edited.Spec.TargetRef.GPUIndexes = []int32{1}
	router.runtimePolicyUpdate(policy, edited)
	require.Equal(t, []statusKey{policiesOf("inventory")}, drainQueue(queues.status))

	retargeted := policy.DeepCopy()
	retargeted.Spec.TargetRef.Name = "other"
	router.runtimePolicyUpdate(policy, retargeted)
	require.ElementsMatch(t, []statusKey{policiesOf("inventory"), policiesOf("other")}, drainQueue(queues.status),
		"the policy leaves one inventory's conflicts and joins another's")

	reported := policy.DeepCopy()
	reported.Status.GPUIndexesSummary = "0"
	router.runtimePolicyUpdate(policy, reported)
	require.Equal(t, []statusKey{policiesOf("inventory")}, drainQueue(queues.status),
		"an observed status write lets the status reconciler trust its cache again")

	labeled := policy.DeepCopy()
	labeled.Labels = map[string]string{"team": "chaos"}
	router.runtimePolicyUpdate(policy, labeled)
	require.Empty(t, drainQueue(queues.status))

	router.runtimePolicyDelete(cache.DeletedFinalStateUnknown{Key: policy.Name, Obj: policy})
	require.Equal(t, []statusKey{policiesOf("inventory")}, drainQueue(queues.status))

	require.Empty(t, drainQueue(queues.inventories), "runtime policies never change materialization")
	require.Empty(t, drainQueue(queues.groups))
	require.Empty(t, drainQueue(queues.projections))
}

func TestInventoryAndProfileEventsReevaluateRuntimePolicies(t *testing.T) {
	t.Parallel()

	inventories := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
	racks := cache.NewIndexer(cache.MetaNamespaceKeyFunc, sgpuinventory.Indexers())
	queues := newQueues(0)
	t.Cleanup(queues.shutdown)
	router := newEventRouter(inventories, racks, nodecatalog.New(), newPlacementRegistry(), queues)
	inventory := testInventory()
	require.NoError(t, inventories.Add(inventory))
	policies := statusKey{kind: statusRuntimePolicies, name: inventory.Name}

	router.inventoryAdd(inventory)
	require.Contains(t, drainQueue(queues.status), policies)

	reshaped := testRuntimeProfile()
	reshaped.Spec.Rack.NodesPerRack = 4
	router.profileUpdate(testRuntimeProfile(), reshaped)
	require.Contains(t, drainQueue(queues.status), policies, "a profile shape bounds Node and GPU indexes")

	router.inventoryDelete(inventory)
	require.Contains(t, drainQueue(queues.status), policies)
}

func TestEffectiveRuntimeCompilesEveryGPUOfTheLogicalNode(t *testing.T) {
	t.Parallel()

	controller := readyRuntimeController(t,
		testInventory(), testRuntimeProfile(),
		testRuntimePolicyWith("warm", "inventory", mokkav1alpha1.PolicyTargetRef{}, gpuTemperature(50)),
		testRuntimePolicyWith("gpu-1-failed", "inventory", mokkav1alpha1.PolicyTargetRef{GPUIndexes: []int32{1}},
			&mokkav1alpha1.RuntimeState{DeviceState: mokkav1alpha1.DeviceStateFailed}),
	)
	rack := testRuntimeRack()

	runtimes, err := controller.EffectiveRuntime(AssignmentSnapshot{Rack: rack, Node: &rack.Spec.Nodes[0]})

	require.NoError(t, err)
	warm, err := testRuntimeDefaults().WithOverride(gpuTemperature(50))
	require.NoError(t, err)
	failed, err := warm.WithOverride(&mokkav1alpha1.RuntimeState{DeviceState: mokkav1alpha1.DeviceStateFailed})
	require.NoError(t, err)
	require.Equal(t, []GPURuntime{{Index: 0, Runtime: *warm}, {Index: 1, Runtime: *failed}}, runtimes)

	*runtimes[1].Runtime.Telemetry.Temperature.GPUCelsius = 1
	again, err := controller.EffectiveRuntime(AssignmentSnapshot{Rack: rack, Node: &rack.Spec.Nodes[0]})
	require.NoError(t, err)
	require.Equal(t, int32(50), *again[1].Runtime.Telemetry.Temperature.GPUCelsius, "callers own the result")
}

func TestEffectiveRuntimeRejectsInputsTheRackWasNotRenderedFrom(t *testing.T) {
	t.Parallel()

	replacedInventory := testInventory()
	replacedInventory.UID = "replacement-uid"
	replacedProfile := testRuntimeProfile()
	replacedProfile.UID = "replacement-uid"
	profileAfterEdit := testRuntimeProfile()
	profileAfterEdit.Generation++
	tests := []struct {
		name    string
		objects []runtime.Object
	}{
		{name: "inventory is missing", objects: []runtime.Object{testRuntimeProfile()}},
		{name: "inventory was replaced", objects: []runtime.Object{replacedInventory, testRuntimeProfile()}},
		{name: "profile is missing", objects: []runtime.Object{testInventory()}},
		{name: "profile was replaced", objects: []runtime.Object{testInventory(), replacedProfile}},
		{name: "profile changed after rendering", objects: []runtime.Object{testInventory(), profileAfterEdit}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			controller := readyRuntimeController(t, tt.objects...)
			rack := testRuntimeRack()

			_, err := controller.EffectiveRuntime(AssignmentSnapshot{Rack: rack, Node: &rack.Spec.Nodes[0]})

			require.ErrorIs(t, err, ErrStaleRuntimeInputs)
		})
	}
}

func TestEffectiveRuntimeRequiresSynchronizedCaches(t *testing.T) {
	t.Parallel()

	controller := readyRuntimeController(t, testInventory(), testRuntimeProfile())
	controller.cacheReady.Store(false)
	rack := testRuntimeRack()

	_, err := controller.EffectiveRuntime(AssignmentSnapshot{Rack: rack, Node: &rack.Spec.Nodes[0]})

	require.ErrorIs(t, err, ErrCacheNotReady)
}

func TestRuntimePolicyStatusReconcileContinuesPastAFailedWrite(t *testing.T) {
	t.Parallel()

	broken := testRuntimePolicy("broken", "inventory")
	healthy := testRuntimePolicy("healthy", "inventory")
	healthy.Spec.TargetRef.GPUIndexes = []int32{1}
	mokka := mokkafake.NewSimpleClientset(broken, healthy)
	mokka.PrependReactor("update", "sgpuruntimepolicies", func(action k8stesting.Action) (bool, runtime.Object, error) {
		update := action.(k8stesting.UpdateAction)
		if action.GetSubresource() == "status" && update.GetObject().(*mokkav1alpha1.SGPURuntimePolicy).Name == broken.Name {
			return true, nil, errors.New("status write failed")
		}
		return false, nil, nil
	})
	view := testRuntimePolicyView(t, testInventory(), testRuntimeProfile(), broken, healthy)
	reconciler := sgpustatus.NewRuntimePolicyReconciler(mokka.MokkaV1alpha1().SGPURuntimePolicies(), nil)

	err := reconcileRuntimePolicyStatus(t.Context(), view, reconciler, "inventory")

	require.ErrorContains(t, err, `update runtime policy "broken" status: status write failed`)
	stored, getErr := mokka.MokkaV1alpha1().SGPURuntimePolicies().Get(t.Context(), healthy.Name, metav1.GetOptions{})
	require.NoError(t, getErr)
	accepted := findCondition(stored.Status.Conditions, mokkav1alpha1.RuntimePolicyConditionAccepted)
	require.NotNil(t, accepted)
	require.Equal(t, string(sgpupolicy.Accepted), accepted.Reason)
}

func TestRuntimePolicyStatusAcceptance(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	mokka := mokkafake.NewSimpleClientset()
	installAcceptanceAPIReactors(t, mokka)
	controller, err := newForNodes(newAcceptanceNodeClient(), mokka, Options{Workers: 2, StatusDebounce: 0})
	require.NoError(t, err)
	runDone := make(chan error, 1)
	go func() { runDone <- runControllerForTest(ctx, controller) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-runDone:
		case <-time.After(5 * time.Second):
			t.Error("controller did not stop")
		}
	})
	require.Eventually(t, controller.LeaderReady, 5*time.Second, 10*time.Millisecond)

	inventory := acceptanceInventory()
	inventory.Spec.RackGroups[0].Count = 2
	_, err = mokka.MokkaV1alpha1().SGPURackProfiles().Create(ctx, acceptanceProfile(2), metav1.CreateOptions{})
	require.NoError(t, err)
	_, err = mokka.MokkaV1alpha1().SGPUInventories().Create(ctx, inventory, metav1.CreateOptions{})
	require.NoError(t, err)
	failed := &mokkav1alpha1.RuntimeState{DeviceState: mokkav1alpha1.DeviceStateFailed}
	degraded := &mokkav1alpha1.RuntimeState{DeviceState: mokkav1alpha1.DeviceStateDegraded}
	for _, policy := range []*mokkav1alpha1.SGPURuntimePolicy{
		acceptancePolicy("inventory-failed", 1, inventory.Name, mokkav1alpha1.PolicyTargetRef{}, failed),
		acceptancePolicy("inventory-degraded", 2, inventory.Name, mokkav1alpha1.PolicyTargetRef{}, degraded),
		acceptancePolicy("second-rack-hot", 3, inventory.Name, mokkav1alpha1.PolicyTargetRef{RackIndexes: []int32{1}}, gpuTemperature(90)),
		acceptancePolicy("missing-target", 4, "missing", mokkav1alpha1.PolicyTargetRef{}, failed),
	} {
		_, err = mokka.MokkaV1alpha1().SGPURuntimePolicies().Create(ctx, policy, metav1.CreateOptions{})
		require.NoError(t, err)
	}

	requireRuntimePolicyReasons(ctx, t, mokka, map[string]sgpupolicy.Outcome{
		"inventory-failed":   sgpupolicy.Accepted,
		"inventory-degraded": sgpupolicy.Conflicted,
		"second-rack-hot":    sgpupolicy.Accepted,
		"missing-target":     sgpupolicy.TargetNotFound,
	})
	secondRack, err := mokka.MokkaV1alpha1().SGPURuntimePolicies().Get(ctx, "second-rack-hot", metav1.GetOptions{})
	require.NoError(t, err)
	require.Equal(t, "1", secondRack.Status.RackIndexesSummary)

	require.NoError(t, mokka.MokkaV1alpha1().SGPURuntimePolicies().Delete(ctx, "inventory-failed", metav1.DeleteOptions{}))
	requireRuntimePolicyReasons(ctx, t, mokka, map[string]sgpupolicy.Outcome{
		"inventory-degraded": sgpupolicy.Accepted,
	})

	current, err := mokka.MokkaV1alpha1().SGPUInventories().Get(ctx, inventory.Name, metav1.GetOptions{})
	require.NoError(t, err)
	current.Spec.RackGroups[0].Count = 1
	current.Generation++
	_, err = mokka.MokkaV1alpha1().SGPUInventories().Update(ctx, current, metav1.UpdateOptions{})
	require.NoError(t, err)
	requireRuntimePolicyReasons(ctx, t, mokka, map[string]sgpupolicy.Outcome{
		"second-rack-hot": sgpupolicy.InvalidTarget,
	})

	mokka.Fake.ClearActions()
	require.Never(t, func() bool {
		for _, action := range mokka.Actions() {
			if action.GetVerb() == "update" && action.GetResource().Resource == "sgpuruntimepolicies" {
				return true
			}
		}
		return false
	}, 300*time.Millisecond, 10*time.Millisecond, "converged policy statuses must not be rewritten")
}

func TestStandbyCompilesEffectiveRuntimeWithoutWriteWork(t *testing.T) {
	profile := acceptanceProfile(1)
	profile.Spec.Defaults = &mokkav1alpha1.SGPURackProfileDefaults{Runtime: gpuTemperature(38)}
	inventory := acceptanceInventory()
	node := mokkav1alpha1.SGPUNodeReference{Name: "node", UID: "node-uid"}
	rack := assignmentRack("rack", "rack-uid", node, 0)
	rack.Spec.InventoryRef = mokkav1alpha1.SGPURackInventoryReference{Name: inventory.Name, UID: inventory.UID}
	rack.Spec.ProfileRef = profile.Reference(strings.Repeat("a", 64))
	rack.Spec.Identity = mokkav1alpha1.SGPURackIdentity{RackGroup: "compute"}
	rack.Spec.Nodes[0].GPUs = []mokkav1alpha1.SGPURackGPU{{Index: 0}}
	policy := acceptancePolicy("gpu-failed", 1, inventory.Name, mokkav1alpha1.PolicyTargetRef{GPUIndexes: []int32{0}},
		&mokkav1alpha1.RuntimeState{DeviceState: mokkav1alpha1.DeviceStateFailed})
	mokka := mokkafake.NewSimpleClientset(profile, inventory, rack, policy)
	controller, err := newForNodes(newAcceptanceNodeClient(), mokka, Options{Workers: 1})
	require.NoError(t, err)
	startCachesForTest(t, controller)
	mokka.Fake.ClearActions()

	assignments, err := controller.AssignmentsForNode(node)
	require.NoError(t, err)
	require.Len(t, assignments, 1)
	runtimes, err := controller.EffectiveRuntime(assignments[0])
	require.NoError(t, err)
	want, err := gpuTemperature(38).WithOverride(&mokkav1alpha1.RuntimeState{DeviceState: mokkav1alpha1.DeviceStateFailed})
	require.NoError(t, err)
	require.Equal(t, []GPURuntime{{Index: 0, Runtime: *want}}, runtimes)
	require.False(t, controller.LeaderReady())
	require.Zero(t, controller.queues.status.Len())
	require.Never(t, func() bool { return hasMutatingAction(mokka.Actions()) }, 100*time.Millisecond, time.Millisecond)

	edited := profile.DeepCopy()
	edited.Generation++
	edited.Spec.Defaults.Runtime = gpuTemperature(45)
	_, err = mokka.MokkaV1alpha1().SGPURackProfiles().Update(t.Context(), edited, metav1.UpdateOptions{})
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		_, err := controller.EffectiveRuntime(assignments[0])
		return errors.Is(err, ErrStaleRuntimeInputs)
	}, 5*time.Second, 10*time.Millisecond, "defaults from a profile the rack was not rendered from must not be served")
}

func requireRuntimePolicyReasons(
	ctx context.Context,
	t *testing.T,
	mokka *mokkafake.Clientset,
	want map[string]sgpupolicy.Outcome,
) {
	t.Helper()
	require.Eventually(t, func() bool {
		for name, outcome := range want {
			policy, err := mokka.MokkaV1alpha1().SGPURuntimePolicies().Get(ctx, name, metav1.GetOptions{})
			if err != nil {
				return false
			}
			accepted := findCondition(policy.Status.Conditions, mokkav1alpha1.RuntimePolicyConditionAccepted)
			if accepted == nil || accepted.Reason != string(outcome) || accepted.ObservedGeneration != policy.Generation {
				return false
			}
		}
		return true
	}, 10*time.Second, 20*time.Millisecond)
}

func acceptancePolicy(
	name string,
	created int64,
	inventoryName string,
	target mokkav1alpha1.PolicyTargetRef,
	runtimeState *mokkav1alpha1.RuntimeState,
) *mokkav1alpha1.SGPURuntimePolicy {
	policy := testRuntimePolicyWith(name, inventoryName, target, runtimeState)
	policy.CreationTimestamp = metav1.NewTime(time.Unix(created, 0))
	return policy
}

func testRuntimePolicy(name, inventoryName string) *mokkav1alpha1.SGPURuntimePolicy {
	return testRuntimePolicyWith(name, inventoryName, mokkav1alpha1.PolicyTargetRef{}, gpuTemperature(90))
}

func testRuntimePolicyWith(
	name, inventoryName string,
	target mokkav1alpha1.PolicyTargetRef,
	runtimeState *mokkav1alpha1.RuntimeState,
) *mokkav1alpha1.SGPURuntimePolicy {
	target.Group, target.Kind, target.Name = mokkav1alpha1.GroupName, "SGPUInventory", inventoryName
	return &mokkav1alpha1.SGPURuntimePolicy{
		TypeMeta: metav1.TypeMeta{APIVersion: mokkav1alpha1.SchemeGroupVersion.String(), Kind: "SGPURuntimePolicy"},
		ObjectMeta: metav1.ObjectMeta{
			Name: name, UID: types.UID(name + "-uid"), Generation: 1, ResourceVersion: "1",
		},
		Spec: mokkav1alpha1.SGPURuntimePolicySpec{TargetRef: target, Runtime: runtimeState},
	}
}

// testRuntimeProfile shapes testInventory's rack group as racks of 2 Nodes
// with 2 GPUs each.
func testRuntimeProfile() *mokkav1alpha1.SGPURackProfile {
	return &mokkav1alpha1.SGPURackProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "profile", UID: "profile-uid", Generation: 2, ResourceVersion: "1"},
		Spec: mokkav1alpha1.SGPURackProfileSpec{
			Rack:     mokkav1alpha1.SGPURackShape{NodesPerRack: 2},
			Node:     mokkav1alpha1.SGPUNode{GPUs: mokkav1alpha1.SGPUGPUs{Count: 2}},
			Defaults: &mokkav1alpha1.SGPURackProfileDefaults{Runtime: testRuntimeDefaults()},
		},
	}
}

// testRuntimeRack is rack 0 of testInventory's rack group, rendered from
// generation 2 of testRuntimeProfile, with logical Node 1 bound.
func testRuntimeRack() *mokkav1alpha1.SGPURack {
	return &mokkav1alpha1.SGPURack{
		ObjectMeta: metav1.ObjectMeta{Name: "rack", UID: "rack-uid"},
		Spec: mokkav1alpha1.SGPURackSpec{
			InventoryRef: mokkav1alpha1.SGPURackInventoryReference{Name: "inventory", UID: "inventory-uid"},
			ProfileRef: mokkav1alpha1.SGPURackProfileReference{
				Name: "profile", UID: "profile-uid", Generation: 2, Revision: strings.Repeat("a", 64),
			},
			Identity: mokkav1alpha1.SGPURackIdentity{RackGroup: "group"},
			Nodes: []mokkav1alpha1.SGPURackNode{{
				Index:   1,
				NodeRef: &mokkav1alpha1.SGPUNodeReference{Name: "node", UID: "node-uid"},
				GPUs:    []mokkav1alpha1.SGPURackGPU{{Index: 0}, {Index: 1}},
			}},
		},
	}
}

func testRuntimeDefaults() *mokkav1alpha1.RuntimeState {
	return &mokkav1alpha1.RuntimeState{
		DeviceState: mokkav1alpha1.DeviceStateHealthy,
		Telemetry: &mokkav1alpha1.RuntimeTelemetry{
			Temperature: &mokkav1alpha1.TemperatureTelemetry{Mode: "Fixed", GPUCelsius: ptr.To[int32](38)},
		},
	}
}

func gpuTemperature(celsius int32) *mokkav1alpha1.RuntimeState {
	return &mokkav1alpha1.RuntimeState{Telemetry: &mokkav1alpha1.RuntimeTelemetry{
		Temperature: &mokkav1alpha1.TemperatureTelemetry{GPUCelsius: ptr.To(celsius)},
	}}
}

// readyRuntimeController serves EffectiveRuntime from the given cached objects
// as a replica whose caches have synchronized.
func readyRuntimeController(t *testing.T, objects ...runtime.Object) *Controller {
	t.Helper()
	controller := &Controller{runtimePolicies: testRuntimePolicyView(t, objects...)}
	controller.cacheReady.Store(true)
	return controller
}

func testRuntimePolicyView(t *testing.T, objects ...runtime.Object) *runtimePolicyView {
	t.Helper()
	inventories := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
	profiles := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
	policies := cache.NewIndexer(cache.MetaNamespaceKeyFunc, runtimePolicyIndexers())
	for _, object := range objects {
		switch typed := object.(type) {
		case *mokkav1alpha1.SGPUInventory:
			require.NoError(t, inventories.Add(typed))
		case *mokkav1alpha1.SGPURackProfile:
			require.NoError(t, profiles.Add(typed))
		case *mokkav1alpha1.SGPURuntimePolicy:
			require.NoError(t, policies.Add(typed))
		default:
			t.Fatalf("unexpected cached object %T", object)
		}
	}
	return &runtimePolicyView{
		inventories: mokkalisters.NewSGPUInventoryLister(inventories),
		profiles:    mokkalisters.NewSGPURackProfileLister(profiles),
		policies:    policies,
	}
}
