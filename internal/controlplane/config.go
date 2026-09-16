// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package controlplane wires the Mokka HTTP and controller processes.
package controlplane

import (
	"errors"
	"fmt"
	"os"
	"time"

	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/client-go/tools/leaderelection"

	"github.com/NVIDIA/k8s-test-infra/internal/mokkacontroller"
)

// Config groups the CLI-driven knobs that shape the control plane. Use
// DefaultConfig as a base and override the fields you need.
type Config struct {
	Server         ServerConfig
	Kubernetes     KubernetesConfig
	LeaderElection LeaderElectionConfig
	Controller     mokkacontroller.Options
}

// validate rejects settings that cannot make progress safely.
//
//nolint:cyclop // Each branch reports a distinct unsafe controller setting.
func (config Config) validate() error {
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

// ServerConfig controls the HTTP server lifecycle.
type ServerConfig struct {
	ListenAddr      string
	ShutdownTimeout time.Duration
}

// KubernetesConfig controls Kubernetes client construction.
type KubernetesConfig struct {
	Kubeconfig string
	QPS        float64
	Burst      int
}

// LeaderElectionConfig controls Lease-based leader election.
type LeaderElectionConfig struct {
	Namespace     string
	Name          string
	LeaseDuration time.Duration
	RenewDeadline time.Duration
	RetryPeriod   time.Duration
}

// DefaultConfig returns the values used when no CLI flag overrides them.
func DefaultConfig() Config {
	namespace := os.Getenv("POD_NAMESPACE")
	if namespace == "" {
		namespace = "default"
	}
	controller := mokkacontroller.DefaultOptions()
	return Config{
		Server: ServerConfig{
			ListenAddr:      ":8080",
			ShutdownTimeout: 5 * time.Second,
		},
		Kubernetes: KubernetesConfig{
			QPS:   50,
			Burst: 100,
		},
		LeaderElection: LeaderElectionConfig{
			Namespace:     namespace,
			Name:          "control-plane.mokka.nvidia.com",
			LeaseDuration: 15 * time.Second,
			RenewDeadline: 10 * time.Second,
			RetryPeriod:   2 * time.Second,
		},
		Controller: controller,
	}
}
