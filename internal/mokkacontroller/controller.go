// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

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
	"k8s.io/apimachinery/pkg/api/equality"
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

	mokkav1alpha1 "github.com/NVIDIA/k8s-test-infra/internal/controlplane/api/v1alpha1"
	sgpuinventory "github.com/NVIDIA/k8s-test-infra/internal/sgpu/inventory"
	"github.com/NVIDIA/k8s-test-infra/internal/sgpu/inventory/allocate"
	inventorycleanup "github.com/NVIDIA/k8s-test-infra/internal/sgpu/inventory/cleanup"
	inventorymetadata "github.com/NVIDIA/k8s-test-infra/internal/sgpu/inventory/metadata"
	nodecatalog "github.com/NVIDIA/k8s-test-infra/internal/sgpu/inventory/nodecatalog"
	inventoryprojection "github.com/NVIDIA/k8s-test-infra/internal/sgpu/inventory/projection"
	rackrender "github.com/NVIDIA/k8s-test-infra/internal/sgpu/inventory/rack"
	inventorystatus "github.com/NVIDIA/k8s-test-infra/internal/sgpu/inventory/status"
	versioned "github.com/NVIDIA/k8s-test-infra/pkg/generated/clientset/versioned"
	mokkainformers "github.com/NVIDIA/k8s-test-infra/pkg/generated/informers/externalversions"
)

const (
	defaultStatusDebounce             = 100 * time.Millisecond
	defaultStatusProgressInterval     = time.Second
	defaultLiveNodeGetTimeout         = 2 * time.Second
	projectionCleanupRevisionAttempts = 5
)

var errProjectionCleanupRevisionChanged = errors.New("allocation revision changed during projection cleanup")

// Options controls worker concurrency and aggregate-status coalescing.
type Options struct {
	Workers int
	// StatusDebounce is the quiet period before a final status update.
	StatusDebounce time.Duration
	// StatusProgressInterval bounds status staleness while changes continue.
	StatusProgressInterval time.Duration
	// LiveNodeGetTimeout bounds the exact GET used when a Node leaves the filtered cache.
	LiveNodeGetTimeout time.Duration
}

// DefaultOptions returns the production controller defaults.
func DefaultOptions() Options {
	return Options{
		Workers: 2, StatusDebounce: defaultStatusDebounce,
		StatusProgressInterval: defaultStatusProgressInterval,
		LiveNodeGetTimeout:     defaultLiveNodeGetTimeout,
	}
}

func (o Options) validate() error {
	if o.Workers < 1 {
		return errors.New("workers must be positive")
	}
	if o.StatusDebounce < 0 {
		return errors.New("status debounce must not be negative")
	}
	if o.StatusProgressInterval < 0 {
		return errors.New("status progress interval must not be negative")
	}
	if o.LiveNodeGetTimeout < 0 {
		return errors.New("live Node GET timeout must not be negative")
	}
	if o.StatusDebounce > 0 && o.statusProgressInterval() < o.StatusDebounce {
		return errors.New("status progress interval must not be shorter than status debounce")
	}
	return nil
}

func (o Options) liveNodeGetTimeout() time.Duration {
	if o.LiveNodeGetTimeout > 0 {
		return o.LiveNodeGetTimeout
	}
	return defaultLiveNodeGetTimeout
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
	nodeIndex int32
	fresh     bool
	cleanup   inventorycleanup.CleanupNeeded
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
	groups      workqueue.TypedRateLimitingInterface[allocate.RackGroupKey]
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
		groups:      workqueue.NewTypedRateLimitingQueue(workqueue.DefaultTypedControllerRateLimiter[allocate.RackGroupKey]()),
		projections: workqueue.NewTypedRateLimitingQueue(workqueue.DefaultTypedControllerRateLimiter[projectionKey]()),
		status:      statusQueue,
		statuses:    newStatusCoalescer(statusQueue, debounce, progressInterval, realStatusScheduler{}),
	}
}

func (q *queues) addStatus(key statusKey) {
	q.statuses.dirty(key)
}

func (q *queues) addInventories(names []string) {
	for _, name := range names {
		q.inventories.Add(name)
	}
}

