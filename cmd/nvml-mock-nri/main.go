// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// nvml-mock-nri injects the nvml-mock overlay into containers through
// containerd's Node Resource Interface.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/NVIDIA/k8s-test-infra/pkg/nri/nvmlmock"
	"github.com/containerd/nri/pkg/api"
	"github.com/containerd/nri/pkg/stub"
)

type plugin struct {
	config nvmlmock.Config
}

func main() {
	cfg := nvmlmock.DefaultConfig()

	socketPath := flag.String("socket-path", envOr("NRI_SOCKET_PATH", "/var/run/nri/nri.sock"), "NRI socket path")
	pluginName := flag.String("plugin-name", envOr("NRI_PLUGIN_NAME", "nvml-mock"), "NRI plugin name")
	pluginIndex := flag.String("plugin-index", envOr("NRI_PLUGIN_INDEX", "10"), "NRI plugin index")
	flag.StringVar(&cfg.HostOverlayPath, "overlay-host-path", envOr("NVML_MOCK_OVERLAY_HOST_PATH", cfg.HostOverlayPath), "host path for the nvml-mock overlay")
	flag.StringVar(&cfg.ContainerOverlayPath, "overlay-mount-path", envOr("NVML_MOCK_OVERLAY_MOUNT_PATH", cfg.ContainerOverlayPath), "container path for the nvml-mock overlay")
	flag.StringVar(&cfg.NodeName, "node-name", envOr("NODE_NAME", cfg.NodeName), "Kubernetes node name; enables ComputeDomain topology injection when a topology document is staged in the overlay")
	flag.StringVar(&cfg.TopologyHostPath, "topology-host-path", envOr("NVML_MOCK_TOPOLOGY_HOST_PATH", cfg.TopologyHostPath), "host path checked for the staged topology document (defaults to <overlay-host-path>/topology/topology.yaml)")
	flag.StringVar(&cfg.TopologyContainerPath, "topology-mount-path", envOr("NVML_MOCK_TOPOLOGY_MOUNT_PATH", cfg.TopologyContainerPath), "container path injected as MOCK_TOPOLOGY_CONFIG (defaults to <overlay-mount-path>/topology/topology.yaml)")
	flag.StringVar(&cfg.DeviceHostPath, "device-host-path", envOr("NVML_MOCK_DEVICE_HOST_PATH", cfg.DeviceHostPath), "host path containing mock /dev/nvidia* nodes")
	flag.StringVar(&cfg.OptOutAnnotation, "opt-out-annotation", envOr("NVML_MOCK_OPT_OUT_ANNOTATION", cfg.OptOutAnnotation), "pod annotation key; value false disables injection")
	flag.StringVar(&cfg.DeviceAnnotation, "device-annotation", envOr("NVML_MOCK_DEVICE_ANNOTATION", cfg.DeviceAnnotation), "pod annotation key; value true adds /dev/nvidia* device nodes")
	excludedNamespaces := flag.String("excluded-namespaces", envOr("NVML_MOCK_EXCLUDED_NAMESPACES", strings.Join(cfg.ExcludedNamespaces, ",")), "comma-separated namespaces to skip")
	shims := flag.String("ld-preload-shims", envOr("NVML_MOCK_LD_PRELOAD_SHIMS", strings.Join(cfg.Shims, ",")), "comma-separated LD_PRELOAD shim paths relative to the overlay mount or absolute paths")
	flag.Parse()

	cfg.ExcludedNamespaces = splitCSV(*excludedNamespaces)
	cfg.Shims = splitCSV(*shims)

	p := &plugin{config: cfg}
	s, err := stub.New(
		p,
		stub.WithSocketPath(*socketPath),
		stub.WithPluginName(*pluginName),
		stub.WithPluginIdx(*pluginIndex),
	)
	if err != nil {
		log.Fatalf("nvml-mock-nri: create stub: %v", err)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	log.Printf("nvml-mock-nri: registering plugin %s/%s on %s", *pluginIndex, *pluginName, *socketPath)
	if err := s.Run(ctx); err != nil && ctx.Err() == nil {
		log.Fatalf("nvml-mock-nri: %v", err)
	}
}

func (p *plugin) Configure(_ context.Context, _, runtime, version string) (stub.EventMask, error) {
	log.Printf("nvml-mock-nri: configured by runtime %s NRI %s", runtime, version)

	var events stub.EventMask
	events.Set(api.Event_CREATE_CONTAINER)
	return events, nil
}

func (p *plugin) CreateContainer(_ context.Context, pod *api.PodSandbox, container *api.Container) (*api.ContainerAdjustment, []*api.ContainerUpdate, error) {
	adjustment, ok, err := nvmlmock.Adjust(p.config, fromNRI(pod, container))
	if err != nil {
		return nil, nil, err
	}
	if !ok {
		return nil, nil, nil
	}

	nriAdjustment, err := toNRI(adjustment)
	if err != nil {
		return nil, nil, err
	}
	return nriAdjustment, nil, nil
}

func fromNRI(pod *api.PodSandbox, container *api.Container) nvmlmock.Container {
	result := nvmlmock.Container{}
	if pod != nil {
		result.Namespace = pod.GetNamespace()
		result.PodAnnotations = pod.GetAnnotations()
	}
	if container != nil {
		result.Env = append([]string(nil), container.GetEnv()...)
		for _, mount := range container.GetMounts() {
			result.Mounts = append(result.Mounts, nvmlmock.Mount{
				Source:      mount.GetSource(),
				Destination: mount.GetDestination(),
				Type:        mount.GetType(),
				Options:     append([]string(nil), mount.GetOptions()...),
			})
		}
	}
	return result
}

func toNRI(adjustment nvmlmock.Adjustment) (*api.ContainerAdjustment, error) {
	result := &api.ContainerAdjustment{}
	for _, mount := range adjustment.Mounts {
		result.AddMount(&api.Mount{
			Source:      mount.Source,
			Destination: mount.Destination,
			Type:        mount.Type,
			Options:     append([]string(nil), mount.Options...),
		})
	}
	for _, env := range adjustment.Env {
		key, value, ok := strings.Cut(env, "=")
		if !ok {
			continue
		}
		result.AddEnv(key, value)
	}
	for _, device := range adjustment.Devices {
		nriDevice, err := nriDevice(device)
		if err != nil {
			// Fail open on a per-device basis: a device node that vanished or
			// is not yet staged shouldn't fail creation of the whole container.
			log.Printf("nvml-mock-nri: skipping device %s: %v", device.Path, err)
			continue
		}
		result.AddDevice(nriDevice)
	}
	return result, nil
}

func nriDevice(device nvmlmock.Device) (*api.LinuxDevice, error) {
	var stat syscall.Stat_t
	if err := syscall.Stat(device.HostPath, &stat); err != nil {
		return nil, fmt.Errorf("stat device %s: %w", device.HostPath, err)
	}
	if stat.Mode&syscall.S_IFMT != syscall.S_IFCHR {
		return nil, fmt.Errorf("%s is not a character device", device.HostPath)
	}

	return &api.LinuxDevice{
		Path:     device.Path,
		Type:     "c",
		Major:    int64(major(uint64(stat.Rdev))),
		Minor:    int64(minor(uint64(stat.Rdev))),
		FileMode: api.FileMode(os.FileMode(stat.Mode) & os.ModePerm),
		Uid:      api.UInt32(stat.Uid),
		Gid:      api.UInt32(stat.Gid),
	}, nil
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

func major(dev uint64) uint64 {
	return (dev >> 8) & 0xfff
}

func minor(dev uint64) uint64 {
	return (dev & 0xff) | ((dev >> 12) & 0xfff00)
}
