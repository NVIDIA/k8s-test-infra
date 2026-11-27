// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"

	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	coreclient "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/ptr"

	api "github.com/NVIDIA/k8s-test-infra/api/v1alpha1"
	"github.com/NVIDIA/k8s-test-infra/internal/sgpu/assignment"
	"github.com/NVIDIA/k8s-test-infra/internal/sgpu/inventory"
	client "github.com/NVIDIA/k8s-test-infra/pkg/generated/clientset/versioned/typed/api/v1alpha1"
	state "github.com/NVIDIA/k8s-test-infra/pkg/rackstate/v1alpha1"
)

type rackAllocator struct {
	slices       *gpuSlices
	dra          bool
	records      client.SGPURackAllocationInterface
	rackAPI      client.SGPURackInterface
	core         coreclient.CoreV1Interface
	racks, nodes cache.Store
	views        *rackViews
	recorder     record.EventRecorder
}

func (a *rackAllocator) run(ctx context.Context) {
	q := a.views.pods.allocations

	for {
		name, shut := q.Get()
		if shut {
			return
		}
		if err := a.reconcile(ctx, name); err != nil && ctx.Err() == nil {
			zap.L().Warn("Reconcile KWOK GPU allocations", zap.String("rack", name), zap.Error(err))
			q.AddRateLimited(name)
		} else {
			q.Forget(name)
		}
		q.Done(name)
	}
}

func (a *rackAllocator) reconcile(ctx context.Context, name string) error {
	obj, exists, err := a.racks.GetByKey(name)
	if err != nil || !exists {
		return err
	}
	rack := obj.(*api.SGPURack)
	var releaseErr error
	if d := rack.Status.Delivery; d != nil && len(d.ReleaseRevisions) > 0 {
		releaseErr = a.releaseSlots(ctx, rack)
	}
	// In DRA mode the scheduler owns device selection. Slot release still runs.
	if a.dra {
		return releaseErr
	}
	slots, ready := a.views.allocationSlots(rack)
	if !ready {
		return releaseErr
	}
	record, create, err := a.allocationRecord(ctx, rack)
	if err != nil {
		return errors.Join(releaseErr, err)
	}
	return errors.Join(releaseErr, a.reconcileAdmissions(ctx, rack, slots, record, create))
}

func (a *rackAllocator) allocationRecord(ctx context.Context, rack *api.SGPURack) (*api.SGPURackAllocation, bool, error) {
	record, err := a.records.Get(ctx, rack.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return api.NewSGPURackAllocation(rack), true, nil
	}
	if err != nil {
		return nil, false, err
	}
	if !record.OwnedByRack(rack) {
		return nil, false, fmt.Errorf("allocation is not owned by rack %s (%s)", rack.Name, rack.UID)
	}
	return record, false, nil
}

func (a *rackAllocator) reconcileAdmissions(ctx context.Context, rack *api.SGPURack, slots []nodeAllocationPlan, record *api.SGPURackAllocation, create bool) error {
	next, err := a.desiredNodes(ctx, slots, record.Nodes)
	if err != nil {
		return err
	}
	changed := !reflect.DeepEqual(record.Nodes, next)
	if !changed && !needsAdmission(slots) {
		return nil
	}
	current, err := a.currentRack(ctx, rack)
	if err != nil || !current {
		return err
	}
	if changed {
		record.Nodes = next
		if create {
			record, err = a.records.Create(ctx, record, metav1.CreateOptions{})
		} else {
			record, err = a.records.Update(ctx, record, metav1.UpdateOptions{})
		}
		if err != nil {
			return err
		}
	}
	return a.publishAdmissions(ctx, rack, slots, record)
}

// Each plan belongs to one reconciliation. A nil pending map means the
// node's binding or Pod cache is uncertain, so its saved entries stay intact.
type nodeAllocationPlan struct {
	name    string
	slot    state.NodePayload
	pending map[types.UID]*corev1.Pod
	kept    []api.PodAllocation
	publish []podGrant
	denied  []*corev1.Pod
}

