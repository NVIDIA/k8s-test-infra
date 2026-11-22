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
