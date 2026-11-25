// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package controller

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	k8stesting "k8s.io/client-go/testing"

	api "github.com/NVIDIA/k8s-test-infra/api/v1alpha1"
	sgpuassignment "github.com/NVIDIA/k8s-test-infra/internal/sgpu/assignment"
	sgpumetadata "github.com/NVIDIA/k8s-test-infra/internal/sgpu/metadata"
	"github.com/NVIDIA/k8s-test-infra/internal/sgpu/rackrender"
	mokkafake "github.com/NVIDIA/k8s-test-infra/pkg/generated/clientset/versioned/fake"
)

func TestLeaderPublishesRackDeliveries(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	nodes := newAcceptanceNodeClient()
	profile, inventory := acceptanceProfile(1), acceptanceInventory()
	other := inventory.Spec.RackGroups[0].DeepCopy()
	other.ID = "other"
	other.Placement.NodeSelector.MatchLabels["pool"] = "other"
	inventory.Spec.RackGroups[0].Count = 2
	inventory.Spec.RackGroups = append(inventory.Spec.RackGroups, *other)
	client := mokkafake.NewSimpleClientset(profile, inventory)
	installAcceptanceAPIReactors(t, client)
	leader, err := newForNodes(nodes, client, Options{Workers: 2, StatusDebounce: 10 * time.Millisecond})
	require.NoError(t, err)
	done := make(chan error, 1)
	go func() { done <- runControllerForTest(ctx, leader) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("leader did not stop")
		}
	})
	racks := []string{
		rackrender.RackName(inventory.Name, inventory.UID, "compute", 0),
		rackrender.RackName(inventory.Name, inventory.UID, "compute", 1),
		rackrender.RackName(inventory.Name, inventory.UID, "other", 0),
	}

	generations := map[string]int64{}
	for _, rack := range racks {
		generations[rack] = waitForRackDelivery(t, client, rack, func(r *api.SGPURack) bool { return r.Status.Delivery.Generation == 1 }).Generation
	}
	// A move changes two rack specs. A release changes only the current rack.

	// Two policies compete for the same field. Only the accepted one is delivered.
	target := api.PolicyTargetRef{RackGroups: []string{"compute"}}
	winner := acceptancePolicy("first", 1, inventory.Name, target, gpuTemperature(50))
	successor := acceptancePolicy("second", 2, inventory.Name, target, gpuTemperature(50))
	policies := client.MokkaV1alpha1().SGPURuntimePolicies()
	client.ClearActions()
	for _, p := range []*api.SGPURuntimePolicy{winner, successor} {
		_, err = policies.Create(ctx, p, metav1.CreateOptions{})
		require.NoError(t, err)
	}

	requirePolicyDelivery(t, client, racks[:2], generations, winner)
	edited, err := policies.Get(ctx, winner.Name, metav1.GetOptions{})
	require.NoError(t, err)
	edited.Generation++
	edited.Spec.Runtime = gpuTemperature(60)
	_, err = policies.Update(ctx, edited, metav1.UpdateOptions{})
	require.NoError(t, err)
	requirePolicyDelivery(t, client, racks[:2], generations, edited)
	// A relist can jump across a change and its reversal. The final spec
	// matches the previous spec, but readers still need the new generation.
	relisted, err := policies.Get(ctx, winner.Name, metav1.GetOptions{})
	require.NoError(t, err)
	relisted.Generation += 2
	_, err = policies.Update(ctx, relisted, metav1.UpdateOptions{})
	require.NoError(t, err)
	requirePolicyDelivery(t, client, racks[:2], generations, relisted)
	require.NoError(t, policies.Delete(ctx, winner.Name, metav1.DeleteOptions{}))
	requirePolicyDelivery(t, client, racks[:2], generations, successor)
	nodes.create(acceptanceNode("node-a", "node-uid", 1))
	checkBindingDelivery(t, client, generations, racks[0], true)
	first := waitForNodePublication(t, nodes, "node-a", 1, racks[0])
	profile = profile.DeepCopy()
	profile.Generation++
	profile.Spec.Defaults = &api.SGPURackProfileDefaults{Runtime: gpuTemperature(40)}
	_, err = client.MokkaV1alpha1().SGPURackProfiles().Update(ctx, profile, metav1.UpdateOptions{})
	require.NoError(t, err)
	for _, name := range racks {
		d := waitForRackDelivery(t, client, name, func(r *api.SGPURack) bool { return r.Status.Delivery.ProfileRef.Generation == profile.Generation })
		require.Equal(t, generations[name]+1, d.Generation)
		generations[name] = d.Generation
	}
	// Change an accepted policy while the Node is bound, too.
	successor.Generation++
	successor.Spec.Runtime = gpuTemperature(65)
	_, err = policies.Update(ctx, successor, metav1.UpdateOptions{})
	require.NoError(t, err)
	for _, name := range racks[:2] {
		d := waitForRackDelivery(t, client, name, func(r *api.SGPURack) bool {
			policies := r.Status.Delivery.AcceptedPolicies
			return len(policies) == 1 && policies[0].UID == successor.UID && policies[0].Generation == successor.Generation
		})
		generations[name] = d.Generation
	}
	require.Equal(t, first.ResourceVersion, nodes.snapshot("node-a").ResourceVersion, "profile and policy edits must keep the Node publication")
	nodes.update("node-a", func(n *corev1.Node) { n.Labels["pool"] = "other" })
	checkBindingDelivery(t, client, generations, racks[0], false)
	checkBindingDelivery(t, client, generations, racks[2], true)
	waitForNodePublication(t, nodes, "node-a", 3, racks[2])
	nodes.update("node-a", func(n *corev1.Node) { n.Labels["pool"] = "outside" })
	checkBindingDelivery(t, client, generations, racks[2], false)
	waitForNodePublication(t, nodes, "node-a", 4, "")
	nodes.update("node-a", func(n *corev1.Node) { n.Labels["pool"] = "other" })
	checkBindingDelivery(t, client, generations, racks[2], true)
	waitForNodePublication(t, nodes, "node-a", 5, racks[2])
}

