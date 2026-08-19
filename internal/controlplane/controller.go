// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package controlplane

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/google/uuid"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
	reconciler, err := mokkacontroller.New(kubeClient, mokkaClient, mokkacontroller.Options{
		Workers: config.Workers, StatusDebounce: config.StatusDebounce,
		StatusProgressInterval: config.StatusProgressInterval,
		LiveNodeGetTimeout:     config.LiveNodeGetTimeout,
	})
	if err != nil {
		return nil, fmt.Errorf("create Mokka controller: %w", err)
	}
	return &Controller{config: config, kubeClient: kubeClient, reconciler: reconciler}, nil
}

// ValidateControllerConfig rejects settings that cannot make progress safely.
//
//nolint:cyclop // Each branch reports a distinct unsafe controller setting.
func ValidateControllerConfig(config Config) error {
	if config.LeaderElectionNamespace == "" || config.LeaderElectionName == "" {
		return errors.New("leader-election namespace and name must not be empty")
	}
	if config.Workers < 1 || config.StatusDebounce < 0 || config.StatusProgressInterval < 0 ||
		config.LiveNodeGetTimeout <= 0 {
		return errors.New("workers and live Node GET timeout must be positive and status intervals non-negative")
	}
	if config.StatusProgressInterval > 0 && config.StatusProgressInterval < config.StatusDebounce {
		return errors.New("status progress interval must not be shorter than status debounce")
	}
	if config.LeaseDuration <= 0 || config.RenewDeadline <= 0 || config.RetryPeriod <= 0 ||
		config.LeaseDuration <= config.RenewDeadline ||
		config.RenewDeadline <= time.Duration(leaderelection.JitterFactor*float64(config.RetryPeriod)) {
		return errors.New("leader-election durations must satisfy lease > renew > retry*jitter")
	}
	if config.KubeAPIQPS <= 0 || config.KubeAPIBurst < 1 {
		return errors.New("kubernetes API QPS and burst must be positive")
	}
	return nil
}

// Ready reports whether this replica is leader and all informer caches synced.
func (c *Controller) Ready() bool {
	return c != nil && c.reconciler.Ready()
}

// Run participates in Lease election and runs workers only while leading.
func (c *Controller) Run(ctx context.Context) error {
	identity, err := leaderIdentity()
	if err != nil {
		return err
	}
	lock := &rl.LeaseLock{
		LeaseMeta: metav1.ObjectMeta{
			Name: c.config.LeaderElectionName, Namespace: c.config.LeaderElectionNamespace,
		},
		Client:     c.kubeClient.CoordinationV1(),
		LockConfig: rl.ResourceLockConfig{Identity: identity},
	}
	return runLeaderElection(ctx, c.config, lock, c.reconciler.Run)
}

func controllerRESTConfig(config Config) (*rest.Config, error) {
	var (
		result *rest.Config
		err    error
	)
	if config.Kubeconfig != "" {
		result, err = clientcmd.BuildConfigFromFlags("", config.Kubeconfig)
	} else {
		result, err = rest.InClusterConfig()
	}
	if err != nil {
		return nil, fmt.Errorf("load Kubernetes configuration: %w", err)
	}
	result = rest.CopyConfig(result)
	result.QPS = float32(config.KubeAPIQPS)
	result.Burst = config.KubeAPIBurst
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

func newLeaderElectionConfig(config Config, lock rl.Interface, run func(context.Context)) leaderelection.LeaderElectionConfig {
	return leaderelection.LeaderElectionConfig{
		Lock: lock, LeaseDuration: config.LeaseDuration, RenewDeadline: config.RenewDeadline,
		RetryPeriod: config.RetryPeriod, ReleaseOnCancel: true, Name: config.LeaderElectionName,
		Callbacks: leaderelection.LeaderCallbacks{
			OnStartedLeading: run,
			OnStoppedLeading: func() {},
		},
	}
}

func runLeaderElection(
	ctx context.Context,
	config Config,
	lock rl.Interface,
	run func(context.Context) error,
) error {
	electionCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	workCtx, stopWork := context.WithCancel(electionCtx)
	defer stopWork()
	work := newLeaderWork()
	draining := &drainingLock{Interface: lock, workDone: work.done, stopWork: stopWork}
	electionConfig := newLeaderElectionConfig(config, draining, func(context.Context) {
		work.start(workCtx, run, cancel)
	})
	elector, err := leaderelection.NewLeaderElector(electionConfig)
	if err != nil {
		return fmt.Errorf("configure leader election: %w", err)
	}
	elector.Run(electionCtx)
	stopWork()
	if !work.finishElection() {
		return nil
	}
	<-work.done
	return work.result()
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

func (l *drainingLock) Update(ctx context.Context, record rl.LeaderElectionRecord) error {
	if record.HolderIdentity == "" {
		if l.stopWork != nil {
			l.stopWork()
		}
		select {
		case <-l.workDone:
		case <-ctx.Done():
			return context.Cause(ctx)
		}
	}
	return l.Interface.Update(ctx, record)
}
