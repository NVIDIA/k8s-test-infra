// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package controlplane hosts the Mokka Control Plane server. This init slice
// exposes only /healthz and /readyz; MEP-0001 lays out the sGPU inventory,
// node-agent heartbeat, and runtime-policy responsibilities that follow.
package controlplane

import "time"

// Config groups the CLI-driven knobs that shape the server. Use DefaultConfig
// as a base and override the fields you need.
type Config struct {
	ListenAddr      string
	LogLevel        string
	LogFormat       string // "json" | "plain"
	ShutdownTimeout time.Duration
}

// DefaultConfig returns the values used when no CLI flag overrides them.
func DefaultConfig() Config {
	return Config{
		ListenAddr:      ":8080",
		LogLevel:        "info",
		LogFormat:       "json",
		ShutdownTimeout: 5 * time.Second,
	}
}
