// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// nvml-mock-nri injects the nvml-mock overlay into containers through
// containerd's Node Resource Interface.
package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"golang.org/x/sync/errgroup"

	"github.com/NVIDIA/k8s-test-infra/internal/health"
	"github.com/NVIDIA/k8s-test-infra/internal/nri"
	"github.com/NVIDIA/k8s-test-infra/internal/nri/inject"
)

func main() {
	cfg := nri.DefaultConfig()

	healthAddr := flag.String("health-addr", envOr("NVML_MOCK_HEALTH_ADDR", ":8080"), "address for the /healthz and /readyz probe endpoints; empty disables them")
	flag.StringVar(&cfg.SocketPath, "socket-path", envOr("NRI_SOCKET_PATH", cfg.SocketPath), "NRI socket path")
	flag.StringVar(&cfg.PluginName, "plugin-name", envOr("NRI_PLUGIN_NAME", cfg.PluginName), "NRI plugin name")
	flag.StringVar(&cfg.PluginIndex, "plugin-index", envOr("NRI_PLUGIN_INDEX", cfg.PluginIndex), "NRI plugin index")
	flag.StringVar(&cfg.Inject.HostOverlayPath, "overlay-host-path", envOr("NVML_MOCK_OVERLAY_HOST_PATH", cfg.Inject.HostOverlayPath), "host path for the nvml-mock overlay")
	flag.StringVar(&cfg.Inject.ContainerOverlayPath, "overlay-mount-path", envOr("NVML_MOCK_OVERLAY_MOUNT_PATH", cfg.Inject.ContainerOverlayPath), "container path for the nvml-mock overlay")
	flag.StringVar(&cfg.Inject.NodeName, "node-name", envOr("NODE_NAME", cfg.Inject.NodeName), "Kubernetes node name; enables ComputeDomain topology injection when a topology document is staged in the overlay")
	flag.StringVar(&cfg.Inject.TopologyHostPath, "topology-host-path", envOr("NVML_MOCK_TOPOLOGY_HOST_PATH", cfg.Inject.TopologyHostPath), "host path checked for the staged topology document (defaults to <overlay-host-path>/topology/topology.yaml)")
	flag.StringVar(&cfg.Inject.TopologyContainerPath, "topology-mount-path", envOr("NVML_MOCK_TOPOLOGY_MOUNT_PATH", cfg.Inject.TopologyContainerPath), "container path injected as MOCK_TOPOLOGY_CONFIG (defaults to <overlay-mount-path>/topology/topology.yaml)")
	flag.StringVar(&cfg.Inject.DeviceHostPath, "device-host-path", envOr("NVML_MOCK_DEVICE_HOST_PATH", cfg.Inject.DeviceHostPath), "host path containing mock /dev/nvidia* nodes")
	deviceInjectionMode := flag.String("device-injection-mode", envOr("NVML_MOCK_DEVICE_INJECTION_MODE", string(cfg.Inject.DeviceInjectionMode)), "how the device opt-in delivers GPUs: raw (device nodes) or cdi (CDI device reference)")
	flag.StringVar(&cfg.Inject.CDIDeviceName, "cdi-device-name", envOr("NVML_MOCK_CDI_DEVICE_NAME", cfg.Inject.CDIDeviceName), "fully-qualified CDI device injected in cdi mode")
	flag.StringVar(&cfg.Inject.CDISpecHostPath, "cdi-spec-host-path", envOr("NVML_MOCK_CDI_SPEC_HOST_PATH", cfg.Inject.CDISpecHostPath), "staged CDI spec checked before a CDI reference is emitted; a missing spec falls back to raw injection")
	flag.StringVar(&cfg.Inject.OptOutAnnotation, "opt-out-annotation", envOr("NVML_MOCK_OPT_OUT_ANNOTATION", cfg.Inject.OptOutAnnotation), "pod annotation key; value false disables injection")
	flag.StringVar(&cfg.Inject.DeviceAnnotation, "device-annotation", envOr("NVML_MOCK_DEVICE_ANNOTATION", cfg.Inject.DeviceAnnotation), "pod annotation key; value true adds /dev/nvidia* device nodes")
	flag.StringVar(&cfg.Inject.ImexChannelAnnotation, "imex-channel-annotation", envOr("NVML_MOCK_IMEX_CHANNEL_ANNOTATION", cfg.Inject.ImexChannelAnnotation), "pod annotation key; value true adds /dev/nvidia-caps-imex-channels/* nodes")
	flag.StringVar(&cfg.Inject.ImexChannelHostPath, "imex-channel-host-path", envOr("NVML_MOCK_IMEX_CHANNEL_HOST_PATH", cfg.Inject.ImexChannelHostPath), "host path containing the mock IMEX channel nodes staged by imex.mockChannels (defaults to <overlay-host-path>/driver/dev/nvidia-caps-imex-channels)")
	excludedNamespaces := flag.String("excluded-namespaces", envOr("NVML_MOCK_EXCLUDED_NAMESPACES", strings.Join(cfg.Inject.ExcludedNamespaces, ",")), "comma-separated namespaces to skip")
	shims := flag.String("ld-preload-shims", envOr("NVML_MOCK_LD_PRELOAD_SHIMS", strings.Join(cfg.Inject.Shims, ",")), "comma-separated LD_PRELOAD shim paths relative to the overlay mount or absolute paths")
	flag.Parse()

	cfg.Inject.ExcludedNamespaces = splitCSV(*excludedNamespaces)
	cfg.Inject.Shims = splitCSV(*shims)

	mode, err := inject.ParseDeviceInjectionMode(*deviceInjectionMode)
	if err != nil {
		log.Fatalf("nvml-mock-nri: --device-injection-mode: %v", err)
	}
	cfg.Inject.DeviceInjectionMode = mode

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	plugin := nri.NewPlugin(cfg)

	// The probes are served alongside the plugin rather than after it, so the
	// un-registered startup window is reported as a 503 rather than a refused
	// connection, and a plugin that never manages to register is visibly
	// NotReady instead of silently idle.
	probes := health.NewServer(*healthAddr, health.DefaultShutdownTimeout)
	probes.SetLiveness(plugin.Liveness)
	probes.SetReadiness(plugin.Readiness)

	group, groupCtx := errgroup.WithContext(ctx)
	group.Go(func() error { return probes.Run(groupCtx) })
	group.Go(func() error { return plugin.Run(groupCtx) })

	if err := group.Wait(); err != nil {
		log.Fatalf("nvml-mock-nri: %v", err)
	}
}

func splitCSV(value string) []string {
	var result []string
	for _, item := range strings.Split(value, ",") {
		item = strings.TrimSpace(item)
		if item != "" {
			result = append(result, item)
		}
	}
	return result
}

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
