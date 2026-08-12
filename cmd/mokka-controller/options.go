// Copyright (c) 2026, NVIDIA CORPORATION.  All rights reserved.
// Licensed under the Apache License, Version 2.0 (the "License");

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"sync"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/leaderelection"
	rl "k8s.io/client-go/tools/leaderelection/resourcelock"

	"github.com/NVIDIA/k8s-test-infra/internal/mokkacontroller"
	versioned "github.com/NVIDIA/k8s-test-infra/pkg/generated/clientset/versioned"
	"github.com/google/uuid"
)

const shutdownTimeout = 5 * time.Second

type options struct {
	Kubeconfig        string
	HealthBindAddress string
	LeaseNamespace    string
	LeaseName         string
	LeaseDuration     time.Duration
	RenewDeadline     time.Duration
	RetryPeriod       time.Duration
	Workers           int
	StatusDebounce    time.Duration
	KubeAPIQPS        float64
	KubeAPIBurst      int
}

func defaultOptions() options {
	namespace := os.Getenv("POD_NAMESPACE")
	if namespace == "" {
		namespace = "default"
	}
	controllerOptions := mokkacontroller.DefaultOptions()
	return options{
		HealthBindAddress: ":8081",
		LeaseNamespace:    namespace,
		LeaseName:         "mokka-controller.mokka.nvidia.com",
		LeaseDuration:     15 * time.Second,
		RenewDeadline:     10 * time.Second,
		RetryPeriod:       2 * time.Second,
		Workers:           controllerOptions.Workers,
		StatusDebounce:    controllerOptions.StatusDebounce,
		KubeAPIQPS:        50,
		KubeAPIBurst:      100,
	}
}

func (o *options) addFlags(flags *flag.FlagSet) {
	flags.StringVar(&o.Kubeconfig, "kubeconfig", o.Kubeconfig, "Path to a kubeconfig; empty uses in-cluster configuration.")
	flags.StringVar(&o.HealthBindAddress, "health-bind-address", o.HealthBindAddress, "Address for health and readiness probes.")
	flags.StringVar(&o.LeaseNamespace, "leader-election-namespace", o.LeaseNamespace, "Namespace containing the leader-election Lease.")
	flags.StringVar(&o.LeaseName, "leader-election-name", o.LeaseName, "Name of the leader-election Lease.")
	flags.DurationVar(&o.LeaseDuration, "leader-election-lease-duration", o.LeaseDuration, "Leader-election lease duration.")
	flags.DurationVar(&o.RenewDeadline, "leader-election-renew-deadline", o.RenewDeadline, "Leader-election renew deadline.")
	flags.DurationVar(&o.RetryPeriod, "leader-election-retry-period", o.RetryPeriod, "Leader-election retry period.")
	flags.IntVar(&o.Workers, "workers", o.Workers, "Workers per controller queue.")
	flags.DurationVar(&o.StatusDebounce, "status-debounce", o.StatusDebounce, "Aggregate status debounce duration.")
	flags.Float64Var(&o.KubeAPIQPS, "kube-api-qps", o.KubeAPIQPS, "Kubernetes client QPS limit.")
	flags.IntVar(&o.KubeAPIBurst, "kube-api-burst", o.KubeAPIBurst, "Kubernetes client burst limit.")
}

func (o options) validate() error {
	if o.HealthBindAddress == "" || o.LeaseNamespace == "" || o.LeaseName == "" {
		return fmt.Errorf("health address and leader-election namespace/name must not be empty")
	}
	if o.Workers < 1 || o.StatusDebounce < 0 {
		return fmt.Errorf("workers must be positive and status debounce non-negative")
	}
	if o.LeaseDuration <= o.RenewDeadline || o.RenewDeadline <= time.Duration(leaderelection.JitterFactor*float64(o.RetryPeriod)) {
		return fmt.Errorf("leader-election durations must satisfy lease > renew > retry*jitter")
	}
	if o.KubeAPIQPS <= 0 || o.KubeAPIBurst < 1 {
		return fmt.Errorf("Kubernetes API QPS and burst must be positive")
	}
	return nil
}

