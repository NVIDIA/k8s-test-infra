// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package tier

import (
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
)

const (
	AnnotationTier         = "nvml-mock.nvidia.com/tier"
	AnnotationInjectedTier = "nvml-mock.nvidia.com/injected-tier"

	CDIKindMock   = "nvml-mock.nvidia.com/mock"
	CDIAnnotMock  = "cdi.k8s.io/nvml-mock.nvidia.com-mock"
	CDIAnnotGPU   = "cdi.k8s.io/nvidia.com-gpu"
	RuntimeNvidia = "nvidia"
	GPUResource   = "nvidia.com/gpu"

	TierNVML = "nvml"
	TierIB   = "ib"
	TierFull = "full"
	TierGPU  = "gpu"
)

// DefaultGPUOperatorComponents lists GPU Operator pod components that receive
// the gpu tier automatically.
var DefaultGPUOperatorComponents = []string{
	"nvidia-device-plugin",
	"gpu-feature-discovery",
	"nvidia-operator-validator",
}

type Config struct {
	GPUOperatorComponents map[string]struct{}
}

func (c Config) Resolve(pod *corev1.Pod) (string, error) {
	if t, ok := pod.Annotations[AnnotationTier]; ok {
		switch strings.ToLower(t) {
		case TierNVML, TierIB, TierFull:
			return strings.ToLower(t), nil
		case TierGPU:
			return "", fmt.Errorf("tier %q cannot be set via annotation; request %s instead", t, GPUResource)
		default:
			return "", fmt.Errorf("unknown tier %q", t)
		}
	}
	if component := pod.Labels["app.kubernetes.io/component"]; component != "" {
		if _, ok := c.GPUOperatorComponents[component]; ok {
			return TierGPU, nil
		}
	}
	gpuResource := corev1.ResourceName(GPUResource)
	for _, ctn := range pod.Spec.Containers {
		if qty, ok := ctn.Resources.Limits[gpuResource]; ok && !qty.IsZero() {
			return TierGPU, nil
		}
		if qty, ok := ctn.Resources.Requests[gpuResource]; ok && !qty.IsZero() {
			return TierGPU, nil
		}
	}
	for _, ctn := range pod.Spec.InitContainers {
		if qty, ok := ctn.Resources.Limits[gpuResource]; ok && !qty.IsZero() {
			return TierGPU, nil
		}
		if qty, ok := ctn.Resources.Requests[gpuResource]; ok && !qty.IsZero() {
			return TierGPU, nil
		}
	}
	return "", nil
}

func CDIDeviceForTier(t string) string {
	switch t {
	case TierGPU, TierFull:
		return TierFull
	case TierIB:
		return TierIB
	case TierNVML:
		return TierNVML
	default:
		return ""
	}
}
