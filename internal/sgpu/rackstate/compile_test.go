// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package rackstate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/yaml"

	api "github.com/NVIDIA/k8s-test-infra/api/v1alpha1"
	"github.com/NVIDIA/k8s-test-infra/internal/sgpu/allocate"
	"github.com/NVIDIA/k8s-test-infra/internal/sgpu/rackrender"
	"github.com/NVIDIA/k8s-test-infra/internal/sgpu/release"
	state "github.com/NVIDIA/k8s-test-infra/pkg/rackstate/v1alpha1"
)

func deliveredRack(t *testing.T, variant string) (*api.SGPURack, *api.SGPURackProfile) {
	t.Helper()
	path := "testdata/gb300-nvl72.yaml"
	if variant != "gb300-nvl72" {
		path = "testdata/pcie-only.yaml"
	}
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	p := new(api.SGPURackProfile)
	require.NoError(t, yaml.UnmarshalStrict(data, p))
	p.UID, p.Generation = "profile-id", 4
	if variant == "multiple-roots" {
		p.Spec.Node.GPUs.Count = 2
		p.Spec.Node.Topology.GPUSlots = append(p.Spec.Node.Topology.GPUSlots, api.GPUSlot{
			Index: 1, PCIAddress: "0001:41:00.0", RootComplex: "pci0001:40", NumaNode: 1, HostProcessorIndex: 1,
		})
	}
	rendered, err := rackrender.RenderRack(rackrender.RackInput{
		InventoryName: "demo", InventoryUID: "inventory-id", Profile: p, Group: api.RackGroup{ID: "train", Count: 1},
	})
	require.NoError(t, err)
	r := &api.SGPURack{ObjectMeta: metav1.ObjectMeta{Name: rendered.Name, UID: "rack-id", Generation: 6}, Spec: rendered.Spec}
	r.Status.Delivery = &api.RackDelivery{Generation: 13, RackSpecGeneration: r.Generation,
		InventoryRef: api.DeliveryObjectReference{UID: "inventory-id", Generation: 1},
		ProfileRef:   api.DeliveryProfileReference{DeliveryObjectReference: api.DeliveryObjectReference{UID: p.UID, Generation: p.Generation}, Revision: r.Spec.ProfileRef.Revision}}
	r.Spec.Nodes[0].NodeRef = &api.SGPUNodeReference{Name: "worker-a", UID: "node-a", AssignmentRevision: 7}
	if variant == "gb300-nvl72" {
		require.Len(t, r.Spec.Nodes, 18)
		r.Spec.Nodes[17].NodeRef = &api.SGPUNodeReference{Name: "worker-z", UID: "node-z", AssignmentRevision: 3}
	}
	return r, p
}

func TestStaticRackState(t *testing.T) {
	t.Parallel()
	for _, variant := range []string{"gb300-nvl72", "pcie-only", "multiple-roots"} {
		t.Run(variant, func(t *testing.T) {
			t.Parallel()
			r, p := deliveredRack(t, variant)
			slices.Reverse(r.Spec.Nodes[0].GPUs)
			slices.Reverse(r.Spec.Nodes)
			originalRack, originalProfile := r.DeepCopy(), p.DeepCopy()
			got, err := Compile(r, p, Observations{})
			require.NoError(t, err)
			raw, err := json.MarshalIndent(got, "", "  ")
			require.NoError(t, err)
			path := filepath.Join("testdata", variant+".golden.json")
			want, err := os.ReadFile(path)
			require.NoError(t, err)
			require.JSONEq(t, string(want), string(raw))

			node, ok := got.ForNode(state.Object{Name: "worker-a", UID: "node-a"})
			require.True(t, ok)
			require.Equal(t, state.Version, node.APIVersion)
			require.Equal(t, got.Hardware, node.Hardware)
			require.Equal(t, got.Nodes[0], node.Slot)
			var nodeJSON map[string]json.RawMessage
			encoded, err := json.Marshal(node)
			require.NoError(t, err)
			require.NoError(t, json.Unmarshal(encoded, &nodeJSON))
			require.JSONEq(t, `"v1alpha1"`, string(nodeJSON["apiVersion"]))
			require.JSONEq(t, `{"name":"worker-a","uid":"node-a"}`, string(nodeJSON["node"]))
			require.JSONEq(t, "7", string(nodeJSON["assignmentRevision"]))
			_, ok = got.ForNode(state.Object{Name: "worker-a", UID: "replacement"})
			require.False(t, ok)
			require.Equal(t, originalRack, r)
			require.Equal(t, originalProfile, p)
		})
	}
}

func TestRejectUnpublishedInputs(t *testing.T) {
	t.Parallel()
	for name, edit := range map[string]func(*api.SGPURack, *api.SGPURackProfile){
		"legacy rack":             func(r *api.SGPURack, _ *api.SGPURackProfile) { r.Status.Delivery = nil },
		"new rack spec":           func(r *api.SGPURack, _ *api.SGPURackProfile) { r.Generation++ },
		"recreated profile":       func(_ *api.SGPURack, p *api.SGPURackProfile) { p.UID = "replacement" },
		"new profile generation":  func(_ *api.SGPURack, p *api.SGPURackProfile) { p.Generation++ },
		"wrong content":           func(_ *api.SGPURack, p *api.SGPURackProfile) { p.Spec.Software.DriverVersion = "changed" },
		"stale rendered topology": func(r *api.SGPURack, _ *api.SGPURackProfile) { r.Spec.ProfileRef.Revision = "old" },
		"unversioned binding":     func(r *api.SGPURack, _ *api.SGPURackProfile) { r.Spec.Nodes[0].NodeRef.AssignmentRevision = 0 },
	} {
		t.Run(name, func(t *testing.T) {
			r, p := deliveredRack(t, "pcie-only")
			edit(r, p)
			got, err := Compile(r, p, Observations{})
			require.Error(t, err)
			require.Zero(t, got)
		})
	}
}

