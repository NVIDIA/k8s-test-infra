// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package rackstate

import (
	"slices"
	"testing"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	api "github.com/NVIDIA/k8s-test-infra/api/v1alpha1"
	"github.com/NVIDIA/k8s-test-infra/internal/sgpu/policy"
)

type manifestInputs struct {
	rack      *api.SGPURack
	inventory *api.SGPUInventory
	profile   *api.SGPURackProfile
	policies  []*api.SGPURuntimePolicy
}

func manifestPolicy(name string, rackIndex int32) *api.SGPURuntimePolicy {
	return &api.SGPURuntimePolicy{
		ObjectMeta: metav1.ObjectMeta{Name: name, UID: types.UID(name), Generation: 1},
		Spec: api.SGPURuntimePolicySpec{
			TargetRef: api.PolicyTargetRef{Name: "demo", Kind: "SGPUInventory", Group: api.GroupName, RackIndexes: []int32{rackIndex}},
			Runtime:   &api.RuntimeState{DeviceState: api.DeviceStateFailed},
		},
	}
}

func (in *manifestInputs) observe(t *testing.T) (api.RackDelivery, bool) {
	t.Helper()
	e, err := policy.Evaluate(in.inventory.Name, in.inventory, map[string]*api.SGPURackProfile{in.profile.Name: in.profile}, in.policies)
	require.NoError(t, err)
	next, changed, err := DependencyManifest(in.rack, in.profile, e)
	require.NoError(t, err)
	return next, changed
}

func TestDependencyManifest(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		edit    func(*manifestInputs)
		changed bool
	}{
		{"unchanged", func(*manifestInputs) {}, false},
		{"first observation", func(i *manifestInputs) { i.rack.Status.Delivery = nil }, true},
		{"rack spec changed", func(i *manifestInputs) { i.rack.Generation++ }, true},
		{"inventory edited", func(i *manifestInputs) { i.inventory.Generation++ }, true},
		{"inventory replaced", func(i *manifestInputs) {
			i.inventory.UID = "new-inventory"
			i.rack.Spec.InventoryRef.UID = i.inventory.UID
		}, true},
		{"profile edited", func(i *manifestInputs) { i.profile.Generation++ }, true},
		{"profile replaced", func(i *manifestInputs) { i.profile.UID = "new-profile" }, true},
		{"profile content changed", func(i *manifestInputs) { i.profile.Spec.Software.DriverVersion = "changed" }, true},
		{"policy edited", func(i *manifestInputs) { i.policies[0].Generation++ }, true},
		{"policy removed", func(i *manifestInputs) { i.policies = slices.Delete(i.policies, 2, 3) }, true},
		{"policy added", func(i *manifestInputs) {
			p := manifestPolicy("gpu", 0)
			p.Spec.TargetRef.GPUIndexes = []int32{0}
			i.policies = append(i.policies, p)
		}, true},
		{"same-value replacement wins", func(i *manifestInputs) { i.policies = i.policies[1:] }, true},
		{"conflicting policy ignored", func(i *manifestInputs) { i.policies[3].Generation++ }, false},
		{"different rack ignored", func(i *manifestInputs) { i.policies[1].Generation++ }, false},
		{"policy order ignored", func(i *manifestInputs) {
			slices.Reverse(i.rack.Status.Delivery.AcceptedPolicies)
			slices.Reverse(i.policies)
		}, false},
		{"status updates ignored", func(i *manifestInputs) {
			i.inventory.Status.RackGroupsSummary = "new"
			i.rack.Status.AssignedNodes++
			i.rack.Status.Delivery.Generation++
			i.policies[0].Status.RackIndexesSummary = "new"
			i.profile.ResourceVersion = "42"
		}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r, p := deliveredRack(t, "pcie-only")
			inv := &api.SGPUInventory{
				ObjectMeta: metav1.ObjectMeta{Name: "demo", UID: "inventory-id", Generation: 3},
				Spec: api.SGPUInventorySpec{RackGroups: []api.RackGroup{
					{ID: "train", Count: 2, ProfileRef: api.ProfileReference{Name: p.Name}},
				}},
			}
			broad := manifestPolicy("broad", 0)
			broad.Spec.TargetRef.RackIndexes = nil
			in := manifestInputs{
				rack: r, inventory: inv, profile: p,
				policies: []*api.SGPURuntimePolicy{manifestPolicy("a-winner", 0), manifestPolicy("other", 1), broad, manifestPolicy("z-challenger", 0)},
			}
			base, _ := in.observe(t)
			require.Equal(t, []api.DeliveryObjectReference{{UID: "a-winner", Generation: 1}, {UID: "broad", Generation: 1}}, base.AcceptedPolicies)
			require.Equal(t, inv.UID, base.InventoryRef.UID)
			require.Equal(t, inv.Generation, base.InventoryRef.Generation)
			require.Equal(t, p.UID, base.ProfileRef.UID)
			require.Equal(t, p.Generation, base.ProfileRef.Generation)
			require.Equal(t, r.Spec.ProfileRef.Revision, base.ProfileRef.Revision)
			require.Equal(t, r.Generation, base.RackSpecGeneration)
			in.rack.Status.Delivery = &base
			tc.edit(&in)
			previous := in.rack.DeepCopy()
			next, changed := in.observe(t)
			require.Equal(t, tc.changed, changed)
			require.Equal(t, previous, in.rack)
			in.rack.Status.Delivery = &next
			_, changed = in.observe(t)
			require.False(t, changed)
		})
	}
}

