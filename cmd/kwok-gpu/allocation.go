// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"cmp"
	"slices"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
)

const gpuResource corev1.ResourceName = "nvidia.com/gpu"

type containerDevices map[string][]string
type assignments map[types.UID]containerDevices

// Allocate whole GPUs to regular containers. Pod UIDs survive controller restarts
// and distinguish a replacement Pod from its namesake.
func allocate(devices []string, previous assignments, pods []corev1.Pod) assignments {
	slices.SortFunc(pods, func(a, b corev1.Pod) int {
		if order := a.CreationTimestamp.Time.Compare(b.CreationTimestamp.Time); order != 0 {
			return order
		}
		return cmp.Compare(a.UID, b.UID)
	})
	next, free := retainAssignments(devices, previous, pods)
	remaining := slices.DeleteFunc(slices.Clone(devices), func(id string) bool { return !free[id] })
	for i := range pods {
		pod := &pods[i]
		if !active(pod) || next[pod.UID] != nil || pod.DeletionTimestamp != nil {
			continue
		}
		picked, used := pick(pod.Spec.Containers, remaining)
		if used > 0 {
			next[pod.UID] = picked
			remaining = remaining[used:]
		}
	}
	return next
}

func retainAssignments(devices []string, previous assignments, pods []corev1.Pod) (assignments, map[string]bool) {
	next := assignments{}
	free := make(map[string]bool, len(devices))
	for _, id := range devices {
		free[id] = true
	}
	// Keep existing assignments before considering new requests, even when a
	// rack changes or an older Pod was bound late.
	for i := range pods {
		pod := &pods[i]
		old := previous[pod.UID]
		if active(pod) && len(old) > 0 && available(old, free) {
			next[pod.UID] = old
			for _, ids := range old {
				for _, id := range ids {
					delete(free, id)
				}
			}
		}
	}
	return next, free
}

func active(pod *corev1.Pod) bool {
	return pod.Status.Phase != corev1.PodSucceeded && pod.Status.Phase != corev1.PodFailed
}

func available(allocation containerDevices, free map[string]bool) bool {
	for _, ids := range allocation {
		for _, id := range ids {
			if !free[id] {
				return false
			}
		}
	}
	return true
}

func pick(containers []corev1.Container, free []string) (containerDevices, int) {
	picked := containerDevices{}
	used := 0
	for _, c := range containers {
		q, ok := c.Resources.Requests[gpuResource]
		if !ok {
			q = c.Resources.Limits[gpuResource]
		}
		count := q.Value()
		if count > int64(len(free)-used) {
			return nil, 0
		}
		if count > 0 {
			end := used + int(count)
			picked[c.Name] = free[used:end]
			used = end
		}
	}
	return picked, used
}
