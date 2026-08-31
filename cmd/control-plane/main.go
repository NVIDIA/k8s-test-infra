// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// control-plane serves probes and reconciles declarative sGPU inventory.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/NVIDIA/k8s-test-infra/internal/controlplane"
	"github.com/NVIDIA/k8s-test-infra/internal/logging"
	"github.com/NVIDIA/k8s-test-infra/internal/mokkacontroller"
	"github.com/urfave/cli/v3"
	"golang.org/x/sync/errgroup"
	"k8s.io/klog/v2"
)

func main() {
	if err := newCLI().Run(context.Background(), os.Args); err != nil {
		fmt.Fprintf(os.Stderr, "control-plane: %v\n", err)
		os.Exit(1)
	}
}

func newCLI() *cli.Command {
	defaults := controlplane.DefaultConfig()
	return &cli.Command{
		Name:  "control-plane",
		Usage: "Mokka Control Plane",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "listen-addr",
				Value:   defaults.Server.ListenAddr,
				Sources: cli.EnvVars("MOKKA_CP_LISTEN_ADDR"),
				Usage:   "address for the HTTP server, e.g. :8080",
			},
			&cli.StringFlag{
				Name:    "log-level",
				Value:   string(logging.LevelInfo),
				Sources: cli.EnvVars("MOKKA_LOG_LEVEL"),
				Usage:   "log level: debug | info | warn | error",
			},
			&cli.StringFlag{
				Name:    "log-format",
				Value:   string(logging.FormatJSON),
				Sources: cli.EnvVars("MOKKA_LOG_FORMAT"),
				Usage:   "log format: json | plain",
			},
			&cli.DurationFlag{
				Name:    "shutdown-timeout",
				Value:   defaults.Server.ShutdownTimeout,
				Sources: cli.EnvVars("MOKKA_CP_SHUTDOWN_TIMEOUT"),
				Usage:   "maximum time to wait for in-flight requests to drain on SIGINT/SIGTERM",
			},
			&cli.StringFlag{
				Name: "kubeconfig", Value: defaults.Kubernetes.Kubeconfig,
				Sources: cli.EnvVars("MOKKA_CP_KUBECONFIG"),
				Usage:   "path to a kubeconfig; empty uses in-cluster configuration",
			},
			&cli.StringFlag{
				Name: "leader-election-namespace", Value: defaults.LeaderElection.Namespace,
				Sources: cli.EnvVars("MOKKA_CP_LEADER_ELECTION_NAMESPACE"),
				Usage:   "namespace containing the leader-election Lease",
			},
			&cli.StringFlag{
				Name: "leader-election-name", Value: defaults.LeaderElection.Name,
				Sources: cli.EnvVars("MOKKA_CP_LEADER_ELECTION_NAME"),
				Usage:   "name of the leader-election Lease",
			},
			&cli.DurationFlag{
				Name: "leader-election-lease-duration", Value: defaults.LeaderElection.LeaseDuration,
			},
			&cli.DurationFlag{
				Name: "leader-election-renew-deadline", Value: defaults.LeaderElection.RenewDeadline,
			},
			&cli.DurationFlag{
				Name: "leader-election-retry-period", Value: defaults.LeaderElection.RetryPeriod,
			},
			&cli.IntFlag{
				Name: "workers", Value: defaults.Controller.Workers, Usage: "workers per controller queue",
			},
			&cli.DurationFlag{
				Name: "status-debounce", Value: defaults.Controller.StatusDebounce,
				Usage: "quiet period before an aggregate status update",
			},
			&cli.DurationFlag{
				Name: "status-progress-interval", Value: defaults.Controller.StatusProgressInterval,
				Usage: "maximum aggregate status staleness during continuous changes",
			},
			&cli.DurationFlag{
				Name: "live-node-get-timeout", Value: defaults.Controller.LiveNodeGetTimeout,
				Sources: cli.EnvVars("MOKKA_CP_LIVE_NODE_GET_TIMEOUT"),
				Usage:   "timeout for an exact Node GET after it leaves the filtered cache",
			},
			&cli.FloatFlag{Name: "kube-api-qps", Value: defaults.Kubernetes.QPS},
			&cli.IntFlag{Name: "kube-api-burst", Value: defaults.Kubernetes.Burst},
		},
		Action: run,
	}
}

func run(ctx context.Context, cmd *cli.Command) error {
	level, err := logging.ParseLevel(cmd.String("log-level"))
	if err != nil {
		return err
	}
	format, err := logging.ParseFormat(cmd.String("log-format"))
	if err != nil {
		return err
	}
	logger := logging.NewLogger(logging.Config{Level: level, Format: format})
	klog.SetSlogLogger(logger)

	cfg := controlplane.Config{
		Server: controlplane.ServerConfig{
			ListenAddr:      cmd.String("listen-addr"),
			ShutdownTimeout: cmd.Duration("shutdown-timeout"),
		},
		Kubernetes: controlplane.KubernetesConfig{
			Kubeconfig: cmd.String("kubeconfig"),
			QPS:        cmd.Float("kube-api-qps"),
			Burst:      cmd.Int("kube-api-burst"),
		},
		LeaderElection: controlplane.LeaderElectionConfig{
			Namespace:     cmd.String("leader-election-namespace"),
			Name:          cmd.String("leader-election-name"),
			LeaseDuration: cmd.Duration("leader-election-lease-duration"),
			RenewDeadline: cmd.Duration("leader-election-renew-deadline"),
			RetryPeriod:   cmd.Duration("leader-election-retry-period"),
		},
		Controller: mokkacontroller.Options{
			Workers:                cmd.Int("workers"),
			StatusDebounce:         cmd.Duration("status-debounce"),
			StatusProgressInterval: cmd.Duration("status-progress-interval"),
			LiveNodeGetTimeout:     cmd.Duration("live-node-get-timeout"),
		},
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
