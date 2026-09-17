// Copyright (c) 2026, NVIDIA CORPORATION.  All rights reserved.
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package engine

import (
	"testing"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/stretchr/testify/require"

	"github.com/NVIDIA/k8s-test-infra/internal/gpuarch"
)

// TestArchConstantsMatchNVML is the contract behind the plain cast the engine
// uses to turn Config.Architecture into a gpuarch.Arch. internal/gpuarch
// cannot assert this itself without importing go-nvml, which would put cgo on
// the e2e harness, so the check lives here. If go-nvml ever renumbers a
// generation, this fails instead of every architecture gate quietly shifting.
func TestArchConstantsMatchNVML(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		got  gpuarch.Arch
		want nvml.DeviceArchitecture
	}{
		{"kepler", gpuarch.Kepler, nvml.DEVICE_ARCH_KEPLER},
		{"maxwell", gpuarch.Maxwell, nvml.DEVICE_ARCH_MAXWELL},
		{"pascal", gpuarch.Pascal, nvml.DEVICE_ARCH_PASCAL},
		{"volta", gpuarch.Volta, nvml.DEVICE_ARCH_VOLTA},
		{"turing", gpuarch.Turing, nvml.DEVICE_ARCH_TURING},
		{"ampere", gpuarch.Ampere, nvml.DEVICE_ARCH_AMPERE},
		{"ada", gpuarch.Ada, nvml.DEVICE_ARCH_ADA},
		{"hopper", gpuarch.Hopper, nvml.DEVICE_ARCH_HOPPER},
		{"blackwell", gpuarch.Blackwell, nvml.DEVICE_ARCH_BLACKWELL},
		{"rubin", gpuarch.Rubin, nvml.DEVICE_ARCH_RUBIN},
		{"unknown", gpuarch.Unknown, nvml.DEVICE_ARCH_UNKNOWN},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, nvml.DeviceArchitecture(tt.got),
				"arch.%s must equal nvml.DEVICE_ARCH_%s", tt.name, tt.name)
		})
	}
}

// TestParseArchitectureRubin covers the generation the engine could not name
// before this change: NVML defines NVML_DEVICE_ARCH_RUBIN, the e2e harness
// already expected Rubin to report T.Limit, and the engine answered UNKNOWN,
// which fails every architecture gate.
func TestParseArchitectureRubin(t *testing.T) {
	t.Parallel()
	require.Equal(t, nvml.DeviceArchitecture(nvml.DEVICE_ARCH_RUBIN), parseArchitecture("rubin"))
}

// TestParseArchitectureNormalizes pins the leniency that moved into
// gpuarch.Parse. The engine matched exactly before, so a config saying "Hopper"
// silently became UNKNOWN and failed every gate.
func TestParseArchitectureNormalizes(t *testing.T) {
	t.Parallel()
	for _, in := range []string{"Hopper", "HOPPER", "  hopper  "} {
		t.Run(in, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, nvml.DeviceArchitecture(nvml.DEVICE_ARCH_HOPPER), parseArchitecture(in),
				"parseArchitecture(%q)", in)
		})
	}
}

// TestDeviceArchitectureIsOrdered pins that a configured architecture survives
// construction as the ordered value the gates compare, so a gate cannot be
// passing for the wrong reason.
func TestDeviceArchitectureIsOrdered(t *testing.T) {
	t.Parallel()
	dev := newTestDeviceWithConfig(t, &DeviceConfig{Architecture: "hopper"})
	got := gpuarch.Arch(dev.Config.Architecture)
	require.Equal(t, gpuarch.Hopper, got)
	require.True(t, got.AtLeast(gpuarch.Hopper))
	require.False(t, got.AtLeast(gpuarch.Blackwell))
}
