//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/yaml"

	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/kube"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/pod"
)

// The scenario's pod shapes are asserted through the API types, so the checks
// hold whatever the renderer emits. framework/pod covers the rendering contract
// itself; this covers the choices this scenario makes on top of it — placement,
// the device and IMEX opt-ins, and the GPU request.
func TestNRIPodManifests(t *testing.T) {
	sleepForever := []string{"/bin/sh", "-c", "sleep 3600"}
	anyGPUNode := map[string]string{gpuPresentLabel: "true"}

	tests := map[string]struct {
		manifest     []byte
		image        string
		command      []string
		nodeName     string
		nodeSelector map[string]string
		annotations  map[string]string
		labels       map[string]string
		gpuLimit     string
	}{
		"request pins its node and requests GPUs": {
			manifest: nriRequestPodManifest("workload", "worker-1", 4),
			image:    nriWorkloadImage,
			command:  sleepForever,
			nodeName: "worker-1",
			gpuLimit: "4",
		},
		"request carries through extra annotations": {
			manifest: nriRequestPodManifest("workload", "", 1,
				map[string]string{nriDeviceAnnotation: "true"}),
			image:       nriWorkloadImage,
			command:     sleepForever,
			annotations: map[string]string{nriDeviceAnnotation: "true"},
			gpuLimit:    "1",
		},
		"annotated falls back to any GPU node": {
			manifest:     nriAnnotatedPodManifest("workload"),
			image:        nriWorkloadImage,
			command:      sleepForever,
			nodeSelector: anyGPUNode,
			annotations:  map[string]string{nriDeviceAnnotation: "true"},
		},
		"annotated prefers an explicit node over the selector": {
			manifest:    nriAnnotatedPodManifest("workload", "worker-2"),
			image:       nriWorkloadImage,
			command:     sleepForever,
			nodeName:    "worker-2",
			annotations: map[string]string{nriDeviceAnnotation: "true"},
		},
		"plain opts into nothing": {
			manifest:     nriPlainPodManifest("workload"),
			image:        nriWorkloadImage,
			command:      sleepForever,
			nodeSelector: anyGPUNode,
		},
		"minimal IB invokes the overlay tool by absolute path": {
			manifest:     nriMinimalIBPodManifest("workload", "ibstat", "-l"),
			image:        nriMinimalImage,
			command:      []string{nriOverlayBinDir + "/ibstat", "-l"},
			nodeSelector: anyGPUNode,
			labels:       map[string]string{"app": "workload"},
		},
		"imex opts into channels": {
			manifest:    nriImexPodManifest("workload", "worker-3", true),
			image:       nriWorkloadImage,
			command:     sleepForever,
			nodeName:    "worker-3",
			annotations: map[string]string{nriImexAnnotation: "true"},
		},
		"imex without the opt-in carries no annotation": {
			manifest: nriImexPodManifest("workload", "worker-3", false),
			image:    nriWorkloadImage,
			command:  sleepForever,
			nodeName: "worker-3",
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			var rendered corev1.Pod
			require.NoError(t, yaml.UnmarshalStrict(tc.manifest, &rendered),
				"manifest must be valid YAML:\n%s", tc.manifest)

			require.Equal(t, "workload", rendered.Name)
			require.Equal(t, nriWorkloadNS, rendered.Namespace)
			require.Equal(t, tc.annotations, rendered.Annotations)
			require.Equal(t, tc.labels, rendered.Labels)

			require.Equal(t, corev1.RestartPolicyNever, rendered.Spec.RestartPolicy)
			require.Equal(t, ptr.To(pod.DefaultGracePeriodSeconds), rendered.Spec.TerminationGracePeriodSeconds,
				"an uncapped grace period stalls every teardown for 30s")
			require.Equal(t, tc.nodeName, rendered.Spec.NodeName)
			require.Equal(t, tc.nodeSelector, rendered.Spec.NodeSelector)

			require.Len(t, rendered.Spec.Containers, 1)
			container := rendered.Spec.Containers[0]
			require.Equal(t, tc.image, container.Image)
			require.Equal(t, tc.command, container.Command)

			gpus, ok := container.Resources.Limits[kube.GPUResourceName]
			if tc.gpuLimit == "" {
				require.False(t, ok, "expected no GPU limit, got %s", gpus.String())
				return
			}
			require.True(t, ok, "expected a %s limit", kube.GPUResourceName)
			require.Equal(t, tc.gpuLimit, gpus.String())
		})
	}
}
