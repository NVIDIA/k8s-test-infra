//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package pod

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/yaml"

	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/kube"
)

// decode reads a manifest the way a Go caller would. Strict, so a field the
// template indents into the wrong parent is caught rather than silently ignored.
func decode(t *testing.T, manifest []byte) corev1.Pod {
	t.Helper()
	var pod corev1.Pod
	require.NoError(t, yaml.UnmarshalStrict(manifest, &pod), "manifest:\n%s", manifest)
	return pod
}

// decodeAsAPIServer reads a manifest the way the API server does: YAML converted
// to JSON with no knowledge of the target type, then a strict typed unmarshal.
// This is deliberately not sigs.k8s.io/yaml.Unmarshal, which coerces scalars
// using the target's reflected type and so accepts unquoted values the server
// would reject.
func decodeAsAPIServer(t *testing.T, manifest []byte) (corev1.Pod, error) {
	t.Helper()
	asJSON, err := yaml.YAMLToJSON(manifest)
	require.NoError(t, err, "manifest:\n%s", manifest)

	var pod corev1.Pod
	decoder := json.NewDecoder(bytes.NewReader(asJSON))
	decoder.DisallowUnknownFields()
	return pod, decoder.Decode(&pod)
}

func TestRenderIsValidPodFromMinimalSpec(t *testing.T) {
	manifest := Spec{Name: "probe", Namespace: "workloads", Image: "busybox:1.36"}.Render()
	rendered := decode(t, manifest)

	require.Equal(t, "v1", rendered.APIVersion)
	require.Equal(t, "Pod", rendered.Kind)
	require.Equal(t, "probe", rendered.Name)
	require.Equal(t, "workloads", rendered.Namespace)
	require.Len(t, rendered.Spec.Containers, 1)
	require.Equal(t, "busybox:1.36", rendered.Spec.Containers[0].Image)

	_, err := decodeAsAPIServer(t, manifest)
	require.NoError(t, err, "the API server must accept the minimal pod")
}

// The defaults are what let a caller state only what varies for its pod.
//
// Asserted as literals rather than against the package constants: comparing the
// rendered value to the constant that produced it is tautological, and would
// stay green if someone changed a default to something the suite cannot use.
func TestRenderAppliesDefaults(t *testing.T) {
	rendered := decode(t, Spec{Name: "probe", Image: "busybox:1.36"}.Render())

	require.Equal(t, "app", rendered.Spec.Containers[0].Name)
	require.Equal(t, corev1.RestartPolicyNever, rendered.Spec.RestartPolicy)
	require.NotNil(t, rendered.Spec.TerminationGracePeriodSeconds)
	require.Equal(t, int64(1), *rendered.Spec.TerminationGracePeriodSeconds)

	// The exported constants must describe what is actually rendered, since
	// callers read them to decide whether they need an override at all.
	require.Equal(t, "app", DefaultContainerName)
	require.Equal(t, "Never", DefaultRestartPolicy)
	require.Equal(t, 1, DefaultGracePeriodSeconds)
}

func TestRenderHonoursOverriddenDefaults(t *testing.T) {
	rendered := decode(t, Spec{
		Name:               "probe",
		Image:              "busybox:1.36",
		ContainerName:      "tool",
		RestartPolicy:      "OnFailure",
		GracePeriodSeconds: 45,
	}.Render())

	require.Equal(t, "tool", rendered.Spec.Containers[0].Name)
	require.Equal(t, corev1.RestartPolicyOnFailure, rendered.Spec.RestartPolicy)
	require.Equal(t, int64(45), *rendered.Spec.TerminationGracePeriodSeconds)
}

// The capped grace period is the one rendered property the e2e run cannot check
// for itself. Everything else fails loudly — a wrong image or command breaks the
// spec that uses it, and an unquoted annotation value is rejected by the API
// server — but an uncapped grace period keeps the suite green and merely makes
// it slower, blocking every `kubectl delete` for the full 30s default. Asserted
// on the rendered value rather than the constant so that neither dropping the
// field from the template nor raising the default can pass.
func TestRenderCapsGracePeriodByDefault(t *testing.T) {
	rendered := decode(t, Spec{Name: "probe", Image: "busybox:1.36"}.Render())

	require.NotNil(t, rendered.Spec.TerminationGracePeriodSeconds,
		"no terminationGracePeriodSeconds means the API server applies its 30s default")
	require.LessOrEqual(t, *rendered.Spec.TerminationGracePeriodSeconds, int64(1),
		"a grace period above 1s puts pod teardown back on the suite's critical path")
}

