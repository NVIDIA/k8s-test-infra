// Copyright (c) 2026, NVIDIA CORPORATION.  All rights reserved.
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"context"
	"fmt"
	"os/signal"
	"syscall"

	"github.com/urfave/cli/v3"

	"github.com/NVIDIA/k8s-test-infra/pkg/gpu/allocwatch"
)

// watchAllocationsCommand runs the allocation watcher until the process is
// signalled.
//
// This is #506 item 1: it is the producer that makes memory.used_bytes respond
// to a pod holding an nvidia.com/gpu claim. The consumer side already existed —
// the engine re-reads the override file this writes within one TTL — so the
// watcher only has to keep that file honest.
//
// It runs as a sidecar in the nvml-mock DaemonSet rather than as its own
// workload so it shares the driver-root mount the override file lives in.
func watchAllocationsCommand() *cli.Command {
	return &cli.Command{
		Name:  "watch-allocations",
		Usage: "mirror each pod's nvidia.com/gpu claim into memory.used_bytes/free_bytes",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "socket",
				Value:   allocwatch.DefaultSocketPath,
				Usage:   "kubelet pod-resources socket",
				Sources: cli.EnvVars("MOCK_NVML_PODRESOURCES_SOCKET"),
			},
			&cli.DurationFlag{
				Name:  "interval",
				Value: allocwatch.DefaultInterval,
				Usage: "allocation poll interval",
			},
			&cli.FloatFlag{
				Name:  "used-fraction",
				Value: allocwatch.DefaultPolicy().UsedFractionPerClaim,
				Usage: "share of the usable aperture attributed per GPU claim",
			},
		},
		Action: runWatchAllocations,
	}
}

func runWatchAllocations(ctx context.Context, cmd *cli.Command) error {
	cfg := loadConfig(cmd)
	if cfg == nil || cfg.NumDevices == 0 {
		return failf("watch-allocations: no GPU config resolved; nothing to reconcile")
	}

	usedFraction := cmd.Float("used-fraction")
	if usedFraction <= 0 || usedFraction > 1 {
		return fmt.Errorf("watch-allocations: --used-fraction must be in (0, 1], got %v", usedFraction)
	}

	signalCtx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	socket := cmd.String("socket")
	lister, err := allocwatch.NewPodResourcesLister(signalCtx, socket)
	if err != nil {
		return failf("watch-allocations: %v", err)
	}
	defer func() { _ = lister.Close() }()

	overridePath := configOverridePath(cmd)
	interval := cmd.Duration("interval")
	devices := allocwatch.DevicesFromConfig(cfg)
	fprintf(cmd.Root().Writer, "watch-allocations: %d GPUs, polling %s every %s -> %s\n",
		len(devices), socket, interval, overridePath)

	w := &allocwatch.Watcher{
		Lister:       lister,
		Devices:      devices,
		Policy:       allocwatch.Policy{UsedFractionPerClaim: usedFraction},
		OverridePath: overridePath,
		Interval:     interval,
	}
	if err := w.Run(signalCtx); err != nil {
		return failf("watch-allocations: %v", err)
	}
	return nil
}
