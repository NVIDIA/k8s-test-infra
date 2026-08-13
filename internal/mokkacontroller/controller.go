// Copyright (c) 2026, NVIDIA CORPORATION.  All rights reserved.
// Licensed under the Apache License, Version 2.0 (the "License");

// Package mokkacontroller wires informer snapshots and keyed workqueues to the
// rack, projection, and status reconcilers.
package mokkacontroller

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"

	controllerprojection "github.com/NVIDIA/k8s-test-infra/internal/mokkacontroller/projection"
	controllerack "github.com/NVIDIA/k8s-test-infra/internal/mokkacontroller/rack"
	controllerstatus "github.com/NVIDIA/k8s-test-infra/internal/mokkacontroller/status"
	mokkav1alpha1 "github.com/NVIDIA/k8s-test-infra/pkg/apis/mokka/v1alpha1"
	versioned "github.com/NVIDIA/k8s-test-infra/pkg/generated/clientset/versioned"
	mokkainformers "github.com/NVIDIA/k8s-test-infra/pkg/generated/informers/externalversions"
	"github.com/NVIDIA/k8s-test-infra/pkg/mokka/allocate"
)

const (
	defaultStatusDebounce         = 100 * time.Millisecond
	defaultStatusProgressInterval = time.Second
)

// Options controls worker concurrency and aggregate-status coalescing.
type Options struct {
	Workers int
	// StatusDebounce is the quiet period before a final status update.
	StatusDebounce time.Duration
	// StatusProgressInterval bounds status staleness while changes continue.
	StatusProgressInterval time.Duration
}

// DefaultOptions returns the production controller defaults.
func DefaultOptions() Options {
	return Options{
		Workers: 2, StatusDebounce: defaultStatusDebounce,
		StatusProgressInterval: defaultStatusProgressInterval,
	}
}

func (o Options) validate() error {
	if o.Workers < 1 {
		return fmt.Errorf("workers must be positive")
	}
	if o.StatusDebounce < 0 {
		return fmt.Errorf("status debounce must not be negative")
	}
	if o.StatusProgressInterval < 0 {
		return fmt.Errorf("status progress interval must not be negative")
	}
	if o.StatusDebounce > 0 && o.statusProgressInterval() < o.StatusDebounce {
		return fmt.Errorf("status progress interval must not be shorter than status debounce")
	}
	return nil
}

func (o Options) statusProgressInterval() time.Duration {
	if o.StatusProgressInterval > 0 {
		return o.StatusProgressInterval
	}
	// A zero value keeps direct API construction useful without weakening the
	// production default or allowing an unbounded stream to starve status.
	return max(defaultStatusProgressInterval, o.StatusDebounce)
}

type projectionMode uint8

const (
	projectionApply projectionMode = iota + 1
	projectionCleanup
)

type projectionKey struct {
	mode      projectionMode
	rackName  string
	slotIndex int32
	fresh     bool
	cleanup   controllerack.CleanupNeeded
}

type statusKind uint8

const (
	statusInventory statusKind = iota + 1
	statusRack
)

type statusKey struct {
	kind statusKind
	name string
	uid  types.UID
}

type queues struct {
	inventories workqueue.TypedRateLimitingInterface[string]
	groups      workqueue.TypedRateLimitingInterface[allocate.GroupKey]
	projections workqueue.TypedRateLimitingInterface[projectionKey]
	status      workqueue.TypedRateLimitingInterface[statusKey]
	statuses    *statusCoalescer
}

func newQueues(debounce time.Duration) *queues {
	return newQueuesWithStatusIntervals(debounce, max(defaultStatusProgressInterval, debounce))
}

func newQueuesWithStatusIntervals(debounce, progressInterval time.Duration) *queues {
	statusQueue := workqueue.NewTypedRateLimitingQueue(workqueue.DefaultTypedControllerRateLimiter[statusKey]())
	return &queues{
		inventories: workqueue.NewTypedRateLimitingQueue(workqueue.DefaultTypedControllerRateLimiter[string]()),
		groups:      workqueue.NewTypedRateLimitingQueue(workqueue.DefaultTypedControllerRateLimiter[allocate.GroupKey]()),
		projections: workqueue.NewTypedRateLimitingQueue(workqueue.DefaultTypedControllerRateLimiter[projectionKey]()),
		status:      statusQueue,
		statuses:    newStatusCoalescer(statusQueue, debounce, progressInterval, realStatusScheduler{}),
	}
}

