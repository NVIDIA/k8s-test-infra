// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// mokka-control-plane serves probes and reconciles declarative sGPU inventory.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/NVIDIA/k8s-test-infra/internal/controlplane"
	"github.com/urfave/cli/v3"
	"golang.org/x/sync/errgroup"
)

func main() {
	if err := newCLI().Run(context.Background(), os.Args); err != nil {
		fmt.Fprintf(os.Stderr, "mokka-control-plane: %v\n", err)
		os.Exit(1)
	}
}

func newCLI() *cli.Command {
	defaults := controlplane.DefaultConfig()
	return &cli.Command{
		Name:  "mokka-control-plane",
		Usage: "Mokka Control Plane",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "listen-addr",
				Value:   defaults.ListenAddr,
				Sources: cli.EnvVars("MOKKA_CP_LISTEN_ADDR"),
				Usage:   "address for the HTTP server, e.g. :8080",
			},
			&cli.StringFlag{
				Name:    "log-level",
				Value:   defaults.LogLevel,
				Sources: cli.EnvVars("MOKKA_CP_LOG_LEVEL"),
				Usage:   "log level: debug | info | warn | error",
			},
			&cli.DurationFlag{
				Name:    "shutdown-timeout",
				Value:   defaults.ShutdownTimeout,
				Sources: cli.EnvVars("MOKKA_CP_SHUTDOWN_TIMEOUT"),
				Usage:   "maximum time to wait for in-flight requests to drain on SIGINT/SIGTERM",
			},
			&cli.StringFlag{
				Name: "kubeconfig", Value: defaults.Kubeconfig,
				Sources: cli.EnvVars("MOKKA_CP_KUBECONFIG"),
				Usage:   "path to a kubeconfig; empty uses in-cluster configuration",
			},
			&cli.StringFlag{
				Name: "leader-election-namespace", Value: defaults.LeaderElectionNamespace,
				Sources: cli.EnvVars("MOKKA_CP_LEADER_ELECTION_NAMESPACE"),
				Usage:   "namespace containing the leader-election Lease",
			},
			&cli.StringFlag{
				Name: "leader-election-name", Value: defaults.LeaderElectionName,
				Sources: cli.EnvVars("MOKKA_CP_LEADER_ELECTION_NAME"),
				Usage:   "name of the leader-election Lease",
			},
			&cli.DurationFlag{Name: "leader-election-lease-duration", Value: defaults.LeaseDuration},
			&cli.DurationFlag{Name: "leader-election-renew-deadline", Value: defaults.RenewDeadline},
			&cli.DurationFlag{Name: "leader-election-retry-period", Value: defaults.RetryPeriod},
			&cli.IntFlag{Name: "workers", Value: defaults.Workers, Usage: "workers per controller queue"},
			&cli.DurationFlag{
				Name: "status-debounce", Value: defaults.StatusDebounce,
				Usage: "quiet period before an aggregate status update",
			},
			&cli.DurationFlag{
				Name: "status-progress-interval", Value: defaults.StatusProgressInterval,
				Usage: "maximum aggregate status staleness during continuous changes",
			},
			&cli.DurationFlag{
				Name: "live-node-get-timeout", Value: defaults.LiveNodeGetTimeout,
				Sources: cli.EnvVars("MOKKA_CP_LIVE_NODE_GET_TIMEOUT"),
				Usage:   "timeout for an exact Node GET after it leaves the filtered cache",
			},
			&cli.FloatFlag{Name: "kube-api-qps", Value: defaults.KubeAPIQPS},
			&cli.IntFlag{Name: "kube-api-burst", Value: defaults.KubeAPIBurst},
		},
		Action: run,
	}
}

func run(ctx context.Context, cmd *cli.Command) error {
	cfg := controlplane.Config{
		ListenAddr:              cmd.String("listen-addr"),
		LogLevel:                cmd.String("log-level"),
		ShutdownTimeout:         cmd.Duration("shutdown-timeout"),
		Kubeconfig:              cmd.String("kubeconfig"),
		LeaderElectionNamespace: cmd.String("leader-election-namespace"),
		LeaderElectionName:      cmd.String("leader-election-name"),
		LeaseDuration:           cmd.Duration("leader-election-lease-duration"),
		RenewDeadline:           cmd.Duration("leader-election-renew-deadline"),
		RetryPeriod:             cmd.Duration("leader-election-retry-period"),
		Workers:                 cmd.Int("workers"),
		StatusDebounce:          cmd.Duration("status-debounce"),
		StatusProgressInterval:  cmd.Duration("status-progress-interval"),
		LiveNodeGetTimeout:      cmd.Duration("live-node-get-timeout"),
		KubeAPIQPS:              cmd.Float("kube-api-qps"),
		KubeAPIBurst:            cmd.Int("kube-api-burst"),
	}

	logger, err := controlplane.NewLogger(cfg)
	if err != nil {
		return err
	}
	controller, err := controlplane.NewController(cfg)
	if err != nil {
		return err
	}

	signalCtx, cancel := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// Group ctx fires on SIGTERM upstream OR when either process returns.
	g, gctx := errgroup.WithContext(signalCtx)
	server := controlplane.NewServerWithReadiness(cfg, logger, controller.Ready)

	g.Go(func() error { return server.Run(gctx) })
	g.Go(func() error { return controller.Run(gctx) })

	return g.Wait()
}
