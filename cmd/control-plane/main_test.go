// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"testing"
	"time"

	"github.com/NVIDIA/k8s-test-infra/internal/controlplane"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

// TestFlagsProduceExpectedConfig pins the CLI-to-Config wiring so a rename or
// missing flag can't silently regress. The Action is swapped so nothing binds
// a listener; the test only cares about flag resolution.
func TestFlagsProduceExpectedConfig(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want controlplane.Config
	}{
		{
			name: "defaults when no flags",
			args: []string{"control-plane"},
			want: controlplane.DefaultConfig(),
		},
		{
			name: "listen-addr override",
			args: []string{"control-plane", "--listen-addr", ":9090"},
			want: controlplane.Config{
				ListenAddr:      ":9090",
				LogLevel:        "info",
				LogFormat:       "json",
				ShutdownTimeout: 5 * time.Second,
			},
		},
		{
			name: "log-level and shutdown-timeout override",
			args: []string{"control-plane", "--log-level", "debug", "--shutdown-timeout", "12s"},
			want: controlplane.Config{
				ListenAddr:      ":8080",
				LogLevel:        "debug",
				LogFormat:       "json",
				ShutdownTimeout: 12 * time.Second,
			},
		},
		{
			name: "log-format plain override",
			args: []string{"control-plane", "--log-format", "plain"},
			want: controlplane.Config{
				ListenAddr:      ":8080",
				LogLevel:        "info",
				LogFormat:       "plain",
				ShutdownTimeout: 5 * time.Second,
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got controlplane.Config
			cmd := newCLI()
			cmd.Action = func(_ context.Context, c *cli.Command) error {
				got = controlplane.Config{
					ListenAddr:      c.String("listen-addr"),
					LogLevel:        c.String("log-level"),
					LogFormat:       c.String("log-format"),
					ShutdownTimeout: c.Duration("shutdown-timeout"),
				}
				return nil
			}
			require.NoError(t, cmd.Run(context.Background(), tc.args))
			require.Equal(t, tc.want, got)
		})
	}
}
