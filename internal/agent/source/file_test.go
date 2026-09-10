// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package source

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"sigs.k8s.io/yaml"

	"github.com/NVIDIA/k8s-test-infra/internal/agent"
	"github.com/NVIDIA/k8s-test-infra/pkg/gpu/mocknvml/engine"
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

func TestFileSource_EmitsInitialState(t *testing.T) {
	configs, _ := filepath.Glob("../../../pkg/gpu/mocknvml/configs/mock-nvml-config-*.yaml")
	require.NotEmpty(t, configs, "no configs found")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fs := NewFileSource(configs[0], filepath.Join(t.TempDir(), "topology.yaml"), zap.NewNop())
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

// GPU_COUNT truncates the device list while pcie_topology is compiled from the
// profile whole, so the two disagree on any capped node. The rendered tree is
// served at the kernel paths now, so a BDF left over from the profile would
// show a consumer an NVIDIA 3D controller that NVML denies exists.
func TestCompileState_TopologyTracksACappedDeviceCount(t *testing.T) {
	t.Setenv("GPU_COUNT", "2")
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "pkg", "gpu", "mocknvml",
		"configs", "mock-nvml-config-gb300.yaml"))
	require.NoError(t, err)

	state, err := compileState(data)
	require.NoError(t, err)
	require.Len(t, state.Devices, 2, "GPU_COUNT caps the devices NVML reports")
	require.Len(t, state.NodeShape.Topology.RootComplexes, 2,
		"the profile's own layout is compiled whole, both roots and all four BDFs")

	rendered := state.PCITopology()

	require.Len(t, rendered, 1, "the root whose GPUs were capped away is dropped")
	require.Equal(t, "pci0000:00", rendered[0].ID)
	require.Equal(t, []string{"0000:0a:00.0", "0000:0b:00.0"}, rendered[0].DeviceBDFs)
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

const helmProfileGlob = "../../../deployments/nvml-mock/helm/nvml-mock/profiles/*.yaml"

// The runtime ConfigMap is rendered from the Helm profiles (see
// nvml-mock.gpuConfigBase in _helpers.tpl), and only those carry an
// infiniband block — the pkg/gpu/mocknvml/configs copies do not.
func TestCompileState_NetworkResolvedForEveryProfile(t *testing.T) {
	profiles, err := filepath.Glob(helmProfileGlob)
	require.NoError(t, err)
	require.NotEmpty(t, profiles, "no Helm profiles found")

	for _, path := range profiles {
		t.Run(filepath.Base(path), func(t *testing.T) {
			data, err := os.ReadFile(path)
			require.NoError(t, err)

			state, err := compileState(data)
			require.NoError(t, err)

			net := state.NodeShape.Network
			if !net.IBEnabled {
				require.Equal(t, agent.NetworkShape{}, net, "disabled IB must leave a zero shape")
				return
			}

			// Every field the renderer needs must be resolved here, so no
			// simulator downstream has to re-apply defaults.
			require.Positive(t, net.HCACount)
			require.NotEmpty(t, net.HCAType)
			require.NotEmpty(t, net.GUIDPrefix)
			require.NotEmpty(t, net.LinkLayer)
			require.NotEmpty(t, net.PortState)
			require.NotEmpty(t, net.PhysState)
			require.Positive(t, net.RateGbps)
		})
	}
}

func TestCompileState_NetworkFromProfile(t *testing.T) {
	data, err := os.ReadFile("../../../deployments/nvml-mock/helm/nvml-mock/profiles/gb200.yaml")
	require.NoError(t, err)

	state, err := compileState(data)
	require.NoError(t, err)

	net := state.NodeShape.Network
	require.True(t, net.IBEnabled)
	require.Equal(t, state.NodeShape.NumGPUs, net.HCACount, "gb200 sets hcas_per_gpu: 1")
	require.Equal(t, "MT4129", net.HCAType)
	require.Equal(t, "28.40.1000", net.FWVersion)
	require.Equal(t, "MT_0000000838", net.BoardID)
	require.Equal(t, "InfiniBand", net.LinkLayer)
	require.Equal(t, 400, net.RateGbps)
	require.Equal(t, "ACTIVE", net.PortState)
	require.Equal(t, "LinkUp", net.PhysState)
	require.Equal(t, "9b88c2:0300:ab", net.GUIDPrefix)
	require.Equal(t, "{node_name} mlx5_{idx}", net.NodeDescTemplate)
}

