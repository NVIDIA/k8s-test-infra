// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// nri-plugin injects the nvml-mock overlay into containers through containerd's
// Node Resource Interface, so a workload sees mock GPUs without being modified.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"slices"
	"syscall"

	"github.com/urfave/cli/v3"
	"golang.org/x/sync/errgroup"

	"github.com/NVIDIA/k8s-test-infra/internal/health"
	"github.com/NVIDIA/k8s-test-infra/internal/logging"
	"github.com/NVIDIA/k8s-test-infra/internal/nri"
	"github.com/NVIDIA/k8s-test-infra/internal/nri/inject"
)

func main() {
	if err := newCLI().Run(context.Background(), os.Args); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "nri-plugin: %v\n", err)
		os.Exit(1)
	}
}

func newCLI() *cli.Command {
	defaults := nri.DefaultConfig()
	return &cli.Command{
		Name:  "nri-plugin",
		Usage: "Mokka NRI plugin — injects the nvml-mock overlay into containers as they are created",
		// Grouped in the order the plugin uses them: how the process behaves,
		// how it attaches to the runtime, then one group per injection step.
		Flags: slices.Concat(
			processFlags(),
			registrationFlags(defaults),
			scopeFlags(defaults),
			overlayFlags(defaults),
			topologyFlags(),
			gpuFlags(defaults),
			imexChannelFlags(defaults),
		),
		Action: run,
	}
}

// processFlags shape the binary itself rather than any injection.
func processFlags() []cli.Flag {
	return []cli.Flag{
		&cli.StringFlag{
			Name:    "log-level",
			Value:   string(logging.LevelInfo),
			Sources: cli.EnvVars("MOKKA_LOG_LEVEL"),
			Usage:   "log level: debug | info | warn | error",
		},
		&cli.StringFlag{
			Name:    "log-format",
			Value:   string(logging.FormatJSON),
			Sources: cli.EnvVars("MOKKA_LOG_FORMAT"),
			Usage:   "log format: json | plain",
		},
		&cli.StringFlag{
			Name:    "health-addr",
			Value:   ":8080",
			Sources: cli.EnvVars("MOKKA_NRI_HEALTH_ADDR"),
			Usage:   "address for the /healthz and /readyz probe endpoints; empty disables them",
		},
	}
}

// registrationFlags decide how the plugin attaches to the container runtime.
func registrationFlags(defaults nri.Config) []cli.Flag {
	return []cli.Flag{
		&cli.StringFlag{
			Name:    "socket-path",
			Value:   defaults.SocketPath,
			Sources: cli.EnvVars("MOKKA_NRI_SOCKET_PATH"),
			Usage:   "NRI socket path",
		},
		&cli.StringFlag{
			Name:    "plugin-name",
			Value:   defaults.PluginName,
			Sources: cli.EnvVars("MOKKA_NRI_PLUGIN_NAME"),
			Usage:   "name this plugin registers with the runtime under",
		},
		&cli.StringFlag{
			Name:    "plugin-index",
			Value:   defaults.PluginIndex,
			Sources: cli.EnvVars("MOKKA_NRI_PLUGIN_INDEX"),
			Usage:   "order against other registered plugins; later indices adjust a container after earlier ones",
		},
	}
}

// scopeFlags decide which containers are left exactly as authored.
func scopeFlags(defaults nri.Config) []cli.Flag {
	return []cli.Flag{
		&cli.StringSliceFlag{
			Name:    "excluded-namespaces",
			Value:   defaults.Inject.ExcludedNamespaces,
			Sources: cli.EnvVars("MOKKA_NRI_EXCLUDED_NAMESPACES"),
			Usage:   "comma-separated namespaces to skip; an empty value excludes nothing",
		},
		&cli.StringFlag{
			Name:    "opt-out-annotation",
			Value:   defaults.Inject.OptOutAnnotation,
			Sources: cli.EnvVars("MOKKA_NRI_OPT_OUT_ANNOTATION"),
			Usage:   "pod annotation key; value false disables injection",
		},
	}
}

