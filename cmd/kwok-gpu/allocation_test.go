// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

func TestAllocatePreservesLiveAssignmentsAndReusesReleasedDevices(t *testing.T) {
	t.Parallel()
	pod := func(id string, count int64) corev1.Pod {
		return corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: id, UID: types.UID(id)},
			Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "worker", Resources: corev1.ResourceRequirements{
				Limits: corev1.ResourceList{gpuResource: *resource.NewQuantity(count, resource.DecimalSI)},
			}}}}}
	}
	a, b, c := pod("a", 1), pod("b", 1), pod("c", 2)
	devices := []string{"GPU-A", "GPU-B"}
	first := allocate(devices, nil, []corev1.Pod{a, b, c})
	require.Equal(t, assignments{"a": {"worker": {"GPU-A"}}, "b": {"worker": {"GPU-B"}}}, first)
	require.Equal(t, first, allocate(devices, first, []corev1.Pod{c, b, a}))
	a.Status.Phase = corev1.PodFailed
	next := allocate(devices, first, []corev1.Pod{a, b, c})
	require.Equal(t, assignments{"b": {"worker": {"GPU-B"}}}, next)
	require.Equal(t, assignments{"c": {"worker": devices}}, allocate(devices, next, []corev1.Pod{c}))
}