func TestCompileState_NetworkDisabled(t *testing.T) {
	// t4 sets infiniband.enabled: false; the mocknvml config omits the block
	// entirely. Both must compile to the same zero shape.
	cases := []struct{ name, path string }{
		{"explicitly disabled", "../../../deployments/nvml-mock/helm/nvml-mock/profiles/t4.yaml"},
		{"block absent", "../../../pkg/gpu/mocknvml/configs/mock-nvml-config-gb200.yaml"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			data, err := os.ReadFile(c.path)
			require.NoError(t, err)

			state, err := compileState(data)
			require.NoError(t, err)
			require.False(t, state.NodeShape.Network.IBEnabled)
			require.Equal(t, agent.NetworkShape{}, state.NodeShape.Network)
		})
	}
}

func TestCompileNetwork_HCACount(t *testing.T) {
	cases := []struct {
		name    string
		yaml    string
		numGPUs int
		want    int
	}{
		{
			name:    "derived from hcas_per_gpu",
			yaml:    "infiniband:\n  enabled: true\n  hcas_per_gpu: 2\n",
			numGPUs: 4,
			want:    8,
		},
		{
			// An explicit count wins: rail-optimized nodes pin HCAs independently
			// of how many GPUs the profile happens to expose.
			name:    "hca_count overrides the derived value",
			yaml:    "infiniband:\n  enabled: true\n  hcas_per_gpu: 2\n  hca_count: 3\n",
			numGPUs: 4,
			want:    3,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			net, err := compileNetwork([]byte(c.yaml), c.numGPUs)
			require.NoError(t, err)
			require.Equal(t, c.want, net.HCACount)
		})
	}
}

// A minimal block must still compile to a renderable shape, matching what
// render.Render would otherwise fill in via config.Infiniband.Defaults().
func TestCompileNetwork_AppliesDefaults(t *testing.T) {
	net, err := compileNetwork([]byte("infiniband:\n  enabled: true\n  hcas_per_gpu: 1\n"), 2)
	require.NoError(t, err)

	require.True(t, net.IBEnabled)
	require.Equal(t, 2, net.HCACount)
	require.NotEmpty(t, net.HCAType)
	require.NotEmpty(t, net.FWVersion)
	require.NotEmpty(t, net.GUIDPrefix)
	require.NotEmpty(t, net.LinkLayer)
	require.NotEmpty(t, net.PortState)
	require.NotEmpty(t, net.PhysState)
	require.Positive(t, net.RateGbps)
}

const topologyDoc = `domains:
  - uuid: 6f0e1b8a-0000-4000-8000-000000000001
    cliques:
      - id: 1
        nodes: [worker-0]
`

// sourceWith returns a FileSource over a gb200 profile and a topology path that
// the caller can create, edit, or leave absent.
func sourceWith(t *testing.T, topology string) (*FileSource, string) {
	t.Helper()

	dir := t.TempDir()
	profile, err := os.ReadFile("../../../pkg/gpu/mocknvml/configs/mock-nvml-config-gb200.yaml")
	require.NoError(t, err)
	configPath := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(configPath, profile, 0o600))

	topologyPath := filepath.Join(dir, "topology.yaml")
	if topology != "" {
		require.NoError(t, os.WriteFile(topologyPath, []byte(topology), 0o600))
	}

	return NewFileSource(configPath, topologyPath, zap.NewNop()), topologyPath
}

