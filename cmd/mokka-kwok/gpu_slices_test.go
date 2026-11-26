// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	resourcev1 "k8s.io/api/resource/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/utils/ptr"

	api "github.com/NVIDIA/k8s-test-infra/api/v1alpha1"
	"github.com/NVIDIA/k8s-test-infra/internal/sgpu/metadata"
	state "github.com/NVIDIA/k8s-test-infra/pkg/rackstate/v1alpha1"
)

// The golden devices come from kubectl get resourceslices on a CPU kind Node
// running the chart's gb300 profile and NVIDIA's v0.5.0 kubelet plugin.
// /sys/bus/pci/devices and /sys/devices were mounted read-only from the
// mock's /var/lib/nvml-mock/sys tree. Only device order was normalized.
func TestGPUSliceMatchesNVIDIAGB300(t *testing.T) {
	t.Parallel()
	slot := state.NodePayload{Hardware: state.Hardware{
		GPUModel: state.GPUModel{ProductName: "NVIDIA GB300 NVL", Architecture: "blackwell", ComputeCapability: "10.0", Memory: "278Gi", AddressingMode: "ATS"},
		Software: &api.SGPUSoftware{DriverVersion: "580.65.06", CUDAVersion: "13.0"},
	}}
	for i, bus := range []string{"0a", "0b", "4a", "4b"} {
		slot.GPUs = append(slot.GPUs, state.GPU{SGPURackGPU: api.SGPURackGPU{
			MinorNumber: int32(i), UUID: fmt.Sprintf("GPU-b300b300-0000-0000-0000-%012d", i),
			PCIAddress: "0000:" + bus + ":00.0", RootComplex: []string{"pci0000:00", "pci0000:40"}[i/2], NUMANode: int32(i / 2),
		}})
	}
	devices, err := sliceDevices(slot)
	require.NoError(t, err)
	actual, err := json.Marshal(devices)
	require.NoError(t, err)
	want, err := os.ReadFile("testdata/gb300-nvidia-dra-v0.5.0.json")
	require.NoError(t, err)
	require.JSONEq(t, string(want), string(actual))
}

func TestGPUSliceLifecycle(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	kube := fake.NewSimpleClientset()
	s := &gpuSlices{client: kube.ResourceV1().ResourceSlices(), driver: "gpu.example.com"}
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "worker", UID: "uid-a"}}
	slot := state.NodePayload{Hardware: state.Hardware{GPUModel: state.GPUModel{Memory: "4Gi"}}, Slot: state.Slot{GPUs: []state.GPU{{SGPURackGPU: api.SGPURackGPU{UUID: "GPU-A", MinorNumber: 7}}}}}
	require.NoError(t, s.publish(ctx, node, slot))
	get := func() *resourcev1.ResourceSlice {
		r, err := s.client.Get(ctx, sliceName(node.UID), metav1.GetOptions{})
		require.NoError(t, err)
		return r
	}
	r := get()
	require.True(t, ownsSlice(r, node.Name, node.UID))
	require.Equal(t, node.Name, *r.Spec.NodeName)
	require.Equal(t, s.driver, r.Spec.Driver)
	require.Equal(t, node.Name, r.Spec.Pool.Name)
	require.EqualValues(t, 1, r.Spec.Pool.ResourceSliceCount)
	require.Equal(t, "gpu-7", r.Spec.Devices[0].Name)
	kube.ClearActions()
	require.NoError(t, s.publish(ctx, node, slot))
	require.Len(t, kube.Actions(), 1)
	require.Equal(t, "get", kube.Actions()[0].GetVerb())
	slot.GPUs[0].UUID = "GPU-B"
	require.NoError(t, s.publish(ctx, node, slot))
	r = get()
	require.EqualValues(t, 2, r.Spec.Pool.Generation)
	require.Equal(t, "GPU-B", *r.Spec.Devices[0].Attributes["uuid"].StringValue)

	// An object recreated at our name with a different owner is never adopted or deleted.
	r.OwnerReferences[0].UID = "other"
	_, err := s.client.Update(ctx, r, metav1.UpdateOptions{})
	require.NoError(t, err)
	require.ErrorContains(t, s.publish(ctx, node, slot), "different owner")
	require.NoError(t, s.remove(ctx, node.Name, node.UID))
	r = get()
	r.OwnerReferences[0].UID = node.UID
	_, err = s.client.Update(ctx, r, metav1.UpdateOptions{})
	require.NoError(t, err)
	require.ErrorContains(t, s.remove(ctx, node.Name, node.UID), "waiting for deletion")
	require.NoError(t, s.remove(ctx, node.Name, node.UID))
}

func TestDeletionPrecedesWithdrawalAndModeChange(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	now := metav1.Now()
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "worker", UID: "uid", Annotations: map[string]string{gpuSliceKey: sliceName("uid")}}}
	slice := &resourcev1.ResourceSlice{ObjectMeta: metav1.ObjectMeta{
		Name: sliceName(node.UID), Finalizers: []string{"example.com/hold"}, DeletionTimestamp: &now,
		OwnerReferences: []metav1.OwnerReference{{Kind: "Node", APIVersion: "v1", Name: node.Name, UID: node.UID, Controller: ptr.To(true)}},
	}}
	kube := fake.NewSimpleClientset(node, slice)
	p := labelPublisher{nodes: kube.CoreV1().Nodes(), dra: true, slices: &gpuSlices{client: kube.ResourceV1().ResourceSlices()}}
	require.ErrorContains(t, p.publishResources(ctx, node, nil), "waiting for deletion")
	require.Len(t, kube.Actions(), 1)
	p.dra, p.slices = false, nil
	require.ErrorContains(t, p.publishResources(ctx, node, map[string]string{metadata.GPUCountLabel: "4"}), "DRA pool")
	require.Len(t, kube.Actions(), 1)
}
