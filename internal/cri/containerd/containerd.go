// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package containerd registers Mokka's runtime handler with containerd: the
// toolkit's own nvidia-ctk writes Mokka's config file into containerd's config
// dir, Mokka adds NRI to it, and systemd restarts containerd to load it.
package containerd

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"go.uber.org/zap"

	"github.com/NVIDIA/k8s-test-infra/internal/cri"
)

// Defaults for a systemd-managed containerd.
const (
	DefaultConfigPath = "/etc/containerd/config.toml"
	DefaultConfigDir  = "/etc/containerd/conf.d"
	DefaultUnit       = "containerd"
)

// mokkaConfig is the name of Mokka's file in the config dir, which config.toml
// imports. containerd loads the directory in name order and later files win,
// so 99-nvidia.toml, which a toolkit installed later writes, takes precedence.
const mokkaConfig = "50-mokka.toml"

// nriPlugin names NRI's table in config versions 2 and 3 alike.
const nriPlugin = "io.containerd.nri.v1.nri"

var _ cri.Runtime = (*Runtime)(nil)

// Runtime registers Mokka's handler with the node's containerd.
type Runtime struct {
	opts cri.RuntimeOptions
	// loaded is set once this process restarted containerd with Mokka's config
	// file as it is now, so an unchanged file needs no restart.
	loaded bool

	// Tests replace these. root is where this process sees the host's paths:
	// the chart mounts containerd's configuration directory at its host path.
	root      string
	configure func(ctx context.Context, args ...string) error
	systemctl func(ctx context.Context, args ...string) error
}

// New returns a containerd Runtime, filling in containerd's defaults. The
// config dir must be under config.toml's directory, which is what the node pod
// mounts.
func New(opts cri.RuntimeOptions) (*Runtime, error) {
	if opts.ConfigPath == "" {
		opts.ConfigPath = DefaultConfigPath
	}

	if opts.ConfigDir == "" {
		opts.ConfigDir = DefaultConfigDir
	}

	if opts.Unit == "" {
		opts.Unit = DefaultUnit
	}

	parent := filepath.Dir(opts.ConfigPath)
	if rel, err := filepath.Rel(parent, opts.ConfigDir); err != nil || rel == ".." || strings.HasPrefix(rel, "../") {
		return nil, fmt.Errorf("config dir %s must be under %s, the directory of %s", opts.ConfigDir, parent, opts.ConfigPath)
	}

	r := &Runtime{opts: opts, root: "/"}
	r.configure = r.nvidiaCTK
	r.systemctl = func(ctx context.Context, args ...string) error {
		return cri.Systemctl(ctx, opts.Chroot, args...)
	}

	return r, nil
}

// Installed returns nil when containerd is on the host's PATH. A node that runs
// another runtime has none there, and neither has one whose containerd k3s or
// rke2 embeds.
func (r *Runtime) Installed() error {
	_, err := r.opts.Chroot.LookPath("containerd")

	return err
}

// Setup has nvidia-ctk register the handler in Mokka's config file, in CDI mode
// and as containerd's default, so a container's NVIDIA_VISIBLE_DEVICES alone
// gets it the mock GPUs. With an NRI socket it turns NRI on in the same file,
// which nvidia-ctk leaves as the node has it. It restarts containerd when the
// file changed or this process has not restarted it yet: a node pod killed
// between writing the file and restarting leaves one containerd never loaded.
func (r *Runtime) Setup(ctx context.Context) error {
	before, err := r.readMokkaConfig()
	if err != nil {
		return err
	}

	if err := r.configure(ctx,
		"runtime", "configure",
		"--runtime=containerd",
		"--config-source=file",
		"--config="+r.opts.ConfigPath,
		"--drop-in-config="+r.mokkaConfigPath(),
		"--nvidia-runtime-name="+cri.HandlerName,
		"--nvidia-runtime-path="+cri.BinaryName,
		"--nvidia-set-as-default",
		"--cdi.enabled",
	); err != nil {
		return fmt.Errorf("nvidia-ctk runtime configure: %w", err)
	}

	if err := r.enableNRI(); err != nil {
		return err
	}

	after, err := r.readMokkaConfig()
	if err != nil {
		return err
	}

	changed := !bytes.Equal(before, after)
	if r.loaded && !changed {
		zap.L().Debug(
			"Mokka's config file is unchanged; containerd needs no restart",
			zap.String("file", r.mokkaConfigPath()),
		)

		return nil
	}

	if r.opts.RestartMode == cri.RestartNone {
		return cri.ErrRestartPending
	}

	reason := "Mokka's config file changed"
	if !changed {
		reason = "node pod started"
	}

	zap.L().Info("restarting containerd",
		zap.String("unit", r.opts.Unit),
		zap.String("file", r.mokkaConfigPath()),
		zap.String("reason", reason),
	)

	if err := r.systemctl(ctx, "restart", r.opts.Unit); err != nil {
		return fmt.Errorf("restart containerd: %w", err)
	}

	r.loaded = true

	return nil
}