func TestManifestRequiresEvaluatedProfile(t *testing.T) {
	t.Parallel()
	rack, current := deliveredRack(t, "multiple-roots")
	_, old := deliveredRack(t, "pcie-only")
	old.Generation = current.Generation - 1
	inventory := &api.SGPUInventory{
		ObjectMeta: metav1.ObjectMeta{Name: "demo", UID: "inventory-id", Generation: 1},
		Spec:       api.SGPUInventorySpec{RackGroups: []api.RackGroup{{ID: "train", Count: 1, ProfileRef: api.ProfileReference{Name: current.Name}}}},
	}
	gpu1 := manifestPolicy("gpu-1", 0)
	gpu1.Spec.TargetRef.GPUIndexes = []int32{1}
	evaluate := func(profile *api.SGPURackProfile) *policy.Evaluation {
		e, err := policy.Evaluate(inventory.Name, inventory, map[string]*api.SGPURackProfile{current.Name: profile}, []*api.SGPURuntimePolicy{gpu1})
		require.NoError(t, err)
		return e
	}
	replaced := current.DeepCopy()
	replaced.UID = "previous-profile"
	for _, stale := range []*api.SGPURackProfile{old, replaced, nil} {
		_, _, err := DependencyManifest(rack, current, evaluate(stale))
		require.Error(t, err)
	}
	manifest, _, err := DependencyManifest(rack, current, evaluate(current))
	require.NoError(t, err)
	require.Equal(t, []api.DeliveryObjectReference{{UID: gpu1.UID, Generation: gpu1.Generation}}, manifest.AcceptedPolicies)
	rack.Status.Delivery = &manifest
	got, err := Compile(rack, current, Observations{Policies: map[types.UID]*api.SGPURuntimePolicy{gpu1.UID: gpu1}})
	require.NoError(t, err)
	require.Equal(t, api.DeviceStateFailed, got.Nodes[0].GPUs[1].Runtime.DeviceState)

	// A removed group still needs a delivery record while its bindings retire.
	inventory.Spec.RackGroups = nil
	inventory.Generation++
	retiring, _, err := DependencyManifest(rack, current, evaluate(nil))
	require.NoError(t, err)
	require.Empty(t, retiring.AcceptedPolicies)
}
