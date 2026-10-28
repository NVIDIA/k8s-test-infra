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
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/containerd/nri/pkg/api"
	"github.com/containerd/nri/pkg/stub"
	"go.uber.org/zap"

	"github.com/NVIDIA/k8s-test-infra/internal/health"
	"github.com/NVIDIA/k8s-test-infra/internal/nri/inject"
	"github.com/NVIDIA/k8s-test-infra/internal/staginggate"
)

// Plugin serves containerd's CreateContainer events for the lifetime of Run.
type Plugin struct {
	cfg         Config
	health      *pluginHealth
	agentClient *http.Client
}

// NewPlugin returns a Plugin that will register as cfg describes.
func NewPlugin(cfg Config) *Plugin {
	return &Plugin{
		cfg:         cfg,
		health:      newPluginHealth(time.Now, wedgeFactor),
		agentClient: &http.Client{Timeout: agentCheckTimeout},
	}
}

// Liveness fails only for a wedged handler; wire it to /healthz.
func (p *Plugin) Liveness() health.Probe { return p.health.liveness() }

// Readiness fails for every window in which injection is silently not
// happening; wire it to /readyz.
func (p *Plugin) Readiness() health.Probe {
	if probe := p.health.readiness(); !probe.OK {
		return probe
	}
	lock, reason := p.agentAccess(context.Background())
	if reason != "" {
		return health.Unhealthy("not injecting new containers: %s", reason)
	}
	if lock != nil {
		releaseAgentLock(lock)
	}
	return health.OK()
}

// agentCheckTimeout bounds the node-agent check on the container-creation path.
// The agent answers over loopback in well under a millisecond; the slack keeps
// a briefly busy agent from costing a container its injection, at the price of
// this much extra latency per container while the agent is frozen.
const agentCheckTimeout = 500 * time.Millisecond

// agentAccess holds a shared staging lock until the caller has decided its
// adjustment, or says why the node agent's tree cannot be used right now. The
// agent takes the exclusive side before any Stage or teardown mutation, and
// its /stagedz endpoint tells a live staged agent from a stopped one, whose
// lock file outlives it.
func (p *Plugin) agentAccess(ctx context.Context) (lock *staginggate.Lock, reason string) {
	if p.cfg.AgentStagedURL == "" {
		return nil, ""
	}
	lock, acquired, err := staginggate.Shared(p.cfg.StagingLockPath)
	if err != nil {
		return nil, fmt.Sprintf("staging lock unavailable: %v", err)
	}
	if !acquired {
		return nil, "node agent is staging"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.cfg.AgentStagedURL, nil)
	if err != nil {
		releaseAgentLock(lock)
		return nil, fmt.Sprintf("invalid node agent URL: %v", err)
	}
	resp, err := p.agentClient.Do(req)
	if err != nil {
		releaseAgentLock(lock)
		return nil, fmt.Sprintf("node agent unreachable: %v", err)
	}
	if err := resp.Body.Close(); err != nil {
		zap.L().Debug("close node agent staged response", zap.Error(err))
	}
	if resp.StatusCode != http.StatusOK {
		releaseAgentLock(lock)
		return nil, "node agent has not staged the driver tree"
	}
	return lock, ""
}

func releaseAgentLock(lock *staginggate.Lock) {
	if err := lock.Close(); err != nil {
		zap.L().Warn("release staging lock", zap.Error(err))
	}
}

// Run registers with the runtime and serves until ctx is cancelled.
func (p *Plugin) Run(ctx context.Context) error {
	var warnings throttle
	for {
		err := p.register(ctx)
		if ctx.Err() != nil {
			return nil
		}
		// A configuration error never registers: exit instead of retrying.
		if errors.Is(err, errCreateStub) {
			return err
		}
		// Re-register in-process: containerd restarts (the container toolkit
		// restarts it on each rollout) would otherwise cost the kubelet's crash
		// backoff, with every container created meanwhile unmocked.
		log := zap.L().Debug
		if warnings.allow(time.Now()) {
			log = zap.L().Warn
		}
		log("not registered with the runtime; retrying", zap.Duration("in", reregisterInterval), zap.Error(err))
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(reregisterInterval):
		}
	}
}

