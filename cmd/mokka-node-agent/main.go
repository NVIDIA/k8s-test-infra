// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/urfave/cli/v3"

	"github.com/NVIDIA/k8s-test-infra/internal/agent"
	"github.com/NVIDIA/k8s-test-infra/internal/agent/health"
	"github.com/NVIDIA/k8s-test-infra/internal/agent/host"
	"github.com/NVIDIA/k8s-test-infra/internal/agent/source"
	"github.com/NVIDIA/k8s-test-infra/internal/controlplane"
)

func main() {
	if err := newCLI().Run(context.Background(), os.Args); err != nil {
		fmt.Fprintf(os.Stderr, "mokka-node-agent: %v\n", err)
		os.Exit(1)
	}
}

func newCLI() *cli.Command {
	return &cli.Command{
		Name:  "mokka-node-agent",
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
				Sources: cli.EnvVars("MOKKA_AGENT_LOG_LEVEL"),
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
		},
		Action: runStart,
	}
}

func runStart(ctx context.Context, cmd *cli.Command) error {
	log, err := controlplane.NewLogger(controlplane.Config{LogLevel: cmd.String("log-level")})
	if err != nil {
		return err
	}

	configPath := cmd.String("config")
	if configPath == "" {
		return fmt.Errorf("--config is required")
	}

	signalCtx, cancel := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	h := host.New(cmd.String("host-root"))
	src := source.NewFileSource(configPath, log)
	healthSrv := health.NewServer(cmd.String("health-addr"), log)

	a := agent.New(agent.Config{
		Source: src,
		Host:   h,
		Health: healthSrv,
		Log:    log,
	})

	return a.Run(signalCtx)
}
