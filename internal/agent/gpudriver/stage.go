// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package gpudriver

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"sigs.k8s.io/yaml"

	"github.com/NVIDIA/k8s-test-infra/internal/agent"
	"github.com/NVIDIA/k8s-test-infra/internal/agent/host"
	"github.com/NVIDIA/k8s-test-infra/pkg/gpu/mocknvml/engine"
)

// stageCharDevs creates the GPU character devices that ioctl-based callers
// (CUDA, nvidia-smi) open to reach the driver. Without them open() fails.
// Major 195 = nvidia (per-GPU + nvidiactl); major 510 = nvidia-uvm.
func stageCharDevs(ctx context.Context, h *host.Host, state *agent.State) error {
	devRoot := filepath.Join(h.Root, "driver/dev")
	if err := h.MkdirAll(devRoot, 0o755); err != nil {
		return err
	}

	type charDev struct {
		name         string
		major, minor uint32
	}
	devs := make([]charDev, 0, len(state.Devices)+3)
	for _, d := range state.Devices {
		devs = append(devs, charDev{fmt.Sprintf("nvidia%d", d.Index), 195, uint32(d.Index)})
	}
	devs = append(devs,
		charDev{"nvidiactl", 195, 255},
		charDev{"nvidia-uvm", 510, 0},
		charDev{"nvidia-uvm-tools", 510, 1},
	)

	wanted := make(map[string]bool, len(devs))
	for _, d := range devs {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := h.Mknod(filepath.Join(devRoot, d.name), d.major, d.minor); err != nil {
			return fmt.Errorf("chardev %s: %w", d.name, err)
		}
		wanted[d.name] = true
	}

	return pruneGPUNodes(devRoot, wanted)
}

// gpuNodeName matches only the per-GPU character devices. Scoped this tightly
// because setup.sh owns other nvidia-prefixed entries in the same directory,
// notably the nvidia-caps-imex-channels/ tree.
var gpuNodeName = regexp.MustCompile(`^nvidia[0-9]+$`)

// pruneGPUNodes removes per-GPU nodes left by a larger previous device set.
// Staging alone is additive, so shrinking the GPU count would otherwise leave
// openable /dev/nvidiaN that NVML no longer reports.
func pruneGPUNodes(devRoot string, wanted map[string]bool) error {
	entries, err := os.ReadDir(devRoot)
	if err != nil {
		return fmt.Errorf("read %s: %w", devRoot, err)
	}

	var errs []error
	for _, e := range entries {
		if wanted[e.Name()] || !gpuNodeName.MatchString(e.Name()) {
			continue
		}

		p := filepath.Join(devRoot, e.Name())
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			errs = append(errs, fmt.Errorf("remove %s: %w", p, err))
		}
	}

	return errors.Join(errs...)
}

// stageNVMLShim installs the mock libnvidia-ml so that nvidia-smi, the device
// plugin, and NVML-using workloads can dlopen it without a real kernel driver.
func stageNVMLShim(ctx context.Context, h *host.Host, state *agent.State) error {
	matches, _ := filepath.Glob("/usr/local/lib/libnvidia-ml.so.*.*.*")
	if len(matches) == 0 {
		return errors.New("libnvidia-ml.so.*.*.* not found in /usr/local/lib")
	}
	lib64 := filepath.Join(h.Root, "driver/usr/lib64")
	if err := h.MkdirAll(lib64, 0o755); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	soVersioned := "libnvidia-ml.so." + state.Software.DriverVersion
	if err := h.CopyFile(matches[0], filepath.Join(lib64, soVersioned), 0o755); err != nil {
		return err
	}
	if err := h.Symlink(soVersioned, filepath.Join(lib64, "libnvidia-ml.so.1")); err != nil {
		return err
	}
	return h.Symlink("libnvidia-ml.so.1", filepath.Join(lib64, "libnvidia-ml.so"))
}

// stageCUDAShim installs the mock libcuda so that CUDA workloads can link and
// run without a real driver. Absence is non-fatal — not all profiles need it.
func stageCUDAShim(ctx context.Context, h *host.Host, state *agent.State) error {
	matches, _ := filepath.Glob("/usr/local/lib/libcuda.so.*.*.*")

	if len(matches) == 0 {
		return nil
	}

	lib64 := filepath.Join(h.Root, "driver/usr/lib64")
	if err := h.MkdirAll(lib64, 0o755); err != nil {
		return err
	}

	if err := ctx.Err(); err != nil {
		return err
	}

	soVersioned := "libcuda.so." + state.Software.DriverVersion
	if err := h.CopyFile(matches[0], filepath.Join(lib64, soVersioned), 0o755); err != nil {
		return err
	}

	// The mock exports CUDA Runtime API symbols under libcuda.so; vectorAdd and
	// similar samples link against libcudart.so, so create compatibility links.
	type symlink struct{ name, target string }

	for _, lnk := range []symlink{
		{"libcuda.so.1", soVersioned},
		{"libcuda.so", "libcuda.so.1"},
		{"libcudart.so.12", "libcuda.so.1"},
		{"libcudart.so", "libcudart.so.12"},
	} {
		if err := h.Symlink(lnk.target, filepath.Join(lib64, lnk.name)); err != nil {
			return err
		}
	}

	return nil
}

