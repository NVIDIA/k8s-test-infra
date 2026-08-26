// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package source

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"sigs.k8s.io/yaml"

	"github.com/NVIDIA/k8s-test-infra/internal/agent"
	"github.com/NVIDIA/k8s-test-infra/pkg/gpu/mocknvml/engine"
	ibconfig "github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/config"
)

func TestCompileState_AllSKUs(t *testing.T) {
	configs, err := filepath.Glob("../../../pkg/gpu/mocknvml/configs/mock-nvml-config-*.yaml")
	require.NoError(t, err)
	require.NotEmpty(t, configs, "no config YAMLs found")

	for _, path := range configs {
		t.Run(filepath.Base(path), func(t *testing.T) {
			data, err := os.ReadFile(path)
			require.NoError(t, err)

			state, err := compileState(data)
			require.NoError(t, err)

			require.NotEmpty(t, state.Software.DriverVersion, "empty DriverVersion")
			require.Positive(t, state.NodeShape.NumGPUs, "NumGPUs must be > 0")
			require.Len(t, state.Devices, state.NodeShape.NumGPUs, "Devices count mismatch")

			for i, d := range state.Devices {
				require.Equal(t, i, d.Index, "device index mismatch at position %d", i)
			}
		})
	}
}

func TestCompileState_FabricState(t *testing.T) {
	// gb200 has nvlink and fabric config
	data, err := os.ReadFile("../../../pkg/gpu/mocknvml/configs/mock-nvml-config-gb200.yaml")
	require.NoError(t, err)

	state, err := compileState(data)
	require.NoError(t, err)

	require.True(t, state.Fabric.Enabled, "gb200 fabric should be enabled")
	require.Positive(t, state.Fabric.LinksPerGPU)
}

// The state dir comes from the environment, not the profile: NVLink in the
// config says nothing about whether fabricmanager runs on the node.
func TestCompileState_ManagerStateDir(t *testing.T) {
	data, err := os.ReadFile("../../../pkg/gpu/mocknvml/configs/mock-nvml-config-gb200.yaml")
	require.NoError(t, err)

	state, err := compileState(data)
	require.NoError(t, err)
	require.Empty(t, state.Fabric.ManagerStateDir)

	t.Setenv(engine.EnvFabricStateDir, " /var/lib/nvml-mock/fabric-state ")
	state, err = compileState(data)
	require.NoError(t, err)
	require.Equal(t, "/var/lib/nvml-mock/fabric-state", state.Fabric.ManagerStateDir)
}

func TestCompileState_RDMAResource(t *testing.T) {
	tests := map[string]struct {
		yaml string
		want agent.RDMAResource
	}{
		"declared": {
			yaml: `
infiniband:
  enabled: true
  rdma_resource:
    name: "rdma/ib"
    hca_max: 64
`,
			want: agent.RDMAResource{Name: "rdma/ib", Count: 64},
		},
		"ib without the block": {
			yaml: "infiniband:\n  enabled: true\n",
			want: agent.RDMAResource{},
		},
		"no infiniband block": {
			yaml: "version: \"1.0\"\n",
			want: agent.RDMAResource{},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			state, err := compileState([]byte(tc.yaml))
			require.NoError(t, err)
			require.Equal(t, tc.want, state.Network.RDMAResource)
		})
	}
}

// The chart profiles are what the ConfigMap carries into the agent. Declaring
// the resource is a per-profile choice, but a misspelled key silently stops
// advertising it on every node running that profile, so a declared block must
// survive the round trip intact and an absent one must stay absent.
func TestCompileState_ChartProfileRDMAResources(t *testing.T) {
	profiles, err := filepath.Glob("../../../deployments/nvml-mock/helm/nvml-mock/profiles/*.yaml")
	require.NoError(t, err)
	require.NotEmpty(t, profiles, "no chart profiles found")

	declared := 0

	for _, path := range profiles {
		t.Run(filepath.Base(path), func(t *testing.T) {
			data, err := os.ReadFile(path)
			require.NoError(t, err)

			state, err := compileState(data)
			require.NoError(t, err)

			var profile ibconfig.Profile
			require.NoError(t, yaml.Unmarshal(data, &profile))

			res := state.Network.RDMAResource
			if profile.Infiniband.RDMAResource == nil {
				require.Empty(t, res.Name)
				require.Zero(t, res.Count)
				return
			}

			declared++
			require.Contains(t, res.Name, "/", "extended resource names must be qualified")
			require.Positive(t, res.Count)
		})
	}

	require.Positive(t, declared, "no chart profile declares an RDMA resource")
}

