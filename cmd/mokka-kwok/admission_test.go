// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package main

import (
	"errors"
	"testing"

	api "github.com/NVIDIA/k8s-test-infra/api/v1alpha1"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clienttesting "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/record"
)

func TestPublishAdmissionAfterDurableWrite(t *testing.T) {
	t.Parallel()
	p := boundTestPod("train", "worker")
	f := newAllocationFixture(t, nil, map[string][]string{"worker": {"gpu-a"}}, p)
	_, err := f.kube.CoreV1().Pods(p.Namespace).Create(t.Context(), p, metav1.CreateOptions{})
	require.NoError(t, err)
	offline := true
	f.mokka.PrependReactor("update", "sgpurackallocations", func(clienttesting.Action) (bool, runtime.Object, error) {
		if offline {
			return true, nil, errors.New("write conflict")
		}
		return false, nil, nil
	})
	get := func() *corev1.Pod {
		t.Helper()
		pod, err := f.kube.CoreV1().Pods(p.Namespace).Get(t.Context(), p.Name, metav1.GetOptions{})
		require.NoError(t, err)
		return pod
	}
	require.Error(t, f.allocator.reconcile(t.Context(), f.rack.Name))
	require.Empty(t, get().Annotations)
	offline = false
	broken := true
	f.kube.PrependReactor("patch", "pods", func(clienttesting.Action) (bool, runtime.Object, error) {
		if broken {
			return true, nil, errors.New("Pod update unavailable")
		}
		return false, nil, nil
	})
	require.Error(t, f.allocator.reconcile(t.Context(), f.rack.Name))
	require.Len(t, f.record(t), 1)
	broken = false
	require.NoError(t, f.allocator.reconcile(t.Context(), f.rack.Name))
	require.JSONEq(t, `{"main":["gpu-a"]}`, get().Annotations["mokka.nvidia.com/gpu-allocation"])
}

func TestAdmissionWaitsForIPAndConfirmsShortage(t *testing.T) {
	t.Parallel()
	p := boundTestPod("too-large", "worker")
	p.Spec.Containers = []corev1.Container{requestingContainer("trainer", "2")}
	p.Status.Phase = corev1.PodPending
	f := newAllocationFixture(t, nil, map[string][]string{"worker": {"gpu-a"}}, p)
	pods := f.kube.CoreV1().Pods(p.Namespace)
	_, err := pods.Create(t.Context(), p, metav1.CreateOptions{})
	require.NoError(t, err)
	require.NoError(t, f.allocator.reconcile(t.Context(), f.rack.Name))
	check := func() *corev1.Pod {
		t.Helper()
		observed, err := pods.Get(t.Context(), p.Name, metav1.GetOptions{})
		require.NoError(t, err)
		return observed
	}
	require.Equal(t, corev1.PodPending, check().Status.Phase)
	initialized := p.DeepCopy()
	initialized.Status.PodIP = "10.1.2.3"
	require.True(t, allocationChanged(p, initialized, false))
	_, err = pods.UpdateStatus(t.Context(), initialized, metav1.UpdateOptions{})
	require.NoError(t, err)
	require.NoError(t, f.allocator.views.pods.Update(initialized))
	fail := true
	f.kube.PrependReactor("list", "pods", func(clienttesting.Action) (bool, runtime.Object, error) {
		if fail {
			return true, nil, errors.New("temporary API outage")
		}
		return false, nil, nil
	})
	require.Error(t, f.allocator.reconcile(t.Context(), f.rack.Name))
	require.Equal(t, corev1.PodPending, check().Status.Phase)
	fail = false
	require.NoError(t, f.allocator.reconcile(t.Context(), f.rack.Name))
	observed := check()
	require.Equal(t, corev1.PodFailed, observed.Status.Phase)
	require.Equal(t, "UnexpectedAdmissionError", observed.Status.Reason)
	require.NotContains(t, observed.Annotations, gpuAllocationAnnotation)
	require.Empty(t, f.record(t))
}

func pendingTestPod(name string) *corev1.Pod {
	p := boundTestPod(name, "worker")
	p.Status.Phase = corev1.PodPending
	p.Status.PodIP = "10.1.0.1"
	return p
}

