// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package nvidiakmod stages the simulated NVIDIA kernel modules for the GPU
// Operator's driver container: their sources and loader, and the modprobe
// install directives that route the container's module loads to that loader.
// Beside them goes the dynamic linker directive that puts the mock NVML ahead
// of the one the container installs. Once the container has loaded the
// modules, the simulator keeps the node's GPUs described in the kernel, in the
// mokka module that holds them.
package nvidiakmod

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"go.uber.org/zap"

	"github.com/NVIDIA/k8s-test-infra/internal/agent"
	"github.com/NVIDIA/k8s-test-infra/internal/agent/host"
	"github.com/NVIDIA/k8s-test-infra/internal/fsutil"
	kmod "github.com/NVIDIA/k8s-test-infra/shims/nvidia-kmod"
)

const name = "nvidiakmod"

// pollInterval is how often Run looks for the mokka module. The driver
// container inserts it on its first driver load and waits for the GPUs, so
// this bounds how long that wait lasts.
const pollInterval = time.Second

var (
	_ agent.Simulator = (*Simulator)(nil)
	_ agent.Daemon    = (*Simulator)(nil)
)

// Simulator stages what the NRI plugin mounts into the GPU Operator's driver
// container, so that the container's own `modprobe nvidia` loads the simulated
// modules, and describes the node's GPUs to the kernel once they are loaded.
// Nothing is built here: the container compiles the modules against the node's
// kernel when it loads them.
type Simulator struct {
	host  *host.Host
	ready atomic.Bool

	// gpus is the latest description Stage recorded, and version counts the
	// changes to it, so Run can tell whether the kernel has the latest.
	gpus    atomic.Pointer[[]gpu]
	version atomic.Uint64

	// rmdir removes a GPU's directory. configfs refuses os.RemoveAll, which
	// unlinks the attribute files first; a field so tests can use a plain tree.
	rmdir func(path string) error
}

// New returns an nvidiakmod Simulator.
func New(h *host.Host) *Simulator { return &Simulator{host: h, rmdir: os.Remove} }

// Name returns the simulator's stable identifier.
func (s *Simulator) Name() string { return name }

// Ready reports whether the last Stage call completed without error.
func (s *Simulator) Ready() bool { return s.ready.Load() }