func (q *queues) shutdown() {
	q.statuses.shutdown()
	q.inventories.ShutDown()
	q.groups.ShutDown()
	q.projections.ShutDown()
	q.status.ShutDown()
}

type handlerSpec struct {
	informer cache.SharedIndexInformer
	handler  cache.ResourceEventHandler
}

// Controller owns process-lifetime informer caches and leader-lifetime workers.
type Controller struct {
	options Options
	queues  *queues

	cacheReady   atomic.Bool
	leaderReady  atomic.Bool
	cachesSynced chan struct{}

	informers        []cache.SharedIndexInformer
	leaderHandlers   []handlerSpec
	waitForCacheSync func(context.Context) bool
	snapshot         *informerCache

	reconcileInventory  func(context.Context, string) error
	reconcileGroup      func(context.Context, allocate.RackGroupKey) error
	reconcileProjection func(context.Context, projectionKey) error
	reconcileStatus     func(context.Context, statusKey) error
}

// New assembles shared zero-resync informers and all committed reconcilers.
func New(kubeClient kubernetes.Interface, mokkaClient versioned.Interface, options Options) (*Controller, error) {
	if kubeClient == nil {
		return nil, errors.New("kubernetes client must not be nil")
	}
	return newForNodes(kubeClient.CoreV1().Nodes(), mokkaClient, options)
}

