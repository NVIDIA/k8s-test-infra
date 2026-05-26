// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// mock-ib renders fake InfiniBand sysfs from the nvml-mock profile (when
// -config is set) and serves libibmockumad over a Unix socket for ibping.
package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/daemon"
)

func main() {
	socket := flag.String("socket", daemon.EnvOr(daemon.EnvMockIBPingSocket, "/run/mock-ib.sock"), "Unix socket path")
	ibRoot := flag.String("ib-root", daemon.EnvOr(daemon.EnvMockIBRoot, "/var/lib/nvml-mock/ib"), "MOCK_IB_ROOT sysfs tree")
	tcpPort := flag.Int("port", daemon.EnvIntOr(daemon.EnvMockIBPingPort, 18515), "TCP fabric port (phase 2)")
	fabric := flag.Bool("fabric", daemon.EnvBoolOr(daemon.EnvMockIBPingFabric, false), "enable TCP fabric relay (phase 2)")
	registerPeers := flag.Bool("register-peers", false, "register local ports with MOCK_IB_PEERS and exit (daemon must already be listening on TCP)")

	configPath := flag.String("config", daemon.EnvOr(daemon.EnvMockIBConfig, ""), "mock-nvml profile YAML (renders infiniband sysfs under -ib-root)")
	gpuCount := flag.Int("gpu-count", daemon.EnvIntOr(daemon.EnvGPUCount, 0), "GPU count for HCA layout when hca_count is unset")
	nodeName := flag.String("node-name", daemon.EnvOr(daemon.EnvNodeName, ""), "node name for per-node GUID/LID")
	renderOnly := flag.Bool("render-only", false, "render sysfs from -config and exit (no UMAD server)")
	dryRun := flag.Bool("dry-run", false, "validate -config and exit without writing files")
	flag.Parse()

	if *configPath != "" {
		if err := daemon.RenderSysfsFromConfig(daemon.RenderSysfsOptions{
			ConfigPath: *configPath,
			GPUCount:   *gpuCount,
			NodeName:   *nodeName,
			OutputDir:  *ibRoot,
			DryRun:     *dryRun,
		}); err != nil {
			log.Fatalf("mock-ib: render: %v", err)
		}
		if *dryRun {
			os.Exit(0)
		}
	}

	if *renderOnly {
		return
	}

	cfg := daemon.Config{
		SocketPath: *socket,
		IBRoot:     *ibRoot,
		TCPPort:    *tcpPort,
		Fabric:     *fabric,
	}
	srv, err := daemon.NewServer(cfg, log.Default())
	if err != nil {
		log.Fatalf("mock-ib: %v", err)
	}

	if *registerPeers {
		srv.RegisterWithPeers()
		return
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if err := srv.ListenAndServe(ctx); err != nil && ctx.Err() == nil {
		log.Fatalf("mock-ib: %v", err)
	}
}
