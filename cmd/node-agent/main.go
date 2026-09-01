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
	"github.com/NVIDIA/k8s-test-infra/internal/agent/fabricmanager"
	"github.com/NVIDIA/k8s-test-infra/internal/agent/gpudriver"
	"github.com/NVIDIA/k8s-test-infra/internal/agent/health"
	"github.com/NVIDIA/k8s-test-infra/internal/agent/host"
	"github.com/NVIDIA/k8s-test-infra/internal/agent/ib"
	"github.com/NVIDIA/k8s-test-infra/internal/agent/imex"
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
			&cli.StringFlag{
				Name:    "ib-mode",
				Value:   "off",
				Usage:   "InfiniBand simulation tier: off, sysfs (render only) or full (adds the mock-ib daemon)",
				Sources: cli.EnvVars("MOCK_IB"),
			},
			&cli.IntFlag{
				Name:    "ib-fabric-port",
				Value:   18515,
				Usage:   "TCP port for the cross-pod mock-ib fabric relay",
				Sources: cli.EnvVars("MOCK_IB_PING_PORT"),
			},
			&cli.BoolFlag{
				Name:    "ib-fabric",
				Usage:   "enable the cross-pod fabric relay; required for multi-node ibping and iblinkinfo",
				Sources: cli.EnvVars("MOCK_IB_PING_FABRIC"),
			},
			&cli.DurationFlag{
				Name:    "fabricmanager-init-delay",
				Usage:   "withhold fabric readiness for this long, simulating NVSwitch registration latency",
				Sources: cli.EnvVars("MOCK_FABRICMANAGER_INIT_DELAY"),
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

	ibMode, err := ib.ParseMode(cmd.String("ib-mode"))
	if err != nil {
		return err
	}

	signalCtx, cancel := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	shutdownTimeout := cmd.Duration("shutdown-timeout")

	healthSrv := health.NewServer(cmd.String("health-addr"), log, shutdownTimeout)

	a := agent.New(agent.Config{
		Simulators: []agent.Simulator{
			gpudriver.New(),
			fabricmanager.New(fabricmanager.Options{
				InitDelay: cmd.Duration("fabricmanager-init-delay"),
			}),
			pcibus.New(),
			imex.New(),
			ib.New(ib.Options{
				Mode:    ibMode,
				TCPPort: cmd.Int("ib-fabric-port"),
				Fabric:  cmd.Bool("ib-fabric"),
			}),
			cdi.New(),
		},
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
