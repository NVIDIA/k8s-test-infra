// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package controlplane wires the Mokka HTTP and controller processes.
package controlplane

import (
	"os"
	"time"

	"github.com/NVIDIA/k8s-test-infra/internal/mokkacontroller"
)

// Config groups the CLI-driven knobs that shape the server. Use DefaultConfig
// as a base and override the fields you need.
type Config struct {
	ListenAddr              string
	LogLevel                string
	ShutdownTimeout         time.Duration
	Kubeconfig              string
	LeaderElectionNamespace string
	LeaderElectionName      string
	LeaseDuration           time.Duration
	RenewDeadline           time.Duration
	RetryPeriod             time.Duration
	Workers                 int
	StatusDebounce          time.Duration
	StatusProgressInterval  time.Duration
	LiveNodeGetTimeout      time.Duration
	KubeAPIQPS              float64
	KubeAPIBurst            int
}

// DefaultConfig returns the values used when no CLI flag overrides them.
func DefaultConfig() Config {
	namespace := os.Getenv("POD_NAMESPACE")
	if namespace == "" {
		namespace = "default"
	}
	controller := mokkacontroller.DefaultOptions()
	return Config{
		ListenAddr:              ":8080",
		LogLevel:                "info",
		ShutdownTimeout:         5 * time.Second,
		LeaderElectionNamespace: namespace,
		LeaderElectionName:      "mokka-control-plane.mokka.nvidia.com",
		LeaseDuration:           15 * time.Second,
		RenewDeadline:           10 * time.Second,
		RetryPeriod:             2 * time.Second,
		Workers:                 controller.Workers,
		StatusDebounce:          controller.StatusDebounce,
		StatusProgressInterval:  controller.StatusProgressInterval,
		LiveNodeGetTimeout:      controller.LiveNodeGetTimeout,
		KubeAPIQPS:              50,
		KubeAPIBurst:            100,
	}
}