func TestRenderPopulatesEveryField(t *testing.T) {
	manifest := Spec{
		Name:               "probe",
		Namespace:          "workloads",
		Labels:             map[string]string{"app": "probe"},
		Annotations:        map[string]string{"nvml-mock.nvidia.com/devices": "true"},
		ContainerName:      "tool",
		Image:              "busybox:1.36",
		Command:            []string{"/bin/sh", "-c", "sleep 3600"},
		Env:                map[string]string{"MOCK_MODE": "cdi"},
		Node:               "worker-1",
		NodeSelector:       map[string]string{"nvidia.com/gpu.present": "true"},
		GPUs:               8,
		RestartPolicy:      "Never",
		GracePeriodSeconds: 1,
	}.Render()
	rendered := decode(t, manifest)

	require.Equal(t, map[string]string{"app": "probe"}, rendered.Labels)
	require.Equal(t, map[string]string{"nvml-mock.nvidia.com/devices": "true"}, rendered.Annotations)
	require.Equal(t, "worker-1", rendered.Spec.NodeName)
	require.Equal(t, map[string]string{"nvidia.com/gpu.present": "true"}, rendered.Spec.NodeSelector)

	container := rendered.Spec.Containers[0]
	require.Equal(t, "tool", container.Name)
	require.Equal(t, []string{"/bin/sh", "-c", "sleep 3600"}, container.Command)
	require.Equal(t, []corev1.EnvVar{{Name: "MOCK_MODE", Value: "cdi"}}, container.Env)

	// Every field set at once must still produce a pod the server accepts; this
	// is where a misindented block shows up.
	_, err := decodeAsAPIServer(t, manifest)
	require.NoError(t, err, "the API server must accept a fully populated pod")
}

// An omitted field must be absent rather than emitted empty, so the API server
// applies its own default instead of receiving a zero value.
func TestRenderOmitsUnsetFields(t *testing.T) {
	manifest := Spec{Name: "probe", Image: "busybox:1.36"}.Render()

	for _, field := range []string{"labels:", "annotations:", "nodeName:", "nodeSelector:", "command:", "env:", "resources:"} {
		require.NotContains(t, string(manifest), field,
			"an unset field must not reach the manifest")
	}

	rendered := decode(t, manifest)
	require.Empty(t, rendered.Labels)
	require.Empty(t, rendered.Annotations)
	require.Empty(t, rendered.Spec.NodeName)
	require.Empty(t, rendered.Spec.NodeSelector)
	require.Empty(t, rendered.Spec.Containers[0].Command)
	require.Empty(t, rendered.Spec.Containers[0].Env)
	require.Empty(t, rendered.Spec.Containers[0].Resources.Limits)
}

// An empty map is the shape a caller produces when it merges an optional
// annotation set and none applied; it must behave as unset, not emit `{}`.
func TestRenderTreatsEmptyMapsAsUnset(t *testing.T) {
	manifest := Spec{
		Name:         "probe",
		Image:        "busybox:1.36",
		Labels:       map[string]string{},
		Annotations:  map[string]string{},
		NodeSelector: map[string]string{},
		Env:          map[string]string{},
	}.Render()

	require.NotContains(t, string(manifest), "annotations:")
	require.NotContains(t, string(manifest), "labels:")
	require.NotContains(t, string(manifest), "nodeSelector:")
	require.NotContains(t, string(manifest), "env:")
	require.NoError(t, yaml.UnmarshalStrict(manifest, &corev1.Pod{}), "manifest:\n%s", manifest)
}

func TestRenderRequestsGPUsAsAnExtendedResource(t *testing.T) {
	rendered := decode(t, Spec{Name: "probe", Image: "busybox:1.36", GPUs: 4}.Render())

	limits := rendered.Spec.Containers[0].Resources.Limits
	quantity, ok := limits[kube.GPUResourceName]
	require.True(t, ok, "expected a %s limit, got %v", kube.GPUResourceName, limits)
	require.Equal(t, "4", quantity.String())
}

func TestRenderOmitsResourcesWithoutGPUs(t *testing.T) {
	rendered := decode(t, Spec{Name: "probe", Image: "busybox:1.36", GPUs: 0}.Render())

	require.Empty(t, rendered.Spec.Containers[0].Resources.Limits,
		"a pod that asks for no GPUs must not carry an empty limits block")
}

// Placement is either a pin or a selector. A pin bypasses the scheduler, which
// specs asserting on scheduling must avoid, so the two must not be conflated.
func TestRenderPlacesByPinOrSelector(t *testing.T) {
	pinned := decode(t, Spec{Name: "probe", Image: "busybox:1.36", Node: "worker-1"}.Render())
	require.Equal(t, "worker-1", pinned.Spec.NodeName)
	require.Empty(t, pinned.Spec.NodeSelector)

	selected := decode(t, Spec{
		Name:         "probe",
		Image:        "busybox:1.36",
		NodeSelector: map[string]string{"nvidia.com/gpu.present": "true"},
	}.Render())
	require.Empty(t, selected.Spec.NodeName)
	require.Equal(t, map[string]string{"nvidia.com/gpu.present": "true"}, selected.Spec.NodeSelector)
}

