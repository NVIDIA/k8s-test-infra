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
	"k8s.io/client-go/kubernetes/scheme"
	coreclient "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"

	"github.com/NVIDIA/k8s-test-infra/internal/health"
	"github.com/NVIDIA/k8s-test-infra/internal/sgpu/inventory"
	mokkaclient "github.com/NVIDIA/k8s-test-infra/pkg/generated/clientset/versioned"
	mokkainformers "github.com/NVIDIA/k8s-test-infra/pkg/generated/informers/externalversions/api/v1alpha1"
)

const rackUIDIndex = "rackUID"

type model struct {
	events    coreclient.EventInterface
	racks     cache.SharedIndexInformer
	pods      *shardPods
	podClient coreclient.PodInterface
	podWatch  *cache.Reflector
	discovery *labelPublisher
	fetcher   *rackFetcher
	allocator *rackAllocator
}

func newModel(mokka mokkaclient.Interface, kube kubernetes.Interface, c config) model {
	racks := mokkainformers.NewFilteredSGPURackInformer(mokka, 0, cache.Indexers{
		rackUIDIndex: func(obj any) ([]string, error) {
			rack, err := meta.Accessor(obj)
			if err != nil {
				return nil, err
			}
			return []string{string(rack.GetUID())}, nil
		},
	}, func(o *metav1.ListOptions) { o.LabelSelector = labels.Set{inventory.KWOKShardLabel: c.shard}.String() })
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
	discovery := newLabelPublisher(kube, racks)
	views := newRackViews(pods, discovery.queue.Add)
	discovery.views = views
	allocator := &rackAllocator{
		records: mokka.MokkaV1alpha1().SGPURackAllocations(),
		rackAPI: mokka.MokkaV1alpha1().SGPURacks(),
		pods:    kube.CoreV1(),
		racks:   racks.GetStore(),
		nodes:   discovery.nodeWatch.GetStore(),
		views:   views,
	}
	return model{
		events:    kube.CoreV1().Events(metav1.NamespaceAll),
		allocator: allocator,
		fetcher:   newRackFetcher(c, views),
		racks:     racks,
		pods:      pods,
		podClient: client,
		podWatch:  podWatch,
		discovery: discovery,
	}
}

func (m model) run(ctx context.Context, c config) error {
	group, ctx := errgroup.WithContext(ctx)
	rackEvents, err := m.racks.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    m.fetcher.observe,
		UpdateFunc: func(_, current any) { m.fetcher.observe(current) },
		DeleteFunc: m.fetcher.remove,
	})
	if err != nil {
		return err
	}
	server := health.NewServer(c.listen, health.DefaultShutdownTimeout)
	server.SetReadiness(func() health.Probe {
		if !rackEvents.HasSynced() || !m.pods.hasSynced() || !m.discovery.nodeWatch.HasSynced() {
			return health.Unhealthy("waiting for rack, pod and node caches")
		}
		return health.OK()
	})
	b := record.NewBroadcaster(record.WithContext(ctx))
	defer b.Shutdown()
	b.StartRecordingToSink(&coreclient.EventSinkImpl{Interface: m.events})
	recorder := b.NewRecorder(scheme.Scheme, corev1.EventSource{Component: fieldManager})
	m.discovery.recorder = recorder
	m.allocator.recorder = recorder
	group.Go(func() error { return m.discovery.run(ctx) })
	group.Go(func() error { return m.fetcher.run(ctx) })
	group.Go(func() error { m.racks.RunWithContext(ctx); return nil })
	group.Go(func() error {
		// Populate initial rack membership before the first cluster-wide Pod list.
		if cache.WaitForCacheSync(ctx.Done(), rackEvents.HasSynced) && m.fetcher.waitInitialReads(ctx) {
			m.podWatch.RunWithContext(ctx)
		}
		return nil
	})
	group.Go(func() error { m.pods.runBackfills(ctx, m.podClient); return nil })
	group.Go(func() error {
		<-ctx.Done()
		m.pods.backfills.ShutDown()
		m.pods.allocations.ShutDown()
		return nil
	})
	group.Go(func() error {
		if cache.WaitForCacheSync(ctx.Done(), m.pods.hasSynced, m.discovery.nodeWatch.HasSynced) {
			zap.L().Info("Rack and Pod caches synced", zap.String("shard", c.shard), zap.Int("racks", len(m.racks.GetStore().ListKeys())),
				zap.Int("boundPods", m.pods.podCount()))
			m.allocator.run(ctx)
		}
		return nil
	})
	group.Go(func() error { return server.Run(ctx) })
	return group.Wait()
}
