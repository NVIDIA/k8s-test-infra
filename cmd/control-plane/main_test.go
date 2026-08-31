// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"testing"
	"time"

	"github.com/NVIDIA/k8s-test-infra/internal/controlplane"
	"github.com/NVIDIA/k8s-test-infra/internal/logging"
	"github.com/NVIDIA/k8s-test-infra/internal/mokkacontroller"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

type capturedArgs struct {
	cfg    controlplane.Config
	level  logging.Level
	format logging.Format
}

// TestFlagsProduceExpectedConfig pins the CLI-to-Config wiring so a rename or
// missing flag can't silently regress. The Action is swapped so nothing binds
// a listener; the test only cares about flag resolution.
func TestFlagsProduceExpectedConfig(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want capturedArgs
	}{
		{
			name: "defaults when no flags",
			args: []string{"control-plane"},
			want: capturedArgs{
				cfg:    controlplane.DefaultConfig(),
				level:  logging.LevelInfo,
				format: logging.FormatJSON,
			},
		},
		{
			name: "listen-addr override",
			args: []string{"control-plane", "--listen-addr", ":9090"},
			want: capturedArgs{
				cfg: configWith(func(config *controlplane.Config) {
					config.Server.ListenAddr = ":9090"
				}),
				level:  logging.LevelInfo,
				format: logging.FormatJSON,
			},
		},
		{
			name: "log-level and shutdown-timeout override",
			args: []string{"control-plane", "--log-level", "debug", "--shutdown-timeout", "12s"},
			want: capturedArgs{
				cfg: configWith(func(config *controlplane.Config) {
					config.Server.ShutdownTimeout = 12 * time.Second
				}),
				level:  logging.LevelDebug,
				format: logging.FormatJSON,
			},
		},
		{
			name: "log-format plain override",
			args: []string{"control-plane", "--log-format", "plain"},
			want: capturedArgs{
				cfg:    controlplane.DefaultConfig(),
				level:  logging.LevelInfo,
				format: logging.FormatPlain,
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got capturedArgs
			cmd := newCLI()
			cmd.Action = func(_ context.Context, c *cli.Command) error {
				level, err := logging.ParseLevel(c.String("log-level"))
				require.NoError(t, err)
				format, err := logging.ParseFormat(c.String("log-format"))
				require.NoError(t, err)
				got = capturedArgs{
					cfg: controlplane.Config{
						Server: controlplane.ServerConfig{
							ListenAddr:      c.String("listen-addr"),
							ShutdownTimeout: c.Duration("shutdown-timeout"),
						},
						Kubernetes: controlplane.KubernetesConfig{
							Kubeconfig: c.String("kubeconfig"),
							QPS:        c.Float("kube-api-qps"),
							Burst:      c.Int("kube-api-burst"),
						},
						LeaderElection: controlplane.LeaderElectionConfig{
							Namespace:     c.String("leader-election-namespace"),
							Name:          c.String("leader-election-name"),
							LeaseDuration: c.Duration("leader-election-lease-duration"),
							RenewDeadline: c.Duration("leader-election-renew-deadline"),
							RetryPeriod:   c.Duration("leader-election-retry-period"),
						},
						Controller: mokkacontroller.Options{
							Workers:                c.Int("workers"),
							StatusDebounce:         c.Duration("status-debounce"),
							StatusProgressInterval: c.Duration("status-progress-interval"),
							LiveNodeGetTimeout:     c.Duration("live-node-get-timeout"),
						},
					},
					level:  level,
					format: format,
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
