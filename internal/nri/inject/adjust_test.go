// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package inject

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func requireAdjust(t testing.TB, cfg Config, container Container) (Adjustment, bool) {
	t.Helper()
	adjustment, ok, err := Adjust(cfg, container)
	require.NoError(t, err)
	return adjustment, ok
}

// overlayMount is the one mount every adjusted container gets, whatever the
// opt-ins say.
func overlayMount() Mount {
	return Mount{
		Source:      "/var/lib/nvml-mock",
		Destination: "/opt/nvml-mock",
		Type:        "bind",
		Options:     []string{"rbind", "ro", "nosuid", "nodev"},
	}
}

// configMount is the writable window in the otherwise read-only overlay.
func configMount() Mount {
	return Mount{
		Source:      "/var/lib/nvml-mock/driver/config",
		Destination: "/opt/nvml-mock/driver/config",
		Type:        "bind",
		Options:     []string{"rbind", "rw", "nosuid", "nodev"},
	}
}

func TestAdjustMountsTheOverlayForAPlainContainer(t *testing.T) {
	t.Parallel()

	adjustment, ok := requireAdjust(t, DefaultConfig(), Container{Namespace: "default"})
	require.True(t, ok)
	require.Contains(t, adjustment.Mounts, overlayMount())
}

// `nvidia-smi --gpu-reset` clears the device's bucket from overrides.yaml, which
// sits beside the config.yaml MOCK_NVML_CONFIG points at. With the whole overlay
// read-only that write failed with EROFS on exactly the GPUs that had state to
// clear, so the config directory gets its own writable bind. It has to stay
// layered over the overlay: ordered first, the read-only rbind would cover it.
func TestAdjustMountsConfigDirWritableOverReadOnlyOverlay(t *testing.T) {
	t.Parallel()

	adjustment, ok := requireAdjust(t, DefaultConfig(), Container{Namespace: "default"})
	require.True(t, ok)
	require.Contains(t, adjustment.Mounts, configMount())

	overlay, config := -1, -1
	for i, m := range adjustment.Mounts {
		switch m.Destination {
		case "/opt/nvml-mock":
			overlay = i
			require.Contains(t, m.Options, "ro", "the mock library and nvidia-smi stay immutable")
		case "/opt/nvml-mock/driver/config":
			config = i
		}
	}
	require.NotEqual(t, -1, overlay)
	require.NotEqual(t, -1, config)
	require.Less(t, overlay, config, "the writable config bind must be applied after the overlay it sits inside")
}

// An unannotated container gets the overlay and the environment but nothing
// else: both device paths are opt-in, so the default must stay empty.
func TestAdjustWithoutOptInsDeliversNoDevices(t *testing.T) {
	t.Parallel()

	adjustment, ok := requireAdjust(t, DefaultConfig(), Container{Namespace: "default"})
	require.True(t, ok)
	require.Empty(t, adjustment.Devices)
	require.Empty(t, adjustment.CDIDevices)
	require.NotEmpty(t, adjustment.Env)
}

func TestAdjustComputeDomainCDIMountsOnlyMissingFiles(t *testing.T) {
	cfg := DefaultConfig()
	cfg.HostOverlayPath = t.TempDir()
	cfg.NodeName = "worker-1"
	realIMEX := cfg.HostOverlayPath + "/driver/usr/bin/nvidia-imex.real"
	require.NoError(t, os.MkdirAll(cfg.HostOverlayPath+"/driver/usr/bin", 0o755))
	require.NoError(t, os.WriteFile(realIMEX, []byte("imex"), 0o755))
	cfg.TopologyHostPath = cfg.HostOverlayPath + "/topology/topology.yaml"
	require.NoError(t, os.MkdirAll(cfg.HostOverlayPath+"/topology", 0o755))
	require.NoError(t, os.WriteFile(cfg.TopologyHostPath, []byte("version: 1\n"), 0o600))
	cfg.TopologyContainerPath = "/etc/nvml-mock/topology.yaml"
	adjustment, ok := requireAdjust(t, cfg, Container{
		Name:      computeDomainContainerName,
		Namespace: "nvidia",
		PodLabels: map[string]string{computeDomainLabel: "domain-uid"},
	})

	require.True(t, ok)
	require.Equal(t, []Mount{
		{
			Source:      realIMEX,
			Destination: "/usr/bin/nvidia-imex.real",
			Type:        "bind",
			Options:     []string{"rbind", "ro", "nosuid", "nodev"},
		},
		{
			Source:      cfg.TopologyHostPath,
			Destination: cfg.TopologyContainerPath,
			Type:        "bind",
			Options:     []string{"rbind", "ro", "nosuid", "nodev"},
		},
	}, adjustment.Mounts)
	require.Equal(t, []string{"MOCK_TOPOLOGY_CONFIG=/etc/nvml-mock/topology.yaml"}, adjustment.Env)
	require.Empty(t, adjustment.Devices)
	require.Empty(t, adjustment.CDIDevices)
}

