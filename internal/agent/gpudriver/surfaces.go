// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package gpudriver

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/unix"

	"github.com/NVIDIA/k8s-test-infra/internal/agent"
	"github.com/NVIDIA/k8s-test-infra/internal/agent/host"
)

// stageCharDevs creates GPU character devices under driverRoot/dev/.
// Major 195 = nvidia (per-GPU + nvidiactl); major 510 = nvidia-uvm.
func stageCharDevs(_ context.Context, driverRoot string, state *agent.State) error {
	devRoot := filepath.Join(driverRoot, "dev")
	if err := os.MkdirAll(devRoot, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", devRoot, err)
	}
	for _, dev := range state.Devices {
		path := filepath.Join(devRoot, fmt.Sprintf("nvidia%d", dev.Index))
		if err := mknodChar(path, 195, uint32(dev.Index)); err != nil {
			return fmt.Errorf("chardev nvidia%d: %w", dev.Index, err)
		}
	}
	if err := mknodChar(filepath.Join(devRoot, "nvidiactl"), 195, 255); err != nil {
		return fmt.Errorf("chardev nvidiactl: %w", err)
	}
	if err := mknodChar(filepath.Join(devRoot, "nvidia-uvm"), 510, 0); err != nil {
		return fmt.Errorf("chardev nvidia-uvm: %w", err)
	}
	if err := mknodChar(filepath.Join(devRoot, "nvidia-uvm-tools"), 510, 1); err != nil {
		return fmt.Errorf("chardev nvidia-uvm-tools: %w", err)
	}
	return nil
}

// mknodChar creates a character device; EEXIST is treated as success (idempotent).
func mknodChar(path string, major, minor uint32) error {
	//nolint:gosec // Mknod requires the cast; values are controlled constants
	dev := int(unix.Mkdev(major, minor))
	err := unix.Mknod(path, uint32(syscall.S_IFCHR)|0o666, dev)
	if err != nil && !errors.Is(err, unix.EEXIST) {
		return fmt.Errorf("mknod %s: %w", path, err)
	}
	return nil
}

// stageNVMLShim copies the mock NVML library and creates the versioned symlinks.
func stageNVMLShim(_ context.Context, driverRoot string, sw agent.SoftwareVersions) error {
	matches, _ := filepath.Glob("/usr/local/lib/libnvidia-ml.so.*.*.*")
	if len(matches) == 0 {
		return fmt.Errorf("libnvidia-ml.so.*.*.* not found in /usr/local/lib")
	}
	lib64 := filepath.Join(driverRoot, "usr/lib64")
	if err := os.MkdirAll(lib64, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", lib64, err)
	}
	soVersioned := "libnvidia-ml.so." + sw.DriverVersion
	if err := copyFile(matches[0], filepath.Join(lib64, soVersioned), 0o755); err != nil {
		return fmt.Errorf("copy nvml shim: %w", err)
	}
	if err := symlinkIn(lib64, "libnvidia-ml.so.1", soVersioned); err != nil {
		return err
	}
	return symlinkIn(lib64, "libnvidia-ml.so", "libnvidia-ml.so.1")
}

// stageCUDAShim copies the mock CUDA library and creates the versioned symlinks.
func stageCUDAShim(_ context.Context, driverRoot string, sw agent.SoftwareVersions) error {
	matches, _ := filepath.Glob("/usr/local/lib/libcuda.so.*.*.*")
	if len(matches) == 0 {
		// Not fatal: CUDA shim is optional (some environments skip it).
		return nil
	}
	lib64 := filepath.Join(driverRoot, "usr/lib64")
	if err := os.MkdirAll(lib64, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", lib64, err)
	}
	soVersioned := "libcuda.so." + sw.DriverVersion
	if err := copyFile(matches[0], filepath.Join(lib64, soVersioned), 0o755); err != nil {
		return fmt.Errorf("copy cuda shim: %w", err)
	}
	for _, link := range []struct{ name, target string }{
		{"libcuda.so.1", soVersioned},
		{"libcuda.so", "libcuda.so.1"},
		// The mock exports CUDA Runtime API symbols under libcuda.so; vectorAdd
		// and similar samples expect libcudart.so, so create compatibility links.
		{"libcudart.so.12", "libcuda.so.1"},
		{"libcudart.so", "libcudart.so.12"},
	} {
		if err := symlinkIn(lib64, link.name, link.target); err != nil {
			return err
		}
	}
	return nil
}

