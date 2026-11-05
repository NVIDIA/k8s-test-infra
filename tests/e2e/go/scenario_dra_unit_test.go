//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/yaml"
)

func TestDRAResourceClaimManifest(t *testing.T) {
	manifest := string(draResourceClaimManifest())
	for _, want := range []string{
		"apiVersion: resource.k8s.io/v1beta1",
		"kind: ResourceClaimTemplate",
		"deviceClassName: gpu.nvidia.com",
		"name: gpu-test-pod",
	} {
		if !strings.Contains(manifest, want) {
			t.Fatalf("expected DRA ResourceClaim manifest to contain %q:\n%s", want, manifest)
		}
	}
}

// The claim pods are hand-written, not rendered by framework/pod, because that
// cannot express resourceClaims; so the grace cap framework/pod tests for its
// own pods is tested here. Without it every delete blocks for the 30s default.
// The pin must be node affinity: nodeName would bypass the scheduler, which is
// what allocates the claim.
func TestDRAClaimPodManifest(t *testing.T) {
	t.Parallel()
	for _, node := range []string{"", "worker-0"} {
		manifest := draClaimPodManifest("gpu-test-pod-2", node)

		var rendered corev1.Pod
		require.NoError(t, yaml.UnmarshalStrict(manifest, &rendered), "manifest:\n%s", manifest)

		require.NotNil(t, rendered.Spec.TerminationGracePeriodSeconds, "no grace cap")
		require.LessOrEqual(t, *rendered.Spec.TerminationGracePeriodSeconds, int64(1), "grace cap")
		require.Equal(t, "gpu", rendered.Spec.Containers[0].Resources.Claims[0].Name, "container claim")
		require.Equal(t, "gpu-claim", *rendered.Spec.ResourceClaims[0].ResourceClaimTemplateName, "claim template")
		require.Empty(t, rendered.Spec.NodeName, "nodeName bypasses the scheduler")
		if node == "" {
			require.Nil(t, rendered.Spec.Affinity, "unpinned pod")
			continue
		}
		field := rendered.Spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.
			NodeSelectorTerms[0].MatchFields[0]
		require.Equal(t, "metadata.name", field.Key)
		require.Equal(t, []string{node}, field.Values)
	}
}