func TestComputeDomainDaemonRequiresNameAndDomainLabel(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		container Container
		want      bool
	}{
		"name and label": {
			container: Container{
				Name:      computeDomainContainerName,
				PodLabels: map[string]string{computeDomainLabel: "domain-uid"},
			},
			want: true,
		},
		"name alone": {
			container: Container{Name: computeDomainContainerName},
		},
		"label alone": {
			container: Container{PodLabels: map[string]string{computeDomainLabel: "domain-uid"}},
		},
		"empty label": {
			container: Container{
				Name:      computeDomainContainerName,
				PodLabels: map[string]string{computeDomainLabel: ""},
			},
		},
		"none": {},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, computeDomainDaemon(tt.container))
		})
	}
}

func TestAdjustComputeDomainRejectsPartialNodeStaging(t *testing.T) {
	t.Parallel()

	for name, staged := range map[string]struct {
		realIMEX bool
		topology bool
	}{
		"neither prerequisite": {},
		"real IMEX only":       {realIMEX: true},
		"topology only":        {topology: true},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			cfg := DefaultConfig()
			cfg.HostOverlayPath = t.TempDir()
			cfg.NodeName = "worker-1"
			cfg.TopologyHostPath = cfg.HostOverlayPath + "/topology/topology.yaml"
			if staged.realIMEX {
				require.NoError(t, os.MkdirAll(cfg.HostOverlayPath+"/driver/usr/bin", 0o755))
				require.NoError(t, os.WriteFile(cfg.HostOverlayPath+"/driver/usr/bin/nvidia-imex.real", []byte("imex"), 0o755))
			}
			if staged.topology {
				require.NoError(t, os.MkdirAll(cfg.HostOverlayPath+"/topology", 0o755))
				require.NoError(t, os.WriteFile(cfg.TopologyHostPath, []byte("version: 1\n"), 0o600))
			}

			adjustment, ok, err := Adjust(cfg, Container{
				Name:      computeDomainContainerName,
				Namespace: "nvidia",
				PodLabels: map[string]string{computeDomainLabel: "domain-uid"},
			})

			require.Error(t, err)
			require.False(t, ok)
			require.Empty(t, adjustment)
		})
	}
}

func TestAdjustSkipsOptOutExcludedNamespaceAndExistingMount(t *testing.T) {
	t.Parallel()

	cfg := DefaultConfig()
	cfg.ExcludedNamespaces = []string{"kube-system", "nvml-mock"}

	tests := map[string]Container{
		"opt out annotation": {
			Namespace: "default",
			PodAnnotations: map[string]string{
				"nvml-mock.nvidia.com/inject": "false",
			},
		},
		"excluded namespace": {
			Namespace: "kube-system",
		},
		// A container already carrying the overlay has been through here
		// before; injecting twice would stack duplicate LD_PRELOAD entries.
		"existing overlay mount": {
			Namespace: "default",
			Mounts:    []Mount{{Destination: "/opt/nvml-mock"}},
		},
	}

	for name, container := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			adjustment, ok := requireAdjust(t, cfg, container)
			require.False(t, ok)
			require.Empty(t, adjustment)
		})
	}
}

// An empty exclusion list means "exclude nothing", so it must not fall back to
// the packaged kube-system default.
func TestEmptyExclusionListExcludesNothing(t *testing.T) {
	t.Parallel()

	cfg := DefaultConfig()
	cfg.ExcludedNamespaces = nil

	_, ok := requireAdjust(t, cfg, Container{Namespace: "kube-system"})
	require.True(t, ok)
}
