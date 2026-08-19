//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package pod

import (
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/yaml"

	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/kube"
)

func decode(t *testing.T, manifest []byte) corev1.Pod {
	t.Helper()
	var decoded corev1.Pod
	require.NoError(t, yaml.UnmarshalStrict(manifest, &decoded), "manifest:\n%s", manifest)
	return decoded
}

func TestRenderMinimalSpec(t *testing.T) {
	rendered := decode(t, Spec{Name: "probe", Image: "busybox:1.36"}.Render())

	require.Equal(t, "Pod", rendered.Kind)
	require.Equal(t, "v1", rendered.APIVersion)
	require.Equal(t, "probe", rendered.Name)
	require.Empty(t, rendered.Namespace, "an unset namespace must defer to the kubectl context")
	require.Equal(t, corev1.RestartPolicyNever, rendered.Spec.RestartPolicy)
	require.Empty(t, rendered.Spec.NodeName)
	require.Empty(t, rendered.Spec.NodeSelector)

	require.Len(t, rendered.Spec.Containers, 1)
	require.Equal(t, DefaultContainerName, rendered.Spec.Containers[0].Name)
	require.Equal(t, "busybox:1.36", rendered.Spec.Containers[0].Image)
	require.Empty(t, rendered.Spec.Containers[0].Resources.Limits, "no GPUs means no extended-resource limit")
}

// An uncapped grace period stalls `kubectl delete` for 30s per pod, so the
// default must survive a caller that says nothing about termination.
func TestRenderCapsGracePeriodByDefault(t *testing.T) {
	rendered := decode(t, Spec{Name: "probe", Image: "busybox:1.36"}.Render())

	require.NotNil(t, rendered.Spec.TerminationGracePeriodSeconds)
	require.Equal(t, DefaultGracePeriodSeconds, *rendered.Spec.TerminationGracePeriodSeconds)
	require.LessOrEqual(t, DefaultGracePeriodSeconds, int64(1))
}

func TestRenderHonoursGracePeriodOverride(t *testing.T) {
	override := int64(45)
	rendered := decode(t, Spec{
		Name:               "probe",
		Image:              "busybox:1.36",
		GracePeriodSeconds: &override,
	}.Render())

	require.NotNil(t, rendered.Spec.TerminationGracePeriodSeconds)
	require.Equal(t, override, *rendered.Spec.TerminationGracePeriodSeconds)
}

func TestRenderFullSpec(t *testing.T) {
	rendered := decode(t, Spec{
		Name:          "probe",
		Namespace:     "workloads",
		ContainerName: "test",
		Image:         "busybox:1.36",
		Command:       []string{"sh", "-c", "sleep 5"},
		NodeName:      "worker-1",
		NodeSelector:  map[string]string{"nvidia.com/gpu.present": "true"},
		Labels:        map[string]string{"app": "probe"},
		Annotations:   map[string]string{"nvml-mock.nvidia.com/devices": "true"},
		GPUs:          8,
	}.Render())

	require.Equal(t, "workloads", rendered.Namespace)
	require.Equal(t, map[string]string{"app": "probe"}, rendered.Labels)
	require.Equal(t, map[string]string{"nvml-mock.nvidia.com/devices": "true"}, rendered.Annotations)
	require.Equal(t, "worker-1", rendered.Spec.NodeName)
	require.Equal(t, map[string]string{"nvidia.com/gpu.present": "true"}, rendered.Spec.NodeSelector)

	container := rendered.Spec.Containers[0]
	require.Equal(t, "test", container.Name)
	require.Equal(t, []string{"sh", "-c", "sleep 5"}, container.Command)
	gpus := container.Resources.Limits[kube.GPUResourceName]
	require.Equal(t, "8", gpus.String())
}

// Annotation and label values are strings. Rendered as a bare `true` they would
// decode as a bool and the API server would reject the pod, which is the class
// of bug hand-written YAML invites and typed marshalling forecloses.
func TestRenderQuotesBooleanLikeValues(t *testing.T) {
	manifest := Spec{
		Name:        "probe",
		Image:       "busybox:1.36",
		Annotations: map[string]string{"nvml-mock.nvidia.com/devices": "true"},
		Labels:      map[string]string{"enabled": "false"},
	}.Render()

	require.Contains(t, string(manifest), `nvml-mock.nvidia.com/devices: "true"`)
	require.Contains(t, string(manifest), `enabled: "false"`)
}
