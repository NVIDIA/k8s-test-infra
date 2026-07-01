// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package inject computes the RFC 6902 JSON patch that overlays the mock GPU
// environment onto an arbitrary pod. It is pure (no I/O) so it can be unit
// tested exhaustively; the HTTP server in package main wraps it.
package inject

import (
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
)

const (
	// OverlayVolumeName is the injected hostPath volume AND the idempotency
	// marker: if a pod already has it, Mutate is a no-op.
	OverlayVolumeName = "nvml-mock-overlay"

	// InjectedAnnotation is recorded on mutated pods for debugging.
	InjectedAnnotation = "nvml-mock.nvidia.com/injected"

	// defaultPATH is the conservative system PATH appended after the overlay
	// dir when a container declares no PATH of its own. A container env entry
	// overrides the image's built-in PATH, so injecting only the overlay dir
	// would strip /usr/bin, /bin, etc. and break bare-name entrypoints.
	defaultPATH = "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
)

// Config parameterizes a mutation. Values come from the injector Deployment env
// (wired by Helm).
type Config struct {
	HostPath          string // host source, e.g. /var/lib/nvml-mock
	MountPath         string // in-container path, e.g. /opt/nvml-mock
	EnableIB          bool
	EnablePCI         bool
	OptOutAnnotation  string // e.g. nvml-mock.nvidia.com/inject ("false" opts out)
	DevicesAnnotation string // e.g. nvml-mock.nvidia.com/devices ("true" adds /dev/nvidia*)
	GPUCount          int    // number of nvidiaN device nodes for device opt-in
}

// PatchOp is a single RFC 6902 operation. Value is omitted for removes.
type PatchOp struct {
	Op    string      `json:"op"`
	Path  string      `json:"path"`
	Value interface{} `json:"value,omitempty"`
}

// Mutate returns the patch ops for pod, or nil when the pod should be left
// untouched (opt-out, already injected).
func Mutate(pod *corev1.Pod, cfg Config) ([]PatchOp, error) {
	if cfg.OptOutAnnotation != "" && strings.EqualFold(pod.Annotations[cfg.OptOutAnnotation], "false") {
		return nil, nil
	}
	for _, v := range pod.Spec.Volumes {
		if v.Name == OverlayVolumeName {
			return nil, nil // idempotent
		}
	}

	devices := cfg.DevicesAnnotation != "" &&
		strings.EqualFold(pod.Annotations[cfg.DevicesAnnotation], "true")

	var ops []PatchOp

	// 1. Volumes: overlay + (optional) device nodes.
	volumes := []corev1.Volume{overlayVolume(cfg)}
	if devices {
		volumes = append(volumes, deviceVolumes(cfg)...)
	}
	ops = append(ops, addOrReplace(len(pod.Spec.Volumes) == 0, "/spec/volumes",
		mergeVolumes(pod.Spec.Volumes, volumes)))

	// 2. Per-container edits (containers + initContainers).
	env := buildEnv(cfg)
	mounts := containerMounts(cfg, devices)
	ops = append(ops, containerOps("/spec/containers", pod.Spec.Containers, env, mounts, devices)...)
	ops = append(ops, containerOps("/spec/initContainers", pod.Spec.InitContainers, env, mounts, devices)...)

	// 3. Audit annotation (also documents that injection happened).
	if pod.Annotations == nil {
		ops = append(ops, PatchOp{Op: "add", Path: "/metadata/annotations",
			Value: map[string]string{InjectedAnnotation: "true"}})
	} else {
		ops = append(ops, PatchOp{Op: "add",
			Path:  "/metadata/annotations/" + escapeJSONPointer(InjectedAnnotation),
			Value: "true"})
	}

	return ops, nil
}

func addOrReplace(absent bool, path string, value interface{}) PatchOp {
	if absent {
		return PatchOp{Op: "add", Path: path, Value: value}
	}
	return PatchOp{Op: "replace", Path: path, Value: value}
}

func mergeVolumes(existing, add []corev1.Volume) []corev1.Volume {
	out := make([]corev1.Volume, 0, len(existing)+len(add))
	out = append(out, existing...)
	out = append(out, add...)
	return out
}

