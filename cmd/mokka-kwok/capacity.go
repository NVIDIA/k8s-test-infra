// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package main

import (
	"context"
	"encoding/json"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

const gpuResource = "nvidia.com/gpu"

type gpuStatusField struct {
	value          string
	owned, foreign bool
}

type gpuStatus struct {
	capacity, allocatable gpuStatusField
}

func readGPUStatus(node *corev1.Node) gpuStatus {
	var status gpuStatus
	if q, ok := node.Status.Capacity[gpuResource]; ok {
		status.capacity.value = q.String()
	}
	if q, ok := node.Status.Allocatable[gpuResource]; ok {
		status.allocatable.value = q.String()
	}
	for _, entry := range node.ManagedFields {
		if entry.FieldsV1 == nil || !strings.Contains(entry.FieldsV1.GetRawString(), `"f:nvidia.com/gpu"`) {
			continue
		}
		// An external node source can supply status on creation as well as
		// through /status. Only our updates through /status belong to us.
		var fields struct {
			Status struct {
				Capacity    map[string]json.RawMessage `json:"f:capacity"`
				Allocatable map[string]json.RawMessage `json:"f:allocatable"`
			} `json:"f:status"`
		}
		if err := json.Unmarshal(entry.FieldsV1.GetRawBytes(), &fields); err != nil {
			continue // The API server supplies structured FieldsV1 entries.
		}
		ours := entry.Manager == fieldManager && entry.Subresource == "status"
		if _, ok := fields.Status.Capacity["f:"+gpuResource]; ok {
			status.capacity.markOwner(ours)
		}
		if _, ok := fields.Status.Allocatable["f:"+gpuResource]; ok {
			status.allocatable.markOwner(ours)
		}
	}
	return status
}

func (f *gpuStatusField) markOwner(ours bool) {
	if ours {
		f.owned = true
	} else {
		f.foreign = true
	}
}

// Publish the two totals together. A partial update in the presence of a
// conflicting source could advertise more allocatable GPUs than capacity.
func (p *labelPublisher) publishCapacity(ctx context.Context, node *corev1.Node, desired string) (*corev1.Node, error) {
	gpu := readGPUStatus(node)
	fields := map[string]gpuStatusField{"capacity": gpu.capacity, "allocatable": gpu.allocatable}
	status := make(map[string]map[string]*string)
	for section, f := range fields {
		if f.value == desired {
			continue
		}
		if f.value != "" && (!f.owned || f.foreign) {
			if desired != "" {
				p.recorder.Eventf(node, corev1.EventTypeWarning, "GPUCapacityConflict",
					"Keeping existing status.%s[%s]=%s. The rack specifies %s", section, gpuResource, f.value, desired)
				return node, nil
			}
			continue
		}
		var value *string
		if desired != "" {
			value = &desired
		}
		status[section] = map[string]*string{gpuResource: value}
	}
	if len(status) == 0 {
		return node, nil
	}
	patch, err := json.Marshal(map[string]any{
		"metadata": map[string]string{"uid": string(node.UID), "resourceVersion": node.ResourceVersion},
		"status":   status,
	})
	if err != nil {
		return nil, err
	}
	return p.nodes.Patch(ctx, node.Name, types.MergePatchType, patch, metav1.PatchOptions{FieldManager: fieldManager}, "status")
}
