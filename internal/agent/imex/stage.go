// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package imex

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"go.uber.org/zap"
	"golang.org/x/sys/unix"

	"github.com/NVIDIA/k8s-test-infra/internal/fsutil"

	"github.com/NVIDIA/k8s-test-infra/internal/agent"
	"github.com/NVIDIA/k8s-test-infra/internal/agent/host"
	"github.com/NVIDIA/k8s-test-infra/internal/imex"
)

// stageKernelSurface stages what the NVIDIA kernel module provides on a real
// node: channel0, the /proc/devices substitute and the fabric-imex-mgmt
// capability file. A configured major that the node's kernel already gives to
// another driver is replaced by a free one, so the mock never shadows a real
// device; channel0 and the substitute always carry the same majors.
func stageKernelSurface(h *host.Host, state *agent.State, procDevicesPath string) error {
	src, err := os.ReadFile(procDevicesPath) //nolint:gosec // path is validated by caller
	if err != nil {
		return fmt.Errorf("read %s: %w", procDevicesPath, err)
	}
	imexMajor, capsMajor, err := imex.Majors(string(src), state.IMEX.IMEXMajor, state.IMEX.CapsMajor)
	if err != nil {
		return err
	}
	if imexMajor != state.IMEX.IMEXMajor || capsMajor != state.IMEX.CapsMajor {
		zap.L().Warn("configured IMEX device majors are taken on this node; staging free ones",
			zap.String("simulator", name),
			zap.Int("channelMajor", state.IMEX.IMEXMajor), zap.Int("capsMajor", state.IMEX.CapsMajor),
			zap.Int("stagedChannelMajor", imexMajor), zap.Int("stagedCapsMajor", capsMajor))
	}

	if err := stageChannelDevs(h, imexMajor); err != nil {
		return fmt.Errorf("imex channel devs: %w", err)
	}
	if err := stageProcDevices(h, string(src), imexMajor, capsMajor); err != nil {
		return fmt.Errorf("imex proc-devices: %w", err)
	}
	if err := stageFabricImexMgmt(h); err != nil {
		return fmt.Errorf("imex fabric-imex-mgmt: %w", err)
	}
	return nil
}

// stageChannelDevs stages the IMEX channel files a real node has.
//
// A real node has only the IMEX channel files that someone created: an admin
// with mknod, or the NVIDIA driver, which creates channel0 at load time when
// the nvidia module is loaded with NVreg_CreateImexChannel0=1. The driver
// creates no other channels on its own. Stage only channel0, as that driver
// option does, so a container that opts into channels through the NRI
// annotation starts without creating thousands of device nodes. DRA does not
// read these files: its CDI spec carries the device numbers, and the runtime
// creates channel0, or every channel for allocationMode All, inside the
// container.
func stageChannelDevs(h *host.Host, imexMajor int) error {
	dir := h.RootPath(imex.ChannelsDir)

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	major := uint32(imexMajor) //nolint:gosec // bounded by imex.Majors
	channel0 := filepath.Join(dir, "channel0")
	// Mknod keeps a node that already exists, so a channel0 staged with a major
	// the host has since taken is replaced, to match the /proc/devices substitute.
	if err := removeOtherDevice(channel0, unix.Mkdev(major, 0)); err != nil {
		return err
	}
	if err := fsutil.Mknod(channel0, major, 0); err != nil {
		return err
	}

	// NRI injects every file here, so drop channels an earlier version staged.
	return fsutil.PruneDir(dir, func(name string) bool { return name == "channel0" })
}

// removeOtherDevice removes path unless it is the character device dev.
func removeOtherDevice(path string, dev uint64) error {
	var st unix.Stat_t
	err := unix.Lstat(path, &st)
	switch {
	case errors.Is(err, unix.ENOENT):
		return nil
	case err != nil:
		return fmt.Errorf("stat %s: %w", path, err)
	case st.Mode&unix.S_IFMT == unix.S_IFCHR && uint64(st.Rdev) == dev: //nolint:unconvert // stat.Rdev is uint64 on Linux (no-op) but int32 on Darwin — the cast is required for local macOS builds
		return nil
	}
	return fsutil.Remove(path)
}

// stageProcDevices synthesises the substitute /proc/devices the DRA compute-domain
// plugin reads via ALT_PROC_DEVICES_PATH — without the entry the plugin aborts at startup.
// src is the node's /proc/devices.
func stageProcDevices(h *host.Host, src string, imexMajor, capsMajor int) error {
	rendered, err := imex.ProcDevices(src, imexMajor, capsMajor)

	if err != nil {
		return fmt.Errorf("render proc-devices: %w", err)
	}

	return fsutil.Write(h.RootPath(imex.ProcDevicesPath), []byte(rendered), 0o644)
}

// stageFabricImexMgmt writes the capability file the DRA plugin reads alongside
// proc-devices to discover the fabric IMEX management device minor.
func stageFabricImexMgmt(h *host.Host) error {
	content := `DeviceFileMinor: 512
DeviceFileMode: 438
DeviceFileModify: 0
`
	p := h.RootPath(imex.FabricIMEXMgmtPath)
	return fsutil.Write(p, []byte(content), 0o644)
}
