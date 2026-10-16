// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package nri

import (
	"testing"

	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
)

func TestPassthroughConfigOverridesTheProfileMode(t *testing.T) {
	t.Parallel()

	profile := []byte(`version: "1.0"
device_defaults:
  name: "NVIDIA GB300 NVL"
  # Virtualization
  virtualization:
    mode: "none"
    host_vgpu_mode: "N/A"
devices:
  - index: 0
`)
	got, err := passthroughConfig(profile)
	require.NoError(t, err)

	var parsed struct {
		Version        string `yaml:"version"`
		DeviceDefaults struct {
			Name           string            `yaml:"name"`
			Virtualization map[string]string `yaml:"virtualization"`
		} `yaml:"device_defaults"`
		Devices []map[string]int `yaml:"devices"`
	}
	require.NoError(t, yaml.Unmarshal(got, &parsed))
	require.Equal(t, "1.0", parsed.Version)
	require.Equal(t, "NVIDIA GB300 NVL", parsed.DeviceDefaults.Name)
	require.Equal(t, map[string]string{"mode": "passthrough", "host_vgpu_mode": "N/A"}, parsed.DeviceDefaults.Virtualization)
	require.Equal(t, []map[string]int{{"index": 0}}, parsed.Devices)
}

func TestPassthroughConfigCreatesTheModeWhenAbsent(t *testing.T) {
	t.Parallel()

	got, err := passthroughConfig([]byte("version: test\n"))
	require.NoError(t, err)
	require.Equal(t, "version: test\ndevice_defaults:\n  virtualization:\n    mode: passthrough\n", string(got))
}

func TestPassthroughConfigRejectsAProfileThatIsNotAMapping(t *testing.T) {
	t.Parallel()

	for name, profile := range map[string]string{
		"list":                  "- a\n",
		"empty":                 "",
		"defaults not a map":    "device_defaults: 3\n",
		"virtualization a list": "device_defaults:\n  virtualization: [a]\n",
	} {
		_, err := passthroughConfig([]byte(profile))
		require.Error(t, err, name)
	}
}
