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

	"github.com/NVIDIA/k8s-test-infra/internal/fsutil"

	"sigs.k8s.io/yaml"

	"github.com/NVIDIA/k8s-test-infra/internal/agent"
	"github.com/NVIDIA/k8s-test-infra/internal/agent/host"
	"github.com/NVIDIA/k8s-test-infra/pkg/gpu/mocknvml/engine"
)

// The mock's own copies of the two dynamically-linked objects the driver root
// serves. Both are baked into the nvml-mock image, which is also where their
// libc and loader come from, so a closure taken here matches by construction.
const (
	nvidiaSMISource = "/usr/local/bin/nvidia-smi"
	nvmlShimGlob    = "/usr/local/lib/libnvidia-ml.so.*.*.*"
)

// stageCharDevs creates the GPU character devices that ioctl-based callers
// (CUDA, nvidia-smi) open to reach the driver. Without them open() fails.
// Major 195 = nvidia (per-GPU + nvidiactl); major 510 = nvidia-uvm.
func stageCharDevs(ctx context.Context, h *host.Host, state *agent.State) error {
	devRoot := filepath.Join(h.Root, "driver/dev")
	if err := os.MkdirAll(devRoot, 0o755); err != nil {
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
		if err := fsutil.Mknod(filepath.Join(devRoot, d.name), d.major, d.minor); err != nil {
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
	matches, _ := filepath.Glob(nvmlShimGlob)
	if len(matches) == 0 {
		return errors.New("libnvidia-ml.so.*.*.* not found in /usr/local/lib")
	}
	lib64 := filepath.Join(h.Root, "driver/usr/lib64")
	if err := os.MkdirAll(lib64, 0o755); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	soVersioned := "libnvidia-ml.so." + state.Software.DriverVersion
	if err := fsutil.Copy(matches[0], filepath.Join(lib64, soVersioned), 0o755); err != nil {
		return err
	}
	if err := fsutil.Symlink(soVersioned, filepath.Join(lib64, "libnvidia-ml.so.1")); err != nil {
		return err
	}
	return fsutil.Symlink("libnvidia-ml.so.1", filepath.Join(lib64, "libnvidia-ml.so"))
}

// stageCUDAShim installs the mock libcuda so that CUDA workloads can link and
// run without a real driver. Absence is non-fatal — not all profiles need it.
func stageCUDAShim(ctx context.Context, h *host.Host, state *agent.State) error {
	matches, _ := filepath.Glob("/usr/local/lib/libcuda.so.*.*.*")

	if len(matches) == 0 {
		return nil
	}

	lib64 := filepath.Join(h.Root, "driver/usr/lib64")
	if err := os.MkdirAll(lib64, 0o755); err != nil {
		return err
	}

	if err := ctx.Err(); err != nil {
		return err
	}

	soVersioned := "libcuda.so." + state.Software.DriverVersion
	if err := fsutil.Copy(matches[0], filepath.Join(lib64, soVersioned), 0o755); err != nil {
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
		if err := fsutil.Symlink(lnk.target, filepath.Join(lib64, lnk.name)); err != nil {
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
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	dv := state.Software.DriverVersion
	fallback := fmt.Sprintf("#!/bin/sh\necho \"NVIDIA-SMI %s\"\necho \"Driver Version: %s\"\necho \"CUDA Version: 12.4\"\n", dv, dv)
	if err := fsutil.Write(filepath.Join(binDir, "nvidia-smi.sh"), []byte(fallback), 0o755); err != nil {
		return err
	}
	elfPath := filepath.Join(binDir, "nvidia-smi")
	if _, err := os.Stat(nvidiaSMISource); err == nil {
		return fsutil.Copy(nvidiaSMISource, elfPath, 0o755)
	}
	return fsutil.Symlink("nvidia-smi.sh", elfPath)
}

// writeProcFS provides the procfs entries that libnvidia-ml and
// nvidia-container-toolkit read to discover driver version without dlopen.
func writeProcFS(ctx context.Context, h *host.Host, state *agent.State) error {
	procDir := filepath.Join(h.Root, "driver/proc/driver/nvidia")
	if err := os.MkdirAll(procDir, 0o755); err != nil {
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
	if err := fsutil.Write(filepath.Join(procDir, "version"), []byte(version), 0o644); err != nil {
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
	return fsutil.Write(filepath.Join(procDir, "params"), []byte(params), 0o644)
}

// machineTypeRel is the machine type served to containers at
// /etc/nvml-mock/machine-type, alongside config.yaml under the same CDI mount.
const machineTypeRel = "driver/config/machine-type"

// writeMachineType serves the string GFD turns into nvidia.com/gpu.machine,
// pointed at by GFD_MACHINE_TYPE_FILE in the chart's GPU Operator values.
//
// GFD defaults to /sys/class/dmi/id/product_name, which cannot carry a mocked
// value under kind: the node image writes "kind" there and re-binds it into
// every container after the container's own mounts are set up, and on hosts
// without DMI (Docker Desktop) the path does not exist at all (#681). A file of
// our own sidesteps both.
//
// The value is the GPU's product name for want of a platform field in the
// profiles, so gpu.machine reads NVIDIA-GB300-NVL rather than the
// NVIDIA-GB300-NVL72 a real tray reports.
func writeMachineType(ctx context.Context, h *host.Host, state *agent.State) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(state.Devices) == 0 || state.Devices[0].Name == "" {
		return nil
	}
	return fsutil.Write(filepath.Join(h.Root, machineTypeRel),
		[]byte(state.Devices[0].Name+"\n"), 0o644)
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
		if err := fsutil.Write(p, configBytes, 0o644); err != nil {
			return err
		}
	}
	return nil
}