// overlayFlags describe the mock driver tree and the loader environment that
// reaches into it.
func overlayFlags(defaults nri.Config) []cli.Flag {
	return []cli.Flag{
		&cli.StringFlag{
			Name:    "overlay-host-path",
			Value:   defaults.Inject.HostOverlayPath,
			Sources: cli.EnvVars("MOKKA_NRI_OVERLAY_HOST_PATH"),
			Usage:   "host path for the nvml-mock overlay",
		},
		&cli.StringFlag{
			Name:    "overlay-mount-path",
			Value:   defaults.Inject.ContainerOverlayPath,
			Sources: cli.EnvVars("MOKKA_NRI_OVERLAY_MOUNT_PATH"),
			Usage:   "container path for the nvml-mock overlay",
		},
		&cli.StringSliceFlag{
			Name:    "ld-preload-shims",
			Value:   defaults.Inject.Shims,
			Sources: cli.EnvVars("MOKKA_NRI_LD_PRELOAD_SHIMS"),
			Usage:   "comma-separated LD_PRELOAD shim paths relative to the overlay mount, or absolute paths",
		},
	}
}

// topologyFlags gate the ComputeDomain environment. Their defaults are derived
// from the overlay roots, so they are left empty here rather than duplicating
// that arithmetic.
func topologyFlags() []cli.Flag {
	return []cli.Flag{
		// NODE_NAME rather than a MOKKA_NRI_ name: the chart supplies it through
		// the downward API under its conventional Kubernetes spelling.
		&cli.StringFlag{
			Name:    "node-name",
			Sources: cli.EnvVars("NODE_NAME"),
			Usage:   "Kubernetes node name; enables ComputeDomain topology injection when a topology document is staged in the overlay",
		},
		&cli.StringFlag{
			Name:    "topology-host-path",
			Sources: cli.EnvVars("MOKKA_NRI_TOPOLOGY_HOST_PATH"),
			Usage:   "host path checked for the staged topology document (defaults to <overlay-host-path>/topology/topology.yaml)",
		},
		&cli.StringFlag{
			Name:    "topology-mount-path",
			Sources: cli.EnvVars("MOKKA_NRI_TOPOLOGY_MOUNT_PATH"),
			Usage:   "container path injected as MOCK_TOPOLOGY_CONFIG (defaults to <overlay-mount-path>/topology/topology.yaml)",
		},
	}
}

// gpuFlags control the annotation-gated GPU opt-in and how it is delivered.
func gpuFlags(defaults nri.Config) []cli.Flag {
	return []cli.Flag{
		&cli.StringFlag{
			Name:    "device-annotation",
			Value:   defaults.Inject.DeviceAnnotation,
			Sources: cli.EnvVars("MOKKA_NRI_DEVICE_ANNOTATION"),
			Usage:   "pod annotation key; value true adds /dev/nvidia* device nodes",
		},
		&cli.StringFlag{
			Name:    "device-host-path",
			Value:   defaults.Inject.DeviceHostPath,
			Sources: cli.EnvVars("MOKKA_NRI_DEVICE_HOST_PATH"),
			Usage:   "host path containing mock /dev/nvidia* nodes",
		},
		&cli.StringFlag{
			Name:    "device-injection-mode",
			Value:   string(defaults.Inject.DeviceInjectionMode),
			Sources: cli.EnvVars("MOKKA_NRI_DEVICE_INJECTION_MODE"),
			Usage:   "how the device opt-in delivers GPUs: raw (device nodes) or cdi (CDI device reference)",
		},
		&cli.StringFlag{
			Name:    "cdi-device-name",
			Value:   defaults.Inject.CDIDeviceName,
			Sources: cli.EnvVars("MOKKA_NRI_CDI_DEVICE_NAME"),
			Usage:   "fully-qualified CDI device injected in cdi mode",
		},
		&cli.StringFlag{
			Name:    "cdi-spec-host-path",
			Value:   defaults.Inject.CDISpecHostPath,
			Sources: cli.EnvVars("MOKKA_NRI_CDI_SPEC_HOST_PATH"),
			Usage:   "staged CDI spec checked before a CDI reference is emitted; a missing spec falls back to raw injection",
		},
	}
}