// stageNvidiaSMI satisfies tooling (GPU Operator validator, health checks) that
// exec nvidia-smi to confirm driver presence. The ELF uses the NVML shim via
// RPATH; the shell fallback covers environments where the ELF is unavailable.
func stageNvidiaSMI(ctx context.Context, h *host.Host, state *agent.State) error {
	binDir := filepath.Join(h.Root, "driver/usr/bin")
	if err := h.MkdirAll(binDir, 0o755); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	dv := state.Software.DriverVersion
	fallback := fmt.Sprintf("#!/bin/sh\necho \"NVIDIA-SMI %s\"\necho \"Driver Version: %s\"\necho \"CUDA Version: 12.4\"\n", dv, dv)
	if err := h.WriteFile(filepath.Join(binDir, "nvidia-smi.sh"), []byte(fallback), 0o755); err != nil {
		return err
	}
	elfPath := filepath.Join(binDir, "nvidia-smi")
	if _, err := os.Stat("/usr/local/bin/nvidia-smi"); err == nil {
		return h.CopyFile("/usr/local/bin/nvidia-smi", elfPath, 0o755)
	}
	return h.Symlink("nvidia-smi.sh", elfPath)
}

// writeProcFS provides the procfs entries that libnvidia-ml and
// nvidia-container-toolkit read to discover driver version without dlopen.
func writeProcFS(ctx context.Context, h *host.Host, state *agent.State) error {
	procDir := filepath.Join(h.Root, "driver/proc/driver/nvidia")
	if err := h.MkdirAll(procDir, 0o755); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	version := fmt.Sprintf(
		"NVRM version: NVIDIA UNIX x86_64 Kernel Module  %s  Thu Feb 20 23:41:34 UTC 2026\n"+
			"GCC version:  gcc version 12.2.0 (Debian 12.2.0-14)\n",
		state.Software.DriverVersion,
	)
	if err := h.WriteFile(filepath.Join(procDir, "version"), []byte(version), 0o644); err != nil {
		return err
	}
	const params = "EnableMSI: 1\n" +
		"NVreg_RegistryDwords:\n" +
		"NVreg_DeviceFileGID: 0\n" +
		"NVreg_DeviceFileMode: 438\n" +
		"NVreg_DeviceFileUID: 0\n" +
		"NVreg_ModifyDeviceFiles: 1\n" +
		"NVreg_PreserveVideoMemoryAllocations: 0\n" +
		"NVreg_EnableResizableBar: 0\n"
	return h.WriteFile(filepath.Join(procDir, "params"), []byte(params), 0o644)
}

// writeEngineConfig writes the GPU profile so the mock NVML shim knows how many
// GPUs to expose and their properties at dlopen time.
// Written to two locations: h.Root/config/ (device-plugin) and
// h.Root/driver/config/ (auto-discovered by the .so via /proc/self/maps).
//
// system.num_devices is stamped to len(state.Devices) so the shim caps at the
// runtime GPU count even when the profile lists more devices than are active.
func writeEngineConfig(ctx context.Context, h *host.Host, state *agent.State) error {
	if len(state.ConfigRaw) == 0 {
		return errors.New("state.ConfigRaw is empty; FileSource must populate it")
	}
	var cfg engine.YAMLConfig
	if err := yaml.Unmarshal(state.ConfigRaw, &cfg); err != nil {
		return fmt.Errorf("parse engine config: %w", err)
	}
	// TODO: replace with Runtime.System.NumDevices once the Profile/Runtime split lands.
	cfg.System.NumDevices = len(state.Devices)
	configBytes, err := yaml.Marshal(&cfg)
	if err != nil {
		return fmt.Errorf("marshal engine config: %w", err)
	}
	for _, p := range []string{
		filepath.Join(h.Root, "config/config.yaml"),
		filepath.Join(h.Root, "driver/config/config.yaml"),
	} {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := h.WriteFile(p, configBytes, 0o644); err != nil {
			return err
		}
	}
	return nil
}
