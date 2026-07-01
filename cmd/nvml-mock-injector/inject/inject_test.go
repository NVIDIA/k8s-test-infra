// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package inject

import (
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
)

func testConfig() Config {
	return Config{
		HostPath:          "/var/lib/nvml-mock",
		MountPath:         "/opt/nvml-mock",
		EnableIB:          true,
		EnablePCI:         true,
		OptOutAnnotation:  "nvml-mock.nvidia.com/inject",
		DevicesAnnotation: "nvml-mock.nvidia.com/devices",
		GPUCount:          2,
	}
}

func opsByPath(ops []PatchOp) map[string]PatchOp {
	m := map[string]PatchOp{}
	for _, o := range ops {
		m[o.Path] = o
	}
	return m
}

func plainPod() *corev1.Pod {
	return &corev1.Pod{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "app"}},
		},
	}
}

func TestMutate_PlainPod_AddsOverlay(t *testing.T) {
	ops, err := Mutate(plainPod(), testConfig())
	require.NoError(t, err)
	by := opsByPath(ops)

	// Overlay volume added (spec.volumes absent -> add whole array).
	vol, ok := by["/spec/volumes"]
	require.True(t, ok)
	require.Equal(t, "add", vol.Op)

	// Container 0 gets a volumeMount and env (both absent -> add).
	_, ok = by["/spec/containers/0/volumeMounts"]
	require.True(t, ok)
	envOp, ok := by["/spec/containers/0/env"]
	require.True(t, ok)
	require.Equal(t, "add", envOp.Op)

	// Idempotency/audit annotation recorded.
	_, ok = by["/metadata/annotations"]
	require.True(t, ok)
}

func TestMutate_MergesExistingEnv(t *testing.T) {
	pod := plainPod()
	pod.Spec.Containers[0].Env = []corev1.EnvVar{
		{Name: "PATH", Value: "/custom/bin"},
		{Name: "LD_PRELOAD", Value: "/x/pre.so"},
		{Name: "FOO", Value: "bar"},
	}
	ops, err := Mutate(pod, testConfig())
	require.NoError(t, err)
	by := opsByPath(ops)

	envOp := by["/spec/containers/0/env"]
	require.Equal(t, "replace", envOp.Op) // env existed -> replace whole array
	env := envOp.Value.([]corev1.EnvVar)
	got := map[string]string{}
	for _, e := range env {
		got[e.Name] = e.Value
	}
	require.Equal(t, "/opt/nvml-mock/driver/usr/bin:/custom/bin", got["PATH"])
	require.Contains(t, got["LD_PRELOAD"], "/x/pre.so")
	require.Contains(t, got["LD_PRELOAD"], "libibmockumad.so.1")
	require.Contains(t, got["LD_PRELOAD"], "libpcimocksys.so.1")
	require.Equal(t, "bar", got["FOO"]) // untouched entries preserved
	require.Equal(t, "/opt/nvml-mock", got["MOCK_PCI_ROOT"])
}

func TestMutate_OptOut(t *testing.T) {
	pod := plainPod()
	pod.Annotations = map[string]string{"nvml-mock.nvidia.com/inject": "false"}
	ops, err := Mutate(pod, testConfig())
	require.NoError(t, err)
	require.Empty(t, ops)
}

func TestMutate_Idempotent(t *testing.T) {
	pod := plainPod()
	pod.Spec.Volumes = []corev1.Volume{{Name: OverlayVolumeName}}
	ops, err := Mutate(pod, testConfig())
	require.NoError(t, err)
	require.Empty(t, ops)
}

func TestMutate_DeviceOptIn(t *testing.T) {
	pod := plainPod()
	pod.Annotations = map[string]string{"nvml-mock.nvidia.com/devices": "true"}
	ops, err := Mutate(pod, testConfig())
	require.NoError(t, err)
	by := opsByPath(ops)

	sc, ok := by["/spec/containers/0/securityContext"]
	require.True(t, ok)
	priv := sc.Value.(*corev1.SecurityContext)
	require.NotNil(t, priv.Privileged)
	require.True(t, *priv.Privileged)

	// GPUCount=2 -> nvidia0, nvidia1 + nvidiactl + uvm(+tools) volumes present.
	vols := by["/spec/volumes"].Value.([]corev1.Volume)
	names := map[string]bool{}
	for _, v := range vols {
		names[v.Name] = true
	}
	require.True(t, names["nvml-mock-dev-nvidia0"])
	require.True(t, names["nvml-mock-dev-nvidia1"])
	require.True(t, names["nvml-mock-dev-nvidiactl"])
}

func TestMutate_InitContainersToo(t *testing.T) {
	pod := plainPod()
	pod.Spec.InitContainers = []corev1.Container{{Name: "init"}}
	ops, err := Mutate(pod, testConfig())
	require.NoError(t, err)
	by := opsByPath(ops)
	_, ok := by["/spec/initContainers/0/env"]
	require.True(t, ok)
}
