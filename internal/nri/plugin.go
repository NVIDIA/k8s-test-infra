// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package nri is the containerd Node Resource Interface plugin that injects the
// nvml-mock overlay into containers as they are created.
//
// It is the runtime-coupled half of the plugin: it registers with containerd,
// translates the runtime's container types to and from the decision types in
// internal/nri/inject, and reports whether injection is actually happening.
// That last part matters because the plugin fails open — a plugin containerd
// has unregistered stays alive and silently stops injecting.
package nri

import (
	"context"
	"fmt"
	"time"

	"github.com/containerd/nri/pkg/api"
	"github.com/containerd/nri/pkg/stub"
	"go.uber.org/zap"

	"github.com/NVIDIA/k8s-test-infra/internal/health"
	"github.com/NVIDIA/k8s-test-infra/internal/nri/inject"
)

// Plugin serves containerd's CreateContainer events for the lifetime of Run.
type Plugin struct {
	cfg    Config
	health *pluginHealth
}

// NewPlugin returns a Plugin that will register as cfg describes.
func NewPlugin(cfg Config) *Plugin {
	return &Plugin{cfg: cfg, health: newPluginHealth(time.Now, wedgeFactor)}
}

// Liveness fails only for a wedged handler; wire it to /healthz.
func (p *Plugin) Liveness() health.Probe { return p.health.liveness() }

// Readiness fails for every window in which injection is silently not
// happening; wire it to /readyz.
func (p *Plugin) Readiness() health.Probe { return p.health.readiness() }

// Run registers with the runtime and serves until ctx is cancelled.
func (p *Plugin) Run(ctx context.Context) error {
	pluginStub, err := stub.New(
		p,
		stub.WithSocketPath(p.cfg.SocketPath),
		stub.WithPluginName(p.cfg.PluginName),
		stub.WithPluginIdx(p.cfg.PluginIndex),
		// The runtime dropping the connection is the fail-open failure mode:
		// the process stays up but stops being asked to inject anything, and
		// containers created from here on come up unmocked. Clearing the
		// registered flag is what makes that window visible as a NotReady pod.
		stub.WithOnClose(func() {
			zap.L().Warn("runtime closed the connection; no longer registered")
			p.health.setRegistered(false)
		}),
	)
	if err != nil {
		return fmt.Errorf("create nri stub: %w", err)
	}
	p.health.setTimeoutSource(pluginStub)

	zap.L().Info("registering NRI plugin",
		zap.String("index", p.cfg.PluginIndex), zap.String("name", p.cfg.PluginName), zap.String("socket", p.cfg.SocketPath))

	if err := pluginStub.Run(ctx); err != nil && ctx.Err() == nil {
		return fmt.Errorf("nri stub: %w", err)
	}
	return nil
}

// Configure is the last step of registration, so it is the point at which the
// plugin actually starts receiving containers.
func (p *Plugin) Configure(_ context.Context, _, runtime, version string) (stub.EventMask, error) {
	zap.L().Info("configured by runtime", zap.String("runtime", runtime), zap.String("nri_version", version))
	p.health.setRegistered(true)

	var events stub.EventMask
	events.Set(api.Event_CREATE_CONTAINER)
	return events, nil
}

// CreateContainer decides the adjustment, then renders it for the runtime.
func (p *Plugin) CreateContainer(_ context.Context, pod *api.PodSandbox, container *api.Container) (*api.ContainerAdjustment, []*api.ContainerUpdate, error) {
	// Bracket the handler so a call that never returns is visible to the
	// probes. A wedged handler keeps both the process and the connection
	// alive, so nothing else notices it.
	defer p.health.begin()()

	adjustment, ok := inject.Adjust(p.cfg.Inject, containerFromNRI(pod, container))
	if !ok {
		return nil, nil, nil
	}
	return adjustmentToNRI(adjustment), nil, nil
}
