// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	mokkav1 "github.com/NVIDIA/k8s-test-infra/api/v1alpha1"
)

func TestMetricsFollowAssignedGPU(t *testing.T) {
	t.Parallel()
	p := &mokkav1.SGPURackProfile{}
	p.Spec.Node.GPUs.Memory.Capacity = resource.MustParse("40Gi")
	slot := &mokkav1.SGPURackNode{GPUs: []mokkav1.SGPURackGPU{{UUID: "first"}, {UUID: "second", Index: 1, MinorNumber: 1}}}
	pods := []corev1.Pod{{ObjectMeta: metav1.ObjectMeta{Name: "work", Namespace: "demo", UID: "uid", Annotations: map[string]string{"mokka.nvidia.com/gpu-utilization": "72", "mokka.nvidia.com/gpu-memory-used": "25"}}}}
	var m gpuMetrics
	m.addNode("virtual", slot, p, pods, assignments{"uid": {"worker": {"second"}}})
	lines := strings.Split(strings.TrimSpace(m.String()), "\n")
	require.Len(t, lines, 6)
	require.Contains(t, lines[0], `Hostname="virtual",UUID="first"`)
	require.NotContains(t, lines[0], `pod="work"`)
	require.Contains(t, lines[3], `pod="work",namespace="demo",container="worker"} 72`)
	require.Contains(t, lines[4], "} 10240")
	require.Contains(t, lines[5], "} 30720")
}