// Stage writes the tree under the overlay root, replacing every file on each
// call, so an agent upgrade reaches the sources. Files are replaced rather than
// the directory, because the driver container holds the directory as a bind
// mount. The node's GPUs go to the kernel instead, through Run.
func (s *Simulator) Stage(ctx context.Context, state *agent.State) error {
	s.ready.Store(false)
	zap.L().Info("staging simulator", zap.String("simulator", name))

	dir := s.host.RootPath(kmod.StagedDir)
	if err := stageSources(ctx, dir); err != nil {
		return fmt.Errorf("stage module sources: %w", err)
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

	s.describe(state.Devices)

	s.ready.Store(true)
	zap.L().Info("simulator staged", zap.String("simulator", name), zap.Int("gpus", len(state.Devices)))

	return nil
}

// Discard empties the staged tree but keeps its directory. A running driver
// container holds the directory as a bind mount; removed, it would stay empty
// for that container while the next agent stages a new one at the same path.
// The GPUs stay described in the kernel: a GPU stays plugged in while the
// software around it restarts, and the next agent takes the description over
// from there.
func (s *Simulator) Discard(_ context.Context) error {
	zap.L().Info("discarding simulator", zap.String("simulator", name))

	dir := s.host.RootPath(kmod.StagedDir)
	if err := fsutil.PruneDir(dir, func(string) bool { return false }); err != nil {
		return fmt.Errorf("empty %s: %w", dir, err)
	}

	return nil
}

// Run keeps the kernel's description of the node's GPUs in step with the latest
// one Stage recorded. It polls rather than waiting for a State change: the mokka
// module appears whenever the driver container first loads the driver, and a
// reloaded module starts with no GPUs.
func (s *Simulator) Run(ctx context.Context) error {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	var applied uint64
	var failure string
	for {
		applied = s.sync(applied, &failure)

		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// Reload records the latest description, which Run applies.
func (s *Simulator) Reload(_ context.Context, state *agent.State) error {
	s.describe(state.Devices)

	return nil
}

// describe records the node's GPUs for Run, counting a change only when the
// description differs from the last.
func (s *Simulator) describe(devices []agent.DeviceSpec) {
	gpus := gpusOf(devices)
	if last := s.gpus.Load(); last != nil && slices.Equal(*last, gpus) {
		return
	}

	s.gpus.Store(&gpus)
	s.version.Add(1)
}

// sync applies the latest description unless the kernel already has it, and
// returns the version the kernel has. failure holds the last error logged, so
// an error that persists is logged once rather than on every poll.
func (s *Simulator) sync(applied uint64, failure *string) uint64 {
	hw := hardware{root: s.host.SysPath("kernel", "config", kmod.HardwareConfigDir), rmdir: s.rmdir}
	if _, err := os.Stat(hw.root); err != nil {
		// Not loaded yet: the driver container inserts it with the driver.
		return applied
	}

	gpus := s.gpus.Load()
	if gpus == nil {
		return applied
	}

	version := s.version.Load()
	if version == applied && hw.described() {
		return applied
	}

	generation, err := hw.apply(*gpus)
	if err != nil {
		if msg := err.Error(); msg != *failure {
			*failure = msg
			zap.L().Warn("cannot describe the node's GPUs to the kernel",
				zap.String("simulator", name), zap.Error(err))
		}

		return applied
	}

	*failure = ""
	zap.L().Info("described the node's GPUs to the kernel", zap.String("simulator", name),
		zap.Int("gpus", len(*gpus)), zap.Uint64("generation", generation))

	return version
}

// gpu is a GPU as the mokka module takes it.
type gpu struct {
	// BusID is the canonical PCI location, which names the GPU's directory.
	BusID string
	UUID  string
	Name  string
	Minor int
	// DeviceID is the PCI device ID, without the vendor half.
	DeviceID uint16
}

// gpusOf describes the node's GPUs as the mokka module takes them. A profile
// writes the PCI device ID as NVML reports it, device ID over vendor ID, and
// NVML may spell the domain with eight digits in upper case, while the module
// takes the device half alone and names a GPU by its canonical location.
func gpusOf(devices []agent.DeviceSpec) []gpu {
	gpus := make([]gpu, 0, len(devices))
	for _, device := range devices {
		busID, err := canonicalBusID(device.PCIBusID)
		if err != nil {
			zap.L().Warn("leaving out a GPU the kernel could not place", zap.String("simulator", name),
				zap.String("uuid", device.UUID), zap.Error(err))
			continue
		}

		gpus = append(gpus, gpu{
			BusID:    busID,
			UUID:     device.UUID,
			Name:     device.Name,
			Minor:    device.MinorNumber,
			DeviceID: uint16(device.PCIDeviceID >> 16),
		})
	}

	return gpus
}

// canonicalBusID spells a PCI location as the mokka module names GPUs:
// dddd:bb:ss.f, lower case.
func canonicalBusID(location string) (string, error) {
	var domain, bus, slot, function uint32
	if _, err := fmt.Sscanf(location, "%x:%x:%x.%x", &domain, &bus, &slot, &function); err != nil {
		return "", fmt.Errorf("parse PCI location %q: %w", location, err)
	}
	if domain > 0xffff || bus > 0xff || slot > 0x1f || function > 0x7 {
		return "", fmt.Errorf("PCI location %q is out of range", location)
	}

	return fmt.Sprintf("%04x:%02x:%02x.%x", domain, bus, slot, function), nil
}

// hardware is the mokka module's configfs tree.
type hardware struct {
	root  string
	rmdir func(path string) error
}

// present is a GPU the tree already describes.
type present struct {
	gpu  gpu
	live bool
}

// is reports whether the GPU is live as g describes it.
func (p present) is(g gpu) bool { return p.live && p.gpu == g }

// described reports whether a description has reached the module since it
// loaded.
func (h hardware) described() bool {
	generation, err := h.generation()

	return err == nil && generation != 0
}

// apply makes the tree describe gpus and returns the generation it leaves.
// Every GPU that goes away or changes is unplugged before any is plugged in, so
// two GPUs trading minors never collide, and the generation goes up last, once
// the whole description is in place.
func (h hardware) apply(gpus []gpu) (uint64, error) {
	dir := filepath.Join(h.root, kmod.HardwareGPUsDir)
	have, err := readGPUs(dir)
	if err != nil {
		return 0, err
	}

	unplugged, err := h.unplugStale(dir, have, gpus)
	if err != nil {
		return 0, err
	}

	plugged, err := plugWanted(dir, have, gpus)
	if err != nil {
		return 0, err
	}

	return h.bump(unplugged || plugged)
}

func (h hardware) generation() (uint64, error) {
	value, err := readAttr(filepath.Join(h.root, kmod.HardwareGeneration))
	if err != nil {
		return 0, err
	}

	return strconv.ParseUint(value, 10, 64)
}

// bump tells the loader the description is in place: after a change, and on a
// module no description has reached yet.
func (h hardware) bump(changed bool) (uint64, error) {
	generation, err := h.generation()
	if err != nil {
		return 0, fmt.Errorf("read the generation: %w", err)
	}
	if !changed && generation != 0 {
		return generation, nil
	}

	generation++
	if err := writeAttr(filepath.Join(h.root, kmod.HardwareGeneration), strconv.FormatUint(generation, 10)); err != nil {
		return 0, err
	}

	return generation, nil
}

// unplugStale unplugs every live GPU that goes away or changes, and removes the
// directories of those that go away.
func (h hardware) unplugStale(dir string, have map[string]present, gpus []gpu) (bool, error) {
	want := make(map[string]gpu, len(gpus))
	for _, g := range gpus {
		want[g.BusID] = g
	}

	changed := false
	for busID, p := range have {
		g, wanted := want[busID]
		if wanted && p.is(g) {
			continue
		}
		changed = true

		if p.live {
			if err := writeAttr(filepath.Join(dir, busID, kmod.GPULive), "0"); err != nil {
				return changed, err
			}
		}
		if !wanted {
			if err := h.rmdir(filepath.Join(dir, busID)); err != nil {
				return changed, fmt.Errorf("remove GPU %s: %w", busID, err)
			}
		}
	}

	return changed, nil
}

// plugWanted describes and plugs in every GPU that is not live as described.
func plugWanted(dir string, have map[string]present, gpus []gpu) (bool, error) {
	changed := false
	for _, g := range gpus {
		p, exists := have[g.BusID]
		if exists && p.is(g) {
			continue
		}
		changed = true

		if err := plug(filepath.Join(dir, g.BusID), g, exists && p.gpu == g); err != nil {
			return changed, fmt.Errorf("plug in GPU %s: %w", g.BusID, err)
		}
	}

	return changed, nil
}

// plug creates the GPU's directory if missing, writes its attributes unless
// they already hold g, and switches it live.
func plug(path string, g gpu, described bool) error {
	if err := os.Mkdir(path, 0o755); err != nil && !errors.Is(err, fs.ErrExist) {
		return err
	}

	if !described {
		for _, attr := range []struct{ name, value string }{
			{kmod.GPUUUID, g.UUID},
			{kmod.GPUName, g.Name},
			{kmod.GPUMinor, strconv.Itoa(g.Minor)},
			{kmod.GPUDeviceID, fmt.Sprintf("%04x", g.DeviceID)},
		} {
			if err := writeAttr(filepath.Join(path, attr.name), attr.value); err != nil {
				return err
			}
		}
	}

	return writeAttr(filepath.Join(path, kmod.GPULive), "1")
}

// readGPUs reads the GPUs the tree describes, keyed by PCI location.
func readGPUs(dir string) (map[string]present, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("list GPUs: %w", err)
	}

	have := make(map[string]present, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		p, err := readGPU(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("read GPU %s: %w", entry.Name(), err)
		}
		have[entry.Name()] = p
	}

	return have, nil
}

// readGPU reads one GPU's directory. A minor never written reads as -1, so it
// matches no description.
func readGPU(path string) (present, error) {
	values := make(map[string]string, 5)
	for _, attr := range []string{kmod.GPUUUID, kmod.GPUName, kmod.GPUMinor, kmod.GPUDeviceID, kmod.GPULive} {
		value, err := readAttr(filepath.Join(path, attr))
		if err != nil {
			return present{}, err
		}
		values[attr] = value
	}

	p := present{
		gpu:  gpu{BusID: filepath.Base(path), UUID: values[kmod.GPUUUID], Name: values[kmod.GPUName], Minor: -1},
		live: values[kmod.GPULive] == "1",
	}
	if minor, err := strconv.Atoi(values[kmod.GPUMinor]); err == nil {
		p.gpu.Minor = minor
	}
	if id, err := strconv.ParseUint(values[kmod.GPUDeviceID], 16, 16); err == nil {
		p.gpu.DeviceID = uint16(id)
	}

	return p, nil
}

// readAttr reads a configfs attribute without the newline it ends with.
func readAttr(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}

	return strings.TrimSuffix(string(data), "\n"), nil
}

// writeAttr writes a configfs attribute, which configfs takes in one write.
func writeAttr(path, value string) error {
	return os.WriteFile(path, []byte(value+"\n"), 0o600)
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
