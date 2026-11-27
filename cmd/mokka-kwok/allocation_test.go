// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package main

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	kubefake "k8s.io/client-go/kubernetes/fake"
	clienttesting "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/ptr"

	api "github.com/NVIDIA/k8s-test-infra/api/v1alpha1"
	"github.com/NVIDIA/k8s-test-infra/internal/sgpu/allocate"
	"github.com/NVIDIA/k8s-test-infra/internal/sgpu/assignment"
	"github.com/NVIDIA/k8s-test-infra/internal/sgpu/metadata"
	mokkafake "github.com/NVIDIA/k8s-test-infra/pkg/generated/clientset/versioned/fake"
	state "github.com/NVIDIA/k8s-test-infra/pkg/rackstate/v1alpha1"
)

func requestingContainer(name, count string) corev1.Container {
	return corev1.Container{Name: name, Resources: corev1.ResourceRequirements{Limits: corev1.ResourceList{gpuResource: resource.MustParse(count)}}}
}

func TestAllocatePodSharesOnlyAcrossSequentialPhases(t *testing.T) {
	t.Parallel()
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "train", Namespace: "team", UID: "train-uid"}}
	sidecar := requestingContainer("sidecar", "1")
	sidecar.RestartPolicy = ptr.To(corev1.ContainerRestartPolicyAlways)
	pod.Spec.InitContainers = []corev1.Container{sidecar, requestingContainer("prepare", "2")}
	pod.Spec.Containers = []corev1.Container{requestingContainer("main", "1"), requestingContainer("worker", "1")}
	got, peak := allocatePod(pod, []string{"A", "B", "C"})
	require.Equal(t, []api.ContainerAllocation{
		{Name: "sidecar", GPUs: []string{"A"}}, {Name: "prepare", GPUs: []string{"B", "C"}},
		{Name: "main", GPUs: []string{"B"}}, {Name: "worker", GPUs: []string{"C"}},
	}, got.Containers)
	require.Equal(t, 3, peak)
	refused, peak := allocatePod(pod, []string{"A", "B"})
	require.Nil(t, refused)
	require.Zero(t, peak)
}

func TestAllocationRetryKeepsWinnerAndReusesReleasedGPUs(t *testing.T) {
	t.Parallel()
	first, second := boundTestPod("first", "node"), boundTestPod("second", "node")
	// Only the API has observed the winning writer's Pod.
	f := newAllocationFixture(t, nil, map[string][]string{"node": {"A", "B"}}, first)
	_, err := f.kube.CoreV1().Pods("team").Create(t.Context(), second, metav1.CreateOptions{})
	require.NoError(t, err)
	allocator, views, mokka, rack := &f.allocator, f.allocator.views, f.mokka, f.rack
	var wroteWinner atomic.Bool
	mokka.PrependReactor("update", "sgpurackallocations", func(action clienttesting.Action) (bool, runtime.Object, error) {
		if !wroteWinner.CompareAndSwap(false, true) {
			return false, nil, nil
		}
		winner := api.NewSGPURackAllocation(rack)
		allocation, _ := allocatePod(second, []string{"A"})
		winner.Nodes = []api.NodeAllocation{{Name: "node", Pods: []api.PodAllocation{*allocation}}}
		if err := mokka.Tracker().Update(action.GetResource(), winner, ""); err != nil {
			return true, nil, err
		}
		return true, nil, apierrors.NewConflict(action.GetResource().GroupResource(), rack.Name, errors.New("concurrent allocation"))
	})
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { defer close(done); allocator.run(ctx) }()
	t.Cleanup(func() { cancel(); views.pods.allocations.ShutDown(); views.pods.backfills.ShutDown(); <-done })
	assignments := func() map[string]string {
		saved, err := allocator.records.Get(ctx, rack.Name, metav1.GetOptions{})
		require.NoError(t, err)
		m := map[string]string{}
		for _, n := range saved.Nodes {
			for _, p := range n.Pods {
				m[p.Pod] = p.Containers[0].GPUs[0]
			}
		}
		return m
	}
	require.Eventually(t, func() bool {
		return maps.Equal(assignments(), map[string]string{"team/second": "A", "team/first": "B"})
	}, 5*time.Second, 10*time.Millisecond)
	ended := first.DeepCopy()
	ended.Status.Phase = corev1.PodSucceeded
	require.NoError(t, views.pods.Update(ended))
	require.NoError(t, views.pods.Add(boundTestPod("third", "node")))
	require.Eventually(t, func() bool {
		return maps.Equal(assignments(), map[string]string{"team/second": "A", "team/third": "B"})
	}, 5*time.Second, 10*time.Millisecond)
}

