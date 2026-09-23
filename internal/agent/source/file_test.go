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
	configs := shippedConfigs(t)

	for _, path := range configs {
		t.Run(filepath.Base(path), func(t *testing.T) {
			data, err := os.ReadFile(path)
			require.NoError(t, err)

			state, err := compileState(data, path)
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

	state, err := compileState(data, "../../../pkg/gpu/mocknvml/configs/mock-nvml-config-gb200.yaml")
	require.NoError(t, err)

	require.True(t, state.Fabric.Enabled, "gb200 fabric should be enabled")
	require.Positive(t, state.Fabric.LinksPerGPU)
}

// The state dir comes from the environment, not the profile: NVLink in the
// config says nothing about whether fabricmanager runs on the node.
func TestCompileState_ManagerStateDir(t *testing.T) {
	data, err := os.ReadFile("../../../pkg/gpu/mocknvml/configs/mock-nvml-config-gb200.yaml")
	require.NoError(t, err)

	state, err := compileState(data, "../../../pkg/gpu/mocknvml/configs/mock-nvml-config-gb200.yaml")
	require.NoError(t, err)
	require.Empty(t, state.Fabric.ManagerStateDir)

	t.Setenv(engine.EnvFabricStateDir, " /var/lib/nvml-mock/fabric-state ")
	state, err = compileState(data, "../../../pkg/gpu/mocknvml/configs/mock-nvml-config-gb200.yaml")
	require.NoError(t, err)
	require.Equal(t, "/var/lib/nvml-mock/fabric-state", state.Fabric.ManagerStateDir)
}

func TestFileSource_EmitsInitialState(t *testing.T) {
	configs := shippedConfigs(t)
	require.NotEmpty(t, configs, "no configs found")

	ctx, cancel := context.WithCancel(t.Context())
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

	state, err := compileState(data, "../../../pkg/gpu/mocknvml/configs/mock-nvml-config-h100.yaml")
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
	configs := shippedConfigs(t)

	for _, path := range configs {
		t.Run(filepath.Base(path), func(t *testing.T) {
			data, err := os.ReadFile(path)
			require.NoError(t, err)

			state, err := compileState(data, path)
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
	path := filepath.Join("..", "..", "..", "pkg", "gpu", "mocknvml",
		"configs", "mock-nvml-config-gb300.yaml")
	data, err := os.ReadFile(path)
	require.NoError(t, err)

	state, err := compileState(data, path)
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
	state, err := compileState([]byte(cfg), "")
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

// shippedConfigs lists the standalone board configs, without the MIG tables
// that sit beside them. A mock-nvml-config-<board>.mig.yaml is one board's
// partition table, not a config, and compiles to no node state at all.
func shippedConfigs(t *testing.T) []string {
	t.Helper()

	matched, err := filepath.Glob("../../../pkg/gpu/mocknvml/configs/mock-nvml-config-*.yaml")
	require.NoError(t, err)

	configs := make([]string, 0, len(matched))
	for _, path := range matched {
		if !strings.HasSuffix(path, ".mig.yaml") {
			configs = append(configs, path)
		}
	}
	require.NotEmpty(t, configs, "no config YAMLs found")
	return configs
}

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

			state, err := compileState(data, path)
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

	state, err := compileState(data, "../../../deployments/nvml-mock/helm/nvml-mock/profiles/gb200.yaml")
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

			state, err := compileState(data, c.path)
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
	f.poll(t.Context(), ch, lastHash)
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
	state, err := compileState([]byte(cfg), "")
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
	state, err := compileState([]byte(cfg), "")
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
	_, err := compileState([]byte(cfg), "")
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
	state, err := compileState([]byte(cfg), "")
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
  driver_version: "550.163.01"
  num_devices: 2
device_defaults:
  name: "NVIDIA A100-SXM4-40GB"
  memory:
    total_bytes: 42949672960
  mig:
    mode_current: "enabled"
    mode_pending: "enabled"
    max_gpu_instances: 7
    # A board is MIG-capable by declaring its profile table, so the row this
    # layout partitions with has to be in it.
    supported_profiles:
      - name: "1g.5gb"
        nvml_profile: "1_SLICE"
        profile_id: 19
        instances: 7
        memory_mb: 4864
        multiprocessors: 14
        copy_engines: 1
        placements:
          - {start: 0, size: 1}
          - {start: 1, size: 1}
          - {start: 2, size: 1}
          - {start: 3, size: 1}
          - {start: 4, size: 1}
          - {start: 5, size: 1}
          - {start: 6, size: 1}
        compute_instances:
          - nvml_profile: "1_SLICE"
            slices: 1
            instances: 1
            multiprocessors: 14
            shared_copy_engines: 1
    gpu_instances:
      - profile: "1g.5gb"
        count: 3
`)

	state, err := compileState(data, "")
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

// migLayoutWithoutTable is a MIG-capable board that declares a layout and no
// partition table, which is what the chart renders now that the table is a
// document of its own. The name it partitions with is a row of the shipped
// gb200 table, so the two files only compile together.
const migLayoutWithoutTable = `
version: "1.0"
system:
  driver_version: "580.65.06"
  num_devices: 1
device_defaults:
  name: "NVIDIA GB200"
  memory:
    total_bytes: 193273528320
  mig:
    mode_current: "enabled"
    mode_pending: "enabled"
    max_gpu_instances: 7
    gpu_instances:
      - profile: "1g.23gb"
        count: 7
`

// migSiblingLayout writes that config and the shipped gb200 table into a
// directory as the sibling pair, and returns the config path.
func migSiblingLayout(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte(migLayoutWithoutTable), 0o600))

	return configPath
}

// migWriteSiblingTable puts the shipped gb200 partition table beside configPath.
func migWriteSiblingTable(t *testing.T, configPath string) {
	t.Helper()

	table, err := os.ReadFile("../../../pkg/gpu/mocknvml/configs/mock-nvml-config-gb200.mig.yaml")
	require.NoError(t, err)

	sibling := strings.TrimSuffix(configPath, filepath.Ext(configPath)) + ".mig.yaml"
	require.NoError(t, os.WriteFile(sibling, table, 0o600))
}

// The agent has to resolve the partition table the same way the engine does.
// Compiling the profile alone leaves a MIG-capable board looking unpartitionable,
// and the symptom is remote from the cause: NVML enumerates the partitions
// correctly while the node stages no capability table at all, so a consumer
// finds a MIG device it has no cap node to reach.
func TestCompileState_MIGTableFromASiblingDocument(t *testing.T) {
	t.Parallel()

	configPath := migSiblingLayout(t)
	migWriteSiblingTable(t, configPath)

	state, err := compileState([]byte(migLayoutWithoutTable), configPath)
	require.NoError(t, err)

	require.True(t, state.MIG.Partitioned(),
		"a board whose table sits in a sibling document must still partition")
	require.Len(t, state.MIG.GPUs, 1)
	require.Len(t, state.MIG.GPUs[0].GPUInstances, 7,
		"every partition the layout asks for must resolve against the sibling table")
}

// The table is a second mount, so it can arrive after the first poll. Hashing
// the config alone would latch the unpartitioned compile: the config never
// changes afterwards, so no later poll would reconcile and the node would stage
// no capability table for the rest of the pod's life.
func TestFileSource_MIGTableArrivingLateTriggersAReconcile(t *testing.T) {
	t.Parallel()

	configPath := migSiblingLayout(t)
	f := NewFileSource(configPath, filepath.Join(filepath.Dir(configPath), "topology.yaml"), zap.NewNop())

	var hash [32]byte
	u := pollOnce(t, f, &hash)
	require.NotNil(t, u, "initial poll emits")
	require.NoError(t, u.Err)
	require.False(t, u.State.MIG.Partitioned(), "no table is mounted yet")

	migWriteSiblingTable(t, configPath)

	u = pollOnce(t, f, &hash)
	require.NotNil(t, u, "a table that arrives late must emit an update")
	require.NoError(t, u.Err)
	require.True(t, u.State.MIG.Partitioned())
}

// A table that cannot be read is not a board without one. The distinction
// matters here rather than in the engine because this is where it turns
// destructive: gpudriver withdraws the staged table whenever the state it is
// given carries none, so compiling an unreadable mount as "unpartitioned"
// would take the partition table and the capability nodes away from a node
// that is serving partitions. An error leaves the last good state in place.
func TestFileSource_AnUnreadableMIGTableDoesNotWithdrawThePartitions(t *testing.T) {
	t.Parallel()

	configPath := migSiblingLayout(t)
	migWriteSiblingTable(t, configPath)

	f := NewFileSource(configPath, filepath.Join(filepath.Dir(configPath), "topology.yaml"), zap.NewNop())

	var hash [32]byte
	u := pollOnce(t, f, &hash)
	require.NotNil(t, u)
	require.NoError(t, u.Err)
	require.True(t, u.State.MIG.Partitioned(), "the board starts out partitioned")

	// A symlink loop in place of the table: the name still resolves as
	// present, so this is not absence, and the lookup fails with ELOOP rather
	// than "not found". Deterministic, and unlike an unreadable directory it
	// does not depend on the uid the tests run as.
	sibling := strings.TrimSuffix(configPath, filepath.Ext(configPath)) + ".mig.yaml"
	require.NoError(t, os.Remove(sibling))
	require.NoError(t, os.Symlink(sibling, sibling))

	u = pollOnce(t, f, &hash)
	require.NotNil(t, u, "an unreadable table must emit rather than go quiet")
	require.Error(t, u.Err, "an unreadable table must fail the compile, not partition the board away")
	require.Nil(t, u.State, "no state means gpudriver stages nothing and withdraws nothing")
}

// The same rule as the unreadable table, one step further in: a profile that
// reads back fine and does not validate is still not a board to reconfigure
// the node onto. gpudriver stages whatever state it is handed, so emitting a
// nil state with the error is what keeps the partitions, the capability nodes
// and the CDI entries of the last good profile in place.
func TestFileSource_AProfileTheLibraryWouldRefuseDoesNotReplaceTheLastGoodOne(t *testing.T) {
	t.Parallel()

	configPath := migSiblingLayout(t)
	migWriteSiblingTable(t, configPath)

	f := NewFileSource(configPath, filepath.Join(filepath.Dir(configPath), "topology.yaml"), zap.NewNop())

	var hash [32]byte
	u := pollOnce(t, f, &hash)
	require.NotNil(t, u)
	require.NoError(t, u.Err)
	require.True(t, u.State.MIG.Partitioned(), "the board starts out partitioned")

	refused := strings.Replace(migLayoutWithoutTable, "  driver_version: \"580.65.06\"\n", "", 1)
	require.NoError(t, os.WriteFile(configPath, []byte(refused), 0o600))

	u = pollOnce(t, f, &hash)
	require.NotNil(t, u, "a profile that cannot be staged must emit rather than go quiet")
	require.Error(t, u.Err, "the library would refuse this profile and fall back to its defaults")
	require.Nil(t, u.State, "no state means gpudriver stages nothing and withdraws nothing")
}

// The agent stages the very document the mock library later loads, and the
// library answers a profile it cannot validate by falling back to its built-in
// eight-A100 default. A profile the agent accepts and the library refuses
// therefore does not surface as an error anywhere: the node simulates hardware
// that the character devices, capability nodes and CDI entries staged beside it
// do not describe. The two verdicts have to be the same verdict.
func TestCompileState_AcceptsExactlyWhatTheLibraryAccepts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		doc  string
	}{
		{
			name: "the layout the chart renders",
			doc:  migLayoutWithoutTable,
		},
		{
			// Naming a row twice leaves no single row selected.
			name: "an entry carrying both profile selectors",
			doc: strings.Replace(migLayoutWithoutTable,
				`      - profile: "1g.23gb"`,
				"      - profile: \"1g.23gb\"\n        profile_id: 19", 1),
		},
		{
			name: "no driver version",
			doc:  strings.Replace(migLayoutWithoutTable, "  driver_version: \"580.65.06\"\n", "", 1),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			configPath := filepath.Join(t.TempDir(), "config.yaml")
			require.NoError(t, os.WriteFile(configPath, []byte(tt.doc), 0o600))
			migWriteSiblingTable(t, configPath)

			_, libErr := engine.LoadYAMLConfig(configPath)
			_, agentErr := compileState([]byte(tt.doc), configPath)

			if libErr == nil {
				require.NoError(t, agentErr, "the library loads this profile, so the agent must compile it")

				return
			}
			require.Error(t, agentErr,
				"the library refuses this profile (%v) and would fall back to its defaults, so the agent must not stage it", libErr)
		})
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

			state, err := compileState(data, path)
			require.NoError(t, err)
			require.False(t, state.MIG.Partitioned(),
				"%s must boot unpartitioned", filepath.Base(path))
		})
	}
}

// chartMIGLayouts is the partitioning gpu.mig.gpuInstances documents for each
// MIG-capable board: the board filled with its smallest slice. No profile
// declares a layout of its own, so these are what an install actually asks
// for, and what the test below drives.
var chartMIGLayouts = map[string]engine.MIGGPUInstanceConfig{
	"a100":  {Profile: "1g.5gb", Count: 7},
	"h100":  {Profile: "1g.10gb", Count: 7},
	"b200":  {Profile: "1g.23gb", Count: 7},
	"gb200": {Profile: "1g.23gb", Count: 7},
	"gb300": {Profile: "1g.35gb", Count: 7},
}

// TestCompileState_ChartMIGLayoutsAllResolve guards the documented layouts
// against a partition name the board's MIG table does not know. An
// unresolvable name is only warned about and skipped, so without this an
// install would quietly get fewer partitions than it asked for — visible only
// as a smaller allocatable count in a cluster.
//
// It drives compileState, and mounts the table the way the chart does, so the
// agent's own resolution is what is under test. Attaching the table here
// instead would prove the two documents agree while saying nothing about
// whether the agent ever joins them.
func TestCompileState_ChartMIGLayoutsAllResolve(t *testing.T) {
	profiles, err := filepath.Glob(helmProfileGlob)
	require.NoError(t, err)
	require.NotEmpty(t, profiles)

	covered := 0
	for _, path := range profiles {
		name := strings.TrimSuffix(filepath.Base(path), ".yaml")
		want, capable := chartMIGLayouts[name]
		if !capable {
			continue
		}
		covered++

		t.Run(name, func(t *testing.T) {
			data, err := os.ReadFile(path)
			require.NoError(t, err)

			var cfg engine.YAMLConfig
			require.NoError(t, yaml.Unmarshal(data, &cfg))
			require.NotNil(t, cfg.DeviceDefaults.MIG, "%s should be a MIG-capable board", name)

			// What the chart renders for gpu.mig.enabled=true with
			// gpu.mig.gpuInstances: the mode on, the layout supplied.
			cfg.DeviceDefaults.MIG.ModeCurrent = "enabled"
			cfg.DeviceDefaults.MIG.GPUInstances = []engine.MIGGPUInstanceConfig{want}
			enabled, err := yaml.Marshal(&cfg)
			require.NoError(t, err)

			table := filepath.Join(filepath.Dir(path), "mig", name+".yaml")
			require.FileExists(t, table, "%s declares a mig block but ships no table", name)
			t.Setenv(engine.EnvMIGProfilesConfig, table)

			state, err := compileState(enabled, path)
			require.NoError(t, err)

			require.NotEmpty(t, state.MIG.GPUs)
			for _, gpu := range state.MIG.GPUs {
				require.Len(t, gpu.GPUInstances, want.Count,
					"every partition asked for must resolve and fit")
				for _, gi := range gpu.GPUInstances {
					require.NotEmpty(t, gi.ComputeInstances)
				}
			}
		})
	}
	require.Equal(t, len(chartMIGLayouts), covered,
		"a board in chartMIGLayouts has no chart profile of that name")
}

// A profile is free to number the device nodes independently of the NVML
// index, and the MIG capability names have to follow the nodes gpudriver
// actually creates. Keying them on the index instead would point a consumer
// at another GPU's cap devices on exactly the profiles that renumber.
func TestCompileState_MIGCapsFollowTheDeviceMinor(t *testing.T) {
	data := []byte(`
version: "1.0"
system:
  driver_version: "550.163.01"
  num_devices: 2
device_defaults:
  name: "NVIDIA A100-SXM4-40GB"
  memory:
    total_bytes: 42949672960
  mig:
    mode_current: "enabled"
    mode_pending: "enabled"
    max_gpu_instances: 7
    supported_profiles:
      - name: "1g.5gb"
        nvml_profile: "1_SLICE"
        profile_id: 19
        instances: 7
        memory_mb: 4864
        multiprocessors: 14
        copy_engines: 1
        placements:
          - {start: 0, size: 1}
          - {start: 1, size: 1}
          - {start: 2, size: 1}
          - {start: 3, size: 1}
          - {start: 4, size: 1}
          - {start: 5, size: 1}
          - {start: 6, size: 1}
        compute_instances:
          - nvml_profile: "1_SLICE"
            slices: 1
            instances: 1
            multiprocessors: 14
            shared_copy_engines: 1
    gpu_instances:
      - profile: "1g.5gb"
        count: 1
devices:
  - index: 0
    minor_number: 5
  - index: 1
    minor_number: 4
`)

	state, err := compileState(data, "")
	require.NoError(t, err)
	require.True(t, state.MIG.Partitioned())
	require.Len(t, state.MIG.GPUs, 2)

	minors := []int{state.MIG.GPUs[0].Minor, state.MIG.GPUs[1].Minor}
	require.Equal(t, []int{5, 4}, minors,
		"cap names must use the profile's minor numbers, not the NVML indices")
}
