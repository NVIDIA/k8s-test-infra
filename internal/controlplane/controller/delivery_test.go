// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package controller

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8stesting "k8s.io/client-go/testing"

	api "github.com/NVIDIA/k8s-test-infra/api/v1alpha1"
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
	// A move changes two rack specs. A release changes only the current rack.
	checkBinding := func(name string, bound bool) {
		t.Helper()
		d := waitForRackDelivery(t, client, name, func(r *api.SGPURack) bool {
			return (r.Spec.Nodes[0].NodeRef != nil) == bound && r.Status.Delivery.Generation > generations[name]
		})
		require.Equal(t, generations[name]+1, d.Generation)
		generations[name] = d.Generation
	}
	nodes.create(acceptanceNode("node-a", "node-uid", 1))
	checkBinding(racks[0], true)
	nodes.update("node-a", func(n *corev1.Node) { n.Labels["pool"] = "other" })
	checkBinding(racks[0], false)
	checkBinding(racks[2], true)
	nodes.delete("node-a")
	checkBinding(racks[2], false)
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
