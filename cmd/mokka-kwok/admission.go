// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"

	api "github.com/NVIDIA/k8s-test-infra/api/v1alpha1"
	"github.com/NVIDIA/k8s-test-infra/internal/sgpu/metadata"
)

const gpuAllocationAnnotation = "mokka.nvidia.com/gpu-allocation"

type podGrant struct {
	pod        *corev1.Pod
	annotation *string // nil removes a disputed grant from a Pending Pod.
}

func (p *nodeAllocationPlan) syncGrant(pod *corev1.Pod, saved api.PodAllocation, valid bool) {
	if pod.DeletionTimestamp != nil {
		return
	}
	previous, published := pod.Annotations[gpuAllocationAnnotation]
	if !valid {
		if published && pod.Status.Phase == corev1.PodPending {
			p.publish = append(p.publish, podGrant{pod: pod})
		}
		return
	}
	devices := make(map[string][]string, len(saved.Containers))
	for _, c := range saved.Containers {
		devices[c.Name] = c.GPUs
	}
	// A map of strings to string slices is always JSON-encodable.
	value, _ := json.Marshal(devices)
	annotation := string(value)
	if previous != annotation {
		p.publish = append(p.publish, podGrant{pod: pod, annotation: &annotation})
	}
}

func needsAdmission(plans []nodeAllocationPlan) bool {
	for i := range plans {
		if len(plans[i].publish)+len(plans[i].denied) > 0 {
			return true
		}
	}
	return false
}

func awaitingAdmission(p *corev1.Pod) bool {
	return p.DeletionTimestamp == nil && p.Status.Phase == corev1.PodPending && p.Status.PodIP != ""
}

// Metadata preconditions bind each write to the observed Pod instance and
// version. A replacement or a newer terminal status must never inherit it.
type admissionPatch struct {
	Metadata admissionMetadata `json:"metadata"`
	Status   map[string]string `json:"status,omitempty"`
}

type admissionMetadata struct {
	UID             types.UID          `json:"uid"`
	ResourceVersion string             `json:"resourceVersion"`
	Annotations     map[string]*string `json:"annotations,omitempty"`
}

func (a *rackAllocator) patchPod(ctx context.Context, pod *corev1.Pod, patch admissionPatch, subresource ...string) error {
	patch.Metadata.UID = pod.UID
	patch.Metadata.ResourceVersion = pod.ResourceVersion
	data, err := json.Marshal(patch)
	if err != nil {
		return err
	}
	_, err = a.core.Pods(pod.Namespace).Patch(ctx, pod.Name, types.MergePatchType, data, metav1.PatchOptions{}, subresource...)
	if apierrors.IsNotFound(err) {
		return nil
	}
	return err
}

func (a *rackAllocator) publishAdmissions(ctx context.Context, rack *api.SGPURack, plans []nodeAllocationPlan, record *api.SGPURackAllocation) error {
	var failures []error
	for i := range plans {
		p := &plans[i]
		for _, g := range p.publish {
			err := a.patchPod(ctx, g.pod, admissionPatch{Metadata: admissionMetadata{Annotations: map[string]*string{gpuAllocationAnnotation: g.annotation}}})
			if err != nil {
				failures = append(failures, err)
			}
		}
		if len(p.denied) > 0 {
			if err := a.confirmDenials(ctx, rack, p, record); err != nil {
				failures = append(failures, err)
			}
		}
	}
	return errors.Join(failures...)
}

// A stale watch or a UUID held for an uncertain slot can reduce the free
// pool temporarily. Only charge assignments of live Pods on this node when
// deciding whether to permanently fail an admission.
func (a *rackAllocator) confirmDenials(ctx context.Context, rack *api.SGPURack, p *nodeAllocationPlan, record *api.SGPURackAllocation) error {
	live, err := a.livePodsOnNode(ctx, p.name)
	if err != nil {
		return err
	}
	available := p.confirmedFree(live)
	for _, candidate := range p.denied {
		pod := live[candidate.UID]
		if pod == nil || !awaitingAdmission(pod) {
			continue
		}
		_, _, shortage := allocatePod(pod, available)
		if shortage == nil {
			continue
		}
		current, err := a.denialCurrent(ctx, rack, p, record)
		if err != nil || !current {
			return err
		}
		patch := admissionPatch{Status: map[string]string{
			"phase":   string(corev1.PodFailed),
			"reason":  "UnexpectedAdmissionError",
			"message": fmt.Sprintf("Pod was rejected: Allocate failed due to %s, which is unexpected", shortage),
		}}
		err = a.patchPod(ctx, pod, patch, "status")
		if err != nil {
			return err
		}
	}
	return nil
}

// A denial is terminal. Check the live binding and the rack delivery again
// after listing Pods, immediately before each status patch.
func (a *rackAllocator) denialCurrent(ctx context.Context, rack *api.SGPURack, p *nodeAllocationPlan, record *api.SGPURackAllocation) (bool, error) {
	if err := a.checkRecordVersion(ctx, record); err != nil {
		return false, err
	}
	node, err := a.core.Nodes().Get(ctx, p.name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if node.DeletionTimestamp != nil || node.UID != p.slot.Node.UID || !isKWOK(node.ObjectMeta) || node.Labels[metadata.AssignedLabel] != "true" || !matchesNodeAssignment(p.slot, node.Annotations[metadata.AssignmentAnnotation]) {
		return false, nil
	}
	return a.currentRack(ctx, rack)
}

func (a *rackAllocator) livePodsOnNode(ctx context.Context, node string) (map[types.UID]*corev1.Pod, error) {
	list, err := a.core.Pods(metav1.NamespaceAll).List(ctx, metav1.ListOptions{FieldSelector: fields.OneTermEqualSelector("spec.nodeName", node).String()})
	if err != nil {
		return nil, err
	}
	live := make(map[types.UID]*corev1.Pod, len(list.Items))
	for i := range list.Items {
		pod := &list.Items[i]
		if pod.Spec.NodeName == node && !terminalPod(pod) {
			live[pod.UID] = pod
		}
	}
	return live, nil
}

func (a *rackAllocator) checkRecordVersion(ctx context.Context, record *api.SGPURackAllocation) error {
	// A competing writer may already have admitted a Pod in this batch.
	latest, err := a.records.Get(ctx, record.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) && record.UID == "" {
		latest = record
	} else if err != nil {
		return err
	}
	if latest.UID != record.UID || latest.ResourceVersion != record.ResourceVersion {
		return fmt.Errorf("allocation for rack %s changed during admission", record.Name)
	}
	return nil
}

func (p *nodeAllocationPlan) confirmedFree(live map[types.UID]*corev1.Pod) []string {
	free := sets.New[string]()
	for _, gpu := range p.slot.GPUs {
		free.Insert(gpu.UUID)
	}
	for _, saved := range p.kept {
		if live[saved.UID] != nil {
			for _, c := range saved.Containers {
				free.Delete(c.GPUs...)
			}
		}
	}
	return free.UnsortedList()
}
