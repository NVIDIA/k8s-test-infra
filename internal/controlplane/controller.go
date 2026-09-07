// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package controlplane

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/leaderelection"
	rl "k8s.io/client-go/tools/leaderelection/resourcelock"

	"github.com/NVIDIA/k8s-test-infra/internal/mokkacontroller"
	versioned "github.com/NVIDIA/k8s-test-infra/pkg/generated/clientset/versioned"
)

// Controller owns the elected controller lifecycle and readiness state.
type Controller struct {
	config     Config
	kubeClient kubernetes.Interface
	reconciler *mokkacontroller.Controller
	readiness  *electionReadiness
}

// NewController builds Kubernetes clients and the informer-driven reconciler.
func NewController(config Config) (*Controller, error) {
	if err := ValidateControllerConfig(config); err != nil {
		return nil, err
	}
	restConfig, err := controllerRESTConfig(config)
	if err != nil {
		return nil, err
	}
	kubeClient, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("create Kubernetes client: %w", err)
	}
	mokkaClient, err := versioned.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("create Mokka client: %w", err)
	}
	reconciler, err := mokkacontroller.New(kubeClient, mokkaClient, config.Controller)
	if err != nil {
		return nil, fmt.Errorf("create Mokka controller: %w", err)
	}
	return &Controller{
		config: config, kubeClient: kubeClient, reconciler: reconciler,
		readiness: newElectionReadiness(),
	}, nil
}

// ValidateControllerConfig rejects settings that cannot make progress safely.
//
//nolint:cyclop // Each branch reports a distinct unsafe controller setting.
func ValidateControllerConfig(config Config) error {
	leaderElection := config.LeaderElection
	if leaderElection.Namespace == "" || leaderElection.Name == "" {
		return errors.New("leader-election namespace and name must not be empty")
	}
	if errs := validation.IsDNS1123Subdomain(leaderElection.Name); len(errs) > 0 {
		return fmt.Errorf("leader-election name %q is invalid: %s", leaderElection.Name, errs[0])
	}
	if errs := validation.IsDNS1123Label(leaderElection.Namespace); len(errs) > 0 {
		return fmt.Errorf("leader-election namespace %q is invalid: %s", leaderElection.Namespace, errs[0])
	}
	controller := config.Controller
	if controller.Workers < 1 || controller.StatusDebounce < 0 || controller.StatusProgressInterval < 0 ||
		controller.LiveNodeGetTimeout <= 0 {
		return errors.New("workers and live Node GET timeout must be positive and status intervals non-negative")
	}
	if controller.StatusProgressInterval > 0 && controller.StatusProgressInterval < controller.StatusDebounce {
		return errors.New("status progress interval must not be shorter than status debounce")
	}
	if leaderElection.LeaseDuration <= 0 || leaderElection.RenewDeadline <= 0 ||
		leaderElection.RetryPeriod <= 0 || leaderElection.LeaseDuration <= leaderElection.RenewDeadline ||
		leaderElection.RenewDeadline <= time.Duration(
			leaderelection.JitterFactor*float64(leaderElection.RetryPeriod),
		) {
		return errors.New("leader-election durations must satisfy lease > renew > retry*jitter")
	}
	if config.Kubernetes.QPS <= 0 || config.Kubernetes.Burst < 1 {
		return errors.New("kubernetes API QPS and burst must be positive")
	}
	return nil
}

// Ready reports whether this replica can participate in controller service.
// Standbys become ready after observing the Lease; a leader additionally waits
// for every informer cache to synchronize.
func (c *Controller) Ready() bool {
	return c != nil && c.reconciler != nil && c.readiness != nil && c.readiness.ready(c.reconciler.Ready())
}

// Run participates in Lease election and runs workers only while leading.
func (c *Controller) Run(ctx context.Context) error {
	identity, err := leaderIdentity()
	if err != nil {
		return err
	}
	lock := &rl.LeaseLock{
		LeaseMeta: metav1.ObjectMeta{
			Name: c.config.LeaderElection.Name, Namespace: c.config.LeaderElection.Namespace,
		},
		Client:     c.kubeClient.CoordinationV1(),
		LockConfig: rl.ResourceLockConfig{Identity: identity},
	}
	return runLeaderElection(ctx, c.config, lock, c.reconciler.Run, c.readiness)
}

func controllerRESTConfig(config Config) (*rest.Config, error) {
	var (
		result *rest.Config
		err    error
	)
	if config.Kubernetes.Kubeconfig != "" {
		result, err = clientcmd.BuildConfigFromFlags("", config.Kubernetes.Kubeconfig)
	} else {
		result, err = rest.InClusterConfig()
	}
	if err != nil {
		return nil, fmt.Errorf("load Kubernetes configuration: %w", err)
	}
	result = rest.CopyConfig(result)
	result.QPS = float32(config.Kubernetes.QPS)
	result.Burst = config.Kubernetes.Burst
	result.UserAgent = "mokka-control-plane"
	return result, nil
}

func leaderIdentity() (string, error) {
	hostname, err := os.Hostname()
	if err != nil {
		return "", fmt.Errorf("get hostname for leader identity: %w", err)
	}
	return hostname + "_" + uuid.NewString(), nil
}