// poll is what the ticker calls, so driving it directly exercises the same
// change detection the running agent sees without waiting on the interval.
func pollOnce(t *testing.T, f *FileSource, lastHash *[32]byte) *agent.Update {
	t.Helper()

	ch := make(chan agent.Update, 1)
	f.poll(context.Background(), ch, lastHash)
	select {
	case u := <-ch:
		return &u
	default:
		return nil
	}
}

// The topology document reaches simulators through State, so nvlink never has
// to read the ConfigMap mount itself.
func TestFileSource_CarriesTheTopologyDocument(t *testing.T) {
	f, _ := sourceWith(t, topologyDoc)

	var hash [32]byte
	u := pollOnce(t, f, &hash)
	require.NotNil(t, u)
	require.NoError(t, u.Err)
	require.Equal(t, topologyDoc, string(u.State.TopologyRaw))
}

// The chart mounts the ConfigMap only when topology is enabled, so an absent
// document is an ordinary state, and it is what retracts a staged overlay.
func TestFileSource_AbsentTopologyIsEmptyNotAnError(t *testing.T) {
	f, _ := sourceWith(t, "")

	var hash [32]byte
	u := pollOnce(t, f, &hash)
	require.NotNil(t, u)
	require.NoError(t, u.Err)
	require.Empty(t, u.State.TopologyRaw)
}

// Editing the topology alone must reconcile: nothing in the profile changes, so
// hashing the profile by itself would leave workloads on the previous clique.
func TestFileSource_TopologyEditTriggersAReconcile(t *testing.T) {
	f, topologyPath := sourceWith(t, topologyDoc)

	var hash [32]byte
	require.NotNil(t, pollOnce(t, f, &hash), "initial poll emits")
	require.Nil(t, pollOnce(t, f, &hash), "an unchanged pair emits nothing")

	updated := topologyDoc + "      - id: 2\n        nodes: [worker-1]\n"
	require.NoError(t, os.WriteFile(topologyPath, []byte(updated), 0o600))

	u := pollOnce(t, f, &hash)
	require.NotNil(t, u, "a topology edit must emit an update")
	require.NoError(t, u.Err)
	require.Equal(t, updated, string(u.State.TopologyRaw))
}

// Deleting the ConfigMap is the retraction path, and it has to reconcile too.
func TestFileSource_TopologyRemovalTriggersAReconcile(t *testing.T) {
	f, topologyPath := sourceWith(t, topologyDoc)

	var hash [32]byte
	require.NotNil(t, pollOnce(t, f, &hash))

	require.NoError(t, os.Remove(topologyPath))

	u := pollOnce(t, f, &hash)
	require.NotNil(t, u, "a withdrawn topology must emit an update")
	require.NoError(t, u.Err)
	require.Empty(t, u.State.TopologyRaw)
}

// The chart omits --topology where the cluster declares none, so an unset path
// carries the same meaning as an unmounted ConfigMap.
func TestFileSource_UnsetTopologyPathIsEmptyNotAnError(t *testing.T) {
	f, _ := sourceWith(t, topologyDoc)
	f.topologyPath = ""

	var hash [32]byte
	u := pollOnce(t, f, &hash)
	require.NotNil(t, u)
	require.NoError(t, u.Err)
	require.Empty(t, u.State.TopologyRaw)
}

// A topology that cannot be read is a broken mount, not a cluster without
// topology; emitting an empty document would retract every node's overlay.
func TestFileSource_ReportsAnUnreadableTopology(t *testing.T) {
	f, topologyPath := sourceWith(t, topologyDoc)
	f.topologyPath = filepath.Join(topologyPath, "topology.yaml") // a file, not a directory

	var hash [32]byte
	u := pollOnce(t, f, &hash)
	require.NotNil(t, u)
	require.Error(t, u.Err)
	require.Nil(t, u.State)
}