func (q *queues) addStatus(key statusKey) {
	q.statuses.dirty(key)
}

func (q *queues) shutdown() {
	q.statuses.shutdown()
	q.inventories.ShutDown()
	q.groups.ShutDown()
	q.projections.ShutDown()
	q.status.ShutDown()
}

// Controller owns informer and worker lifecycle for one elected replica.
type Controller struct {
	options Options
	queues  *queues
	ready   atomic.Bool

	starters    []func(context.Context)
	waitForSync func(context.Context) bool

	reconcileInventory  func(context.Context, string) error
	reconcileGroup      func(context.Context, allocate.GroupKey) error
	reconcileProjection func(context.Context, projectionKey) error
	reconcileStatus     func(context.Context, statusKey) error
}

// New assembles shared zero-resync informers and all committed reconcilers.
func New(kubeClient kubernetes.Interface, mokkaClient versioned.Interface, options Options) (*Controller, error) {
	if kubeClient == nil {
		return nil, fmt.Errorf("Kubernetes client must not be nil")
	}
	return newForNodes(kubeClient.CoreV1().Nodes(), mokkaClient, options)
}

func newForNodes(nodes corev1client.NodeInterface, mokkaClient versioned.Interface, options Options) (*Controller, error) {
	if nodes == nil || mokkaClient == nil {
		return nil, fmt.Errorf("controller clients must not be nil")
	}
	if err := options.validate(); err != nil {
		return nil, err
	}

	factory := mokkainformers.NewSharedInformerFactory(mokkaClient, 0)
	mokka := factory.Mokka().V1alpha1()
	profiles := mokka.SGPUProfiles()
	inventories := mokka.SGPUInventories()
	racks := mokka.SGPURacks()
	profileInformer := profiles.Informer()
	inventoryInformer := inventories.Informer()
	rackInformer := racks.Informer()
	if err := inventoryInformer.AddIndexers(controllerack.InventoryIndexers()); err != nil {
		return nil, fmt.Errorf("add inventory indexes: %w", err)
	}
	if err := rackInformer.AddIndexers(controllerack.RackIndexers()); err != nil {
		return nil, fmt.Errorf("add rack indexes: %w", err)
	}
	nodeInformer := newFilteredNodeInformer(nodes)

	snapshot := newInformerCache(
		inventories.Lister(), profiles.Lister(), rackInformer.GetIndexer(), nodeInformer.GetIndexer(), nodes,
	)
	projection := controllerprojection.NewController(snapshot, nodes)
	rackReconciler := controllerack.NewReconciler(
		snapshot,
		mokkaClient.MokkaV1alpha1().SGPUInventories(),
		mokkaClient.MokkaV1alpha1().SGPURacks(),
		projection,
	)
	statusReconciler := controllerstatus.NewReconciler(
		mokkaClient.MokkaV1alpha1().SGPUInventories(),
		mokkaClient.MokkaV1alpha1().SGPURacks(),
		nil,
	)

	controller := &Controller{
		options: options,
		queues:  newQueuesWithStatusIntervals(options.StatusDebounce, options.statusProgressInterval()),
	}
	results := newResultStore()
	var inventoryLocks sync.Map
	withInventoryLock := func(name string, reconcile func() error) error {
		value, _ := inventoryLocks.LoadOrStore(name, &sync.Mutex{})
		lock := value.(*sync.Mutex)
		lock.Lock()
		defer lock.Unlock()
		return reconcile()
	}
	finishRackReconcile := func(name string, group *allocate.GroupKey, result controllerack.Result) error {
		inventory, getErr := snapshot.Inventory(name)
		if getErr == nil {
			if group == nil {
				results.put(inventory.Name, inventory.UID, result)
			} else {
				results.putGroup(inventory.Name, inventory.UID, group.RackGroup, result)
			}
			controller.queues.addStatus(statusKey{kind: statusInventory, name: inventory.Name, uid: inventory.UID})
		} else if !apierrors.IsNotFound(getErr) {
			return getErr
		}
		for _, cleanup := range result.CleanupNeeded {
			controller.queues.projections.Add(projectionKey{mode: projectionCleanup, cleanup: cleanup})
		}
		return nil
	}
	controller.reconcileInventory = func(ctx context.Context, name string) error {
		return withInventoryLock(name, func() error {
			result, err := rackReconciler.Reconcile(ctx, name)
			if err != nil {
				var ownershipErr *controllerack.RackOwnershipConflictError
				if errors.As(err, &ownershipErr) {
					if statusErr := finishRackReconcile(name, nil, result); statusErr != nil {
						return errors.Join(err, statusErr)
					}
				}
				return err
			}
			return finishRackReconcile(name, nil, result)
		})
	}
	controller.reconcileGroup = func(ctx context.Context, key allocate.GroupKey) error {
		return withInventoryLock(key.InventoryName, func() error {
			inventory, err := snapshot.Inventory(key.InventoryName)
			if apierrors.IsNotFound(err) || (err == nil && inventory.UID != key.InventoryUID) {
				return nil
			}
			if err != nil {
				return err
			}
			result, err := rackReconciler.ReconcileGroup(ctx, key)
			if err != nil {
				var ownershipErr *controllerack.RackOwnershipConflictError
				if errors.As(err, &ownershipErr) {
					if statusErr := finishRackReconcile(key.InventoryName, &key, result); statusErr != nil {
						return errors.Join(err, statusErr)
					}
				}
				return err
			}
			return finishRackReconcile(key.InventoryName, &key, result)
		})
	}
	controller.reconcileProjection = func(ctx context.Context, key projectionKey) error {
		var err error
		switch key.mode {
		case projectionApply:
			rack, getErr := snapshot.Rack(key.rackName)
			if apierrors.IsNotFound(getErr) {
				return nil
			}
			if getErr != nil {
				return getErr
			}
			slot := boundRackSlot(rack, key.slotIndex)
			if slot == nil {
				return nil
			}
			if projection.Ready(cleanupFor(rack, *slot, controllerack.CleanupNodeIneligible)) {
				return nil
			}
			_, err = projection.Project(ctx, key.rackName, key.slotIndex)
			controller.queues.addStatus(statusKey{kind: statusRack, name: rack.Name, uid: rack.UID})
			controller.queues.addStatus(statusKey{kind: statusInventory, name: rack.Spec.InventoryRef.Name, uid: rack.Spec.InventoryRef.UID})
		case projectionCleanup:
			err = withInventoryLock(key.cleanup.Binding.Coordinate.Group.InventoryName, func() error {
				_, cleanupErr := projection.Cleanup(ctx, key.cleanup)
				return cleanupErr
			})
			if err == nil {
				switch key.cleanup.Reason {
				case controllerack.CleanupCapacityShrink,
					controllerack.CleanupGroupRemoved,
					controllerack.CleanupRackDeleting,
					controllerack.CleanupInventoryDeleting:
					controller.queues.inventories.Add(key.cleanup.Binding.Coordinate.Group.InventoryName)
				default:
					controller.queues.groups.Add(key.cleanup.Binding.Coordinate.Group)
				}
			}
			controller.queues.addStatus(statusKey{
				kind: statusInventory, name: key.cleanup.Binding.Coordinate.Group.InventoryName,
				uid: key.cleanup.Binding.Coordinate.Group.InventoryUID,
			})
			if rack, getErr := snapshot.Rack(key.cleanup.RackName); getErr == nil {
				controller.queues.addStatus(statusKey{kind: statusRack, name: rack.Name, uid: rack.UID})
			}
		default:
			return fmt.Errorf("unknown projection work mode %d", key.mode)
		}
		return err
	}
	controller.reconcileStatus = func(ctx context.Context, key statusKey) error {
		switch key.kind {
		case statusInventory:
			return reconcileInventoryStatus(ctx, snapshot, statusReconciler, projection, results, key)
		case statusRack:
			return reconcileRackStatus(ctx, snapshot, statusReconciler, projection, key)
		default:
			return fmt.Errorf("unknown status work kind %d", key.kind)
		}
	}

	router := newEventRouter(inventoryInformer.GetIndexer(), rackInformer.GetIndexer(), newPlacementRegistry(), controller.queues)
	if err := addHandler(profileInformer, cache.ResourceEventHandlerFuncs{
		AddFunc: router.profileAdd, UpdateFunc: router.profileUpdate, DeleteFunc: router.profileDelete,
	}); err != nil {
		return nil, err
	}
	if err := addHandler(inventoryInformer, cache.ResourceEventHandlerFuncs{
		AddFunc: router.inventoryAdd, UpdateFunc: router.inventoryUpdate, DeleteFunc: router.inventoryDelete,
	}); err != nil {
		return nil, err
	}
	if err := addHandler(rackInformer, cache.ResourceEventHandlerFuncs{
		AddFunc: router.rackAdd, UpdateFunc: router.rackUpdate, DeleteFunc: router.rackDelete,
	}); err != nil {
		return nil, err
	}
	if err := addHandler(nodeInformer, cache.ResourceEventHandlerFuncs{
		AddFunc: router.nodeAdd, UpdateFunc: router.nodeUpdate, DeleteFunc: router.nodeDelete,
	}); err != nil {
		return nil, err
	}

	informers := []cache.SharedIndexInformer{profileInformer, inventoryInformer, rackInformer, nodeInformer}
	controller.starters = make([]func(context.Context), 0, len(informers))
	synced := make([]cache.InformerSynced, 0, len(informers))
	for _, informer := range informers {
		controller.starters = append(controller.starters, informer.RunWithContext)
		synced = append(synced, informer.HasSynced)
	}
	controller.waitForSync = func(ctx context.Context) bool {
		return cache.WaitForCacheSync(ctx.Done(), synced...)
	}
	return controller, nil
}

