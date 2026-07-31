// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// nvml-mock-nri injects the nvml-mock overlay into containers through
// containerd's Node Resource Interface.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/NVIDIA/k8s-test-infra/pkg/nri/nvmlmock"
	"github.com/containerd/nri/pkg/api"
	"github.com/containerd/nri/pkg/stub"
)

type plugin struct {
	config nvmlmock.Config
	health *health
}

func main() {
	cfg := nvmlmock.DefaultConfig()

	socketPath := flag.String("socket-path", envOr("NRI_SOCKET_PATH", "/var/run/nri/nri.sock"), "NRI socket path")
	healthAddr := flag.String("health-addr", envOr("NVML_MOCK_HEALTH_ADDR", ":8080"), "address for the /healthz and /readyz probe endpoints; empty disables them")
	pluginName := flag.String("plugin-name", envOr("NRI_PLUGIN_NAME", "nvml-mock"), "NRI plugin name")
	pluginIndex := flag.String("plugin-index", envOr("NRI_PLUGIN_INDEX", "10"), "NRI plugin index")
	flag.StringVar(&cfg.HostOverlayPath, "overlay-host-path", envOr("NVML_MOCK_OVERLAY_HOST_PATH", cfg.HostOverlayPath), "host path for the nvml-mock overlay")
	flag.StringVar(&cfg.ContainerOverlayPath, "overlay-mount-path", envOr("NVML_MOCK_OVERLAY_MOUNT_PATH", cfg.ContainerOverlayPath), "container path for the nvml-mock overlay")
	flag.StringVar(&cfg.NodeName, "node-name", envOr("NODE_NAME", cfg.NodeName), "Kubernetes node name; enables ComputeDomain topology injection when a topology document is staged in the overlay")
	flag.StringVar(&cfg.TopologyHostPath, "topology-host-path", envOr("NVML_MOCK_TOPOLOGY_HOST_PATH", cfg.TopologyHostPath), "host path checked for the staged topology document (defaults to <overlay-host-path>/topology/topology.yaml)")
	flag.StringVar(&cfg.TopologyContainerPath, "topology-mount-path", envOr("NVML_MOCK_TOPOLOGY_MOUNT_PATH", cfg.TopologyContainerPath), "container path injected as MOCK_TOPOLOGY_CONFIG (defaults to <overlay-mount-path>/topology/topology.yaml)")
	flag.StringVar(&cfg.DeviceHostPath, "device-host-path", envOr("NVML_MOCK_DEVICE_HOST_PATH", cfg.DeviceHostPath), "host path containing mock /dev/nvidia* nodes")
	flag.StringVar(&cfg.DeviceInjectionMode, "device-injection-mode", envOr("NVML_MOCK_DEVICE_INJECTION_MODE", cfg.DeviceInjectionMode), "how the device opt-in delivers GPUs: raw (device nodes) or cdi (CDI device reference)")
	flag.StringVar(&cfg.CDIDeviceName, "cdi-device-name", envOr("NVML_MOCK_CDI_DEVICE_NAME", cfg.CDIDeviceName), "fully-qualified CDI device injected in cdi mode")
	flag.StringVar(&cfg.CDISpecHostPath, "cdi-spec-host-path", envOr("NVML_MOCK_CDI_SPEC_HOST_PATH", cfg.CDISpecHostPath), "staged CDI spec checked before a CDI reference is emitted; a missing spec falls back to raw injection")
	flag.StringVar(&cfg.OptOutAnnotation, "opt-out-annotation", envOr("NVML_MOCK_OPT_OUT_ANNOTATION", cfg.OptOutAnnotation), "pod annotation key; value false disables injection")
	flag.StringVar(&cfg.DeviceAnnotation, "device-annotation", envOr("NVML_MOCK_DEVICE_ANNOTATION", cfg.DeviceAnnotation), "pod annotation key; value true adds /dev/nvidia* device nodes")
	flag.StringVar(&cfg.ImexChannelAnnotation, "imex-channel-annotation", envOr("NVML_MOCK_IMEX_CHANNEL_ANNOTATION", cfg.ImexChannelAnnotation), "pod annotation key; value true adds /dev/nvidia-caps-imex-channels/* nodes")
	flag.StringVar(&cfg.ImexChannelHostPath, "imex-channel-host-path", envOr("NVML_MOCK_IMEX_CHANNEL_HOST_PATH", cfg.ImexChannelHostPath), "host path containing the mock IMEX channel nodes staged by imex.mockChannels (defaults to <overlay-host-path>/driver/dev/nvidia-caps-imex-channels)")
	excludedNamespaces := flag.String("excluded-namespaces", envOr("NVML_MOCK_EXCLUDED_NAMESPACES", strings.Join(cfg.ExcludedNamespaces, ",")), "comma-separated namespaces to skip")
	shims := flag.String("ld-preload-shims", envOr("NVML_MOCK_LD_PRELOAD_SHIMS", strings.Join(cfg.Shims, ",")), "comma-separated LD_PRELOAD shim paths relative to the overlay mount or absolute paths")
	flag.Parse()

	cfg.ExcludedNamespaces = splitCSV(*excludedNamespaces)
	cfg.Shims = splitCSV(*shims)

	// Reject an unknown mode rather than coercing it. A typo that silently
	// resolved to raw would look exactly like a working CDI deployment, and the
	// difference is only visible in the OCI spec of an already-running pod.
	switch cfg.DeviceInjectionMode {
	case nvmlmock.DeviceInjectionModeRaw, nvmlmock.DeviceInjectionModeCDI:
	default:
		log.Fatalf("nvml-mock-nri: --device-injection-mode=%q is invalid; expected %q or %q",
			cfg.DeviceInjectionMode, nvmlmock.DeviceInjectionModeRaw, nvmlmock.DeviceInjectionModeCDI)
	}

	p := &plugin{config: cfg, health: newHealth(time.Now, wedgeFactor)}
	s, err := stub.New(
		p,
		stub.WithSocketPath(*socketPath),
		stub.WithPluginName(*pluginName),
		stub.WithPluginIdx(*pluginIndex),
		// The runtime dropping the connection is the fail-open failure mode:
		// the process stays up but stops being asked to inject anything, and
		// containers created from here on come up unmocked. Clearing the
		// registered flag is what makes that window visible as a NotReady pod.
		stub.WithOnClose(func() {
			log.Printf("nvml-mock-nri: runtime closed the connection; no longer registered")
			p.health.setRegistered(false)
		}),
	)
	if err != nil {
		log.Fatalf("nvml-mock-nri: create stub: %v", err)
	}
	p.health.setTimeoutSource(s)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// Serve the probes before Run, so the un-registered startup window is
	// reported as a 503 rather than a refused connection, and so a plugin that
	// never manages to register is visibly NotReady instead of silently idle.
	if stopHealth, err := serveHealth(*healthAddr, p.health); err != nil {
		log.Fatalf("nvml-mock-nri: serve health endpoints: %v", err)
	} else {
		defer stopHealth()
	}

	log.Printf("nvml-mock-nri: registering plugin %s/%s on %s", *pluginIndex, *pluginName, *socketPath)
	if err := s.Run(ctx); err != nil && ctx.Err() == nil {
		log.Fatalf("nvml-mock-nri: %v", err)
	}
}