func newLeaderElectionConfig(
	config Config,
	lock rl.Interface,
	run func(context.Context),
	readiness *electionReadiness,
) leaderelection.LeaderElectionConfig {
	onStartedLeading := run
	onStoppedLeading := func() {}
	var onNewLeader func(string)
	if readiness != nil {
		onStartedLeading = func(ctx context.Context) {
			readiness.startLeading()
			run(ctx)
		}
		onStoppedLeading = readiness.stop
		onNewLeader = func(identity string) {
			readiness.observeLeader(identity == lock.Identity())
		}
	}
	return leaderelection.LeaderElectionConfig{
		Lock:            lock,
		LeaseDuration:   config.LeaderElection.LeaseDuration,
		RenewDeadline:   config.LeaderElection.RenewDeadline,
		RetryPeriod:     config.LeaderElection.RetryPeriod,
		ReleaseOnCancel: true,
		Name:            config.LeaderElection.Name,
		Callbacks: leaderelection.LeaderCallbacks{
			OnStartedLeading: onStartedLeading,
			OnStoppedLeading: onStoppedLeading,
			OnNewLeader:      onNewLeader,
		},
	}
}

func runLeaderElection(
	ctx context.Context,
	config Config,
	lock rl.Interface,
	run func(context.Context) error,
	readiness *electionReadiness,
) error {
	if readiness != nil {
		readiness.start()
		defer readiness.stop()
	}
	electionCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	work := newLeaderWork()
	stopWork := cancel
	if readiness != nil {
		stopWork = func() {
			cancel()
			readiness.stop()
		}
	}
	draining := &drainingLock{Interface: lock, workDone: work.done, stopWork: stopWork}
	electionConfig := newLeaderElectionConfig(
		config, draining, work.onStartedLeading(run, cancel), readiness,
	)
	elector, err := leaderelection.NewLeaderElector(electionConfig)
	if err != nil {
		return fmt.Errorf("configure leader election: %w", err)
	}
	elector.Run(context.WithValue(electionCtx, leaderElectionContextKey{}, true))
	cancel()
	if work.finishElection() {
		<-work.done
		if err := work.result(); err != nil {
			return err
		}
	}
	if ctx.Err() != nil {
		return nil
	}
	return errors.New("leader election ended unexpectedly")
}

type electionReadinessState uint8

const (
	electionStopped electionReadinessState = iota
	electionWaiting
	electionStandby
	electionLeader
)

type electionReadiness struct {
	state atomic.Uint32
}

func newElectionReadiness() *electionReadiness {
	return &electionReadiness{}
}

func (r *electionReadiness) start() {
	r.state.Store(uint32(electionWaiting))
}

func (r *electionReadiness) observeLeader(self bool) {
	if self {
		r.startLeading()
		return
	}
	// OnNewLeader callbacks run asynchronously. Once this replica is elected,
	// a delayed observation of the previous leader must not mark it standby.
	r.state.CompareAndSwap(uint32(electionWaiting), uint32(electionStandby))
}

func (r *electionReadiness) startLeading() {
	for {
		state := electionReadinessState(r.state.Load())
		switch state {
		case electionWaiting, electionStandby:
			if r.state.CompareAndSwap(uint32(state), uint32(electionLeader)) {
				return
			}
		case electionStopped, electionLeader:
			return
		}
	}
}

func (r *electionReadiness) stop() {
	r.state.Store(uint32(electionStopped))
}

func (r *electionReadiness) ready(leaderReady bool) bool {
	switch electionReadinessState(r.state.Load()) {
	case electionStandby:
		return true
	case electionLeader:
		return leaderReady
	default:
		return false
	}
}

type leaderWork struct {
	mu               sync.Mutex
	done             chan struct{}
	electionFinished bool
	started          bool
	err              error
}

func newLeaderWork() *leaderWork {
	return &leaderWork{done: make(chan struct{})}
}

func (w *leaderWork) onStartedLeading(
	run func(context.Context) error,
	stopElection context.CancelFunc,
) func(context.Context) {
	return func(ctx context.Context) {
		w.start(ctx, run, stopElection)
	}
}

func (w *leaderWork) start(ctx context.Context, run func(context.Context) error, stopElection context.CancelFunc) {
	w.mu.Lock()
	if w.electionFinished {
		w.mu.Unlock()
		return
	}
	w.started = true
	w.mu.Unlock()

	err := run(ctx)
	w.mu.Lock()
	w.err = err
	w.mu.Unlock()
	close(w.done)
	stopElection()
}

func (w *leaderWork) finishElection() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.electionFinished = true
	return w.started
}

func (w *leaderWork) result() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.err
}

type drainingLock struct {
	rl.Interface
	workDone <-chan struct{}
	stopWork context.CancelFunc
}

type leaderElectionContextKey struct{}

func (l *drainingLock) Get(ctx context.Context) (*rl.LeaderElectionRecord, []byte, error) {
	// client-go starts release with a fresh context, so drain before its
	// potentially slow GET instead of waiting for the final Lease update.
	if ctx.Value(leaderElectionContextKey{}) == nil {
		if err := l.stopAndWait(ctx); err != nil {
			return nil, nil, err
		}
	}
	return l.Interface.Get(ctx)
}

func (l *drainingLock) Update(ctx context.Context, record rl.LeaderElectionRecord) error {
	if record.HolderIdentity == "" {
		if err := l.stopAndWait(ctx); err != nil {
			return err
		}
	}
	return l.Interface.Update(ctx, record)
}

func (l *drainingLock) stopAndWait(ctx context.Context) error {
	if l.stopWork != nil {
		l.stopWork()
	}
	select {
	case <-l.workDone:
		return nil
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}
