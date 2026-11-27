// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package main

import (
	"context"
	"encoding/json"
	"fmt"
	state "github.com/NVIDIA/k8s-test-infra/pkg/rackstate/v1alpha1"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	kubefake "k8s.io/client-go/kubernetes/fake"
	metafake "k8s.io/client-go/metadata/fake"
	clienttesting "k8s.io/client-go/testing"

	mokkav1 "github.com/NVIDIA/k8s-test-infra/api/v1alpha1"
	"github.com/NVIDIA/k8s-test-infra/internal/sgpu/inventory"
	mokkafake "github.com/NVIDIA/k8s-test-infra/pkg/generated/clientset/versioned/fake"
)

func TestConfig(t *testing.T) {
	t.Parallel()
	c, err := parseConfig([]string{"data-plane", "--shard-name", "replica-0", "--control-plane-url=http://cp:8080"})
	require.NoError(t, err)
	require.Equal(t, config{shard: "replica-0", listen: ":8080", controlPlane: "http://cp:8080", batch: batchOptions{100, time.Second, 2}}, c)
	for _, args := range [][]string{nil, {"data-plane"}, {"data-plane", "--shard-name", "bad/name"}, {"data-plane", "--shard-name", "valid", "--listen-addr", "bad"}} {
		_, err = parseConfig(args)
		require.Error(t, err)
	}
}

func TestRackModel(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	rack := &mokkav1.SGPURack{ObjectMeta: metav1.ObjectMeta{Name: "owned", UID: "first", Labels: map[string]string{inventory.KWOKShardLabel: "replica-0"}}}
	rack.Spec.NodeType = mokkav1.NodeTypeKWOK
	other := rack.DeepCopy()
	other.Name = "other"
	other.UID = "second"
	other.Labels[inventory.KWOKShardLabel] = "replica-1"
	rack.Spec.Nodes = []mokkav1.SGPURackNode{{NodeRef: &mokkav1.SGPUNodeReference{Name: "initial-node"}}}
	rack.Status.Delivery = &mokkav1.RackDelivery{Generation: 1}
	mokka := mokkafake.NewSimpleClientset(rack, other)
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "worker", Namespace: "test"}, Spec: corev1.PodSpec{NodeName: "node-a"}}
	foreign := pod.DeepCopy()
	foreign.Name = "foreign"
	foreign.Spec.NodeName = "cpu"
	initial := pod.DeepCopy()
	initial.Name = "initial"
	initial.Spec.NodeName = "initial-node"
	kube := kubefake.NewSimpleClientset(pod, foreign, initial)
	firstRequest := make(chan struct{})
	allowReply := make(chan struct{})
	server := rackModelServer(t, firstRequest, allowReply)
	c := config{shard: "replica-0", listen: "127.0.0.1:0", controlPlane: server.URL, batch: batchOptions{100, time.Millisecond, 2}}
	m := newModel(mokka, kube, metafake.NewSimpleMetadataClient(runtime.NewScheme()), c)
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- m.run(runCtx, c) }()
	t.Cleanup(func() { cancel(); require.NoError(t, <-done) })
	select {
	case <-firstRequest:
	case <-time.After(3 * time.Second):
		t.Fatal("missing initial rack request")
	}
	require.Never(t, m.pods.hasSynced, 300*time.Millisecond, 10*time.Millisecond)
	for _, action := range kube.Actions() {
		require.NotEqual(t, "pods", action.GetResource().Resource)
	}
	close(allowReply)
	require.Eventually(t, m.pods.hasSynced, 5*time.Second, 10*time.Millisecond)
	require.Equal(t, []string{"owned"}, m.racks.GetStore().ListKeys())
	owned, err := m.racks.GetIndexer().IndexKeys(rackUIDIndex, "first")
	require.NoError(t, err)
	require.Equal(t, []string{"owned"}, owned)
	require.Eventually(t, func() bool { return retainedPods(m.pods).Equal(sets.New("test/initial")) }, 5*time.Second, 10*time.Millisecond)
	requireSingleInitialPodList(t, kube.Actions())
	racks := mokka.MokkaV1alpha1().SGPURacks()
	rack.Spec.Nodes = []mokkav1.SGPURackNode{{NodeRef: &mokkav1.SGPUNodeReference{Name: "node-a"}}}
	rack.Status.Delivery = &mokkav1.RackDelivery{Generation: 2}
	_, err = racks.Update(ctx, rack, metav1.UpdateOptions{})
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		return retainedPods(m.pods).Equal(sets.New("test/worker"))
	}, 5*time.Second, 10*time.Millisecond)

	newPod := pod.DeepCopy()
	newPod.Name = "second"
	_, err = kube.CoreV1().Pods("test").Create(ctx, newPod, metav1.CreateOptions{})
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		return retainedPods(m.pods).Equal(sets.New("test/worker", "test/second"))
	}, 5*time.Second, 10*time.Millisecond)

	require.NoError(t, racks.Delete(ctx, rack.Name, metav1.DeleteOptions{}))
	require.Eventually(t, func() bool {
		return len(m.racks.GetIndexer().ListIndexFuncValues(rackUIDIndex)) == 0 && len(retainedPods(m.pods)) == 0
	}, 5*time.Second, 10*time.Millisecond)
}