// stageNvidiaSMI installs the nvidia-smi ELF (if present) and a shell fallback.
func stageNvidiaSMI(_ context.Context, driverRoot string, sw agent.SoftwareVersions) error {
	binDir := filepath.Join(driverRoot, "usr/bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", binDir, err)
	}

	const smiSh = `#!/bin/sh
echo "NVIDIA-SMI %s"
echo "Driver Version: %s"
echo "CUDA Version: 12.4"
`
	fallback := fmt.Sprintf(smiSh, sw.DriverVersion, sw.DriverVersion)
	shPath := filepath.Join(binDir, "nvidia-smi.sh")
	if err := os.WriteFile(shPath, []byte(fallback), 0o755); err != nil {
		return fmt.Errorf("write nvidia-smi.sh: %w", err)
	}

	elfPath := filepath.Join(binDir, "nvidia-smi")
	if _, err := os.Stat("/usr/local/bin/nvidia-smi"); err == nil {
		if err := copyFile("/usr/local/bin/nvidia-smi", elfPath, 0o755); err != nil {
			return fmt.Errorf("copy nvidia-smi: %w", err)
		}
	} else {
		// ELF unavailable; fall back to the shell script.
		if err := symlinkIn(binDir, "nvidia-smi", "nvidia-smi.sh"); err != nil {
			return err
		}
	}
	return nil
}

// writeProcVersion writes the /proc/driver/nvidia/version mock entry.
func writeProcVersion(_ context.Context, driverRoot string, sw agent.SoftwareVersions) error {
	procDir := filepath.Join(driverRoot, "proc/driver/nvidia")
	if err := os.MkdirAll(procDir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", procDir, err)
	}
	content := fmt.Sprintf(
		"NVRM version: NVIDIA UNIX x86_64 Kernel Module  %s  Thu Feb 20 23:41:34 UTC 2026\n"+
			"GCC version:  gcc version 12.2.0 (Debian 12.2.0-14)\n",
		sw.DriverVersion,
	)
	path := filepath.Join(procDir, "version")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// writeProcParams writes the /proc/driver/nvidia/params mock entry.
func writeProcParams(_ context.Context, driverRoot string) error {
	procDir := filepath.Join(driverRoot, "proc/driver/nvidia")
	if err := os.MkdirAll(procDir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", procDir, err)
	}
	const params = "EnableMSI: 1\n" +
		"NVreg_RegistryDwords:\n" +
		"NVreg_DeviceFileGID: 0\n" +
		"NVreg_DeviceFileMode: 438\n" +
		"NVreg_DeviceFileUID: 0\n" +
		"NVreg_ModifyDeviceFiles: 1\n" +
		"NVreg_PreserveVideoMemoryAllocations: 0\n" +
		"NVreg_EnableResizableBar: 0\n"
	path := filepath.Join(procDir, "params")
	if err := os.WriteFile(path, []byte(params), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// writeEngineConfig writes state.ConfigRaw to both engine config locations:
//   - h.Root/config/config.yaml  (device-plugin / canonical)
//   - h.Root/driver/config/config.yaml (auto-discovered by .so via /proc/self/maps)
func writeEngineConfig(_ context.Context, h *host.Host, state *agent.State) error {
	if len(state.ConfigRaw) == 0 {
		return fmt.Errorf("state.ConfigRaw is empty; FileSource must populate it")
	}
	paths := []string{
		filepath.Join(h.Root, "config", "config.yaml"),
		filepath.Join(h.Root, "driver", "config", "config.yaml"),
	}
	for _, p := range paths {
		if err := h.WriteFile(p, state.ConfigRaw, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// copyFile copies src to dst with mode perm, overwriting dst if it exists.
func copyFile(src, dst string, perm os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open %s: %w", src, err)
	}
	defer in.Close()

	tmp := dst + ".tmp"
	out, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return fmt.Errorf("create %s: %w", tmp, err)
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return fmt.Errorf("copy %s -> %s: %w", src, dst, err)
	}
	if err := out.Close(); err != nil {
		return fmt.Errorf("close %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, dst); err != nil {
		return fmt.Errorf("rename %s -> %s: %w", tmp, dst, err)
	}
	return nil
}

// symlinkIn creates or replaces a symlink named linkName inside dir pointing to target.
func symlinkIn(dir, linkName, target string) error {
	path := filepath.Join(dir, linkName)
	_ = os.Remove(path) // idempotent
	if err := os.Symlink(target, path); err != nil {
		return fmt.Errorf("symlink %s -> %s: %w", path, target, err)
	}
	return nil
}
