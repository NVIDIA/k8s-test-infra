// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package controlplane wires the Mokka HTTP and controller processes.
package controlplane

import (
	"os"
	"time"

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
			Name:          "mokka-control-plane.mokka.nvidia.com",
			LeaseDuration: 15 * time.Second,
			RenewDeadline: 10 * time.Second,
			RetryPeriod:   2 * time.Second,
		},
		Controller: controller,
	}
}