func TestTerminatingPodCannotTakeFirstGPU(t *testing.T) {
	t.Parallel()
	deleting := pendingTestPod("a-deleting")
	now := metav1.Now()
	deleting.DeletionTimestamp = &now
	deleting.Finalizers = []string{"test.example.com/drain"}
	waiting := pendingTestPod("b-waiting")
	f := newAllocationFixture(t, nil, map[string][]string{"worker": {"gpu-a"}}, deleting, waiting)
	pods := f.kube.CoreV1().Pods(waiting.Namespace)
	for _, pod := range []*corev1.Pod{deleting, waiting} {
		_, err := pods.Create(t.Context(), pod, metav1.CreateOptions{})
		require.NoError(t, err)
	}
	require.NoError(t, f.allocator.reconcile(t.Context(), f.rack.Name))
	require.Equal(t, []api.NodeAllocation{{Name: "worker", Pods: []api.PodAllocation{savedPod(waiting, "gpu-a")}}}, f.record(t))
	got, err := pods.Get(t.Context(), waiting.Name, metav1.GetOptions{})
	require.NoError(t, err)
	require.JSONEq(t, `{"main":["gpu-a"]}`, got.Annotations[gpuAllocationAnnotation])
}

func TestPendingPodWithdrawsAndRestoresDisputedGrant(t *testing.T) {
	t.Parallel()
	p := pendingTestPod("train")
	p.Annotations = map[string]string{"example.com/owner": "team"}
	f := newAllocationFixture(t, nil, map[string][]string{"worker": {"gpu-a"}}, p)
	f.allocator.recorder = record.NewFakeRecorder(10)
	pods := f.kube.CoreV1().Pods(p.Namespace)
	_, err := pods.Create(t.Context(), p, metav1.CreateOptions{})
	require.NoError(t, err)
	reconcile := func() *corev1.Pod {
		t.Helper()
		require.NoError(t, f.allocator.reconcile(t.Context(), f.rack.Name))
		got, err := pods.Get(t.Context(), p.Name, metav1.GetOptions{})
		require.NoError(t, err)
		require.NoError(t, f.allocator.views.pods.Update(got))
		return got
	}
	require.Contains(t, reconcile().Annotations, gpuAllocationAnnotation)
	slot := f.allocator.views.nodes["worker-uid"]
	slot.GPUs[0].UUID = "gpu-b"
	require.Equal(t, map[string]string{"example.com/owner": "team"}, reconcile().Annotations)
	require.Equal(t, []api.NodeAllocation{{Name: "worker", Pods: []api.PodAllocation{savedPod(p, "gpu-a")}}}, f.record(t))
	slot.GPUs[0].UUID = "gpu-a"
	require.JSONEq(t, `{"main":["gpu-a"]}`, reconcile().Annotations[gpuAllocationAnnotation])
}

func TestDenialWaitsForCurrentRackAndNode(t *testing.T) {
	t.Parallel()
	for _, change := range []string{"rack generation", "node binding"} {
		t.Run(change, func(t *testing.T) {
			t.Parallel()
			p := pendingTestPod("train")
			p.Spec.Containers = []corev1.Container{requestingContainer("trainer", "2")}
			f := newAllocationFixture(t, nil, map[string][]string{"worker": {"gpu-a"}}, p)
			pods := f.kube.CoreV1().Pods(p.Namespace)
			_, err := pods.Create(t.Context(), p, metav1.CreateOptions{})
			require.NoError(t, err)
			f.kube.PrependReactor("list", "pods", func(clienttesting.Action) (bool, runtime.Object, error) {
				if change == "rack generation" {
					newer := f.rack.DeepCopy()
					newer.Status.Delivery.Generation++
					_, err := f.allocator.rackAPI.UpdateStatus(t.Context(), newer, metav1.UpdateOptions{})
					require.NoError(t, err)
				} else {
					node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "worker", UID: "worker-uid"}}
					require.NoError(t, f.kube.Tracker().Update(corev1.SchemeGroupVersion.WithResource("nodes"), node, ""))
				}
				return false, nil, nil
			})
			require.NoError(t, f.allocator.reconcile(t.Context(), f.rack.Name))
			got, err := pods.Get(t.Context(), p.Name, metav1.GetOptions{})
			require.NoError(t, err)
			require.Equal(t, corev1.PodPending, got.Status.Phase)
		})
	}
}
