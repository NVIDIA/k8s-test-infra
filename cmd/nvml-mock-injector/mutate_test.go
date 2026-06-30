// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/NVIDIA/k8s-test-infra/cmd/nvml-mock-injector/tier"
)

func TestMutatePodIBTier(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ib-discovery",
			Namespace: "default",
			Annotations: map[string]string{
				tier.AnnotationTier: tier.TierIB,
			},
		},
	}

	mutated, err := mutatePod(pod, tier.Config{})
	require.NoError(t, err)
	require.Equal(t, tier.TierIB, mutated.Annotations[tier.CDIAnnotMock])
	require.Equal(t, tier.TierIB, mutated.Annotations[tier.AnnotationInjectedTier])
	require.NotContains(t, mutated.Annotations, tier.CDIAnnotGPU)
	require.NotNil(t, mutated.Spec.RuntimeClassName)
	require.Equal(t, tier.RuntimeNvidia, *mutated.Spec.RuntimeClassName)
}

func TestMutatePodPlainPodNoOp(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "plain"},
	}

	mutated, err := mutatePod(pod, tier.Config{})
	require.NoError(t, err)
	require.Empty(t, mutated.Annotations)
	require.Nil(t, mutated.Spec.RuntimeClassName)
}

func TestMutatePodGPUTier(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{
				"app.kubernetes.io/component": "nvidia-device-plugin",
			},
		},
	}

	cfg := tier.Config{
		GPUOperatorComponents: map[string]struct{}{
			"nvidia-device-plugin": {},
		},
	}

	mutated, err := mutatePod(pod, cfg)
	require.NoError(t, err)
	require.Equal(t, tier.TierFull, mutated.Annotations[tier.CDIAnnotMock])
	require.Equal(t, tier.TierGPU, mutated.Annotations[tier.AnnotationInjectedTier])
	require.Equal(t, "all", mutated.Annotations[tier.CDIAnnotGPU])
	require.Equal(t, tier.RuntimeNvidia, *mutated.Spec.RuntimeClassName)
}