func checkBindingDelivery(t *testing.T, client *mokkafake.Clientset, generations map[string]int64, name string, bound bool) {
	t.Helper()
	d := waitForRackDelivery(t, client, name, func(r *api.SGPURack) bool {
		return (r.Spec.Nodes[0].NodeRef != nil) == bound && r.Status.Delivery.Generation > generations[name] && r.Status.Delivery.RackSpecGeneration == r.Generation
	})
	want := generations[name] + 1
	if !bound {
		// Publish the release, then the cleared slot.
		want++
	}
	require.Equal(t, want, d.Generation)
	generations[name] = d.Generation
}

func waitForNodePublication(t *testing.T, nodes *acceptanceNodeClient, name string, revision int64, rack string) *corev1.Node {
	t.Helper()
	var node *corev1.Node
	require.Eventually(t, func() bool {
		node = nodes.snapshot(name)
		if node == nil {
			return false
		}
		a, err := sgpuassignment.DecodeAssignment(node.Annotations[sgpumetadata.AssignmentAnnotation])
		return err == nil && a.NodeUID == node.UID && a.AssignmentRevision == revision && a.Assigned == (rack != "") && a.Rack.Name == rack
	}, 10*time.Second, 10*time.Millisecond, "Node %s revision %d, rack %q", name, revision, rack)
	return node
}

func waitForRackDelivery(t *testing.T, client *mokkafake.Clientset, name string, match func(*api.SGPURack) bool) *api.RackDelivery {
	t.Helper()
	var r *api.SGPURack
	require.Eventually(t, func() bool {
		r = getAcceptanceRack(t.Context(), t, client, name)
		return r.Status.Delivery != nil && r.Status.Delivery.RackSpecGeneration == r.Generation && match(r)
	}, 15*time.Second, 10*time.Millisecond, "rack %s", name)
	return r.Status.Delivery
}

func requirePolicyDelivery(t *testing.T, client *mokkafake.Clientset, racks []string, generations map[string]int64, p *api.SGPURuntimePolicy) {
	t.Helper()
	for _, name := range racks {
		d := waitForRackDelivery(t, client, name, func(r *api.SGPURack) bool {
			refs := r.Status.Delivery.AcceptedPolicies
			return len(refs) == 1 && refs[0].UID == p.UID && refs[0].Generation == p.Generation
		})
		require.Equal(t, generations[name]+1, d.Generation)
		generations[name] = d.Generation
	}
	writes := map[string]int{}
	for _, a := range client.Actions() {
		if a.GetVerb() == "update" && a.GetResource().Resource == "sgpuracks" && a.GetSubresource() == "status" {
			obj := a.(k8stesting.UpdateAction).GetObject().(*api.SGPURack)
			writes[obj.Name]++
		}
	}
	require.Equal(t, map[string]int{racks[0]: 1, racks[1]: 1}, writes)
	client.ClearActions()
}