func addHandler(informer cache.SharedIndexInformer, handler cache.ResourceEventHandler) error {
	if _, err := informer.AddEventHandler(handler); err != nil {
		return fmt.Errorf("add informer event handler: %w", err)
	}
	return nil
}

// Ready reports whether this elected instance has synchronized every cache.
func (c *Controller) Ready() bool { return c.ready.Load() }

// Run starts informers, gates workers on cache synchronization, and waits for
// all workers to stop before returning.
func (c *Controller) Run(ctx context.Context) error {
	runCtx, cancel := context.WithCancel(ctx)
	var informerWG sync.WaitGroup
	for _, start := range c.starters {
		informerWG.Add(1)
		go func() {
			defer informerWG.Done()
			start(runCtx)
		}()
	}
	if !c.waitForSync(runCtx) {
		cancel()
		c.queues.shutdown()
		informerWG.Wait()
		cause := context.Cause(runCtx)
		if cause == nil {
			cause = errors.New("one or more informer caches did not synchronize")
		}
		return fmt.Errorf("cache sync failed: %w", cause)
	}

	var workers sync.WaitGroup
	startWorkers := func(count int, run func()) {
		for range count {
			workers.Add(1)
			go func() { defer workers.Done(); run() }()
		}
	}
	startWorkers(c.options.Workers, func() {
		for processNext(runCtx, c.queues.inventories, c.reconcileInventory) {
		}
	})
	startWorkers(c.options.Workers, func() {
		for processNext(runCtx, c.queues.groups, c.reconcileGroup) {
		}
	})
	startWorkers(c.options.Workers, func() {
		for processNext(runCtx, c.queues.projections, c.reconcileProjection) {
		}
	})
	startWorkers(c.options.Workers, func() {
		for c.processNextStatus(runCtx) {
		}
	})
	c.ready.Store(true)
	<-runCtx.Done()
	c.ready.Store(false)
	c.queues.shutdown()
	workers.Wait()
	cancel()
	informerWG.Wait()
	return nil
}