// errCreateStub marks a configuration the NRI stub rejects.
var errCreateStub = errors.New("create nri stub")

// reregisterInterval paces registration attempts while the runtime is down.
const reregisterInterval = 500 * time.Millisecond

// reregisterWarnInterval spaces the warnings of a runtime that stays down;
// the attempts in between log at debug.
const reregisterWarnInterval = time.Minute

// throttle allows one event per reregisterWarnInterval.
type throttle struct {
	last time.Time
}

func (t *throttle) allow(now time.Time) bool {
	if !t.last.IsZero() && now.Sub(t.last) < reregisterWarnInterval {
		return false
	}
	t.last = now
	return true
}

// register serves one connection to the runtime, until the runtime closes it
// or ctx is cancelled.
func (p *Plugin) register(ctx context.Context) error {
	// The stub checks the index only at registration, which would look like a
	// runtime that is down.
	if err := api.CheckPluginIndex(p.cfg.PluginIndex); err != nil {
		return fmt.Errorf("%w: %w", errCreateStub, err)
	}
	pluginStub, err := stub.New(
		p,
		stub.WithSocketPath(p.cfg.SocketPath),
		stub.WithPluginName(p.cfg.PluginName),
		stub.WithPluginIdx(p.cfg.PluginIndex),
		// Until the plugin registers again, new containers come up unmocked;
		// clearing the flag shows that window as a NotReady pod.
		stub.WithOnClose(func() {
			zap.L().Warn("runtime closed the connection; no longer registered")
			p.health.setRegistered(false)
		}),
	)
	if err != nil {
		return fmt.Errorf("%w: %w", errCreateStub, err)
	}
	p.health.setTimeoutSource(pluginStub)

	zap.L().Info("registering NRI plugin",
		zap.String("index", p.cfg.PluginIndex), zap.String("name", p.cfg.PluginName), zap.String("socket", p.cfg.SocketPath))

	if err := pluginStub.Run(ctx); err != nil {
		return fmt.Errorf("nri stub: %w", err)
	}
	return errors.New("nri stub stopped")
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
func (p *Plugin) CreateContainer(ctx context.Context, pod *api.PodSandbox, container *api.Container) (*api.ContainerAdjustment, []*api.ContainerUpdate, error) {
	// Bracket the handler so a call that never returns is visible to the
	// probes. A wedged handler keeps both the process and the connection
	// alive, so nothing else notices it.
	defer p.health.begin()()
	candidate := containerFromNRI(pod, container)
	// Containers NRI skips anyway never reach the gate, so a closed gate is
	// reported only for containers it actually leaves unmocked.
	if reason, skipped := inject.Skip(p.cfg.Inject, candidate); skipped {
		zap.L().Debug("container left unmodified",
			zap.String("pod", pod.GetName()), zap.String("container", container.GetName()), zap.String("reason", reason))
		return nil, nil, nil
	}

	if inject.Gated(candidate) {
		lock, reason := p.agentAccess(ctx)
		if reason != "" {
			// Fail open, as the plugin does everywhere else: blocking creation
			// behind the agent would stall every new pod on the node. The warning
			// and the readiness failure are what keep this container visible.
			zap.L().Warn("node agent tree unavailable; leaving container unmodified",
				zap.String("namespace", pod.GetNamespace()), zap.String("pod", pod.GetName()),
				zap.String("container", container.GetName()), zap.String("reason", reason))
			return nil, nil, nil
		}
		if lock != nil {
			defer releaseAgentLock(lock)
		}
	}

	adjustment, ok := inject.Adjust(p.cfg.Inject, candidate)
	if !ok {
		zap.L().Debug("container left unmodified", zap.String("pod", pod.GetName()), zap.String("container", container.GetName()))
		return nil, nil, nil
	}
	zap.L().Debug("container adjusted",
		zap.String("pod", pod.GetName()), zap.String("container", container.GetName()),
		zap.Int("mounts", len(adjustment.Mounts)), zap.Int("devices", len(adjustment.Devices)))
	return adjustmentToNRI(adjustment), nil, nil
}
