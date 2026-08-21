// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"testing"
	"time"

	"github.com/NVIDIA/k8s-test-infra/internal/controlplane"
	"github.com/NVIDIA/k8s-test-infra/internal/logging"
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
				cfg:    controlplane.Config{ListenAddr: ":9090", ShutdownTimeout: 5 * time.Second},
				level:  logging.LevelInfo,
				format: logging.FormatJSON,
			},
		},
		{
			name: "log-level and shutdown-timeout override",
			args: []string{"control-plane", "--log-level", "debug", "--shutdown-timeout", "12s"},
			want: capturedArgs{
				cfg:    controlplane.Config{ListenAddr: ":8080", ShutdownTimeout: 12 * time.Second},
				level:  logging.LevelDebug,
				format: logging.FormatJSON,
			},
		},
		{
			name: "log-format plain override",
			args: []string{"control-plane", "--log-format", "plain"},
			want: capturedArgs{
				cfg:    controlplane.Config{ListenAddr: ":8080", ShutdownTimeout: 5 * time.Second},
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
						ListenAddr:      c.String("listen-addr"),
						ShutdownTimeout: c.Duration("shutdown-timeout"),
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