func (a *rackAllocator) desiredNodes(ctx context.Context, plans []nodeAllocationPlan, saved []api.NodeAllocation) ([]api.NodeAllocation, error) {
	existing := make(map[string][]api.PodAllocation, len(saved))
	for _, n := range saved {
		existing[n.Name] = n.Pods
	}
	// A GPU can move between slots. Reserve every surviving assignment before
	// handing out any UUID, regardless of which slot we visit first.
	used := sets.New[string]()
	for i := range plans {
		p := &plans[i]
		p.kept = existing[p.name]
		p.pending = a.boundPods(p.slot)
		if p.pending != nil && len(p.kept) > 0 {
			if err := a.retainPods(ctx, p); err != nil {
				return nil, err
			}
		}
		for _, pod := range p.kept {
			for _, c := range pod.Containers {
				used.Insert(c.GPUs...)
			}
		}
	}
	var next []api.NodeAllocation
	for i := range plans {
		p := &plans[i]
		p.assignPending(used)
		if len(p.kept) > 0 {
			next = append(next, api.NodeAllocation{Name: p.name, Pods: p.kept})
		}
	}
	return next, nil
}

func (a *rackAllocator) currentRack(ctx context.Context, rack *api.SGPURack) (bool, error) {
	current, err := a.rackAPI.Get(ctx, rack.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return current.UID == rack.UID && current.Generation == rack.Generation && current.Labels[inventory.KWOKShardLabel] == rack.Labels[inventory.KWOKShardLabel] &&
		current.Status.Delivery != nil && current.Status.Delivery.Generation == rack.Status.Delivery.Generation, nil
}

func (item *trackedRack) currentFor(rack *api.SGPURack) bool {
	return item != nil && item.ref.UID == rack.UID && item.applied > 0 && item.applied == item.wanted &&
		rack.Status.Delivery != nil && item.applied == rack.Status.Delivery.Generation
}

func (v *rackViews) allocationSlots(rack *api.SGPURack) ([]nodeAllocationPlan, bool) {
	v.mu.RLock()
	defer v.mu.RUnlock()
	item := v.owned[rack.Name]
	if !item.currentFor(rack) {
		return nil, false
	}
	slots := make([]nodeAllocationPlan, 0, len(item.members))
	for uid, name := range item.members {
		plan := nodeAllocationPlan{name: name}
		if slot, ok := v.nodes[uid]; ok && slot.Hardware.Rack == item.ref {
			if slot.Releasing {
				continue
			}
			plan.slot = slot
		}
		slots = append(slots, plan)
	}
	slices.SortFunc(slots, func(x, y nodeAllocationPlan) int { return cmp.Compare(x.name, y.name) })
	return slots, true
}

func (a *rackAllocator) boundPods(slot state.NodePayload) map[types.UID]*corev1.Pod {
	if slot.Node.UID == "" {
		return nil
	}
	obj, ok, err := a.nodes.GetByKey(slot.Node.Name)
	if err != nil || !ok {
		return nil
	}
	node := obj.(*nodeObservation)
	if !node.kwok || !node.assigned || node.UID != slot.Node.UID {
		return nil
	}
	if !matchesNodeAssignment(slot, node.assignment) {
		return nil
	}
	return a.views.pods.allocationSnapshot(slot.Node.Name)
}

func matchesNodeAssignment(slot state.NodePayload, raw string) bool {
	binding, err := assignment.DecodeAssignment(raw)
	return err == nil && matchesPublication(slot, binding)
}

func (a *rackAllocator) retainPods(ctx context.Context, p *nodeAllocationPlan) error {
	if err := a.confirmMissing(ctx, p); err != nil {
		return fmt.Errorf("confirm saved Pods for node %s: %w", p.name, err)
	}
	inventory := sets.New[string]()
	for _, gpu := range p.slot.GPUs {
		inventory.Insert(gpu.UUID)
	}
	var kept []api.PodAllocation
	for _, saved := range p.kept {
		pod := p.pending[saved.UID]
		delete(p.pending, saved.UID)
		if pod == nil || terminalPod(pod) {
			continue
		}
		kept = append(kept, saved)
		valid := true
		for _, c := range saved.Containers {
			if missing := sets.New(c.GPUs...).Difference(inventory); len(missing) != 0 {
				valid = false
				a.recorder.Eventf(pod, corev1.EventTypeWarning, "GPUAllocationMismatch", "Container %s has saved GPU UUIDs %v outside the current slot for node %s. These UUIDs remain reserved in rack %s until release.", c.Name, sets.List(missing), p.name, p.slot.Hardware.Rack.Name)
			}
		}
		p.syncGrant(pod, saved, valid)
	}
	p.kept = kept
	return nil
}

// A concurrent writer can see Pods before our watch does. A node-scoped list
// confirms a batch of missing UIDs in one request during a mass deletion.
func (a *rackAllocator) confirmMissing(ctx context.Context, p *nodeAllocationPlan) error {
	var only api.PodAllocation
	missing := sets.New[types.UID]()
	for _, saved := range p.kept {
		if p.pending[saved.UID] == nil {
			missing.Insert(saved.UID)
			only = saved
		}
	}
	switch len(missing) {
	case 0:
		return nil
	case 1:
		return a.confirmPod(ctx, p, only)
	}
	list, err := a.core.Pods(metav1.NamespaceAll).List(ctx, metav1.ListOptions{FieldSelector: fields.OneTermEqualSelector("spec.nodeName", p.name).String()})
	if err != nil {
		return err
	}
	for i := range list.Items {
		pod := &list.Items[i]
		if missing.Has(pod.UID) && pod.Spec.NodeName == p.name {
			p.pending[pod.UID] = pod
		}
	}
	return nil
}

func (a *rackAllocator) confirmPod(ctx context.Context, p *nodeAllocationPlan, saved api.PodAllocation) error {
	ns, name, err := cache.SplitMetaNamespaceKey(saved.Pod)
	if err != nil {
		return err
	}
	pod, err := a.core.Pods(ns).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if pod.UID == saved.UID && pod.Spec.NodeName == p.name {
		p.pending[pod.UID] = pod
	}
	return nil
}

func (p *nodeAllocationPlan) assignPending(used sets.Set[string]) {
	if len(p.pending) == 0 {
		return
	}
	var free []string
	seen := sets.New[string]()
	for _, gpu := range p.slot.GPUs {
		if !used.Has(gpu.UUID) && !seen.Has(gpu.UUID) {
			free = append(free, gpu.UUID)
			seen.Insert(gpu.UUID)
		}
	}
	for _, pod := range pendingPods(p.pending) {
		allocation, count, _ := allocatePod(pod, free)
		if allocation != nil {
			p.syncGrant(pod, *allocation, true)
			// A claim-only Pod passes this gate without reserving a whole GPU.
			if count == 0 {
				continue
			}
			p.kept = append(p.kept, *allocation)
			used.Insert(free[:count]...)
			free = free[count:]
		} else if awaitingAdmission(pod) {
			p.denied = append(p.denied, pod)
		}
	}
}

func pendingPods(live map[types.UID]*corev1.Pod) []*corev1.Pod {
	pending := make([]*corev1.Pod, 0, len(live))
	for _, p := range live {
		if p.DeletionTimestamp == nil && !terminalPod(p) {
			pending = append(pending, p)
		}
	}
	slices.SortFunc(pending, func(x, y *corev1.Pod) int {
		if c := x.CreationTimestamp.Time.Compare(y.CreationTimestamp.Time); c != 0 {
			return c
		}
		return cmp.Compare(x.UID, y.UID)
	})
	return pending
}

func terminalPod(pod *corev1.Pod) bool {
	return pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed
}

func requestedGPUs(c *corev1.Container) int64 {
	q, ok := c.Resources.Requests[gpuResource]
	if !ok {
		q = c.Resources.Limits[gpuResource]
	}
	return q.Value()
}

// Ordinary init containers finish before the next container starts. A
// restartable init container holds its GPU while later containers run.
func allocatePod(p *corev1.Pod, free []string) (*api.PodAllocation, int, error) {
	allocation := api.PodAllocation{Pod: cache.MetaObjectToName(p).String(), UID: p.UID}
	persistent, peak := 0, 0
	assign := func(c *corev1.Container, keep bool) error {
		count := requestedGPUs(c)
		if count == 0 {
			return nil
		}
		if count < 0 || count > int64(len(free)-persistent) {
			// The kubelet uses GPUs from completed init containers first.
			return fmt.Errorf("requested number of devices unavailable for %s. Requested: %d, Available: %d", gpuResource, count-int64(peak-persistent), len(free)-peak)
		}
		end := persistent + int(count)
		peak = max(peak, end)
		allocation.Containers = append(allocation.Containers, api.ContainerAllocation{Name: c.Name, GPUs: free[persistent:end]})
		if keep {
			persistent = end
		}
		return nil
	}
	for i := range p.Spec.InitContainers {
		c := &p.Spec.InitContainers[i]
		if err := assign(c, ptr.Deref(c.RestartPolicy, "") == corev1.ContainerRestartPolicyAlways); err != nil {
			return nil, 0, err
		}
	}
	for i := range p.Spec.Containers {
		if err := assign(&p.Spec.Containers[i], true); err != nil {
			return nil, 0, err
		}
	}
	return &allocation, peak, nil
}