type allocationFixture struct {
	rack      *api.SGPURack
	allocator rackAllocator
	kube      *kubefake.Clientset
	mokka     *mokkafake.Clientset
}

func newAllocationFixture(t *testing.T, saved []api.NodeAllocation, nodeGPUs map[string][]string, cached ...any) *allocationFixture {
	t.Helper()
	rack := &api.SGPURack{ObjectMeta: metav1.ObjectMeta{Name: "rack", UID: "rack-uid"}}
	rack.Status.Delivery = &api.RackDelivery{Generation: 1}
	ref := state.Object{Name: rack.Name, UID: rack.UID}
	pods := newShardPods()
	t.Cleanup(pods.allocations.ShutDown)
	t.Cleanup(pods.backfills.ShutDown)
	views := newRackViews(pods, func(string) {})
	members := map[types.UID]string{}
	views.owned[rack.Name] = &trackedRack{ref: ref, wanted: 1, applied: 1, members: members}
	nodes := cache.NewStore(cache.MetaNamespaceKeyFunc)
	kube := kubefake.NewSimpleClientset()
	for i, name := range slices.Sorted(maps.Keys(nodeGPUs)) {
		uid := types.UID(name + "-uid")
		members[uid] = name
		slot := state.NodePayload{Hardware: state.Hardware{Rack: ref, RackDeliveryGeneration: 1}, Slot: state.Slot{Index: int32(i), Node: state.Object{Name: name, UID: uid}, AssignmentRevision: 1}}
		for _, id := range nodeGPUs[name] {
			slot.GPUs = append(slot.GPUs, state.GPU{SGPURackGPU: api.SGPURackGPU{UUID: id}})
		}
		views.nodes[uid] = slot
		raw, err := assignment.EncodePublication(uid, 1, &assignment.PublishedBinding{Rack: assignment.ObjectReference{Name: ref.Name, UID: ref.UID}, RackGroup: "compute", NodeIndex: slot.Index, RackDeliveryGenerationFloor: 1})
		require.NoError(t, err)
		require.NoError(t, kube.Tracker().Add(&corev1.Node{ObjectMeta: metav1.ObjectMeta{
			Name: name, UID: uid, Labels: map[string]string{metadata.AssignedLabel: "true", allocate.NodeTypeLabel: "KWOK"},
			Annotations: map[string]string{metadata.AssignmentAnnotation: raw},
		}}))
		require.NoError(t, nodes.Add(&nodeObservation{PartialObjectMetadata: metav1.PartialObjectMetadata{ObjectMeta: metav1.ObjectMeta{Name: name, UID: uid}}, kwok: true, assigned: true, assignment: raw}))
	}
	pods.setRack(rack.Name, sets.KeySet(nodeGPUs))
	require.NoError(t, pods.Replace(cached, "1"))
	record := api.NewSGPURackAllocation(rack)
	record.Nodes = saved
	mokka := mokkafake.NewSimpleClientset(rack, record)
	a := rackAllocator{records: mokka.MokkaV1alpha1().SGPURackAllocations(), rackAPI: mokka.MokkaV1alpha1().SGPURacks(), core: kube.CoreV1(), nodes: nodes, racks: cache.NewStore(cache.MetaNamespaceKeyFunc), views: views}
	require.NoError(t, a.racks.Add(rack))
	return &allocationFixture{rack: rack, allocator: a, kube: kube, mokka: mokka}
}

func boundTestPod(name, node string) *corev1.Pod {
	return &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "team", UID: types.UID(name)}, Spec: corev1.PodSpec{NodeName: node, Containers: []corev1.Container{requestingContainer("main", "1")}}}
}

func savedPod(p *corev1.Pod, gpu string) api.PodAllocation {
	a, _ := allocatePod(p, []string{gpu})
	return *a
}

func (f *allocationFixture) record(t *testing.T) []api.NodeAllocation {
	t.Helper()
	r, err := f.allocator.records.Get(t.Context(), f.rack.Name, metav1.GetOptions{})
	require.NoError(t, err)
	return r.Nodes
}