func TestDeliveryRetainsLastAdmittedInventory(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	profile, inventory := acceptanceProfile(2), acceptanceInventory()
	client := mokkafake.NewSimpleClientset(profile, inventory)
	installAcceptanceAPIReactors(t, client)
	leader, err := newForNodes(newAcceptanceNodeClient(), client, Options{Workers: 2, StatusDebounce: 5 * time.Millisecond})
	require.NoError(t, err)
	done := make(chan error, 1)
	go func() { done <- runControllerForTest(ctx, leader) }()
	t.Cleanup(func() { cancel(); <-done })
	name := rackrender.RackName(inventory.Name, inventory.UID, "compute", 0)
	before := waitForRackDelivery(t, client, name, func(r *api.SGPURack) bool { return r.Status.Delivery.Generation >= 1 })
	current, err := client.MokkaV1alpha1().SGPUInventories().Get(ctx, inventory.Name, metav1.GetOptions{})
	require.NoError(t, err)
	current.Generation++
	current.Spec.RackGroups[0].Count = 50001
	_, err = client.MokkaV1alpha1().SGPUInventories().Update(ctx, current, metav1.UpdateOptions{})
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		i, err := client.MokkaV1alpha1().SGPUInventories().Get(ctx, inventory.Name, metav1.GetOptions{})
		if err != nil {
			return false
		}
		c := findCondition(i.Status.Conditions, api.InventoryConditionAccepted)
		return c != nil && c.ObservedGeneration == current.Generation && c.Reason == "CapacityExceeded"
	}, 15*time.Second, 10*time.Millisecond)
	require.Never(t, func() bool {
		return getAcceptanceRack(ctx, t, client, name).Status.Delivery.Generation != before.Generation
	}, 300*time.Millisecond, 10*time.Millisecond)
	// A later valid spec must publish even if the policy worker handled it before admission.
	current, err = client.MokkaV1alpha1().SGPUInventories().Get(ctx, inventory.Name, metav1.GetOptions{})
	require.NoError(t, err)
	current.Spec.RackGroups[0].Count = 1
	current.Generation++
	_, err = client.MokkaV1alpha1().SGPUInventories().Update(ctx, current, metav1.UpdateOptions{})
	require.NoError(t, err)
	after := waitForRackDelivery(t, client, name, func(r *api.SGPURack) bool { return r.Status.Delivery.InventoryRef.Generation == current.Generation })
	require.Equal(t, before.Generation+1, after.Generation)
}

func TestDeliveryRelistDetectsGenerationOnlyChanges(t *testing.T) {
	t.Parallel()
	inventory := acceptanceInventory()
	profile := acceptanceProfile(1)
	policy := testRuntimePolicy("policy", inventory.Name)
	rack := testRuntimeRack()
	nextInventory, nextProfile, nextPolicy, nextRack := inventory.DeepCopy(), profile.DeepCopy(), policy.DeepCopy(), rack.DeepCopy()
	nextInventory.Generation += 2
	nextProfile.Generation += 2
	nextPolicy.Generation += 2
	nextRack.Generation += 2
	require.False(t, inventoryUnchanged(inventory, nextInventory))
	require.False(t, profileUnchanged(profile, nextProfile))
	require.False(t, runtimePolicyUnchanged(policy, nextPolicy))
	require.False(t, rackUnchanged(rack, nextRack))
}

// A fresh controller starts with empty queues and caches. Only the API objects
// survive when the previous leader stops.
func TestLeaderResumesAssignmentPublication(t *testing.T) {
	for name, conflict := range map[string]bool{"pending binding": false, "newer Node revision during publish": true} {
		t.Run(name, func(t *testing.T) {
			profile, inventory := acceptanceProfile(1), acceptanceInventory()
			client := mokkafake.NewSimpleClientset(profile, inventory)
			installAcceptanceAPIReactors(t, client)
			nodes := newAcceptanceNodeClient()
			node := acceptanceNode("worker", "worker-uid", 1)
			nodes.create(node)
			rack := rackrender.RackName(inventory.Name, inventory.UID, "compute", 0)

			stop, _ := startPublicationLeader(t, nodes, client, true)
			waitForRackDelivery(t, client, rack, func(r *api.SGPURack) bool {
				ref := r.Spec.Nodes[0].NodeRef
				return ref != nil && ref.AssignmentRevision == 1
			})
			stop()
			require.Empty(t, nodes.snapshot(node.Name).Annotations[sgpumetadata.AssignmentAnnotation])

			expected := int64(1)
			var started <-chan struct{}
			var finish func()
			if conflict {
				started, finish = nodes.blockNextPatch()
				t.Cleanup(finish)
			}
			stop, _ = startPublicationLeader(t, nodes, client, false)
			if conflict {
				select {
				case <-started:
				case <-time.After(5 * time.Second):
					t.Fatal("leader did not attempt the pending write")
				}
				tombstone, err := sgpuassignment.EncodePublication(node.UID, 8, nil)
				require.NoError(t, err)
				nodes.update(node.Name, func(n *corev1.Node) {
					n.Annotations = map[string]string{sgpumetadata.AssignmentAnnotation: tombstone}
				})
				finish()
				require.Eventually(t, func() bool { return nodes.conflicts.Load() > 0 }, 5*time.Second, 10*time.Millisecond)
				expected = 9
			}
			published := waitForNodePublication(t, nodes, node.Name, expected, rack)
			stop()
			require.Equal(t, 1, nodes.patchCalls())
			stop, replayed := startPublicationLeader(t, nodes, client, false)
			select {
			case <-replayed:
			case <-time.After(5 * time.Second):
				t.Fatal("new leader did not reconcile the saved publication")
			}
			waitForRackDelivery(t, client, rack, func(r *api.SGPURack) bool {
				return r.Spec.Nodes[0].NodeRef != nil && r.Spec.Nodes[0].NodeRef.AssignmentRevision == expected
			})
			stop()
			require.Equal(t, published.ResourceVersion, nodes.snapshot(node.Name).ResourceVersion)
			require.Equal(t, 1, nodes.patchCalls())
		})
	}
}