func TestDeliveredRuntime(t *testing.T) {
	t.Parallel()
	r, p := deliveredRack(t, "gb300-nvl72")
	defaults := &api.RuntimeState{DeviceState: api.DeviceStateHealthy, Telemetry: &api.RuntimeTelemetry{PerformanceState: "P0"}}
	p.Spec.Defaults = &api.SGPURackProfileDefaults{Runtime: defaults}
	revision, err := rackrender.ProfileRevision(p.Spec)
	require.NoError(t, err)
	r.Spec.ProfileRef.Revision, r.Status.Delivery.ProfileRef.Revision = revision, revision
	inputs := Observations{Policies: make(map[types.UID]*api.SGPURuntimePolicy)}
	steps := []struct {
		scope                      string
		target                     api.PolicyTargetRef
		value, otherGPU, otherNode string
	}{
		{"inventory", api.PolicyTargetRef{}, "P1", "P1", "P1"},
		{"group", api.PolicyTargetRef{RackGroups: []string{"train"}}, "P2", "P2", "P2"},
		{"rack", api.PolicyTargetRef{RackIndexes: []int32{0}}, "P3", "P3", "P3"},
		{"node", api.PolicyTargetRef{NodeIndexes: []int32{0}}, "P4", "P4", "P3"},
		{"gpu", api.PolicyTargetRef{NodeIndexes: []int32{0}, GPUIndexes: []int32{0}}, "P5", "P4", "P3"},
	}
	for _, step := range steps {
		target := step.target
		target.Group, target.Kind, target.Name = api.GroupName, "SGPUInventory", "demo"
		uid := types.UID(step.scope)
		inputs.Policies[uid] = &api.SGPURuntimePolicy{
			ObjectMeta: metav1.ObjectMeta{UID: uid, Generation: 1},
			Spec: api.SGPURuntimePolicySpec{
				TargetRef: target,
				Runtime:   &api.RuntimeState{Telemetry: &api.RuntimeTelemetry{PerformanceState: step.value}},
			},
		}
		if step.scope == "gpu" {
			inputs.Policies[uid].Spec.Runtime.Modes = &api.RuntimeModes{Compute: "Exclusive"}
		}
		// The published list is not in precedence order.
		r.Status.Delivery.AcceptedPolicies = append([]api.DeliveryObjectReference{{UID: uid, Generation: 1}}, r.Status.Delivery.AcceptedPolicies...)
		got, err := Compile(r, p, inputs)
		require.NoError(t, err)
		require.Equal(t, step.value, got.Nodes[0].GPUs[0].Runtime.Telemetry.PerformanceState)
		require.Equal(t, api.DeviceStateHealthy, got.Nodes[0].GPUs[0].Runtime.DeviceState)
		require.Equal(t, step.otherGPU, got.Nodes[0].GPUs[1].Runtime.Telemetry.PerformanceState)
		require.Equal(t, step.otherNode, got.Nodes[1].GPUs[0].Runtime.Telemetry.PerformanceState)
	}
	// The previous winner is still in the cache when its successor is published.
	successor := inputs.Policies["gpu"].DeepCopy()
	successor.Spec.Runtime.Modes = nil
	successor.UID = "replacement"
	successor.Spec.Runtime.Telemetry.PerformanceState = "P8"
	inputs.Policies[successor.UID] = successor
	r.Status.Delivery.AcceptedPolicies[0].UID = successor.UID
	got, err := Compile(r, p, inputs)
	require.NoError(t, err)
	require.Nil(t, got.Nodes[0].GPUs[0].Runtime.Modes)
	require.Equal(t, "P8", got.Nodes[0].GPUs[0].Runtime.Telemetry.PerformanceState)
	successor.Generation++
	got, err = Compile(r, p, inputs)
	require.Error(t, err)
	require.Zero(t, got)
	delete(inputs.Policies, successor.UID)
	got, err = Compile(r, p, inputs)
	require.Error(t, err)
	require.Zero(t, got)
}

func TestReleasingSlot(t *testing.T) {
	t.Parallel()
	rack, profile := deliveredRack(t, "gb300-nvl72")
	cleanup := release.ForSlot(rack, rack.Spec.Nodes[0], release.NodeGone)
	stale := cleanup
	stale.Binding.Coordinate.NodeIndex = 17
	stale.Binding.Node = allocate.NodeReference{Name: "worker-z", UID: "old-node"}
	got, err := Compile(rack, profile, Observations{PendingReleases: []release.Cleanup{cleanup, stale}})
	require.NoError(t, err)
	require.Len(t, got.Nodes, 2)
	node, ok := got.ForNode(state.Object{Name: "worker-a", UID: "node-a"})
	require.True(t, ok)
	slot, err := json.Marshal(node.Slot)
	require.NoError(t, err)
	require.JSONEq(t, `{"index":0,"node":{"name":"worker-a","uid":"node-a"},"assignmentRevision":7,"releasing":true}`, string(slot))
	require.False(t, got.Nodes[1].Releasing)
	require.Len(t, got.Nodes[1].GPUs, int(profile.Spec.Node.GPUs.Count))

	// The same Kubernetes Node can return to its old slot with a new assignment.
	rack.Spec.Nodes[0].NodeRef.AssignmentRevision = 9
	got, err = Compile(rack, profile, Observations{PendingReleases: []release.Cleanup{cleanup}})
	require.NoError(t, err)
	require.False(t, got.Nodes[0].Releasing)
	require.Len(t, got.Nodes[0].GPUs, int(profile.Spec.Node.GPUs.Count))
}