func (c *Controller) processNextStatus(ctx context.Context) bool {
	key, shutdown := c.queues.status.Get()
	if shutdown {
		return false
	}
	c.queues.statuses.start(key)
	defer c.queues.status.Done(key)
	if err := c.reconcileStatus(ctx, key); err != nil {
		if !c.queues.status.ShuttingDown() && !errors.Is(err, context.Canceled) {
			c.queues.status.AddRateLimited(key)
		}
		c.queues.statuses.finish(key, false)
		klog.FromContext(ctx).Error(err, "Controller reconciliation failed", "key", key)
		return true
	}
	c.queues.status.Forget(key)
	c.queues.statuses.finish(key, true)
	return true
}

func processNext[T comparable](
	ctx context.Context,
	queue workqueue.TypedRateLimitingInterface[T],
	reconcile func(context.Context, T) error,
) bool {
	key, shutdown := queue.Get()
	if shutdown {
		return false
	}
	defer queue.Done(key)
	if err := reconcile(ctx, key); err != nil {
		if !queue.ShuttingDown() && !errors.Is(err, context.Canceled) {
			queue.AddRateLimited(key)
		}
		klog.FromContext(ctx).Error(err, "Controller reconciliation failed", "key", key)
		return true
	}
	queue.Forget(key)
	return true
}