// Cleanup removes Mokka's config file and queues a restart of containerd
// without waiting for it, so systemd completes it after this pod is gone. The
// import nvidia-ctk added to config.toml stays; with the file gone it loads
// nothing.
func (r *Runtime) Cleanup(ctx context.Context) error {
	err := os.Remove(filepath.Join(r.root, r.mokkaConfigPath()))
	if os.IsNotExist(err) {
		zap.L().Debug(
			"no Mokka config file to remove; containerd needs no restart",
			zap.String("file", r.mokkaConfigPath()),
		)

		return nil
	}

	if err != nil {
		return fmt.Errorf("remove %s: %w", r.mokkaConfigPath(), err)
	}

	r.loaded = false

	if r.opts.RestartMode == cri.RestartNone {
		zap.L().Warn("containerd configuration removed; it takes effect when containerd restarts", zap.String("file", r.mokkaConfigPath()))

		return nil
	}

	zap.L().Info("restarting containerd",
		zap.String("unit", r.opts.Unit),
		zap.String("file", r.mokkaConfigPath()),
		zap.String("reason", "Mokka's config file removed"),
	)

	if err := r.systemctl(ctx, "restart", "--no-block", r.opts.Unit); err != nil {
		return fmt.Errorf("restart containerd: %w", err)
	}

	return nil
}

// enableNRI appends NRI's table to the file nvidia-ctk just wrote, unless there
// is no NRI socket to turn on. nvidia-ctk writes the file afresh on every pass,
// so the table is added once, under the config version nvidia-ctk chose.
// containerd before 2.1 replaces a plugin's whole table from an imported file,
// so NRI's other settings take containerd's defaults while the file is in
// place.
func (r *Runtime) enableNRI() error {
	if r.opts.NRISocket == "" {
		return nil
	}

	data, err := r.readMokkaConfig()
	if err != nil {
		return err
	}

	if data == nil {
		return fmt.Errorf("turn NRI on: nvidia-ctk wrote no %s", r.mokkaConfigPath())
	}

	data = fmt.Appendf(
		data,
		"\n[plugins.%q]\n  disable = false\n  socket_path = %q\n",
		nriPlugin,
		r.opts.NRISocket,
	)

	if err := os.WriteFile(filepath.Join(r.root, r.mokkaConfigPath()), data, 0o644); err != nil {
		return fmt.Errorf("turn NRI on in %s: %w", r.mokkaConfigPath(), err)
	}

	zap.L().Debug("turned NRI on in Mokka's config file",
		zap.String("file", r.mokkaConfigPath()),
		zap.String("socket", r.opts.NRISocket),
	)

	return nil
}

// mokkaConfigPath returns where Mokka's config file is on the host.
func (r *Runtime) mokkaConfigPath() string {
	return filepath.Join(r.opts.ConfigDir, mokkaConfig)
}

// readMokkaConfig returns Mokka's config file; nil when there is none.
func (r *Runtime) readMokkaConfig() ([]byte, error) {
	data, err := os.ReadFile(filepath.Join(r.root, r.mokkaConfigPath()))
	if os.IsNotExist(err) {
		return nil, nil
	}

	if err != nil {
		return nil, fmt.Errorf("read %s: %w", r.mokkaConfigPath(), err)
	}

	return data, nil
}

// nvidiaCTK runs the image's nvidia-ctk in this container, with nothing of
// this process's environment: the agent may preload mock libraries.
func (r *Runtime) nvidiaCTK(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, r.opts.NvidiaCTK, args...)
	cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin"}

	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}

	zap.L().Debug("nvidia-ctk configured containerd",
		zap.Strings("args", args),
		zap.String("output", strings.TrimSpace(string(out))),
	)

	return nil
}