func TestReboundNodeReservesMovedGPUUntilPodFinishes(t *testing.T) {
	t.Parallel()
	old := boundTestPod("running", "worker")
	newPod := boundTestPod("waiting", "other")
	saved := []api.NodeAllocation{{Name: "worker", Pods: []api.PodAllocation{savedPod(old, "moved")}}}
	f := newAllocationFixture(t, saved, map[string][]string{"other": {"moved"}, "worker": {"new"}}, old, newPod)
	events := record.NewFakeRecorder(5)
	f.allocator.recorder = events
	require.NoError(t, f.allocator.reconcile(t.Context(), f.rack.Name))
	require.Equal(t, saved, f.record(t))
	select {
	case event := <-events.Events:
		require.Contains(t, event, "Warning GPUAllocationMismatch")
		require.Contains(t, event, "moved")
	default:
		t.Fatal("expected a warning on the affected Pod")
	}
	old = old.DeepCopy()
	old.Status.Phase = corev1.PodSucceeded
	require.NoError(t, f.allocator.views.pods.Update(old))
	require.NoError(t, f.allocator.reconcile(t.Context(), f.rack.Name))
	require.Equal(t, []api.NodeAllocation{{Name: "other", Pods: []api.PodAllocation{savedPod(newPod, "moved")}}}, f.record(t))
}

func TestMissingSlotDoesNotBlockHealthyNode(t *testing.T) {
	t.Parallel()
	old := boundTestPod("old", "unavailable")
	next := boundTestPod("new", "healthy")
	saved := []api.NodeAllocation{{Name: "unavailable", Pods: []api.PodAllocation{savedPod(old, "held")}}}
	f := newAllocationFixture(t, saved, map[string][]string{"healthy": {"held", "free"}, "unavailable": {"held"}}, next)
	f.allocator.views.forgetNode("unavailable-uid")
	require.NoError(t, f.allocator.reconcile(t.Context(), f.rack.Name))
	require.ElementsMatch(t, []api.NodeAllocation{saved[0], {Name: "healthy", Pods: []api.PodAllocation{savedPod(next, "free")}}}, f.record(t))
	next = next.DeepCopy()
	next.Status.Phase = corev1.PodFailed
	require.NoError(t, f.allocator.views.pods.Update(next))
	require.NoError(t, f.allocator.reconcile(t.Context(), f.rack.Name))
	require.Equal(t, saved, f.record(t))
}

func TestBatchReleaseConfirmsMissingPodUIDsOncePerNode(t *testing.T) {
	t.Parallel()
	existing := make([]api.PodAllocation, 100)
	for i := range existing {
		existing[i] = savedPod(boundTestPod(fmt.Sprintf("pod-%d", i), "worker"), fmt.Sprintf("gpu-%d", i))
	}
	saved := []api.NodeAllocation{{Name: "worker", Pods: existing}}
	f := newAllocationFixture(t, saved, map[string][]string{"worker": {"gpu-0"}})
	lagged := boundTestPod("pod-0", "worker")
	lagged.Annotations = map[string]string{gpuAllocationAnnotation: `{"main":["gpu-0"]}`}
	_, err := f.kube.CoreV1().Pods("team").Create(t.Context(), lagged, metav1.CreateOptions{})
	require.NoError(t, err)
	replacement := boundTestPod("pod-1", "worker")
	replacement.UID = "replacement"
	_, err = f.kube.CoreV1().Pods("team").Create(t.Context(), replacement, metav1.CreateOptions{})
	require.NoError(t, err)
	f.kube.ClearActions()
	fail := true
	f.kube.PrependReactor("list", "pods", func(clienttesting.Action) (bool, runtime.Object, error) {
		if fail {
			return true, nil, errors.New("temporary list failure")
		}
		return false, nil, nil
	})
	require.Error(t, f.allocator.reconcile(t.Context(), f.rack.Name))
	require.Equal(t, saved, f.record(t))
	fail = false
	f.kube.ClearActions()
	require.NoError(t, f.allocator.reconcile(t.Context(), f.rack.Name))
	require.Len(t, f.kube.Actions(), 1)
	list := f.kube.Actions()[0].(clienttesting.ListAction)
	require.Equal(t, "spec.nodeName=worker", list.GetListRestrictions().Fields.String())
	require.Empty(t, list.GetNamespace())
	require.Equal(t, []api.NodeAllocation{{Name: "worker", Pods: existing[:1]}}, f.record(t))
}