// Hold the first response so the test can observe the startup barrier.
func rackModelServer(t *testing.T, firstRequest chan<- struct{}, allowReply <-chan struct{}) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req state.BatchRequest
		if e := json.NewDecoder(r.Body).Decode(&req); e != nil {
			t.Error(e)
			return
		}
		ref := req.Racks[0]
		if ref.MinRackDeliveryGeneration == 1 {
			close(firstRequest)
			select {
			case <-allowReply:
			case <-r.Context().Done():
				return
			}
		}
		node := "initial-node"
		if ref.MinRackDeliveryGeneration == 2 {
			node = "node-a"
		}
		entry := state.Rack{Hardware: state.Hardware{Rack: ref.Object, RackDeliveryGeneration: ref.MinRackDeliveryGeneration}, Nodes: []state.Slot{{Node: state.Object{Name: node, UID: "uid"}, AssignmentRevision: ref.MinRackDeliveryGeneration}}}
		if ref.MinRackDeliveryGeneration == 1 {
			for i := 1; i < 20; i++ {
				name := fmt.Sprintf("initial-node-%d", i)
				entry.Nodes = append(entry.Nodes, state.Slot{Index: int32(i), Node: state.Object{Name: name, UID: types.UID(name)}, AssignmentRevision: 1})
			}
		}
		if e := json.NewEncoder(w).Encode(state.BatchResponse{APIVersion: state.Version, Racks: []state.RackResult{{Rack: &entry}}}); e != nil {
			t.Error(e)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func requireSingleInitialPodList(t *testing.T, actions []clienttesting.Action) {
	t.Helper()
	initialLists := 0
	for _, action := range actions {
		if action.GetResource().Resource != "pods" {
			continue
		}
		switch a := action.(type) {
		case clienttesting.ListAction:
			initialLists++
			require.Equal(t, "spec.nodeName!=", a.GetListRestrictions().Fields.String())
		case clienttesting.WatchAction:
			require.Equal(t, "spec.nodeName!=", a.GetWatchRestrictions().Fields.String())
		}
	}

	require.Equal(t, 1, initialLists, "one initial Pod list for all 20 Nodes")
}

func retainedPods(s *shardPods) sets.Set[string] {
	s.mu.Lock()
	defer s.mu.Unlock()
	keys := sets.New[string]()
	for _, node := range s.nodes {
		for key := range node.pods {
			keys.Insert(key.String())
		}
	}
	return keys
}

func TestPodBackfillWhileWatchChanges(t *testing.T) {
	t.Parallel()
	s := newShardPods()
	defer s.backfills.ShutDown()
	deleted := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "deleted"}, Spec: corev1.PodSpec{NodeName: "node"}}
	s.setRack("rack", sets.New("node"))
	require.NoError(t, s.Replace([]any{deleted}, "1"))
	require.True(t, retainedPods(s).Has("deleted"))
	s.setRack("rack", nil)
	s.setRack("rack", sets.New("node"))

	kube := kubefake.NewSimpleClientset()
	kube.PrependReactor("list", "pods", func(action clienttesting.Action) (bool, runtime.Object, error) {
		require.Equal(t, "spec.nodeName=node", action.(clienttesting.ListAction).GetListRestrictions().Fields.String())
		added := deleted.DeepCopy()
		added.Name = "added"
		require.NoError(t, s.Add(added))
		require.NoError(t, s.Delete(deleted))
		return true, &corev1.PodList{Items: []corev1.Pod{*deleted}}, nil
	})
	require.NoError(t, s.backfill(t.Context(), kube.CoreV1().Pods(""), "node"))
	require.Equal(t, sets.New("added"), retainedPods(s))
}

func TestLatePodListAfterReassignment(t *testing.T) {
	t.Parallel()
	s := newShardPods()
	defer s.backfills.ShutDown()
	require.NoError(t, s.Replace(nil, "1"))
	s.setRack("rack", sets.New("node"))
	pod := corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "stale"}, Spec: corev1.PodSpec{NodeName: "node"}}
	kube := kubefake.NewSimpleClientset()
	kube.PrependReactor("list", "pods", func(clienttesting.Action) (bool, runtime.Object, error) {
		s.setRack("rack", nil)
		s.setRack("another-rack", sets.New("node"))
		return true, &corev1.PodList{Items: []corev1.Pod{pod}}, nil
	})
	require.NoError(t, s.backfill(t.Context(), kube.CoreV1().Pods(""), "node"))
	require.Empty(t, retainedPods(s))
	require.True(t, s.nodes["node"].needsList)
}
