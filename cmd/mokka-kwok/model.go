// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package main

import (
	"context"

	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	coreclient "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/tools/cache"

	mokkav1 "github.com/NVIDIA/k8s-test-infra/api/v1alpha1"
	"github.com/NVIDIA/k8s-test-infra/internal/health"
	"github.com/NVIDIA/k8s-test-infra/internal/sgpu/inventory"
	mokkaclient "github.com/NVIDIA/k8s-test-infra/pkg/generated/clientset/versioned"
	mokkainformers "github.com/NVIDIA/k8s-test-infra/pkg/generated/informers/externalversions/api/v1alpha1"
)

const rackUIDIndex = "rackUID"

type model struct {
	racks     cache.SharedIndexInformer
	pods      *shardPods
	podClient coreclient.PodInterface
	podWatch  *cache.Reflector
	products  *productPublisher
}

func newModel(mokka mokkaclient.Interface, kube kubernetes.Interface, shard string) model {
	racks := mokkainformers.NewFilteredSGPURackInformer(mokka, 0, cache.Indexers{
		boundNodeIndex: boundNodeNames,
		rackUIDIndex: func(obj any) ([]string, error) {
			rack, err := meta.Accessor(obj)
			if err != nil {
				return nil, err
			}
			return []string{string(rack.GetUID())}, nil
		},
	}, func(o *metav1.ListOptions) { o.LabelSelector = labels.Set{inventory.KWOKShardLabel: shard}.String() })
	client := kube.CoreV1().Pods(metav1.NamespaceAll)
	bound := fields.OneTermNotEqualSelector("spec.nodeName", "").String()
	lw := &cache.ListWatch{
		ListWithContextFunc: func(ctx context.Context, opts metav1.ListOptions) (runtime.Object, error) {
			opts.FieldSelector = bound
			return client.List(ctx, opts)
		},
		WatchFuncWithContext: func(ctx context.Context, opts metav1.ListOptions) (watch.Interface, error) {
			opts.FieldSelector = bound
			return client.Watch(ctx, opts)
		},
	}
	pods := newShardPods()
	podWatch := cache.NewReflectorWithOptions(cache.ToListWatcherWithWatchListSemantics(lw, kube), &corev1.Pod{}, pods, cache.ReflectorOptions{Name: "bound Pods"})
	return model{racks: racks, pods: pods, podClient: client, podWatch: podWatch, products: newProductPublisher(kube, mokka, racks)}
}

func (m model) run(ctx context.Context, c config) error {
	group, ctx := errgroup.WithContext(ctx)
	rackEvents, err := m.racks.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    m.observeRack,
		UpdateFunc: func(_, current any) { m.observeRack(current) },
		DeleteFunc: m.pods.removeRack,
	})
	if err != nil {
		return err
	}
	server := health.NewServer(c.listen, health.DefaultShutdownTimeout)
	server.SetReadiness(func() health.Probe {
		if !rackEvents.HasSynced() || !m.pods.hasSynced() || !m.products.labeled.HasSynced() {
			return health.Unhealthy("waiting for rack, pod and node caches")
		}
		return health.OK()
	})
	group.Go(func() error { return m.products.run(ctx) })
	group.Go(func() error { m.racks.RunWithContext(ctx); return nil })
	group.Go(func() error {
		// Populate initial rack membership before the first cluster-wide Pod list.
		if cache.WaitForCacheSync(ctx.Done(), rackEvents.HasSynced) {
			m.podWatch.RunWithContext(ctx)
		}
		return nil
	})
	group.Go(func() error { m.pods.runBackfills(ctx, m.podClient); return nil })
	group.Go(func() error {
		<-ctx.Done()
		m.pods.backfills.ShutDown()
		return nil
	})
	group.Go(func() error {
		if cache.WaitForCacheSync(ctx.Done(), m.pods.hasSynced) {
			zap.L().Info("Rack and Pod caches synced", zap.String("shard", c.shard), zap.Int("racks", len(m.racks.GetStore().ListKeys())),
				zap.Int("boundPods", m.pods.podCount()))
		}
		return nil
	})
	group.Go(func() error { return server.Run(ctx) })
	return group.Wait()
}

func (m model) observeRack(obj any) {
	m.pods.observeRack(obj)
	for _, slot := range obj.(*mokkav1.SGPURack).Spec.Nodes {
		if slot.NodeRef != nil {
			m.products.queue.Add(slot.NodeRef.Name)
		}
	}
}
