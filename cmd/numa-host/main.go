// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Command numa-host applies NUMA simulation in the node mount namespace.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"

	"github.com/NVIDIA/k8s-test-infra/internal/agent/numa"
	"github.com/NVIDIA/k8s-test-infra/internal/logging"
)

func main() {
	log := logging.NewLogger(logging.Config{Level: logging.LevelInfo, Format: logging.FormatJSON})
	defer func() { _ = log.Sync() }()
	if err := run(context.Background(), os.Args[1:]); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "numa-host: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	if len(args) < 1 || len(args) > 2 {
		return errors.New("usage: numa-host <apply|restore|mount> [configure-kubelet]")
	}
	managed := false
	if len(args) == 2 {
		var err error
		managed, err = strconv.ParseBool(args[1])
		if err != nil {
			return fmt.Errorf("configure-kubelet: %w", err)
		}
	}
	return numa.RunHost(ctx, args[0], managed)
}
