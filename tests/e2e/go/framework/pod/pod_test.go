//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package pod

import (
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/yaml"
)

// The capped grace period is the one rendered property the e2e run cannot check
// for itself. Everything else this package emits fails loudly — a wrong image or
// command breaks the spec that uses it, and an unquoted annotation value is
// rejected by the API server — but an uncapped grace period keeps the suite
// green and merely makes it slower, blocking every `kubectl delete` for the full
// 30s default. The assertion is on the rendered value rather than on the
// constant so that neither dropping the field from the template nor raising the
// default can pass.
func TestRenderCapsGracePeriod(t *testing.T) {
	manifest := Spec{Name: "probe", Image: "busybox:1.36"}.Render()

	var rendered corev1.Pod
	require.NoError(t, yaml.UnmarshalStrict(manifest, &rendered), "manifest:\n%s", manifest)

	require.NotNil(t, rendered.Spec.TerminationGracePeriodSeconds,
		"no terminationGracePeriodSeconds means the API server applies its 30s default")
	require.LessOrEqual(t, *rendered.Spec.TerminationGracePeriodSeconds, int64(1),
		"a grace period above 1s puts pod teardown back on the suite's critical path")
}