// A profile that leaves minor_number out is saying the driver numbered the
// devices in index order. Compiling that to minor 0 would stage a single
// /dev/nvidia0 for the whole node.
func TestCompileState_MinorNumberDefaultsToIndex(t *testing.T) {
	cfg := `
version: "1.0"
system:
  driver_version: "550.163.01"
  num_devices: 4
devices:
  - index: 0
    uuid: "GPU-aaa"
  - index: 1
    uuid: "GPU-bbb"
`
	state, err := compileState([]byte(cfg))
	require.NoError(t, err)
	require.Len(t, state.Devices, 4)
	for i, d := range state.Devices {
		require.Equal(t, i, d.MinorNumber, "device %d", i)
	}
}

func TestCompileState_HonorsExplicitMinorNumbers(t *testing.T) {
	cfg := `
version: "1.0"
system:
  driver_version: "550.163.01"
  num_devices: 2
devices:
  - index: 0
    minor_number: 1
  - index: 1
    minor_number: 0
`
	state, err := compileState([]byte(cfg))
	require.NoError(t, err)
	require.Equal(t, 1, state.Devices[0].MinorNumber)
	require.Equal(t, 0, state.Devices[1].MinorNumber, "minor 0 on a device that is not index 0")
}

// The agent stages the character devices and writes both CDI specs, so a
// profile whose minor numbers collide has to be rejected here too — the engine
// refusing it later does not stop the nodes from being created.
func TestCompileState_RejectsCollidingMinorNumbers(t *testing.T) {
	cfg := `
version: "1.0"
system:
  driver_version: "550.163.01"
  num_devices: 2
devices:
  - index: 0
    minor_number: 1
  - index: 1
`
	_, err := compileState([]byte(cfg))
	require.ErrorContains(t, err, "duplicate device minor number: 1")
}

// The agent's device nodes and the engine's visibility filter have to agree on
// which minor belongs to which index, or a container is filtered against nodes
// that were never staged.
func TestCompileState_MinorNumbersAgreeWithTheEngine(t *testing.T) {
	cfg := `
version: "1.0"
system:
  driver_version: "550.163.01"
  num_devices: 4
devices:
  - index: 1
    minor_number: 3
  - index: 3
    minor_number: 1
`
	state, err := compileState([]byte(cfg))
	require.NoError(t, err)

	var yc engine.YAMLConfig
	require.NoError(t, yaml.Unmarshal([]byte(cfg), &yc))
	ec := &engine.Config{YAMLConfig: &yc}

	for _, d := range state.Devices {
		require.Equal(t, ec.GetDeviceMinorNumber(d.Index), d.MinorNumber, "device %d", d.Index)
	}
}

func TestCompileState_MIGFromProfile(t *testing.T) {
	data := []byte(`
version: "1.0"
system:
  num_devices: 2
device_defaults:
  name: "NVIDIA A100-SXM4-40GB"
  memory:
    total_bytes: 42949672960
  mig:
    mode_current: "enabled"
    mode_pending: "enabled"
    max_gpu_instances: 7
    gpu_instances:
      - profile: "1g.5gb"
        count: 3
`)

	state, err := compileState(data)
	require.NoError(t, err)

	require.True(t, state.MIG.Partitioned())
	require.Equal(t, 236, state.MIG.CapsMajor)
	require.Len(t, state.MIG.GPUs, 2)
	for i, gpu := range state.MIG.GPUs {
		// The capability names key on the GPU's device-node minor, which this
		// profile leaves implicit, so it follows the index.
		require.Equal(t, i, gpu.Minor)
		require.Len(t, gpu.GPUInstances, 3)
		for _, gi := range gpu.GPUInstances {
			require.Len(t, gi.ComputeInstances, 1)
			require.Equal(t, uint32(0), gi.ComputeInstances[0].ID)
			require.NotEmpty(t, gi.ComputeInstances[0].UUID)
		}
	}
}