// Label, annotation, selector and env values are strings in the API. Rendered
// bare, YAML would hand the server a bool or an int and it would reject the pod,
// so every value has to arrive quoted. These are the YAML 1.1 scalars that
// surprise people; `true` is the one this suite actually relies on.
func TestRenderQuotesValuesTheAPIServerNeedsAsStrings(t *testing.T) {
	for _, value := range []string{"true", "false", "yes", "no", "on", "off", "null", "~", "8", "0755", "1.20", "y", "n"} {
		t.Run(value, func(t *testing.T) {
			manifest := Spec{
				Name:         "probe",
				Image:        "busybox:1.36",
				Labels:       map[string]string{"label": value},
				Annotations:  map[string]string{"annotation": value},
				NodeSelector: map[string]string{"selector": value},
				Env:          map[string]string{"ENV": value},
			}.Render()

			rendered, err := decodeAsAPIServer(t, manifest)
			require.NoError(t, err, "the API server must accept %q as a string value", value)

			require.Equal(t, value, rendered.Labels["label"])
			require.Equal(t, value, rendered.Annotations["annotation"])
			require.Equal(t, value, rendered.Spec.NodeSelector["selector"])
			require.Equal(t, []corev1.EnvVar{{Name: "ENV", Value: value}}, rendered.Spec.Containers[0].Env)
		})
	}
}

// Map keys need the same quoting as values, and are easier to get wrong because
// real keys like `app` look safe. A single-character key is a valid Kubernetes
// label name but a YAML 1.1 bool, so bare `y` reaches the server as `true`.
func TestRenderQuotesKeysTheAPIServerNeedsAsStrings(t *testing.T) {
	for _, key := range []string{"y", "n", "on", "off", "true", "false", "null", "yes", "no"} {
		t.Run(key, func(t *testing.T) {
			manifest := Spec{
				Name:         "probe",
				Image:        "busybox:1.36",
				Labels:       map[string]string{key: "set"},
				Annotations:  map[string]string{key: "set"},
				NodeSelector: map[string]string{key: "set"},
				Env:          map[string]string{key: "set"},
			}.Render()

			rendered, err := decodeAsAPIServer(t, manifest)
			require.NoError(t, err, "manifest:\n%s", manifest)

			require.Equal(t, map[string]string{key: "set"}, rendered.Labels)
			require.Equal(t, map[string]string{key: "set"}, rendered.Annotations)
			require.Equal(t, map[string]string{key: "set"}, rendered.Spec.NodeSelector)
			require.Equal(t, []corev1.EnvVar{{Name: key, Value: "set"}}, rendered.Spec.Containers[0].Env)
		})
	}
}

// Names and images are interpolated too, so a value carrying YAML punctuation
// must not be able to break out of its scalar and restructure the manifest.
func TestRenderEscapesValuesThatCouldBreakTheDocument(t *testing.T) {
	manifest := Spec{
		Name:        `probe": {"evil`,
		Image:       "busybox:1.36",
		Annotations: map[string]string{"annotation": "line\nbreak"},
	}.Render()

	rendered, err := decodeAsAPIServer(t, manifest)
	require.NoError(t, err, "manifest:\n%s", manifest)
	require.Equal(t, `probe": {"evil`, rendered.Name)
	require.Equal(t, "line\nbreak", rendered.Annotations["annotation"])
}

// Multiple keys must each land under the right parent, which is what an
// off-by-one indent in the range block would break.
func TestRenderEmitsEveryKeyOfAMultiKeyMap(t *testing.T) {
	rendered := decode(t, Spec{
		Name:         "probe",
		Image:        "busybox:1.36",
		Labels:       map[string]string{"app": "probe", "tier": "test", "owner": "e2e"},
		Annotations:  map[string]string{"a": "1", "b": "2"},
		NodeSelector: map[string]string{"x": "1", "y": "2"},
		Env:          map[string]string{"A": "1", "B": "2"},
	}.Render())

	require.Equal(t, map[string]string{"app": "probe", "tier": "test", "owner": "e2e"}, rendered.Labels)
	require.Equal(t, map[string]string{"a": "1", "b": "2"}, rendered.Annotations)
	require.Equal(t, map[string]string{"x": "1", "y": "2"}, rendered.Spec.NodeSelector)
	require.Len(t, rendered.Spec.Containers[0].Env, 2)
}

// Rendering is a pure function of the spec: the suite renders the same manifest
// to apply and again to delete, and the two must match.
func TestRenderIsDeterministic(t *testing.T) {
	spec := Spec{
		Name:         "probe",
		Image:        "busybox:1.36",
		Labels:       map[string]string{"app": "probe", "tier": "test", "owner": "e2e"},
		Annotations:  map[string]string{"a": "1", "b": "2", "c": "3"},
		NodeSelector: map[string]string{"x": "1", "y": "2"},
		Env:          map[string]string{"A": "1", "B": "2"},
	}

	first := spec.Render()
	for range 20 {
		require.Equal(t, string(first), string(spec.Render()),
			"the same spec must always render the same manifest")
	}
}
