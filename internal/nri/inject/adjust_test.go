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

// overlayMount is the mount selected GPU and IMEX containers receive.
func overlayMount() Mount {
	return Mount{
		Source:      "/var/lib/nvml-mock",
		Destination: "/opt/nvml-mock",
		Type:        "bind",
		Options:     []string{"rbind", "rprivate", "ro", "nosuid", "nodev"},
	}
}

// configMount is the writable window in the otherwise read-only overlay.
func configMount() Mount {
	return Mount{
		Source:      "/var/lib/nvml-mock/driver/config",
		Destination: "/opt/nvml-mock/driver/config",
		Type:        "bind",
		Options:     []string{"rbind", "rprivate", "rw", "nosuid", "nodev"},
	}
}

func TestAdjustMountsTheOverlayForAManagementContainer(t *testing.T) {
	t.Parallel()

	adjustment, ok := requireAdjust(t, DefaultConfig(), deviceOptIn())
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

	adjustment, ok := requireAdjust(t, DefaultConfig(), deviceOptIn())
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

// An unallocated, unannotated container receives no GPU-tier edits.
func TestAdjustWithoutOptInsIsUnmodified(t *testing.T) {
	t.Parallel()

	adjustment, ok := requireAdjust(t, DefaultConfig(), Container{Namespace: "default"})
	require.False(t, ok)
	require.Empty(t, adjustment.Mounts)
	require.Empty(t, adjustment.Devices)
	require.Empty(t, adjustment.CDIDevices)
	require.Empty(t, adjustment.Env)
}

func TestNoGPUSelectionDoesNotWarn(t *testing.T) {
	warnings := captureWarnings(t)
	adjustment, ok := requireAdjust(t, DefaultConfig(), Container{Namespace: "default"})
	require.False(t, ok)
	require.Empty(t, adjustment)
	require.Empty(t, warnings.captured())
}

func TestAdjustComputeDomainCDIMountsOnlyMissingFiles(t *testing.T) {
	cfg := DefaultConfig()
	cfg.HostOverlayPath = t.TempDir()
	cfg.NodeName = "worker-1"
	cfg.ComputeDomainStaging = true
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
			Options:     []string{"rbind", "rprivate", "ro", "nosuid", "nodev"},
		},
		{
			Source:      cfg.TopologyHostPath,
			Destination: cfg.TopologyContainerPath,
			Type:        "bind",
			Options:     []string{"rbind", "rprivate", "ro", "nosuid", "nodev"},
		},
	}, adjustment.Mounts)
	require.Equal(t, []string{"MOCK_TOPOLOGY_CONFIG=/etc/nvml-mock/topology.yaml"}, adjustment.Env)
	for _, m := range adjustment.Mounts {
		require.Contains(t, m.Options, "rprivate", "%s must not share mount events with the node", m.Destination)
	}
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
			cfg.ComputeDomainStaging = true
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

// With chart defaults the node agent stages neither prerequisite, so they
// would never appear: rejecting the daemon would block it forever.
// The DRA ComputeDomain daemon carries no GPU allocation or opt-in annotation
// at CreateContainer, so it must be routed before those checks, and it is held
// back only while the node agent is expected to stage its prerequisites.
func TestComputeDomainDaemonIsRoutedAndWaitsForStagingOnlyWhenExpected(t *testing.T) {
	t.Parallel()

	daemon := Container{
		Name:      computeDomainContainerName,
		Namespace: "nvidia",
		PodLabels: map[string]string{computeDomainLabel: "domain-uid"},
	}
	cfg := DefaultConfig()

	_, skipped := Skip(cfg, daemon)
	require.False(t, skipped, "the daemon reaches the plugin's gate")
	require.False(t, WaitsForStaging(cfg, daemon))

	cfg.ComputeDomainStaging = true
	require.True(t, WaitsForStaging(cfg, daemon))

	optedOut := daemon
	optedOut.PodAnnotations = map[string]string{cfg.OptOutAnnotation: "false"}
	require.False(t, WaitsForStaging(cfg, optedOut), "the opt-out still wins")

	excluded := daemon
	excluded.Namespace = "kube-system"
	require.False(t, WaitsForStaging(cfg, excluded), "an excluded namespace still wins")

	plain := Container{Name: "main", Namespace: "nvidia"}
	require.False(t, WaitsForStaging(cfg, plain))
}

func TestAdjustComputeDomainLeftUnmodifiedWhenNotStaged(t *testing.T) {
	t.Parallel()

	cfg := DefaultConfig()
	cfg.HostOverlayPath = t.TempDir()
	cfg.NodeName = "worker-1"

	adjustment, ok, err := Adjust(cfg, Container{
		Name:      computeDomainContainerName,
		Namespace: "nvidia",
		PodLabels: map[string]string{computeDomainLabel: "domain-uid"},
	})

	require.NoError(t, err)
	require.False(t, ok)
	require.Empty(t, adjustment)
}

func TestAdjustSkipsOptOutExcludedNamespaceAndExistingMount(t *testing.T) {
	t.Parallel()

	cfg := DefaultConfig()
	cfg.ExcludedNamespaces = []string{"kube-system", "nvml-mock"}

	tests := map[string]struct {
		container Container
		reason    string
	}{
		"opt out annotation": {
			container: Container{
				Namespace: "default",
				PodAnnotations: map[string]string{
					"nvml-mock.nvidia.com/devices": "true",
					"nvml-mock.nvidia.com/inject":  "false",
				},
			},
			reason: "opt-out annotation",
		},
		"excluded namespace": {
			container: Container{
				Namespace:      "kube-system",
				PodAnnotations: map[string]string{"nvml-mock.nvidia.com/devices": "true"},
			},
			reason: "excluded namespace",
		},
		// A container already carrying the overlay has been through here
		// before; injecting twice would stack duplicate LD_PRELOAD entries.
		"existing overlay mount": {
			container: Container{
				Namespace:      "default",
				PodAnnotations: map[string]string{"nvml-mock.nvidia.com/devices": "true"},
				Mounts:         []Mount{{Destination: "/opt/nvml-mock"}},
			},
			reason: "overlay already mounted",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, reason, skipped := decide(cfg, tc.container)
			require.True(t, skipped)
			require.Equal(t, tc.reason, reason)

			adjustment, ok := requireAdjust(t, cfg, tc.container)
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

	_, ok := requireAdjust(t, cfg, Container{Namespace: "kube-system", PodAnnotations: map[string]string{cfg.DeviceAnnotation: "true"}})
	require.True(t, ok)
}

// A container with a Bidirectional volume has an rshared root. While the
// overlay binds named no propagation, the writable config bind of every such
// container was copied onto the node at /var/lib/nvml-mock/driver/config, and
// the copies doubled with each one and outlived it. The e2e suite reproduces
// that on a Kind node; this pins the option that prevents it.
func TestOverlayMountsArePrivate(t *testing.T) {
	t.Parallel()

	var adjustment Adjustment
	mountOverlay(DefaultConfig(), &adjustment)
	require.Len(t, adjustment.Mounts, 2)
	for _, m := range adjustment.Mounts {
		require.Contains(t, m.Options, "rprivate", "%s must not share mount events with the node", m.Destination)
	}
}