func TestFileSource_EmitsInitialState(t *testing.T) {
	configs, _ := filepath.Glob("../../../pkg/gpu/mocknvml/configs/mock-nvml-config-*.yaml")
	require.NotEmpty(t, configs, "no configs found")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fs := NewFileSource(configs[0], slog.New(slog.NewTextHandler(os.Stdout, nil)))
	ch := fs.Watch(ctx)

	u := <-ch
	require.NoError(t, u.Err)
	require.NotNil(t, u.State)

	cancel()
	for range ch { //nolint:revive // drain closed channel
	}
}

func TestCompileState_PCIIdentityFromDefaults(t *testing.T) {
	data, err := os.ReadFile("../../../pkg/gpu/mocknvml/configs/mock-nvml-config-h100.yaml")
	require.NoError(t, err)

	state, err := compileState(data)
	require.NoError(t, err)
	require.NotEmpty(t, state.Devices)

	// Every device in the profile declares only its own bus_id, so both identity
	// words must survive the defaults merge. They feed the rendered sysfs
	// attribute files lspci reads.
	for i, d := range state.Devices {
		require.Equal(t, uint32(0x233010DE), d.PCIDeviceID, "device %d device_id", i)
		require.Equal(t, uint32(0x165810DE), d.PCISubsystemID, "device %d subsystem_id", i)
	}
}

func TestCompileState_EverySKUCarriesPCIIdentity(t *testing.T) {
	configs, err := filepath.Glob("../../../pkg/gpu/mocknvml/configs/mock-nvml-config-*.yaml")
	require.NoError(t, err)
	require.NotEmpty(t, configs, "no config YAMLs found")

	for _, path := range configs {
		t.Run(filepath.Base(path), func(t *testing.T) {
			data, err := os.ReadFile(path)
			require.NoError(t, err)

			state, err := compileState(data)
			require.NoError(t, err)

			for i, d := range state.Devices {
				require.NotZero(t, d.PCIDeviceID, "device %d device_id", i)
				require.NotZero(t, d.PCISubsystemID, "device %d subsystem_id", i)
			}
		})
	}
}

func TestCompileState_PerDevicePCIOverride(t *testing.T) {
	t.Setenv("GPU_COUNT", "")

	const cfg = `
version: "1.0"
system:
  driver_version: "550.163.01"
device_defaults:
  name: "Mock GPU"
  pci:
    device_id: 0x233010DE
    subsystem_id: 0x165810DE
devices:
  - index: 0
    pci:
      bus_id: "0000:1A:00.0"
  - index: 1
    pci:
      bus_id: "0000:1B:00.0"
      device_id: 0x234010DE
      subsystem_id: 0x181810DE
`
	state, err := compileState([]byte(cfg))
	require.NoError(t, err)
	require.Len(t, state.Devices, 2)

	// Device 0 sets only bus_id, so it keeps both profile defaults.
	require.Equal(t, "0000:1A:00.0", state.Devices[0].PCIBusID)
	require.Equal(t, uint32(0x233010DE), state.Devices[0].PCIDeviceID)
	require.Equal(t, uint32(0x165810DE), state.Devices[0].PCISubsystemID)

	// Device 1 overrides each word independently of bus_id.
	require.Equal(t, "0000:1B:00.0", state.Devices[1].PCIBusID)
	require.Equal(t, uint32(0x234010DE), state.Devices[1].PCIDeviceID)
	require.Equal(t, uint32(0x181810DE), state.Devices[1].PCISubsystemID)
}