func overlayVolume(cfg Config) corev1.Volume {
	hpType := corev1.HostPathDirectoryOrCreate
	return corev1.Volume{
		Name: OverlayVolumeName,
		VolumeSource: corev1.VolumeSource{
			HostPath: &corev1.HostPathVolumeSource{Path: cfg.HostPath, Type: &hpType},
		},
	}
}

func containerMounts(cfg Config, devices bool) []corev1.VolumeMount {
	mounts := []corev1.VolumeMount{{
		Name:      OverlayVolumeName,
		MountPath: cfg.MountPath,
		ReadOnly:  true,
	}}
	if devices {
		mounts = append(mounts, deviceMounts(cfg)...)
	}
	return mounts
}

func containerOps(base string, ctrs []corev1.Container, env []corev1.EnvVar,
	mounts []corev1.VolumeMount, devices bool) []PatchOp {
	var ops []PatchOp
	for i, c := range ctrs {
		ops = append(ops, addOrReplace(len(c.VolumeMounts) == 0,
			fmt.Sprintf("%s/%d/volumeMounts", base, i),
			append(append([]corev1.VolumeMount{}, c.VolumeMounts...), mounts...)))

		ops = append(ops, addOrReplace(len(c.Env) == 0,
			fmt.Sprintf("%s/%d/env", base, i), mergeEnv(c.Env, env)))

		if devices {
			priv := true
			// Deep-copy so we never mutate the caller's pod in place.
			var sc *corev1.SecurityContext
			if c.SecurityContext != nil {
				sc = c.SecurityContext.DeepCopy()
			} else {
				sc = &corev1.SecurityContext{}
			}
			sc.Privileged = &priv
			ops = append(ops, PatchOp{Op: "add",
				Path:  fmt.Sprintf("%s/%d/securityContext", base, i),
				Value: sc})
		}
	}
	return ops
}

// buildEnv returns the mock env vars in a deterministic order.
func buildEnv(cfg Config) []corev1.EnvVar {
	driver := cfg.MountPath + "/driver"
	env := []corev1.EnvVar{
		{Name: "PATH", Value: driver + "/usr/bin"},
		{Name: "LD_LIBRARY_PATH", Value: driver + "/usr/lib64"},
		{Name: "MOCK_NVML_CONFIG", Value: driver + "/config/config.yaml"},
		// Per-node NVLink clique overlay source. The mock engine keys the
		// override on NODE_NAME and reads the cluster topology from this path;
		// it silently no-ops when the file is absent (topology disabled). The
		// host DaemonSet stages topology.yaml here alongside config.yaml so
		// injected pods resolve the same per-node clique as the DaemonSet.
		{Name: "MOCK_TOPOLOGY_CONFIG", Value: driver + "/config/topology.yaml"},
	}
	// Preload the mock GPU driver libraries. LD_LIBRARY_PATH alone is not
	// enough: the stock NVIDIA nvidia-smi probes a fixed set of directories /
	// the ld.so cache to locate libnvidia-ml.so.1 and ignores LD_LIBRARY_PATH,
	// so a library staged under the overlay (not a default/cached path) is
	// never found ("NVIDIA-SMI couldn't find libnvidia-ml.so"). Preloading it
	// (and libcuda for CUDA consumers) makes the mock driver resolvable in any
	// image without touching the container's ld.so cache. Both libs are safe to
	// preload into non-GPU processes (sh, sleep, ...): their constructors are
	// no-ops until an API is called.
	preload := []string{
		driver + "/usr/lib64/libnvidia-ml.so.1",
		driver + "/usr/lib64/libcuda.so.1",
	}
	if cfg.EnableIB {
		preload = append(preload,
			driver+"/usr/local/lib/libibmockumad.so.1",
			driver+"/usr/local/lib/libibmockverbs.so.1",
			driver+"/usr/local/lib/libibmocksys.so.1",
		)
		env = append(env,
			corev1.EnvVar{Name: "MOCK_IB", Value: "full"},
			corev1.EnvVar{Name: "MOCK_IB_ROOT", Value: cfg.MountPath + "/ib"},
			corev1.EnvVar{Name: "MOCK_IB_PING_SOCKET", Value: cfg.MountPath + "/run/mock-ib.sock"},
		)
	}
	if cfg.EnablePCI {
		preload = append(preload, driver+"/usr/local/lib/libpcimocksys.so.1")
		env = append(env, corev1.EnvVar{Name: "MOCK_PCI_ROOT", Value: cfg.MountPath})
	}
	if len(preload) > 0 {
		env = append(env, corev1.EnvVar{Name: "LD_PRELOAD", Value: strings.Join(preload, ":")})
	}
	return env
}

