// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package inject

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/NVIDIA/k8s-test-infra/internal/features"
)

// toolkitInstaller is the container the GPU Operator runs its toolkit
// installer in, as NRI presents it.
func toolkitInstaller() Container {
	return Container{
		Name:      "nvidia-container-toolkit-ctr",
		Namespace: "gpu-operator",
		PodLabels: map[string]string{"app": "nvidia-container-toolkit-daemonset"},
		// As the operator renders it.
		Env: []string{
			"NVIDIA_CONTAINER_RUNTIME_MODES_CDI_DEFAULT_KIND=management.nvidia.com/gpu",
			"CDI_ENABLED=true",
		},
	}
}

// Not parallel: it flips a gate the other tests read.
func TestGPUOperatorToolkitIsEnabledByTheDriverGate(t *testing.T) {
	require.False(t, GPUOperatorToolkit{}.Enabled(), "off by default")

	features.SetFeatureGateDuringTest(t, features.GPUOperatorDriver, true)
	require.True(t, GPUOperatorToolkit{}.Enabled())
}

func TestGPUOperatorToolkitMatchesTheInstallerAlone(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		container func() Container
		want      bool
	}{
		"installer": {container: toolkitInstaller, want: true},
		// The pod's init container validates the driver and runs no runtime.
		"init container": {
			container: func() Container {
				c := toolkitInstaller()
				c.Name = "driver-validation"
				return c
			},
		},
		"same container name outside a toolkit pod": {
			container: func() Container {
				c := toolkitInstaller()
				c.PodLabels = map[string]string{"app": "something-else"}
				return c
			},
		},
		"driver installer": {container: driverInstaller},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tt.want, GPUOperatorToolkit{}.Matches(tt.container()))
		})
	}
}

func TestGPUOperatorToolkitPointsTheRuntimeAtTheNodeAgentsSpec(t *testing.T) {
	t.Parallel()

	adjustment, ok := GPUOperatorToolkit{}.Adjust(toolkitInstaller())
	require.True(t, ok)

	// The environment alone: the toolkit installs the runtime from it, and
	// needs neither the overlay nor GPUs.
	require.Equal(t, []string{"NVIDIA_CONTAINER_RUNTIME_MODES_CDI_DEFAULT_KIND=nvidia.com/gpu"}, adjustment.Env)
	require.Empty(t, adjustment.Mounts)
	require.Empty(t, adjustment.Devices)
}
