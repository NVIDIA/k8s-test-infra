// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// nvml-mock-guest runs inside a KubeVirt guest and mirrors the node's
// nvml-mock runtime overrides (injected Xids, health and clock changes) into
// the guest. The NRI plugin rewrites the live area at the end of the guest's
// bundle disk whenever the host's overrides change; this program polls that
// area and writes it to the override file the guest's mock library reads.
//
// It ships as a static binary in every bundle, so it must not link the mock
// library or anything else that needs cgo.
package main

import (
	"context"
	"encoding/hex"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/urfave/cli/v3"
	"go.uber.org/zap"

	"github.com/NVIDIA/k8s-test-infra/internal/logging"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := newCLI().Run(ctx, os.Args); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "nvml-mock-guest: %v\n", err)
		stop()
		os.Exit(1)
	}
}

func newCLI() *cli.Command {
	return &cli.Command{
		Name:  "nvml-mock-guest",
		Usage: "mirror the node's nvml-mock runtime overrides into this KubeVirt guest",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "disk",
				Value: "/dev/disk/by-id/virtio-nvml-mock-guest",
				Usage: "bundle disk whose trailing live area carries the host's overrides",
			},
			&cli.StringFlag{
				Name:  "overrides",
				Value: "/etc/nvml-mock/overrides.yaml",
				Usage: "override file the guest's mock library reads",
			},
			&cli.DurationFlag{
				Name:  "interval",
				Value: time.Second,
				Usage: "how often to poll the live area",
			},
			&cli.BoolFlag{
				Name:  "once",
				Usage: "apply the current overrides and exit",
			},
			&cli.StringFlag{
				Name:  "log-level",
				Value: string(logging.LevelInfo),
				Usage: "log level: debug | info | warn | error",
			},
		},
		Action: run,
	}
}

func run(ctx context.Context, cmd *cli.Command) error {
	level, err := logging.ParseLevel(cmd.String("log-level"))
	if err != nil {
		return err
	}
	// Plain lines: the unit sends them to the serial console for a person to read.
	logger := logging.NewLogger(logging.Config{Level: level, Format: logging.FormatPlain})
	defer func() { _ = logger.Sync() }()

	a := &applier{disk: cmd.String("disk"), overrides: cmd.String("overrides")}
	if cmd.Bool("once") {
		return applyAndLog(a)
	}
	every := cmd.Duration("interval")
	if every <= 0 {
		return fmt.Errorf("--interval must be positive, got %s", every)
	}
	logger.Info("mirroring host overrides",
		zap.String("disk", a.disk),
		zap.String("overrides", a.overrides),
		zap.Duration("interval", every),
	)
	poll(ctx, a, every)

	return nil
}

// poll applies on every tick until ctx ends. A failure repeats every tick
// until its cause goes away, so only a new failure is logged.
func poll(ctx context.Context, a *applier, every time.Duration) {
	ticker := time.NewTicker(every)
	defer ticker.Stop()

	var lastErr string
	for {
		err := applyAndLog(a)
		switch {
		case err == nil:
			lastErr = ""
		case err.Error() != lastErr:
			lastErr = err.Error()
			zap.L().Warn("cannot apply host overrides; keeping the current ones", zap.Error(err))
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func applyAndLog(a *applier) error {
	changed, err := a.apply()
	if err != nil {
		return fmt.Errorf("apply %s to %s: %w", a.disk, a.overrides, err)
	}
	if changed {
		zap.L().Info("applied host overrides",
			zap.String("sha256", hex.EncodeToString(a.last[:])),
			zap.String("overrides", a.overrides),
		)
	}

	return nil
}
