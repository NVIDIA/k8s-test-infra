// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package v1alpha1

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	apixv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

func TestRackAllocationSchema(t *testing.T) {
	t.Parallel()

	crd := loadCRD(t, "mokka.nvidia.com_sgpurackallocations.yaml")
	require.Equal(t, apixv1.ClusterScoped, crd.Spec.Scope)
	root := crd.Spec.Versions[0].Schema.OpenAPIV3Schema
	nodes := schemaProperty(t, root, "nodes")
	require.Equal(t, []string{"name"}, nodes.XListMapKeys)
	pods := schemaProperty(t, nodes.Items.Schema, "pods")
	require.Equal(t, []string{"uid"}, pods.XListMapKeys)
	require.ElementsMatch(t, []string{"pod", "uid", "containers"}, pods.Items.Schema.Required)
	containers := schemaProperty(t, pods.Items.Schema, "containers")
	require.Equal(t, []string{"name"}, containers.XListMapKeys)
	gpus := schemaProperty(t, containers.Items.Schema, "gpus")
	require.Equal(t, "set", *gpus.XListType)
	require.NotEmpty(t, gpus.Items.Schema.Pattern)
	require.Zero(t, *schemaProperty(t, containers.Items.Schema, "numaZone").Minimum)
}

func TestRackAllocationRoundTrip(t *testing.T) {
	t.Parallel()

	rack := &SGPURack{ObjectMeta: metav1.ObjectMeta{Name: "rack-a", UID: "rack-uid"}}
	record := NewSGPURackAllocation(rack)
	containers := []ContainerAllocation{
		{Name: "trainer", GPUs: []string{"GPU-00000000-0000-0000-0000-000000000001"}, NUMAZone: ptr.To(int32(0))},
		{Name: "logger"},
	}
	pod := PodAllocation{Pod: "team-a/train-0", UID: "pod-uid", Containers: containers}
	record.Nodes = []NodeAllocation{{Name: "node-a", Pods: []PodAllocation{pod}}}

	data, err := json.Marshal(record)
	require.NoError(t, err)
	var restored SGPURackAllocation
	require.NoError(t, json.Unmarshal(data, &restored))
	require.Equal(t, record, &restored)
	require.Equal(t, rack.Name, restored.Name)
	require.Len(t, restored.OwnerReferences, 1)
	owner := restored.OwnerReferences[0]
	require.Equal(t, GroupVersion.String(), owner.APIVersion)
	require.Equal(t, "SGPURack", owner.Kind)
	require.Equal(t, rack.Name, owner.Name)
	require.Equal(t, rack.UID, owner.UID)
	// The wire format must distinguish a container assigned zone zero from
	// one for which the data plane has no NUMA placement.
	require.Contains(t, string(data), `"numaZone":0`)
	require.Contains(t, string(data), `{"name":"logger"}`)
}

func TestRackAllocationOwnedByRack(t *testing.T) {
	t.Parallel()

	rack := &SGPURack{ObjectMeta: metav1.ObjectMeta{Name: "rack-a", UID: "current-uid"}}
	record := NewSGPURackAllocation(rack)
	require.True(t, record.OwnedByRack(rack))

	replacement := rack.DeepCopy()
	replacement.UID = "replacement-uid"
	require.False(t, record.OwnedByRack(replacement))

	for _, tc := range []struct {
		name string
		edit func(*SGPURackAllocation)
	}{
		{"wrong record name", func(a *SGPURackAllocation) { a.Name = "other-name" }},
		{"missing owner", func(a *SGPURackAllocation) { a.OwnerReferences = nil }},
		{"wrong owner name", func(a *SGPURackAllocation) { a.OwnerReferences[0].Name = "other-rack" }},
		{"wrong owner kind", func(a *SGPURackAllocation) { a.OwnerReferences[0].Kind = "SGPUInventory" }},
		{"wrong owner API", func(a *SGPURackAllocation) { a.OwnerReferences[0].APIVersion = "other.example/v1" }},
		{"additional owner", func(a *SGPURackAllocation) {
			a.OwnerReferences = append(a.OwnerReferences, metav1.OwnerReference{Name: "another-rack", UID: "other-uid"})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := record.DeepCopy()
			tc.edit(a)
			require.False(t, a.OwnedByRack(rack))
		})
	}

	unpersisted := &SGPURack{ObjectMeta: metav1.ObjectMeta{Name: rack.Name}}
	require.False(t, NewSGPURackAllocation(unpersisted).OwnedByRack(unpersisted))
	require.False(t, record.OwnedByRack(nil))
}