func TestNewLeaderFinishesReleaseAfterTombstone(t *testing.T) {
	t.Parallel()
	profile, inventory := acceptanceProfile(1), acceptanceInventory()
	client := mokkafake.NewSimpleClientset(profile, inventory)
	installAcceptanceAPIReactors(t, client)
	nodes := newAcceptanceNodeClient()
	node := acceptanceNode("worker", "worker-uid", 1)
	nodes.create(node)
	rack := rackrender.RackName(inventory.Name, inventory.UID, "compute", 0)
	// Leave the two API objects exactly as they would be if the first leader
	// stopped after publishing the release and before saving the empty slot.
	var holdClear atomic.Bool
	client.PrependReactor("update", "sgpuracks", func(action k8stesting.Action) (bool, runtime.Object, error) {
		r := action.(k8stesting.UpdateAction).GetObject().(*api.SGPURack)
		if holdClear.Load() && action.GetSubresource() == "" && r.Spec.Nodes[0].NodeRef == nil {
			return true, nil, apierrors.NewServiceUnavailable("rack write interrupted")
		}
		return false, nil, nil
	})
	stop, _ := startPublicationLeader(t, nodes, client, false)
	waitForNodePublication(t, nodes, node.Name, 1, rack)
	holdClear.Store(true)
	nodes.update(node.Name, func(n *corev1.Node) { n.Labels["pool"] = "outside" })
	released := waitForNodePublication(t, nodes, node.Name, 2, "")
	stop()
	ref := getAcceptanceRack(t.Context(), t, client, rack).Spec.Nodes[0].NodeRef
	require.NotNil(t, ref)
	require.EqualValues(t, 1, ref.AssignmentRevision)
	writes := nodes.patchCalls()

	holdClear.Store(false)
	stop, _ = startPublicationLeader(t, nodes, client, false)
	waitForRackDelivery(t, client, rack, func(r *api.SGPURack) bool { return r.Spec.Nodes[0].NodeRef == nil })
	require.Equal(t, writes, nodes.patchCalls())
	require.Equal(t, released.ResourceVersion, nodes.snapshot(node.Name).ResourceVersion)
	nodes.update(node.Name, func(n *corev1.Node) { n.Labels["pool"] = "acceptance" })
	waitForNodePublication(t, nodes, node.Name, 3, rack)
	stop()
}

func startPublicationLeader(t *testing.T, nodes *acceptanceNodeClient, client *mokkafake.Clientset, holdPublication bool) (func(), <-chan struct{}) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	leader, err := newForNodes(nodes, client, Options{Workers: 1, StatusDebounce: 5 * time.Millisecond})
	require.NoError(t, err)
	projected := make(chan struct{})
	observed := sync.OnceFunc(func() { close(projected) })
	reconcile := leader.reconcileProjection
	leader.reconcileProjection = func(ctx context.Context, key projectionKey) error {
		if holdPublication {
			return nil
		}
		err := reconcile(ctx, key)
		if err == nil && key.mode == projectionApply {
			observed()
		}
		return err
	}
	done := make(chan error, 1)
	go func() { done <- runControllerForTest(ctx, leader) }()
	stop := sync.OnceFunc(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("leader did not stop")
		}
	})
	t.Cleanup(stop)
	require.Eventually(t, leader.LeaderReady, 5*time.Second, 10*time.Millisecond)
	return stop, projected
}
