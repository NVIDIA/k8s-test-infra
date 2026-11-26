// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package main

import (
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
	corev1 "k8s.io/api/core/v1"
	resourcev1 "k8s.io/api/resource/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kubefake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/ptr"
)

func TestOptionalAPIs(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name     string
		features features
		required map[string][]string
	}{
		{"default", features{}, nil},
		{"DRA", features{gpuPublishing: "DRA", gpuDriver: "gpu.nvidia.com"}, map[string][]string{
			"resource.k8s.io/v1": {"resourceslices", "resourceclaims", "deviceclasses"},
		}},
		{"domains", features{computeDomains: true}, map[string][]string{
			"resource.k8s.io/v1":          {"resourceslices", "resourceclaims"},
			"resource.nvidia.com/v1beta1": {"computedomains", "computedomaincliques"},
		}},
		{"topology", features{nodeResourceTopology: true}, map[string][]string{
			"topology.node.k8s.io/v1alpha2": {"noderesourcetopologies"},
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			kube := kubefake.NewSimpleClientset()
			for version, names := range tt.required {
				list := &metav1.APIResourceList{GroupVersion: version}
				for _, name := range names {
					list.APIResources = append(list.APIResources, metav1.APIResource{Name: name})
				}
				kube.Resources = append(kube.Resources, list)
			}
			require.NoError(t, tt.features.checkAPIs(t.Context(), kube))
			for _, list := range kube.Resources {
				saved := list.APIResources
				for i, resource := range saved {
					list.APIResources = append(append([]metav1.APIResource(nil), saved[:i]...), saved[i+1:]...)
					require.ErrorContains(t, tt.features.checkAPIs(t.Context(), kube), resource.Name)
				}
				list.APIResources = saved
			}
		})
	}
}

func TestDRADeviceClassWarning(t *testing.T) {
	logs, recorded := observer.New(zap.WarnLevel)
	restore := zap.ReplaceGlobals(zap.New(logs))
	t.Cleanup(restore)
	f := features{gpuPublishing: "DRA", gpuDriver: "example.com"}
	kube := kubefake.NewSimpleClientset()
	kube.Resources = []*metav1.APIResourceList{{GroupVersion: "resource.k8s.io/v1", APIResources: []metav1.APIResource{
		{Name: "resourceslices"}, {Name: "resourceclaims"}, {Name: "deviceclasses"},
	}}}
	ctx := t.Context()
	require.NoError(t, f.checkAPIs(ctx, kube))
	classes := kube.ResourceV1().DeviceClasses()
	class, err := classes.Create(ctx, &resourcev1.DeviceClass{ObjectMeta: metav1.ObjectMeta{Name: f.gpuDriver}}, metav1.CreateOptions{})
	require.NoError(t, err)
	require.NoError(t, f.checkAPIs(ctx, kube))
	require.Len(t, recorded.All(), 2)
	class.Spec.ExtendedResourceName = ptr.To(gpuResource)
	_, err = classes.Update(ctx, class, metav1.UpdateOptions{})
	require.NoError(t, err)
	require.NoError(t, f.checkAPIs(ctx, kube))
	require.Len(t, recorded.All(), 2)
}

func TestDRACapacityEvent(t *testing.T) {
	t.Parallel()
	rec := record.NewFakeRecorder(4)
	p := labelPublisher{dra: true, recorder: rec}
	node := &nodeObservation{kwok: true, gpu: gpuStatus{capacity: gpuStatusField{value: "8"}}}
	p.warnDRACapacity(node)
	require.Contains(t, <-rec.Events, corev1.EventTypeWarning+" GPUCapacityConflict")
	p.nodeUpdated(node, node)
	node.kwok = false
	p.warnDRACapacity(node)
	node.kwok = true
	p.dra = false
	p.warnDRACapacity(node)
	require.Empty(t, rec.Events)
}

func TestDRALeavesDeviceSelectionToScheduler(t *testing.T) {
	t.Parallel()
	pod := boundTestPod("new", "worker")
	f := newAllocationFixture(t, nil, map[string][]string{"worker": {"gpu-a"}}, pod)
	f.allocator.dra = true
	require.NoError(t, f.allocator.reconcile(t.Context(), f.rack.Name))
	require.Empty(t, f.record(t))
	require.Empty(t, f.kube.Actions())
}
