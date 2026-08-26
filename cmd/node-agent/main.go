// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package main is the entry point for the node-agent binary.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/urfave/cli/v3"
	"golang.org/x/sync/errgroup"

	"github.com/NVIDIA/k8s-test-infra/internal/agent"
	"github.com/NVIDIA/k8s-test-infra/internal/agent/cdi"
	"github.com/NVIDIA/k8s-test-infra/internal/agent/gpudriver"
	"github.com/NVIDIA/k8s-test-infra/internal/agent/health"
	"github.com/NVIDIA/k8s-test-infra/internal/agent/host"
	"github.com/NVIDIA/k8s-test-infra/internal/agent/pcibus"
	"github.com/NVIDIA/k8s-test-infra/internal/agent/source"
	"github.com/NVIDIA/k8s-test-infra/internal/logging"
)

func main() {
	if err := newCLI().Run(context.Background(), os.Args); err != nil {
		fmt.Fprintf(os.Stderr, "node-agent: %v\n", err)
		os.Exit(1)
	}
}

func newCLI() *cli.Command {
	return &cli.Command{
		Name:  "node-agent",
		Usage: "Mokka Node Agent — simulates GPU infrastructure on a single node",
		Commands: []*cli.Command{
			startCommand(),
		},
	}
}

func startCommand() *cli.Command {
	return &cli.Command{
		Name:  "start",
		Usage: "start the node agent",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "log-level",
				Value:   "info",
				Sources: cli.EnvVars("MOKKA_LOG_LEVEL"),
			},
			&cli.StringFlag{
				Name:    "log-format",
				Value:   "json",
				Sources: cli.EnvVars("MOKKA_LOG_FORMAT"),
			},
			&cli.StringFlag{
				Name:    "config",
				Usage:   "path to mock-nvml YAML profile",
				Sources: cli.EnvVars("MOKKA_AGENT_CONFIG"),
			},
			&cli.StringFlag{
				Name:    "host-root",
				Value:   "/host",
				Sources: cli.EnvVars("MOKKA_AGENT_HOST_ROOT"),
			},
			&cli.StringFlag{
				Name:    "health-addr",
				Value:   ":9090",
				Sources: cli.EnvVars("MOKKA_AGENT_HEALTH_ADDR"),
			},
			&cli.DurationFlag{
				Name:    "shutdown-timeout",
				Value:   30 * time.Second,
				Sources: cli.EnvVars("MOKKA_AGENT_SHUTDOWN_TIMEOUT"),
				Usage:   "maximum time to wait for simulators to revoke and discard on SIGINT/SIGTERM",
			},
		},
		Action: runStart,
	}
}

func runStart(ctx context.Context, cmd *cli.Command) error {
	level, err := logging.ParseLevel(cmd.String("log-level"))
	if err != nil {
		return err
	}
	format, err := logging.ParseFormat(cmd.String("log-format"))
	if err != nil {
		return err
	}

	log := logging.NewLogger(logging.Config{Level: level, Format: format})

	configPath := cmd.String("config")
	if configPath == "" {
		return errors.New("--config is required")
	}

	signalCtx, cancel := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	shutdownTimeout := cmd.Duration("shutdown-timeout")

	healthSrv := health.NewServer(cmd.String("health-addr"), log, shutdownTimeout)

	a := agent.New(agent.Config{
		Simulators:      []agent.Simulator{gpudriver.New(), pcibus.New(), cdi.New()},
		Source:          source.NewFileSource(configPath, log),
		Host:            host.New(cmd.String("host-root")),
		Log:             log,
		ShutdownTimeout: shutdownTimeout,
	})

	healthSrv.SetLiveness(a.Live)
	healthSrv.SetReadiness(a.Readyz)

	g, gctx := errgroup.WithContext(signalCtx)
	g.Go(func() error { return healthSrv.Run(gctx) })
	g.Go(func() error { return a.Run(gctx) })
	return g.Wait()
}