func boundRackSlot(rack *mokkav1alpha1.SGPURack, index int32) *mokkav1alpha1.SGPURackSlot {
	for i := range rack.Spec.Slots {
		if rack.Spec.Slots[i].Index == index && rack.Spec.Slots[i].NodeRef != nil {
			return &rack.Spec.Slots[i]
		}
	}
	return nil
}

type resultKey struct {
	name string
	uid  types.UID
}

type resultStore struct {
	mu      sync.RWMutex
	results map[resultKey]controllerack.Result
}

func newResultStore() *resultStore {
	return &resultStore{results: make(map[resultKey]controllerack.Result)}
}

func (s *resultStore) put(name string, uid types.UID, result controllerack.Result) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for key := range s.results {
		if key.name == name && key.uid != uid {
			delete(s.results, key)
		}
	}
	s.results[resultKey{name: name, uid: uid}] = result
}

func (s *resultStore) putGroup(name string, uid types.UID, group string, result controllerack.Result) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := resultKey{name: name, uid: uid}
	if previous, found := s.results[key]; found {
		for _, conflict := range previous.OwnershipConflicts {
			if conflict.RackGroup != group {
				result.OwnershipConflicts = append(result.OwnershipConflicts, conflict)
			}
		}
	}
	s.results[key] = result
}

func (s *resultStore) get(name string, uid types.UID) controllerack.Result {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result, found := s.results[resultKey{name: name, uid: uid}]
	if !found {
		return controllerack.Result{Accepted: true, ResolvedRefs: true}
	}
	return result
}

func newFilteredNodeInformer(nodes corev1client.NodeInterface) cache.SharedIndexInformer {
	return cache.NewSharedIndexInformer(
		newFilteredNodeListWatch(nodes),
		&corev1.Node{},
		0,
		statusNodeIndexers(),
	)
}

type nodeListerWatcher interface {
	List(context.Context, metav1.ListOptions) (*corev1.NodeList, error)
	Watch(context.Context, metav1.ListOptions) (watch.Interface, error)
}

func newFilteredNodeListWatch(nodes nodeListerWatcher) *cache.ListWatch {
	selector := labels.SelectorFromSet(labels.Set{allocate.EligibleNodeLabel: "true"}).String()
	return &cache.ListWatch{
		ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
			options.LabelSelector = selector
			return nodes.List(context.Background(), options)
		},
		WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
			options.LabelSelector = selector
			return nodes.Watch(context.Background(), options)
		},
		ListWithContextFunc: func(ctx context.Context, options metav1.ListOptions) (runtime.Object, error) {
			options.LabelSelector = selector
			return nodes.List(ctx, options)
		},
		WatchFuncWithContext: func(ctx context.Context, options metav1.ListOptions) (watch.Interface, error) {
			options.LabelSelector = selector
			return nodes.Watch(ctx, options)
		},
	}
}
