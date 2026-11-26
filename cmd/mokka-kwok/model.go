// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package main

import (
	"context"
	"sync/atomic"
	"time"

	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
	corev1 "k8s.io/api/core/v1"
	resourcev1 "k8s.io/api/resource/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	coreclient "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/metadata"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"

	"github.com/NVIDIA/k8s-test-infra/internal/health"
	"github.com/NVIDIA/k8s-test-infra/internal/sgpu/inventory"
	mokkaclient "github.com/NVIDIA/k8s-test-infra/pkg/generated/clientset/versioned"
	mokkainformers "github.com/NVIDIA/k8s-test-infra/pkg/generated/informers/externalversions/api/v1alpha1"
)

const rackUIDIndex = "rackUID"

type model struct {
	claimWatch *cache.Reflector
	kube       kubernetes.Interface
	events     coreclient.EventInterface
	racks      cache.SharedIndexInformer
	pods       *shardPods
	podClient  coreclient.PodInterface
	podWatch   *cache.Reflector
	discovery  *labelPublisher
	fetcher    *rackFetcher
	allocator  *rackAllocator
}

func newModel(mokka mokkaclient.Interface, kube kubernetes.Interface, c config, domainMeta metadata.Interface) model {
	racks := mokkainformers.NewFilteredSGPURackInformer(mokka, 0, cache.Indexers{
		inventory.RackByNodeUIDIndex: inventory.Indexers()[inventory.RackByNodeUIDIndex],
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
	discovery.dra = c.gpuPublishing == "DRA"
	var claimWatch *cache.Reflector
	if discovery.dra || c.computeDomains {
		pods.claims = &shardClaims{pods: pods, client: kube.ResourceV1(), driver: c.gpuDriver, locations: make(map[cache.ObjectName]sets.Set[string])}
		claims := kube.ResourceV1().ResourceClaims(metav1.NamespaceAll)
		lw := &cache.ListWatch{ListWithContextFunc: func(ctx context.Context, o metav1.ListOptions) (runtime.Object, error) { return claims.List(ctx, o) }, WatchFuncWithContext: claims.Watch}
		claimWatch = cache.NewReflectorWithOptions(cache.ToListWatcherWithWatchListSemantics(lw, kube), &resourcev1.ResourceClaim{}, pods.claims, cache.ReflectorOptions{Name: "allocated ResourceClaims"})

		discovery.slices = &nodeSlices{client: kube.ResourceV1().ResourceSlices()}
		if discovery.dra {
			discovery.slices.driver = c.gpuDriver
		}
		if c.computeDomains {
			discovery.slices.domains = newDomainChannels(domainMeta, pods.claims)
			discovery.slices.domains.retry = discovery.queue.AddAfter
			pods.notifyNode = discovery.queue.Add
		}
	}
	views := newRackViews(pods, discovery.queue.Add)
	discovery.views = views
	allocator := &rackAllocator{
		claims:  pods.claims,
		dra:     discovery.dra,
		slices:  discovery.slices,
		records: mokka.MokkaV1alpha1().SGPURackAllocations(),
		rackAPI: mokka.MokkaV1alpha1().SGPURacks(),
		core:    kube.CoreV1(),
		racks:   racks.GetStore(),
		nodes:   discovery.nodeWatch.GetStore(),
		views:   views,
	}
	return model{
		claimWatch: claimWatch,
		kube:       kube,
		events:     kube.CoreV1().Events(metav1.NamespaceAll),
		allocator:  allocator,
		fetcher:    newRackFetcher(c, views),
		racks:      racks,
		pods:       pods,
		podClient:  client,
		podWatch:   podWatch,
		discovery:  discovery,
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
	var prerequisites atomic.Value
	prerequisites.Store("checking the configured Kubernetes APIs")
	server.SetReadiness(func() health.Probe {
		if reason := prerequisites.Load().(string); reason != "" {
			return health.Unhealthy("%s", reason)
		}
		if c.computeDomains && !m.discovery.slices.domains.watch.HasSynced() {
			return health.Unhealthy("waiting for ComputeDomain cache")
		}
		if !m.claimsSynced() {
			return health.Unhealthy("waiting for ResourceClaim cache")
		}
		if !rackEvents.HasSynced() || !m.pods.hasSynced() || !m.discovery.nodeWatch.HasSynced() {
			return health.Unhealthy("waiting for rack, pod and node caches")
		}
		return health.OK()
	})
	group.Go(func() error { return server.Run(ctx) })
	group.Go(func() error {
		if !c.waitForAPIs(ctx, m.kube, 10*time.Second, func(reason string) { prerequisites.Store(reason) }) {
			return nil
		}
		return m.runWorkers(ctx, c, rackEvents)
	})
	return group.Wait()
}

func (m model) runWorkers(ctx context.Context, c config, rackEvents cache.ResourceEventHandlerRegistration) error {
	group, ctx := errgroup.WithContext(ctx)
	b := record.NewBroadcaster(record.WithContext(ctx))
	defer b.Shutdown()
	b.StartRecordingToSink(&coreclient.EventSinkImpl{Interface: m.events})
	recorder := b.NewRecorder(scheme.Scheme, corev1.EventSource{Component: fieldManager})
	m.discovery.recorder = recorder
	m.allocator.recorder = recorder
	if m.discovery.dra {
		group.Go(func() error { return m.discovery.watchSlices(ctx, m.kube, gpuSliceKey) })
	}
	if c.computeDomains {
		group.Go(func() error { return m.discovery.watchSlices(ctx, m.kube, domainSliceKey) })
		group.Go(func() error { return m.discovery.watchDomains(ctx) })
	}
	if m.claimWatch != nil {
		group.Go(func() error {
			if cache.WaitForCacheSync(ctx.Done(), rackEvents.HasSynced) && m.fetcher.waitInitialReads(ctx) {
				m.claimWatch.RunWithContext(ctx)
			}
			return nil
		})
	}
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
		if cache.WaitForCacheSync(ctx.Done(), m.pods.hasSynced, m.discovery.nodeWatch.HasSynced, m.claimsSynced) {
			zap.L().Info("Rack and Pod caches synced", zap.String("shard", c.shard), zap.Int("racks", len(m.racks.GetStore().ListKeys())),
				zap.Int("boundPods", m.pods.podCount()))
			m.allocator.run(ctx)
		}
		return nil
	})
	return group.Wait()
}

func (m model) claimsSynced() bool { return m.pods.claims == nil || m.pods.claims.hasSynced() }
