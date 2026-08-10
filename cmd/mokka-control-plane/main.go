// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// mokka-control-plane is the entry point for the Mokka Control Plane
// microservice described in MEP-0001. The init slice only serves health
// probes; sGPU inventory management, node-agent heartbeats, and runtime
// policy fan-out land in follow-up work on the same binary.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/NVIDIA/k8s-test-infra/internal/controlplane"
	"github.com/urfave/cli/v3"
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
		},
		Action: run,
	}
}

func run(ctx context.Context, cmd *cli.Command) error {
	cfg := controlplane.Config{
		ListenAddr:      cmd.String("listen-addr"),
		LogLevel:        cmd.String("log-level"),
		ShutdownTimeout: cmd.Duration("shutdown-timeout"),
	}

	logger, err := controlplane.NewLogger(cfg)
	if err != nil {
		return err
	}

	signalCtx, cancel := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	return controlplane.NewServer(cfg, logger).Run(signalCtx)
}