// TestCompileState_MIGDisabledInEveryShippedProfile pins the deliberate default:
// a MIG-capable profile declares what it could be partitioned into but boots
// with MIG off, because migStrategy=single stops publishing nvidia.com/gpu the
// moment a board is partitioned, which would change every existing e2e leg.
func TestCompileState_MIGDisabledInEveryShippedProfile(t *testing.T) {
	profiles, err := filepath.Glob(helmProfileGlob)
	require.NoError(t, err)
	require.NotEmpty(t, profiles)

	for _, path := range profiles {
		if strings.Contains(filepath.Base(path), "-mig") {
			continue // the MIG profiles exist precisely to boot partitioned
		}
		t.Run(filepath.Base(path), func(t *testing.T) {
			data, err := os.ReadFile(path)
			require.NoError(t, err)

			state, err := compileState(data)
			require.NoError(t, err)
			require.False(t, state.MIG.Partitioned(),
				"%s must boot unpartitioned", filepath.Base(path))
		})
	}
}

// TestCompileState_DeclaredMIGPartitionsAllResolve guards the profiles against
// a typo in a partition name. An unresolvable name is only warned about and
// skipped, so without this the profile would quietly produce fewer partitions
// than it declares — visible only as a smaller allocatable count in a cluster.
func TestCompileState_DeclaredMIGPartitionsAllResolve(t *testing.T) {
	profiles, err := filepath.Glob(helmProfileGlob)
	require.NoError(t, err)
	require.NotEmpty(t, profiles)

	sawPartitionedProfile := false
	for _, path := range profiles {
		data, err := os.ReadFile(path)
		require.NoError(t, err)

		var cfg engine.YAMLConfig
		require.NoError(t, yaml.Unmarshal(data, &cfg))
		if cfg.DeviceDefaults.MIG == nil || len(cfg.DeviceDefaults.MIG.GPUInstances) == 0 {
			continue
		}
		sawPartitionedProfile = true

		t.Run(filepath.Base(path), func(t *testing.T) {
			declared := 0
			for _, gi := range cfg.DeviceDefaults.MIG.GPUInstances {
				declared += max(gi.Count, 1)
			}

			// Enable MIG the way gpu.mig.enabled does, since the layout is
			// inert while the profile leaves the mode off.
			cfg.DeviceDefaults.MIG.ModeCurrent = "enabled"
			layout := engine.DeclaredMIGLayout(&engine.Config{NumDevices: 1, YAMLConfig: &cfg})

			require.Len(t, layout, 1)
			require.Len(t, layout[0].GPUInstances, declared,
				"every declared partition must resolve and fit")
			for _, gi := range layout[0].GPUInstances {
				require.NotEmpty(t, gi.Profile)
				require.NotEmpty(t, gi.ComputeInstances)
			}
		})
	}
	require.True(t, sawPartitionedProfile, "no profile declares MIG partitions; has the block moved?")
}

// A profile is free to number the device nodes independently of the NVML
// index, and the MIG capability names have to follow the nodes gpudriver
// actually creates. Keying them on the index instead would point a consumer
// at another GPU's cap devices on exactly the profiles that renumber.
func TestCompileState_MIGCapsFollowTheDeviceMinor(t *testing.T) {
	data := []byte(`
version: "1.0"
system:
  num_devices: 2
device_defaults:
  name: "NVIDIA A100-SXM4-40GB"
  memory:
    total_bytes: 42949672960
  mig:
    mode_current: "enabled"
    mode_pending: "enabled"
    gpu_instances:
      - profile: "1g.5gb"
        count: 1
devices:
  - index: 0
    minor_number: 5
  - index: 1
    minor_number: 4
`)

	state, err := compileState(data)
	require.NoError(t, err)
	require.True(t, state.MIG.Partitioned())
	require.Len(t, state.MIG.GPUs, 2)

	minors := []int{state.MIG.GPUs[0].Minor, state.MIG.GPUs[1].Minor}
	require.Equal(t, []int{5, 4}, minors,
		"cap names must use the profile's minor numbers, not the NVML indices")
}
