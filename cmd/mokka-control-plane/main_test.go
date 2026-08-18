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
			args: []string{"mokka-control-plane"},
			want: controlplane.DefaultConfig(),
		},
		{
			name: "listen-addr override",
			args: []string{"mokka-control-plane", "--listen-addr", ":9090"},
			want: configWith(func(config *controlplane.Config) { config.ListenAddr = ":9090" }),
		},
		{
			name: "log-level and shutdown-timeout override",
			args: []string{"mokka-control-plane", "--log-level", "debug", "--shutdown-timeout", "12s"},
			want: configWith(func(config *controlplane.Config) {
				config.LogLevel = "debug"
				config.ShutdownTimeout = 12 * time.Second
			}),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got controlplane.Config
			cmd := newCLI()
			cmd.Action = func(_ context.Context, c *cli.Command) error {
				got = controlplane.Config{
					ListenAddr: c.String("listen-addr"), LogLevel: c.String("log-level"),
					ShutdownTimeout: c.Duration("shutdown-timeout"), Kubeconfig: c.String("kubeconfig"),
					LeaderElectionNamespace: c.String("leader-election-namespace"),
					LeaderElectionName:      c.String("leader-election-name"),
					LeaseDuration:           c.Duration("leader-election-lease-duration"),
					RenewDeadline:           c.Duration("leader-election-renew-deadline"),
					RetryPeriod:             c.Duration("leader-election-retry-period"), Workers: c.Int("workers"),
					StatusDebounce:         c.Duration("status-debounce"),
					StatusProgressInterval: c.Duration("status-progress-interval"),
					KubeAPIQPS:             c.Float("kube-api-qps"), KubeAPIBurst: c.Int("kube-api-burst"),
				}
				return nil
			}
			require.NoError(t, cmd.Run(context.Background(), tc.args))
			require.Equal(t, tc.want, got)
		})
	}
}

func configWith(mutate func(*controlplane.Config)) controlplane.Config {
	config := controlplane.DefaultConfig()
	mutate(&config)
	return config
}
