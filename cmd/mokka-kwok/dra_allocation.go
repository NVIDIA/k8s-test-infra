// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package main

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	resourcev1 "k8s.io/api/resource/v1"
	scheduling "k8s.io/api/scheduling/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/tools/cache"

	api "github.com/NVIDIA/k8s-test-infra/api/v1alpha1"
)

type claimUse struct {
	claim, request string
	extended       bool
}

func containerClaims(pod *corev1.Pod, c *corev1.Container) ([]claimUse, bool) {
	names := podClaimNames(pod)
	var uses []claimUse
	for _, ref := range c.Resources.Claims {
		name := names[ref.Name]
		if name == "" {
			return nil, false
		}
		uses = append(uses, claimUse{claim: name, request: ref.Request})
	}
	if requestedGPUs(c) == 0 {
		return uses, true
	}
	status := pod.Status.ExtendedResourceClaimStatus
	if status == nil || status.ResourceClaimName == "" {
		return nil, false
	}
	start := len(uses)
	for _, m := range status.RequestMappings {
		if m.ContainerName == c.Name && m.ResourceName == string(gpuResource) {
			uses = append(uses, claimUse{claim: status.ResourceClaimName, request: m.RequestName, extended: true})
		}
	}
	return uses, len(uses) > start
}

func podClaimNames(pod *corev1.Pod) map[string]string {
	names := map[string]string{}
	for _, ref := range pod.Spec.ResourceClaims {
		if ref.ResourceClaimName != nil {
			names[ref.Name] = *ref.ResourceClaimName
		}
	}
	for _, status := range pod.Status.ResourceClaimStatuses {
		if status.ResourceClaimName != nil {
			names[status.Name] = *status.ResourceClaimName
		}
	}
	return names
}

// A name can be reused after deletion. Only copy a claim reserved for this
// consumer. The scheduler uses request/subrequest for firstAvailable results.
func claimUUIDs(claim *resourcev1.ResourceClaim, pod *corev1.Pod, use claimUse, driver string, devices map[string]string) ([]string, bool) {
	if claim == nil || claim.Status.Allocation == nil || !reservedForPod(claim, pod) {
		return nil, false
	}
	var ids []string
	for _, r := range claim.Status.Allocation.Devices.Results {
		request, _, _ := strings.Cut(r.Request, "/")
		if r.Driver != driver || !use.matches(request, r.Request) {
			continue
		}
		uuid := devices[r.Device]
		if r.Pool != pod.Spec.NodeName || uuid == "" {
			return nil, false
		}
		ids = append(ids, uuid)
	}
	return ids, !use.extended || len(ids) > 0
}

func (u claimUse) matches(request, full string) bool {
	return u.request == "" || u.request == request || u.request == full
}

func reservedForPod(claim *resourcev1.ResourceClaim, pod *corev1.Pod) bool {
	return slices.ContainsFunc(claim.Status.ReservedFor, func(r resourcev1.ResourceClaimConsumerReference) bool {
		if r.APIGroup == "" && r.Resource == "pods" {
			return r.Name == pod.Name && r.UID == pod.UID
		}
		// Kubernetes identifies a group reservation by the group name in the
		// same namespace. The Pod has no copy of its PodGroup UID.
		g := pod.Spec.SchedulingGroup
		return r.APIGroup == scheduling.GroupName && r.Resource == "podgroups" && g != nil && g.PodGroupName != nil && r.Name == *g.PodGroupName
	})
}

type nodeClaims struct {
	store   *shardClaims
	devices map[string]string
	loaded  map[cache.ObjectName]*resourcev1.ResourceClaim
}

func (n *nodeClaims) allocation(ctx context.Context, pod *corev1.Pod) (*api.PodAllocation, error) {
	a := &api.PodAllocation{Pod: cache.MetaObjectToName(pod).String(), UID: pod.UID}
	for _, containers := range [][]corev1.Container{pod.Spec.InitContainers, pod.Spec.Containers} {
		for i := range containers {
			c := &containers[i]
			ids, ready, err := n.container(ctx, pod, c)
			if !ready || err != nil {
				return nil, err
			}
			if len(ids) > 0 {
				a.Containers = append(a.Containers, api.ContainerAllocation{Name: c.Name, GPUs: ids})
			}
		}
	}
	return a, nil
}

func (n *nodeClaims) container(ctx context.Context, pod *corev1.Pod, c *corev1.Container) ([]string, bool, error) {
	uses, ready := containerClaims(pod, c)
	if !ready {
		return nil, false, nil
	}
	ids := sets.New[string]()
	for _, use := range uses {
		key := cache.ObjectName{Namespace: pod.Namespace, Name: use.claim}
		claim, cached := n.loaded[key]
		if !cached {
			var err error
			claim, err = n.store.get(ctx, pod.Spec.NodeName, key)
			if err != nil {
				return nil, false, err
			}
			n.loaded[key] = claim
		}
		picks, ok := claimUUIDs(claim, pod, use, n.store.driver, n.devices)
		if !ok {
			return nil, false, nil
		}
		ids.Insert(picks...)
	}
	return sets.List(ids), true, nil
}

func (a *rackAllocator) draPods(ctx context.Context, p *nodeAllocationPlan) error {
	if err := a.confirmMissing(ctx, p); err != nil {
		return err
	}
	saved := make(map[types.UID]api.PodAllocation, len(p.kept))
	for _, row := range p.kept {
		saved[row.UID] = row
	}
	p.kept = nil
	reader := nodeClaims{store: a.claims, devices: make(map[string]string, len(p.slot.GPUs)), loaded: make(map[cache.ObjectName]*resourcev1.ResourceClaim)}
	for _, g := range p.slot.GPUs {
		reader.devices[fmt.Sprintf("gpu-%d", g.MinorNumber)] = g.UUID
	}
	for _, id := range slices.Sorted(maps.Keys(p.pending)) {
		pod := p.pending[id]
		if terminalPod(pod) {
			continue
		}
		if pod.DeletionTimestamp != nil {
			if old, ok := saved[id]; ok {
				p.kept = append(p.kept, old)
			}
			continue
		}
		if err := a.copyGrant(ctx, p, &reader, pod); err != nil {
			return err
		}
	}
	return nil
}

func (a *rackAllocator) copyGrant(ctx context.Context, p *nodeAllocationPlan, reader *nodeClaims, pod *corev1.Pod) error {
	grant, err := reader.allocation(ctx, pod)
	if err != nil {
		return err
	}
	if grant == nil {
		if _, published := pod.Annotations[gpuAllocationAnnotation]; published {
			p.publish = append(p.publish, podGrant{pod: pod})
		}
		// A foreign driver's non-local pool may never produce a node-scoped
		// watch event. Retry while the claim or its reservation is unresolved.
		a.views.pods.allocations.AddAfter(p.slot.Hardware.Rack.Name, 10*time.Second)
		return nil
	}
	if len(grant.Containers) > 0 {
		p.kept = append(p.kept, *grant)
	}
	p.syncGrant(pod, *grant, true)
	return nil
}