// serveHealth starts the probe listener and returns a shutdown function. The
// listener is bound synchronously so a port clash fails startup loudly instead
// of leaving the plugin running with no probe surface — which would read as
// healthy forever.
func serveHealth(addr string, h *health) (func(), error) {
	if addr == "" {
		log.Printf("nvml-mock-nri: health endpoints disabled")
		return func() {}, nil
	}

	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("listen on %s: %w", addr, err)
	}

	srv := &http.Server{
		Handler:           h.handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		if err := srv.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("nvml-mock-nri: health server: %v", err)
		}
	}()
	log.Printf("nvml-mock-nri: serving /healthz and /readyz on %s", listener.Addr())

	return func() { _ = srv.Close() }, nil
}

func (p *plugin) Configure(_ context.Context, _, runtime, version string) (stub.EventMask, error) {
	log.Printf("nvml-mock-nri: configured by runtime %s NRI %s", runtime, version)

	// The runtime calls Configure as the last step of registration, so this is
	// the point at which the plugin actually starts receiving containers.
	p.health.setRegistered(true)

	var events stub.EventMask
	events.Set(api.Event_CREATE_CONTAINER)
	return events, nil
}

func (p *plugin) CreateContainer(_ context.Context, pod *api.PodSandbox, container *api.Container) (*api.ContainerAdjustment, []*api.ContainerUpdate, error) {
	// Bracket the handler so a call that never returns is visible to the
	// probes. A wedged handler keeps both the process and the connection
	// alive, so nothing else notices it.
	defer p.health.begin()()

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
		// What the runtime already applied, so Adjust can tell whether the device
		// plugin served this container. GetLinux() is nil-safe.
		for _, device := range container.GetLinux().GetDevices() {
			result.Devices = append(result.Devices, nvmlmock.Device{Path: device.GetPath()})
		}
		for _, device := range container.GetCDIDevices() {
			result.CDIDevices = append(result.CDIDevices, device.GetName())
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
	// CDI references are resolved by the runtime from the spec setup.sh stages,
	// so there is nothing to stat here — an unresolvable name fails container
	// creation, which is why Adjust only emits one once it has seen the spec.
	for _, name := range adjustment.CDIDevices {
		result.AddCDIDevice(&api.CDIDevice{Name: name})
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

// major and minor decode a Linux dev_t the way glibc encodes it
// (MMMM Mmmm mmmM MMmm): the major occupies bits 8-19 and 44-63, the minor
// bits 0-7 and 20-43.
//
// These deliberately do not call unix.Major and unix.Minor. Those resolve per
// GOOS: on darwin they decode Darwin's dev_t, where the major is bits 24-31.
// stat.Rdev here always describes a device node on a Linux node, whatever host
// the binary was built on, so the decoding must not follow the build platform.
// dev_number_oracle_test.go pins these against unix.Major and unix.Minor so
// they cannot drift from glibc.
func major(dev uint64) uint64 {
	return ((dev & 0x00000000000fff00) >> 8) | ((dev & 0xfffff00000000000) >> 32)
}

func minor(dev uint64) uint64 {
	return (dev & 0x00000000000000ff) | ((dev & 0x00000ffffff00000) >> 12)
}