// imexChannelFlags control the fabric opt-in, which is separate from the GPU
// one because a ComputeDomain workload may want channels without mock GPUs.
func imexChannelFlags(defaults nri.Config) []cli.Flag {
	return []cli.Flag{
		&cli.StringFlag{
			Name:    "imex-channel-annotation",
			Value:   defaults.Inject.ImexChannelAnnotation,
			Sources: cli.EnvVars("MOKKA_NRI_IMEX_CHANNEL_ANNOTATION"),
			Usage:   "pod annotation key; value true adds /dev/nvidia-caps-imex-channels/* nodes",
		},
		&cli.StringFlag{
			Name:    "imex-channel-host-path",
			Sources: cli.EnvVars("MOKKA_NRI_IMEX_CHANNEL_HOST_PATH"),
			Usage:   "host path containing the mock IMEX channel nodes staged by imex.mockChannels (defaults to <overlay-host-path>/driver/dev/nvidia-caps-imex-channels)",
		},
	}
}

// configFrom reads the parsed flags. Paths left empty here are derived from the
// overlay roots by inject.Config's own defaulting, so the CLI does not have to
// repeat that arithmetic.
func configFrom(cmd *cli.Command) (nri.Config, error) {
	mode, err := inject.ParseDeviceInjectionMode(cmd.String("device-injection-mode"))
	if err != nil {
		return nri.Config{}, err
	}

	return nri.Config{
		SocketPath:  cmd.String("socket-path"),
		PluginName:  cmd.String("plugin-name"),
		PluginIndex: cmd.String("plugin-index"),
		Inject: inject.Config{
			HostOverlayPath:       cmd.String("overlay-host-path"),
			ContainerOverlayPath:  cmd.String("overlay-mount-path"),
			NodeName:              cmd.String("node-name"),
			TopologyHostPath:      cmd.String("topology-host-path"),
			TopologyContainerPath: cmd.String("topology-mount-path"),
			DeviceHostPath:        cmd.String("device-host-path"),
			DeviceInjectionMode:   mode,
			CDIDeviceName:         cmd.String("cdi-device-name"),
			CDISpecHostPath:       cmd.String("cdi-spec-host-path"),
			OptOutAnnotation:      cmd.String("opt-out-annotation"),
			DeviceAnnotation:      cmd.String("device-annotation"),
			ImexChannelAnnotation: cmd.String("imex-channel-annotation"),
			ImexChannelHostPath:   cmd.String("imex-channel-host-path"),
			ExcludedNamespaces:    cmd.StringSlice("excluded-namespaces"),
			Shims:                 cmd.StringSlice("ld-preload-shims"),
		},
	}, nil
}

func run(ctx context.Context, cmd *cli.Command) error {
	level, err := logging.ParseLevel(cmd.String("log-level"))
	if err != nil {
		return err
	}

	format, err := logging.ParseFormat(cmd.String("log-format"))
	if err != nil {
		return err
	}

	logging.NewLogger(logging.Config{Level: level, Format: format})

	cfg, err := configFrom(cmd)

	if err != nil {
		return err
	}

	signalCtx, cancel := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	plugin := nri.NewPlugin(cfg)

	// The probes serve alongside the plugin rather than after it, so the
	// un-registered startup window reads as a 503 rather than a refused
	// connection, and a plugin that never manages to register is visibly
	// NotReady instead of silently idle.
	probes := health.NewServer(cmd.String("health-addr"), health.DefaultShutdownTimeout)
	probes.SetLiveness(plugin.Liveness)
	probes.SetReadiness(plugin.Readiness)

	group, groupCtx := errgroup.WithContext(signalCtx)
	group.Go(func() error { return probes.Run(groupCtx) })
	group.Go(func() error { return plugin.Run(groupCtx) })
	return group.Wait()
}