//nolint:cyclop // Keeping ownership wiring together makes every informer-to-queue edge auditable.
func newForNodes(nodes corev1client.NodeInterface, mokkaClient versioned.Interface, options Options) (*Controller, error) {
	if nodes == nil || mokkaClient == nil {
		return nil, errors.New("controller clients must not be nil")
	}
	if err := options.validate(); err != nil {
		return nil, err
	}

	factory := mokkainformers.NewSharedInformerFactory(mokkaClient, 0)
	mokka := factory.Mokka().V1alpha1()
	profiles := mokka.SGPURackProfiles()
	inventories := mokka.SGPUInventories()
	racks := mokka.SGPURacks()
	profileInformer := profiles.Informer()
	inventoryInformer := inventories.Informer()
	rackInformer := racks.Informer()
	if err := inventoryInformer.AddIndexers(sgpuinventory.InventoryIndexers()); err != nil {
		return nil, fmt.Errorf("add inventory indexes: %w", err)
	}
	if err := rackInformer.AddIndexers(sgpuinventory.Indexers()); err != nil {
		return nil, fmt.Errorf("add rack indexes: %w", err)
	}
	nodeInformer, err := newFilteredNodeInformer(nodes)
	if err != nil {
		return nil, err
	}
	nodeCatalog := nodecatalog.New()

	snapshot := newInformerCache(
		inventories.Lister(), profiles.Lister(), rackInformer.GetIndexer(), nodeCatalog, nodes,
		options,
	)
	projection := inventoryprojection.NewController(snapshot, nodes)
	allocation := sgpuinventory.NewAllocationCache(snapshot)
	rackReconciler := sgpuinventory.NewReconcilerWithAllocationCache(
		snapshot,
		mokkaClient.MokkaV1alpha1().SGPUInventories(),
		mokkaClient.MokkaV1alpha1().SGPURacks(),
		projection,
		allocation,
	)
	statusReconciler := inventorystatus.NewReconciler(
		mokkaClient.MokkaV1alpha1().SGPUInventories(),
		mokkaClient.MokkaV1alpha1().SGPURacks(),
		nil,
	)

	controller := &Controller{
		options:      options,
		queues:       newQueuesWithStatusIntervals(options.StatusDebounce, options.statusProgressInterval()),
		cachesSynced: make(chan struct{}),
		snapshot:     snapshot,
	}
	// Test environments use short-lived controller instances and only a few inventories,
	// so per-name locks and results intentionally live for the controller's lifetime.
	results := newResultStore()
	rackWaiters := newRackConflictWaiters()
	routeCapacityTransitions := func() {
		controller.queues.addInventories(allocation.CapacityTransitions())
	}
	var inventoryLocks sync.Map
	withInventoryLock := func(name string, reconcile func() error) error {
		value, _ := inventoryLocks.LoadOrStore(name, &sync.Mutex{})
		lock := value.(*sync.Mutex)
		lock.Lock()
		defer lock.Unlock()
		return reconcile()
	}
	finishRackReconcile := func(
		name string,
		group *allocate.RackGroupKey,
		observed *mokkav1alpha1.SGPUInventory,
		result sgpuinventory.Result,
	) error {
		inventory, getErr := snapshot.Inventory(name)
		if getErr == nil {
			updateRackConflictWaiters(rackWaiters, inventory, observed, group, result)
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
		if projection.HasAcknowledgedCleanups() {
			for _, binding := range result.Allocation.Retained {
				group := binding.Coordinate.Group
				rackName := rackrender.RackName(
					group.InventoryName, group.InventoryUID, group.RackGroup, binding.Coordinate.RackIndex,
				)
				cleanup, acknowledged := projection.AcknowledgedCleanup(rackName, binding)
				if acknowledged && cleanupTracksAllocation(cleanup.Reason) {
					controller.queues.projections.Add(projectionKey{mode: projectionCleanup, cleanup: cleanup})
				}
			}
		}
		return nil
	}
	controller.reconcileInventory = func(ctx context.Context, name string) error {
		return withInventoryLock(name, func() error {
			observed, _ := snapshot.Inventory(name)
			result, err := rackReconciler.Reconcile(ctx, name)
			routeCapacityTransitions()
			if err != nil {
				var ownershipErr *sgpuinventory.OwnershipConflictError
				if errors.As(err, &ownershipErr) || errors.Is(err, sgpuinventory.ErrRackCacheStale) {
					if statusErr := finishRackReconcile(name, nil, observed, result); statusErr != nil {
						return errors.Join(err, statusErr)
					}
				}
				return err
			}
			return finishRackReconcile(name, nil, observed, result)
		})
	}
	controller.reconcileGroup = func(ctx context.Context, key allocate.RackGroupKey) error {
		return withInventoryLock(key.InventoryName, func() error {
			inventory, err := snapshot.Inventory(key.InventoryName)
			if apierrors.IsNotFound(err) || (err == nil && inventory.UID != key.InventoryUID) {
				return nil
			}
			if err != nil {
				return err
			}
			observed := inventory.DeepCopy()
			result, err := rackReconciler.ReconcileGroup(ctx, key)
			routeCapacityTransitions()
			if err != nil {
				var ownershipErr *sgpuinventory.OwnershipConflictError
				if errors.As(err, &ownershipErr) || errors.Is(err, sgpuinventory.ErrRackCacheStale) {
					if statusErr := finishRackReconcile(key.InventoryName, &key, observed, result); statusErr != nil {
						return errors.Join(err, statusErr)
					}
				}
				return err
			}
			return finishRackReconcile(key.InventoryName, &key, observed, result)
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
			slot := boundRackSlot(rack, key.nodeIndex)
			if slot == nil {
				return nil
			}
			if !key.fresh && projection.Ready(cleanupFor(rack, *slot, inventorycleanup.CleanupNodeIneligible)) {
				return nil
			}
			if key.fresh {
				_, err = projection.ProjectFresh(ctx, key.rackName, key.nodeIndex)
			} else {
				_, err = projection.Project(ctx, key.rackName, key.nodeIndex)
			}
			controller.queues.addStatus(statusKey{kind: statusRack, name: rack.Name, uid: rack.UID})
			controller.queues.addStatus(statusKey{kind: statusInventory, name: rack.Spec.InventoryRef.Name, uid: rack.Spec.InventoryRef.UID})
		case projectionCleanup:
			var outcome inventoryprojection.Outcome
			err = withInventoryLock(key.cleanup.Binding.Coordinate.Group.InventoryName, func() error {
				if !cleanupTracksAllocation(key.cleanup.Reason) {
					var cleanupErr error
					outcome, cleanupErr = projection.Cleanup(ctx, key.cleanup)
					return cleanupErr
				}
				retryErr := retryProjectionCleanupRevision(ctx, func() (bool, error) {
					if !cleanupBindingCurrent(snapshot, key.cleanup) {
						var cleanupErr error
						outcome, cleanupErr = projection.Cleanup(ctx, key.cleanup)
						return true, cleanupErr
					}
					desired, revision, desiredErr := allocation.BindingDesiredRevision(key.cleanup.Binding)
					if desiredErr != nil {
						return true, desiredErr
					}
					if desired {
						var projectErr error
						outcome, projectErr = projection.ProjectFresh(
							ctx,
							key.cleanup.RackName,
							key.cleanup.Binding.Coordinate.NodeIndex,
						)
						return true, projectErr
					}
					if !allocation.RevisionCurrent(revision) {
						return false, nil
					}
					var cleanupErr error
					outcome, cleanupErr = projection.Cleanup(ctx, key.cleanup)
					if allocation.RevisionCurrent(revision) {
						return true, cleanupErr
					}
					projection.RevokeCleanup(key.cleanup)
					return false, nil
				})
				if errors.Is(retryErr, errProjectionCleanupRevisionChanged) &&
					!cleanupBindingCurrent(snapshot, key.cleanup) {
					var cleanupErr error
					outcome, cleanupErr = projection.Cleanup(ctx, key.cleanup)
					return cleanupErr
				}
				return retryErr
			})
			if err == nil && outcome.State == inventoryprojection.StateCleaned {
				switch key.cleanup.Reason {
				case inventorycleanup.CleanupCapacityShrink,
					inventorycleanup.CleanupCapacityRejected,
					inventorycleanup.CleanupGroupRemoved,
					inventorycleanup.CleanupRackDeleting,
					inventorycleanup.CleanupInventoryDeleting:
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
		return projectionRetryError(key.mode, err)
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

	router := newEventRouter(
		inventoryInformer.GetIndexer(), rackInformer.GetIndexer(), newPlacementRegistry(), controller.queues,
		allocation.InvalidateAllocation,
		allocation.InvalidateCapacity,
	)
	router.waiters = rackWaiters
	router.capacityWakeup = allocation.CapacityWakeup
	router.observeRackStatus = statusReconciler.ObserveRackStatus
	router.forgetRackStatus = statusReconciler.ForgetRackStatus
	controller.informers = []cache.SharedIndexInformer{
		profileInformer, inventoryInformer, rackInformer, nodeInformer,
	}
	controller.leaderHandlers = []handlerSpec{
		{
			informer: profileInformer,
			handler: cache.ResourceEventHandlerFuncs{
				AddFunc: router.profileAdd, UpdateFunc: router.profileUpdate, DeleteFunc: router.profileDelete,
			},
		},
		{
			informer: inventoryInformer,
			handler: cache.ResourceEventHandlerFuncs{
				AddFunc: router.inventoryAdd, UpdateFunc: router.inventoryUpdate, DeleteFunc: router.inventoryDelete,
			},
		},
		{
			informer: rackInformer,
			handler: cache.ResourceEventHandlerFuncs{
				AddFunc: router.rackAdd, UpdateFunc: router.rackUpdate, DeleteFunc: router.rackDelete,
			},
		},
		{
			informer: nodeInformer,
			handler: cache.ResourceEventHandlerFuncs{
				AddFunc: func(object any) {
					node, ok := eventObject[*corev1.Node](object)
					if !ok {
						return
					}
					nodeCatalog.Upsert(node)
					router.nodeAdd(node)
				},
				UpdateFunc: func(oldObject, newObject any) {
					node, ok := eventObject[*corev1.Node](newObject)
					if !ok {
						return
					}
					nodeCatalog.Upsert(node)
					router.nodeUpdate(oldObject, node)
				},
				DeleteFunc: func(object any) {
					node, ok := eventObject[*corev1.Node](object)
					if !ok {
						return
					}
					nodeCatalog.Delete(node.Name, node.UID)
					router.nodeDelete(node)
				},
			},
		},
	}
	cacheSynced := make([]cache.InformerSynced, 0, len(controller.informers))
	for _, informer := range controller.informers {
		cacheSynced = append(cacheSynced, informer.HasSynced)
	}
	controller.waitForCacheSync = func(ctx context.Context) bool {
		return cache.WaitForCacheSync(ctx.Done(), cacheSynced...)
	}
	return controller, nil
}

func metadataConflict(err error) bool {
	var conflict *inventoryprojection.MetadataConflictError
	return errors.As(err, &conflict)
}

func projectionRetryError(mode projectionMode, err error) error {
	if mode == projectionApply && metadataConflict(err) {
		return nil
	}
	return err
}

func retryProjectionCleanupRevision(ctx context.Context, attempt func() (bool, error)) error {
	for range projectionCleanupRevisionAttempts {
		if err := context.Cause(ctx); err != nil {
			return err
		}
		settled, err := attempt()
		if err != nil || settled {
			return err
		}
		if err := context.Cause(ctx); err != nil {
			return err
		}
	}
	return errProjectionCleanupRevisionChanged
}

func updateRackConflictWaiters(
	waiters *rackConflictWaiters,
	current, observed *mokkav1alpha1.SGPUInventory,
	group *allocate.RackGroupKey,
	result sgpuinventory.Result,
) {
	// A result computed from an obsolete Inventory must not restore waiters
	// that its update event already discarded.
	if waiters == nil || current == nil || observed == nil || current.UID != observed.UID ||
		!equality.Semantic.DeepEqual(current.Spec, observed.Spec) ||
		!equality.Semantic.DeepEqual(current.DeletionTimestamp, observed.DeletionTimestamp) {
		return
	}
	if group == nil {
		waiters.replaceInventory(current, result.OwnershipConflicts)
		return
	}
	waiters.replaceGroup(*group, result.OwnershipConflicts)
}

func addHandler(
	informer cache.SharedIndexInformer,
	handler cache.ResourceEventHandler,
) (cache.ResourceEventHandlerRegistration, error) {
	registration, err := informer.AddEventHandler(handler)
	if err != nil {
		return nil, fmt.Errorf("add informer event handler: %w", err)
	}
	return registration, nil
}

// CachesSynced is closed after every process-lifetime informer store has synchronized.
func (c *Controller) CachesSynced() <-chan struct{} { return c.cachesSynced }

// CacheReady reports whether synchronized process-lifetime caches are running.
func (c *Controller) CacheReady() bool { return c.cacheReady.Load() }

// LeaderReady reports whether leader handlers have replayed and workers are running.
func (c *Controller) LeaderReady() bool { return c.leaderReady.Load() }

// RunCaches runs informer stores for the process lifetime without routing write work.
func (c *Controller) RunCaches(ctx context.Context) error {
	runCtx, cancel := context.WithCancel(ctx)
	var informers sync.WaitGroup
	for _, informer := range c.informers {
		informers.Add(1)
		go func() {
			defer informers.Done()
			informer.RunWithContext(runCtx)
		}()
	}
	if !c.waitForCacheSync(runCtx) {
		cancel()
		informers.Wait()
		if ctx.Err() != nil {
			return nil
		}
		return errors.New("cache sync failed: one or more informer stores did not synchronize")
	}

	c.cacheReady.Store(true)
	close(c.cachesSynced)
	<-runCtx.Done()
	c.cacheReady.Store(false)
	cancel()
	informers.Wait()
	return nil
}

// RunLeader attaches write-side handlers to warm caches and runs workers for one elected term.
func (c *Controller) RunLeader(ctx context.Context) error {
	if !c.CacheReady() {
		return ErrCacheNotReady
	}

	detachHandlers, err := c.attachLeaderHandlers(ctx)
	if err != nil {
		c.queues.shutdown()
		return err
	}
	workers := c.startLeaderWorkers(ctx)
	c.leaderReady.Store(true)
	<-ctx.Done()
	c.leaderReady.Store(false)
	shutdownErr := detachHandlers()
	c.queues.shutdown()
	workers.Wait()
	return shutdownErr
}

func (c *Controller) attachLeaderHandlers(ctx context.Context) (func() error, error) {
	registrations := make([]cache.ResourceEventHandlerRegistration, 0, len(c.leaderHandlers))
	registered := make([]handlerSpec, 0, len(c.leaderHandlers))
	for _, spec := range c.leaderHandlers {
		registration, err := addHandler(spec.informer, spec.handler)
		if err != nil {
			return nil, errors.Join(err, shutDownEventHandlers(registered, registrations))
		}
		registered = append(registered, spec)
		registrations = append(registrations, registration)
	}

	handlersSynced := make([]cache.InformerSynced, 0, len(registrations))
	for _, registration := range registrations {
		handlersSynced = append(handlersSynced, registration.HasSynced)
	}
	if !cache.WaitForCacheSync(ctx.Done(), handlersSynced...) {
		shutdownErr := shutDownEventHandlers(registered, registrations)
		if ctx.Err() != nil {
			return nil, errors.Join(context.Cause(ctx), shutdownErr)
		}
		return nil, errors.Join(errors.New("leader handler sync failed"), shutdownErr)
	}
	return func() error { return shutDownEventHandlers(registered, registrations) }, nil
}

func (c *Controller) startLeaderWorkers(ctx context.Context) *sync.WaitGroup {
	workers := &sync.WaitGroup{}
	startWorkers := func(count int, run func()) {
		for range count {
			workers.Add(1)
			go func() { defer workers.Done(); run() }()
		}
	}
	startWorkers(c.options.Workers, func() {
		for processNext(ctx, c.queues.inventories, c.reconcileInventory) {
		}
	})
	startWorkers(c.options.Workers, func() {
		for processNext(ctx, c.queues.groups, c.reconcileGroup) {
		}
	})
	startWorkers(c.options.Workers, func() {
		for processNext(ctx, c.queues.projections, c.reconcileProjection) {
		}
	})
	startWorkers(c.options.Workers, func() {
		for c.processNextStatus(ctx) {
		}
	})
	return workers
}

func shutDownEventHandlers(
	specs []handlerSpec,
	registrations []cache.ResourceEventHandlerRegistration,
) error {
	var errs []error
	for index := len(registrations) - 1; index >= 0; index-- {
		if err := cache.ShutDownEventHandler(specs[index].informer, registrations[index]); err != nil {
			errs = append(errs, fmt.Errorf("shut down informer event handler: %w", err))
		}
	}
	return errors.Join(errs...)
}

func (c *Controller) processNextStatus(ctx context.Context) bool {
	key, shutdown := c.queues.status.Get()
	if shutdown {
		return false
	}
	if ctx.Err() != nil {
		c.queues.statuses.start(key)
		c.queues.status.Forget(key)
		c.queues.status.Done(key)
		c.queues.statuses.finish(key, false)
		return false
	}
	c.queues.statuses.start(key)
	defer c.queues.status.Done(key)
	if err := c.reconcileStatus(ctx, key); err != nil {
		if shouldRetry(ctx, err) && !c.queues.status.ShuttingDown() {
			c.queues.status.AddRateLimited(key)
		} else {
			c.queues.status.Forget(key)
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
	if ctx.Err() != nil {
		queue.Forget(key)
		queue.Done(key)
		return false
	}
	defer queue.Done(key)
	if err := reconcile(ctx, key); err != nil {
		if shouldRetry(ctx, err) && !queue.ShuttingDown() {
			queue.AddRateLimited(key)
		} else {
			queue.Forget(key)
		}
		klog.FromContext(ctx).Error(err, "Controller reconciliation failed", "key", key)
		return true
	}
	queue.Forget(key)
	return true
}

func shouldRetry(ctx context.Context, err error) bool {
	return ctx.Err() == nil && !errors.Is(err, context.Canceled)
}

func boundRackSlot(rack *mokkav1alpha1.SGPURack, index int32) *mokkav1alpha1.SGPURackNode {
	for i := range rack.Spec.Nodes {
		if rack.Spec.Nodes[i].Index == index && rack.Spec.Nodes[i].NodeRef != nil {
			return &rack.Spec.Nodes[i]
		}
	}
	return nil
}

func cleanupTracksAllocation(reason inventorycleanup.CleanupReason) bool {
	switch reason {
	case inventorycleanup.CleanupCapacityShrink,
		inventorycleanup.CleanupCapacityRejected,
		inventorycleanup.CleanupGroupRemoved,
		inventorycleanup.CleanupNodeIneligible,
		inventorycleanup.CleanupSelectorMismatch:
		return true
	case inventorycleanup.CleanupRackDeleting, inventorycleanup.CleanupInventoryDeleting:
		return false
	default:
		return false
	}
}

func cleanupBindingCurrent(snapshot *informerCache, cleanup inventorycleanup.CleanupNeeded) bool {
	rack, err := snapshot.Rack(cleanup.RackName)
	if err != nil || rack.UID != cleanup.RackUID {
		return false
	}
	binding := cleanup.Binding
	if rack.Spec.InventoryRef.Name != binding.Coordinate.Group.InventoryName ||
		rack.Spec.InventoryRef.UID != binding.Coordinate.Group.InventoryUID ||
		rack.Spec.Identity.RackGroup != binding.Coordinate.Group.RackGroup ||
		rack.Spec.Identity.RackIndex != binding.Coordinate.RackIndex {
		return false
	}
	slot := boundRackSlot(rack, binding.Coordinate.NodeIndex)
	return slot != nil && slot.NodeRef.Name == binding.Node.Name && slot.NodeRef.UID == binding.Node.UID
}

type resultKey struct {
	name string
	uid  types.UID
}

type resultStore struct {
	mu      sync.RWMutex
	results map[resultKey]sgpuinventory.Result
}

func newResultStore() *resultStore {
	return &resultStore{results: make(map[resultKey]sgpuinventory.Result)}
}

func (s *resultStore) put(name string, uid types.UID, result sgpuinventory.Result) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for key := range s.results {
		if key.name == name && key.uid != uid {
			delete(s.results, key)
		}
	}
	s.results[resultKey{name: name, uid: uid}] = result
}

func (s *resultStore) putGroup(name string, uid types.UID, group string, result sgpuinventory.Result) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := resultKey{name: name, uid: uid}
	if result.InventoryAllocation != nil {
		result.Allocation = *result.InventoryAllocation
		result.InventoryAllocation = nil
	}
	if previous, found := s.results[key]; found {
		for _, conflict := range previous.OwnershipConflicts {
			if conflict.RackGroup != group {
				result.OwnershipConflicts = append(result.OwnershipConflicts, conflict)
			}
		}
	}
	s.results[key] = result
}

func (s *resultStore) get(name string, uid types.UID) sgpuinventory.Result {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result, found := s.results[resultKey{name: name, uid: uid}]
	if !found {
		return sgpuinventory.Result{Accepted: true, ResolvedRefs: true}
	}
	return result
}

func newFilteredNodeInformer(nodes corev1client.NodeInterface) (cache.SharedIndexInformer, error) {
	informer := cache.NewSharedIndexInformer(
		newFilteredNodeListWatch(nodes),
		&corev1.Node{},
		0,
		cache.Indexers{},
	)
	if err := informer.SetTransform(compactNodeObject); err != nil {
		return nil, fmt.Errorf("set Node informer transform: %w", err)
	}
	return informer, nil
}

func compactNodeObject(object any) (any, error) {
	node, ok := object.(*corev1.Node)
	if !ok {
		return nil, fmt.Errorf("compact Node received %T", object)
	}
	annotations := make(map[string]string, 1)
	if assignment := node.Annotations[inventorymetadata.AssignmentAnnotation]; assignment != "" {
		annotations[inventorymetadata.AssignmentAnnotation] = assignment
	}
	managedFields := inventoryprojection.CompactManagedFields(node.ManagedFields)
	return &corev1.Node{
		TypeMeta: node.TypeMeta,
		ObjectMeta: metav1.ObjectMeta{
			Name: node.Name, UID: node.UID, ResourceVersion: node.ResourceVersion,
			CreationTimestamp: node.CreationTimestamp, DeletionTimestamp: node.DeletionTimestamp,
			Labels: node.Labels, Annotations: annotations, ManagedFields: managedFields,
		},
	}, nil
}

type nodeListerWatcher interface {
	List(context.Context, metav1.ListOptions) (*corev1.NodeList, error)
	Watch(context.Context, metav1.ListOptions) (watch.Interface, error)
}

func newFilteredNodeListWatch(nodes nodeListerWatcher) *cache.ListWatch {
	selector := labels.SelectorFromSet(labels.Set{allocate.EligibleNodeLabel: "true"}).String()
	return &cache.ListWatch{
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