// mergeEnv layers our env onto the container's existing list. PATH/LD_LIBRARY_PATH
// prepend, LD_PRELOAD appends, everything else sets-if-absent. Existing entries
// we don't touch are preserved in place.
func mergeEnv(existing, ours []corev1.EnvVar) []corev1.EnvVar {
	out := append([]corev1.EnvVar{}, existing...)
	idx := map[string]int{}
	for i, e := range out {
		idx[e.Name] = i
	}
	for _, e := range ours {
		i, found := idx[e.Name]
		if !found {
			// The container inherits PATH from the image, which is invisible at
			// admission time and is clobbered by any env entry we add. Append a
			// conservative system default so bare-name executables still resolve.
			if e.Name == "PATH" {
				e.Value = e.Value + ":" + defaultPATH
			}
			out = append(out, e)
			idx[e.Name] = len(out) - 1
			continue
		}
		// Never write .Value onto an entry sourced via ValueFrom: doing so
		// produces an invalid EnvVar (both Value and ValueFrom set) that the
		// API server rejects. Leave such unusual vars untouched.
		if out[i].ValueFrom != nil {
			continue
		}
		switch e.Name {
		case "PATH", "LD_LIBRARY_PATH":
			out[i].Value = e.Value + ":" + out[i].Value
		case "LD_PRELOAD":
			if out[i].Value == "" {
				out[i].Value = e.Value
			} else {
				out[i].Value = out[i].Value + ":" + e.Value
			}
		default:
			// Leave a user-provided MOCK_* value untouched.
		}
	}
	return out
}

func deviceVolumes(cfg Config) []corev1.Volume {
	char := corev1.HostPathCharDev
	dev := cfg.HostPath + "/driver/dev"
	mk := func(name, file string) corev1.Volume {
		return corev1.Volume{
			Name: "nvml-mock-dev-" + name,
			VolumeSource: corev1.VolumeSource{
				HostPath: &corev1.HostPathVolumeSource{Path: dev + "/" + file, Type: &char},
			},
		}
	}
	vols := []corev1.Volume{
		mk("nvidiactl", "nvidiactl"),
		mk("nvidia-uvm", "nvidia-uvm"),
		mk("nvidia-uvm-tools", "nvidia-uvm-tools"),
	}
	for i := 0; i < cfg.GPUCount; i++ {
		vols = append(vols, mk(fmt.Sprintf("nvidia%d", i), fmt.Sprintf("nvidia%d", i)))
	}
	return vols
}

func deviceMounts(cfg Config) []corev1.VolumeMount {
	mk := func(name, file string) corev1.VolumeMount {
		return corev1.VolumeMount{Name: "nvml-mock-dev-" + name, MountPath: "/dev/" + file}
	}
	mounts := []corev1.VolumeMount{
		mk("nvidiactl", "nvidiactl"),
		mk("nvidia-uvm", "nvidia-uvm"),
		mk("nvidia-uvm-tools", "nvidia-uvm-tools"),
	}
	for i := 0; i < cfg.GPUCount; i++ {
		mounts = append(mounts, mk(fmt.Sprintf("nvidia%d", i), fmt.Sprintf("nvidia%d", i)))
	}
	return mounts
}

// escapeJSONPointer encodes '~' and '/' per RFC 6901 for use in a patch path.
func escapeJSONPointer(s string) string {
	s = strings.ReplaceAll(s, "~", "~0")
	s = strings.ReplaceAll(s, "/", "~1")
	return s
}
