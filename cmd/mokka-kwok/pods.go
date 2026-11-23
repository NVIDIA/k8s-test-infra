// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package main

import (
	"context"
	"sync"

	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/util/sets"
	coreclient "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
)

// The API can select all bound Pods, but cannot select a set of node names.
// Filter at the reflector's store so unrelated Pods never enter the model.
type shardPods struct {
	mu        sync.Mutex
	racks     map[string]sets.Set[string]
	nodes     map[string]*nodePods
	synced    bool
	backfills workqueue.TypedRateLimitingInterface[string]
}

type nodePods struct {
	owners int
	pods   map[cache.ObjectName]*corev1.Pod
	// While a node list is in flight, watch events take precedence over that
	// snapshot, including deletions that would otherwise resurrect a Pod.
	touched   sets.Set[cache.ObjectName]
	needsList bool
}

func newShardPods() *shardPods {
	return &shardPods{
		racks: make(map[string]sets.Set[string]), nodes: make(map[string]*nodePods),
		backfills: workqueue.NewTypedRateLimitingQueue(workqueue.DefaultTypedControllerRateLimiter[string]()),
	}
}

func (s *shardPods) setRack(rack string, nodes sets.Set[string]) {
	s.mu.Lock()
	defer s.mu.Unlock()
	previous := s.racks[rack]
	for name := range previous.Difference(nodes) {
		s.nodes[name].owners--
		if s.nodes[name].owners == 0 {
			delete(s.nodes, name)
		}
	}
	for name := range nodes.Difference(previous) {
		node := s.nodes[name]
		if node == nil {
			node = &nodePods{pods: make(map[cache.ObjectName]*corev1.Pod), needsList: s.synced}
			s.nodes[name] = node
			if node.needsList {
				s.backfills.Add(name)
			}
		}
		node.owners++
	}
	if len(nodes) == 0 {
		delete(s.racks, rack)
	} else {
		s.racks[rack] = nodes
	}
}

func (s *shardPods) Add(obj any) error    { return s.Update(obj) }
func (s *shardPods) Update(obj any) error { return s.apply(obj.(*corev1.Pod), false) }
func (s *shardPods) Delete(obj any) error { return s.apply(obj.(*corev1.Pod), true) }
func (s *shardPods) Resync() error        { return nil }

func (s *shardPods) apply(pod *corev1.Pod, deleted bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if node := s.nodes[pod.Spec.NodeName]; node != nil {
		key := cache.MetaObjectToName(pod)
		if node.touched != nil {
			node.touched.Insert(key)
		}
		if deleted {
			delete(node.pods, key)
		} else {
			node.pods[key] = pod
		}
	}
	return nil
}

func (s *shardPods) Replace(objects []any, _ string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, node := range s.nodes {
		clear(node.pods)
		node.needsList = false
		node.touched = nil
	}
	for _, obj := range objects {
		pod := obj.(*corev1.Pod)
		if node := s.nodes[pod.Spec.NodeName]; node != nil {
			node.pods[cache.MetaObjectToName(pod)] = pod
		}
	}
	s.synced = true
	return nil
}

func (s *shardPods) podCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	count := 0
	for _, node := range s.nodes {
		count += len(node.pods)
	}
	return count
}

func (s *shardPods) hasSynced() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.synced
}

func (s *shardPods) runBackfills(ctx context.Context, client coreclient.PodInterface) {
	for {
		name, shutdown := s.backfills.Get()
		if shutdown {
			return
		}
		if err := s.backfill(ctx, client, name); err != nil && ctx.Err() == nil {
			zap.L().Warn("List Pods for newly assigned node", zap.String("node", name), zap.Error(err))
			s.backfills.AddRateLimited(name)
		} else {
			s.backfills.Forget(name)
		}
		s.backfills.Done(name)
	}
}

func (s *shardPods) backfill(ctx context.Context, client coreclient.PodInterface, name string) error {
	s.mu.Lock()
	node := s.nodes[name]
	if node == nil || !node.needsList {
		s.mu.Unlock()
		return nil
	}
	node.touched = sets.New[cache.ObjectName]()
	s.mu.Unlock()

	pods, err := client.List(ctx, metav1.ListOptions{FieldSelector: fields.OneTermEqualSelector("spec.nodeName", name).String()})
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.nodes[name] != node || !node.needsList {
		return nil // A release, reassignment or reflector relist superseded this request.
	}
	defer func() { node.touched = nil }()
	if err != nil {
		return err
	}
	node.mergeList(name, pods.Items)
	node.needsList = false
	return nil
}

func (node *nodePods) mergeList(name string, items []corev1.Pod) {
	for key := range node.pods {
		if !node.touched.Has(key) {
			delete(node.pods, key)
		}
	}
	for i := range items {
		pod := &items[i]
		key := cache.MetaObjectToName(pod)
		if pod.Spec.NodeName == name && !node.touched.Has(key) {
			// Detach from the list's backing array so each Pod can be released independently.
			item := *pod
			node.pods[key] = &item
		}
	}
}