func (o options) run(ctx context.Context) error {
	if err := o.validate(); err != nil {
		return err
	}
	config, err := o.restConfig()
	if err != nil {
		return err
	}
	kubeClient, err := kubernetes.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("create Kubernetes client: %w", err)
	}
	mokkaClient, err := versioned.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("create Mokka client: %w", err)
	}
	controller, err := mokkacontroller.New(kubeClient, mokkaClient, mokkacontroller.Options{
		Workers: o.Workers, StatusDebounce: o.StatusDebounce,
	})
	if err != nil {
		return fmt.Errorf("create controller: %w", err)
	}

	server := &http.Server{Addr: o.HealthBindAddress, Handler: newProbeHandler(controller.Ready)}
	serverErrors := make(chan error, 1)
	var (
		serverErr error
		serverMu  sync.Mutex
	)
	go func() {
		err := server.ListenAndServe()
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErrors <- err
		}
		close(serverErrors)
	}()

	runCtx, cancel := context.WithCancel(ctx)
	go func() {
		if err := <-serverErrors; err != nil {
			serverMu.Lock()
			serverErr = err
			serverMu.Unlock()
			cancel()
		}
	}()
	identity, err := leaderIdentity()
	if err != nil {
		cancel()
		return err
	}
	lock := &rl.LeaseLock{
		LeaseMeta:  metav1.ObjectMeta{Name: o.LeaseName, Namespace: o.LeaseNamespace},
		Client:     kubeClient.CoordinationV1(),
		LockConfig: rl.ResourceLockConfig{Identity: identity},
	}
	electionErr := runLeaderElection(runCtx, o, lock, controller.Run)
	cancel()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer shutdownCancel()
	shutdownErr := server.Shutdown(shutdownCtx)
	serverMu.Lock()
	defer serverMu.Unlock()
	return errors.Join(electionErr, serverErr, shutdownErr)
}

func (o options) restConfig() (*rest.Config, error) {
	var (
		config *rest.Config
		err    error
	)
	if o.Kubeconfig != "" {
		config, err = clientcmd.BuildConfigFromFlags("", o.Kubeconfig)
	} else {
		config, err = rest.InClusterConfig()
	}
	if err != nil {
		return nil, fmt.Errorf("load Kubernetes configuration: %w", err)
	}
	config = rest.CopyConfig(config)
	config.QPS = float32(o.KubeAPIQPS)
	config.Burst = o.KubeAPIBurst
	config.UserAgent = "mokka-controller"
	return config, nil
}

func newProbeHandler(ready func() bool) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/readyz", func(writer http.ResponseWriter, _ *http.Request) {
		if !ready() {
			http.Error(writer, "not ready", http.StatusServiceUnavailable)
			return
		}
		writer.WriteHeader(http.StatusOK)
	})
	return mux
}

func leaderIdentity() (string, error) {
	hostname, err := os.Hostname()
	if err != nil {
		return "", fmt.Errorf("get hostname for leader identity: %w", err)
	}
	return hostname + "_" + uuid.NewString(), nil
}

func newLeaderElectionConfig(o options, lock rl.Interface, run func(context.Context)) leaderelection.LeaderElectionConfig {
	return leaderelection.LeaderElectionConfig{
		Lock:            lock,
		LeaseDuration:   o.LeaseDuration,
		RenewDeadline:   o.RenewDeadline,
		RetryPeriod:     o.RetryPeriod,
		ReleaseOnCancel: true,
		Name:            o.LeaseName,
		Callbacks: leaderelection.LeaderCallbacks{
			OnStartedLeading: run,
			OnStoppedLeading: func() {},
		},
	}
}

func runLeaderElection(
	ctx context.Context,
	o options,
	lock rl.Interface,
	run func(context.Context) error,
) error {
	electionCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	workCtx, stopWork := context.WithCancel(electionCtx)
	defer stopWork()
	workDone := make(chan struct{})
	draining := &drainingLock{Interface: lock, workDone: workDone, stopWork: stopWork}
	var (
		workErr error
		workMu  sync.Mutex
	)
	config := newLeaderElectionConfig(o, draining, func(context.Context) {
		err := run(workCtx)
		workMu.Lock()
		workErr = err
		workMu.Unlock()
		close(workDone)
		cancel()
	})
	elector, err := leaderelection.NewLeaderElector(config)
	if err != nil {
		return fmt.Errorf("configure leader election: %w", err)
	}
	elector.Run(electionCtx)
	workMu.Lock()
	defer workMu.Unlock()
	return workErr
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
