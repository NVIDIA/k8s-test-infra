//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"testing"

	"github.com/stretchr/testify/require"
	"sigs.k8s.io/yaml"
)

// Every long-lived NRI workload pod must cap its termination grace period. The
// containers run `sleep` as PID 1, and PID 1 never receives a signal it
// installs no handler for, so kubelet's SIGTERM is discarded and the pod only
// dies on the post-grace SIGKILL. At the 30s default every `kubectl delete`
// blocked for the full 30s, which dominated this Ordered suite's runtime.
//
// The manifests are assembled by string concatenation, so this parses the YAML
// rather than matching a substring: a fragment spliced in at the wrong
// indentation would still contain the right text while landing the field
// outside the pod spec.
func TestNRIWorkloadManifestsCapTerminationGrace(t *testing.T) {
	manifests := map[string][]byte{
		"request":   nriRequestPodManifest("pod", "node", 1),
		"annotated": nriAnnotatedPodManifest("pod"),
		"plain":     nriPlainPodManifest("pod"),
		"imex":      nriImexPodManifest("pod", "node", true),
	}
	for name, manifest := range manifests {
		t.Run(name, func(t *testing.T) {
			var pod struct {
				Spec struct {
					RestartPolicy string `json:"restartPolicy"`
					GracePeriod   *int64 `json:"terminationGracePeriodSeconds"`
				} `json:"spec"`
			}
			require.NoError(t, yaml.Unmarshal(manifest, &pod), "manifest must be valid YAML:\n%s", manifest)
			require.Equal(t, "Never", pod.Spec.RestartPolicy, "manifest:\n%s", manifest)
			require.NotNil(t, pod.Spec.GracePeriod, "terminationGracePeriodSeconds must be set:\n%s", manifest)
			require.LessOrEqual(t, *pod.Spec.GracePeriod, int64(1), "manifest:\n%s", manifest)
		})
	}
}
