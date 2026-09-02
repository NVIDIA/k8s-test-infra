// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package nri

import (
	"fmt"
	"log/slog"
	"os"
	"strings"
	"syscall"

	"github.com/containerd/nri/pkg/api"

	"github.com/NVIDIA/k8s-test-infra/internal/nri/inject"
)

// containerFromNRI reads the runtime's view of a container into the form the
// injection steps consume. Both arguments may be nil: NRI delivers either as
// nil for minimal containers, and a panic in CreateContainer takes the plugin
// down and stops injection node-wide.
func containerFromNRI(pod *api.PodSandbox, container *api.Container) inject.Container {
	result := inject.Container{}

	if pod != nil {
		result.Namespace = pod.GetNamespace()
		result.PodAnnotations = pod.GetAnnotations()
	}
	if container == nil {
		return result
	}

	// The slices are copied because NRI owns them; writing through them would
	// corrupt the runtime's own view of the container.
	result.Env = append([]string(nil), container.GetEnv()...)
	for _, mount := range container.GetMounts() {
		result.Mounts = append(result.Mounts, inject.Mount{
			Source:      mount.GetSource(),
			Destination: mount.GetDestination(),
			Type:        mount.GetType(),
			Options:     append([]string(nil), mount.GetOptions()...),
		})
	}
	// What the runtime already applied, so the steps can tell whether the device
	// plugin served this container. GetLinux() is nil-safe.
	for _, device := range container.GetLinux().GetDevices() {
		result.Devices = append(result.Devices, inject.Device{Path: device.GetPath()})
	}
	for _, device := range container.GetCDIDevices() {
		result.CDIDevices = append(result.CDIDevices, device.GetName())
	}

	return result
}

// adjustmentToNRI renders the decision as the runtime's adjustment type.
//
// Devices fail open individually rather than as a batch, which is why this
// cannot fail as a whole: one device node that vanished or was never staged
// must not fail creation of the entire container.
func adjustmentToNRI(adjustment inject.Adjustment) *api.ContainerAdjustment {
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
			// An entry with no separator would become a KeyValue with an empty
			// key, claiming NRI ownership of a variable that does not exist.
			continue
		}
		result.AddEnv(key, value)
	}
	for _, device := range adjustment.Devices {
		nriDevice, err := linuxDevice(device)
		if err != nil {
			slog.Warn("skipping device", "path", device.Path, "err", err)
			continue
		}
		result.AddDevice(nriDevice)
	}
	// CDI references are resolved by the runtime from the spec the cdi simulator
	// writes, so there is nothing to stat here — an unresolvable name fails
	// container creation, which is why the steps only emit one once they have
	// seen the spec.
	for _, name := range adjustment.CDIDevices {
		result.AddCDIDevice(&api.CDIDevice{Name: name})
	}

	return result
}

// linuxDevice reads the device numbers containerd needs to recreate the node
// inside the container. A path that is not a character device is an error
// rather than a best guess: the runtime would fail the container anyway, and a
// zeroed Stat_t would silently describe device 0:0.
func linuxDevice(device inject.Device) (*api.LinuxDevice, error) {
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
		Major:    int64(major(uint64(stat.Rdev))), //nolint:unconvert // stat.Rdev is uint64 on Linux (no-op) but int32 on Darwin — the cast is required for local macOS builds
		Minor:    int64(minor(uint64(stat.Rdev))), //nolint:unconvert // stat.Rdev is uint64 on Linux (no-op) but int32 on Darwin — the cast is required for local macOS builds
		FileMode: api.FileMode(os.FileMode(stat.Mode) & os.ModePerm),
		Uid:      api.UInt32(stat.Uid),
		Gid:      api.UInt32(stat.Gid),
	}, nil
}
