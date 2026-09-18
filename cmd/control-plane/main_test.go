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

// TestConfigFromFlags pins the production CLI-to-Config wiring. The Action is
// swapped so the test only resolves flags and does not contact a cluster.
func TestConfigFromFlags(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want controlplane.Config
	}{
		{
			name: "defaults align with control plane defaults",
			args: []string{"control-plane"},
			want: controlplane.DefaultConfig(),
		},
		{
			name: "all config flags overridden",
			args: []string{
				"control-plane",
				"--listen-addr", ":19090",
				"--shutdown-timeout", "11s",
				"--kubeconfig", "/tmp/control-plane.kubeconfig",
				"--kube-api-qps", "12.5",
				"--kube-api-burst", "13",
				"--leader-election-namespace", "leader-system",
				"--leader-election-name", "control-plane-test",
				"--leader-election-lease-duration", "37s",
				"--leader-election-renew-deadline", "29s",
				"--leader-election-retry-period", "17s",
				"--workers", "19",
				"--status-debounce", "23ms",
				"--status-progress-interval", "31s",
				"--live-node-get-timeout", "41ms",
			},
			want: controlplane.Config{
				Server: controlplane.ServerConfig{
					ListenAddr:      ":19090",
					ShutdownTimeout: 11 * time.Second,
				},
				Kubernetes: controlplane.KubernetesConfig{
					Kubeconfig: "/tmp/control-plane.kubeconfig",
					QPS:        12.5,
					Burst:      13,
				},
				LeaderElection: controlplane.LeaderElectionConfig{
					Namespace:     "leader-system",
					Name:          "control-plane-test",
					LeaseDuration: 37 * time.Second,
					RenewDeadline: 29 * time.Second,
					RetryPeriod:   17 * time.Second,
				},
				Controller: mokkacontroller.Options{
					Workers:                19,
					StatusDebounce:         23 * time.Millisecond,
					StatusProgressInterval: 31 * time.Second,
					LiveNodeGetTimeout:     41 * time.Millisecond,
				},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got controlplane.Config
			cmd := newCLI()
			cmd.Action = func(_ context.Context, cmd *cli.Command) error {
				got = configFrom(cmd)
				return nil
			}

			require.NoError(t, cmd.Run(context.Background(), tc.args))
			require.Equal(t, tc.want, got)
		})
	}
}

func TestLoggingFlags(t *testing.T) {
	for _, tc := range []struct {
		name       string
		args       []string
		wantLevel  logging.Level
		wantFormat logging.Format
	}{
		{
			name:       "defaults",
			args:       []string{"control-plane"},
			wantLevel:  logging.LevelInfo,
			wantFormat: logging.FormatJSON,
		},
		{
			name:       "overrides",
			args:       []string{"control-plane", "--log-level", "debug", "--log-format", "plain"},
			wantLevel:  logging.LevelDebug,
			wantFormat: logging.FormatPlain,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var gotLevel logging.Level
			var gotFormat logging.Format
			cmd := newCLI()
			cmd.Action = func(_ context.Context, cmd *cli.Command) error {
				var err error
				gotLevel, err = logging.ParseLevel(cmd.String("log-level"))
				if err != nil {
					return err
				}
				gotFormat, err = logging.ParseFormat(cmd.String("log-format"))
				return err
			}

			require.NoError(t, cmd.Run(context.Background(), tc.args))
			require.Equal(t, tc.wantLevel, gotLevel)
			require.Equal(t, tc.wantFormat, gotFormat)
		})
	}
}
