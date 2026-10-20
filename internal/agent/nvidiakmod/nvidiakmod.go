// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package nvidiakmod stages the simulated NVIDIA kernel modules for the GPU
// Operator's driver container: their sources and loader, the modprobe install
// directives that route the container's module loads to that loader, and the
// node's GPUs as the modules' parameter. Beside them goes the dynamic linker
// directive that puts the mock NVML ahead of the one the container installs.
package nvidiakmod

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sync/atomic"

	"go.uber.org/zap"

	"github.com/NVIDIA/k8s-test-infra/internal/agent"
	"github.com/NVIDIA/k8s-test-infra/internal/agent/host"
	"github.com/NVIDIA/k8s-test-infra/internal/fsutil"
	kmod "github.com/NVIDIA/k8s-test-infra/shims/nvidia-kmod"
)

const name = "nvidiakmod"

var _ agent.Simulator = (*Simulator)(nil)

// Simulator stages what the NRI plugin mounts into the GPU Operator's driver
// container, so that the container's own `modprobe nvidia` loads the simulated
// modules. Nothing is built here: the container compiles the modules against
// the node's kernel when it loads them.
type Simulator struct {
	host  *host.Host
	ready atomic.Bool
}

// New returns an nvidiakmod Simulator.
func New(h *host.Host) *Simulator { return &Simulator{host: h} }

// Name returns the simulator's stable identifier.
func (s *Simulator) Name() string { return name }

// Ready reports whether the last Stage call completed without error.
func (s *Simulator) Ready() bool { return s.ready.Load() }

// Stage writes the tree under the overlay root, replacing every file on each
// call, so a profile change reaches the GPU records and an agent upgrade the
// sources. Files are replaced rather than the directory, because the driver
// container holds the directory as a bind mount. A container that already
// loaded the modules keeps the GPUs it loaded them with, as a real driver
// keeps those it probed.
func (s *Simulator) Stage(ctx context.Context, state *agent.State) error {
	s.ready.Store(false)
	zap.L().Info("staging simulator", zap.String("simulator", name))

	dir := s.host.RootPath(kmod.StagedDir)
	if err := stageSources(ctx, dir); err != nil {
		return fmt.Errorf("stage module sources: %w", err)
	}

	gpus := kmod.Records(gpusOf(state.Devices))
	if err := fsutil.Write(filepath.Join(dir, kmod.GPUsFile), []byte(gpus), 0o644); err != nil {
		return fmt.Errorf("stage GPU records: %w", err)
	}

	ldconf := kmod.LdSoConf(kmod.ContainerNVMLDir)
	if err := fsutil.Write(filepath.Join(dir, kmod.LdSoConfFile), []byte(ldconf), 0o644); err != nil {
		return fmt.Errorf("stage dynamic linker configuration: %w", err)
	}

	// Last: the NRI plugin takes the install directives as the sign that the
	// tree is complete.
	conf := kmod.ModprobeConf(kmod.ContainerDir)
	if err := fsutil.Write(filepath.Join(dir, kmod.ModprobeConfFile), []byte(conf), 0o644); err != nil {
		return fmt.Errorf("stage modprobe configuration: %w", err)
	}

	s.ready.Store(true)
	zap.L().Info("simulator staged", zap.String("simulator", name), zap.Int("gpus", len(state.Devices)))

	return nil
}

// Discard removes the staged tree.
func (s *Simulator) Discard(_ context.Context) error {
	zap.L().Info("discarding simulator", zap.String("simulator", name))

	dir := s.host.RootPath(kmod.StagedDir)
	if err := os.RemoveAll(dir); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove %s: %w", dir, err)
	}

	return nil
}

// stageSources copies the embedded sources under dir. embed drops file modes,
// and modprobe executes the loader directly, as the driver container does the
// persistenced stand-in, so they are made executable here.
func stageSources(ctx context.Context, dir string) error {
	return fs.WalkDir(kmod.Sources, ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}

		data, err := kmod.Sources.ReadFile(path)
		if err != nil {
			return err
		}

		perm := os.FileMode(0o644)
		if slices.Contains(kmod.Executables, path) {
			perm = 0o755
		}

		return fsutil.Write(filepath.Join(dir, path), data, perm)
	})
}

// gpusOf describes the node's GPUs as the modules' parameter does. A profile
// writes the PCI device ID as NVML reports it, device ID over vendor ID, while
// card info carries the device half alone.
func gpusOf(devices []agent.DeviceSpec) []kmod.GPU {
	gpus := make([]kmod.GPU, 0, len(devices))
	for _, device := range devices {
		gpus = append(gpus, kmod.GPU{
			BusID:    device.PCIBusID,
			UUID:     device.UUID,
			Model:    device.Name,
			Minor:    device.MinorNumber,
			DeviceID: uint16(device.PCIDeviceID >> 16),
		})
	}

	return gpus
}
