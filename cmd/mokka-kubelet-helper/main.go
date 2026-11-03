// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Command mokka-kubelet-helper resets Mokka-owned kubelet state on node boot.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/NVIDIA/k8s-test-infra/internal/agent/host"
	"github.com/NVIDIA/k8s-test-infra/internal/kubelet"
	"github.com/NVIDIA/k8s-test-infra/internal/logging"
	"github.com/urfave/cli/v3"
)

func main() {
	log := logging.NewLogger(logging.Config{Level: logging.LevelInfo, Format: logging.FormatJSON})
	defer func() { _ = log.Sync() }()
	if err := newCLI().Run(context.Background(), os.Args); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "mokka-kubelet-helper: %v\n", err)
		os.Exit(1)
	}
}

func newCLI() *cli.Command {
	return &cli.Command{
		Name:  "mokka-kubelet-helper",
		Usage: "prepare Mokka-owned kubelet state for node startup",
		Flags: []cli.Flag{&cli.StringFlag{Name: "host-root", Value: "/", Usage: "node filesystem root"}},
		Action: func(_ context.Context, cmd *cli.Command) error {
			if cmd.Args().Len() != 0 {
				return fmt.Errorf("unknown command %q", cmd.Args().First())
			}
			return cli.ShowAppHelp(cmd)
		},
		Commands: []*cli.Command{{
			Name:  "reset-for-boot",
			Usage: "reset registered kubelet state before kubelet starts",
			Action: func(_ context.Context, cmd *cli.Command) error {
				if cmd.Args().Len() != 0 {
					return errors.New("reset-for-boot takes no arguments")
				}
				return kubelet.ResetForBoot(host.New(cmd.String("host-root")))
			},
		}},
	}
}
