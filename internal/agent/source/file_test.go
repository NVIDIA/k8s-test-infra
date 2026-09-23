// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package source

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

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

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	fs := NewFileSource(configs[0], filepath.Join(t.TempDir(), "topology.yaml"), 0, zap.NewNop())
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

	return NewFileSource(configPath, topologyPath, 0, zap.NewNop()), topologyPath
}

// reload is what both an event and the resync call, so driving it directly
// exercises the same change detection without waiting on either.
func reloadOnce(t *testing.T, f *FileSource, lastHash *[32]byte) *agent.Update {
	t.Helper()

	ch := make(chan agent.Update, 1)
	f.reload(t.Context(), ch, lastHash)
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
	u := reloadOnce(t, f, &hash)
	require.NotNil(t, u)
	require.NoError(t, u.Err)
	require.Equal(t, topologyDoc, string(u.State.TopologyRaw))
}

// The chart mounts the ConfigMap only when topology is enabled, so an absent
// document is an ordinary state, and it is what retracts a staged overlay.
func TestFileSource_AbsentTopologyIsEmptyNotAnError(t *testing.T) {
	f, _ := sourceWith(t, "")

	var hash [32]byte
	u := reloadOnce(t, f, &hash)
	require.NotNil(t, u)
	require.NoError(t, u.Err)
	require.Empty(t, u.State.TopologyRaw)
}

// Editing the topology alone must reconcile: nothing in the profile changes, so
// hashing the profile by itself would leave workloads on the previous clique.
func TestFileSource_TopologyEditTriggersAReconcile(t *testing.T) {
	f, topologyPath := sourceWith(t, topologyDoc)

	var hash [32]byte
	require.NotNil(t, reloadOnce(t, f, &hash), "the initial reload emits")
	require.Nil(t, reloadOnce(t, f, &hash), "an unchanged pair emits nothing")

	updated := topologyDoc + "      - id: 2\n        nodes: [worker-1]\n"
	require.NoError(t, os.WriteFile(topologyPath, []byte(updated), 0o600))

	u := reloadOnce(t, f, &hash)
	require.NotNil(t, u, "a topology edit must emit an update")
	require.NoError(t, u.Err)
	require.Equal(t, updated, string(u.State.TopologyRaw))
}

// Deleting the ConfigMap is the retraction path, and it has to reconcile too.
func TestFileSource_TopologyRemovalTriggersAReconcile(t *testing.T) {
	f, topologyPath := sourceWith(t, topologyDoc)

	var hash [32]byte
	require.NotNil(t, reloadOnce(t, f, &hash))

	require.NoError(t, os.Remove(topologyPath))

	u := reloadOnce(t, f, &hash)
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
	u := reloadOnce(t, f, &hash)
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
	u := reloadOnce(t, f, &hash)
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

// profileBytes returns a real profile and an edited copy. The edit is a
// trailing comment: it moves the bytes the source hashes while leaving every
// field compileState reads exactly as the fixture has them, so an assertion on
// the raw document does not depend on GPU_COUNT or DRIVER_VERSION.
func profileBytes(t *testing.T) (original, edited []byte) {
	t.Helper()

	original, err := os.ReadFile("../../../pkg/gpu/mocknvml/configs/mock-nvml-config-gb200.yaml")
	require.NoError(t, err)

	return original, []byte(string(original) + "\n# edited\n")
}

// watchedSource subscribes and consumes the state emitted on subscribe, leaving
// the caller ready to edit. The resync is disabled, so an update the caller
// then observes proves a filesystem event delivered it.
func watchedSource(t *testing.T, configPath, topologyPath string) <-chan agent.Update {
	t.Helper()

	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)

	ch := NewFileSource(configPath, topologyPath, 0, zap.NewNop()).Watch(ctx)
	u := <-ch
	require.NoError(t, u.Err)

	return ch
}

// nextUpdate waits for the update an edit should produce. The budget is
// generous for CI's sake; the watch fires within milliseconds or not at all.
func nextUpdate(t *testing.T, ch <-chan agent.Update) agent.Update {
	t.Helper()

	select {
	case u := <-ch:
		return u
	case <-time.After(10 * time.Second):
		require.FailNow(t, "the edit never reached the watch")
		return agent.Update{}
	}
}

// swapConfigMap performs the update the kubelet performs: a new timestamped
// directory holding the content, then a rename of ..data onto it. It leaves the
// superseded directory in place, though the kubelet removes it, so that a watch
// which only recovers once the old content is unlinked still fails here.
func swapConfigMap(t *testing.T, dir, stamp string, data []byte) {
	t.Helper()

	versioned := filepath.Join(dir, stamp)
	require.NoError(t, os.Mkdir(versioned, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(versioned, "config.yaml"), data, 0o600))

	tmp := filepath.Join(dir, "..data_tmp")
	require.NoError(t, os.Symlink(stamp, tmp))
	require.NoError(t, os.Rename(tmp, filepath.Join(dir, "..data")))

}

// A hostPath mount and a local `go run` both write the profile in place.
func TestFileSource_EmitsOnConfigWrite(t *testing.T) {
	t.Parallel()

	original, edited := profileBytes(t)
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(configPath, original, 0o600))

	ch := watchedSource(t, configPath, "")
	require.NoError(t, os.WriteFile(configPath, edited, 0o600))

	u := nextUpdate(t, ch)
	require.NoError(t, u.Err)
	require.Equal(t, string(edited), string(u.State.ConfigRaw))
}

// vim, sed -i and Ansible all save by renaming a temporary file over the
// target, replacing the inode that a watch on the file itself would hold.
func TestFileSource_EmitsOnAtomicRename(t *testing.T) {
	t.Parallel()

	original, edited := profileBytes(t)
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(configPath, original, 0o600))

	ch := watchedSource(t, configPath, "")

	tmp := filepath.Join(dir, "config.yaml.tmp")
	require.NoError(t, os.WriteFile(tmp, edited, 0o600))
	require.NoError(t, os.Rename(tmp, configPath))

	u := nextUpdate(t, ch)
	require.NoError(t, u.Err)
	require.Equal(t, string(edited), string(u.State.ConfigRaw))
}

// Pointing --config at a symlink into a directory of profiles is how a host
// switches between them, and the edit lands in a directory the literal path
// never names.
func TestFileSource_EmitsWhenConfigIsASymlinkElsewhere(t *testing.T) {
	t.Parallel()

	original, edited := profileBytes(t)
	etc, profiles := t.TempDir(), t.TempDir()

	target := filepath.Join(profiles, "gb200.yaml")
	require.NoError(t, os.WriteFile(target, original, 0o600))
	configPath := filepath.Join(etc, "config.yaml")
	require.NoError(t, os.Symlink(target, configPath))

	ch := watchedSource(t, configPath, "")
	require.NoError(t, os.WriteFile(target, edited, 0o600))

	u := nextUpdate(t, ch)
	require.NoError(t, u.Err)
	require.Equal(t, string(edited), string(u.State.ConfigRaw))
}

// Kubernetes renames ..data onto a fresh directory and deletes the old one, so
// the leaf the agent reads never changes and the inode behind it does.
func TestFileSource_EmitsOnConfigMapSymlinkSwap(t *testing.T) {
	t.Parallel()

	original, edited := profileBytes(t)
	dir := t.TempDir()

	swapConfigMap(t, dir, "..2026_09_09_12_00_00.000000001", original)
	configPath := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.Symlink("..data/config.yaml", configPath))

	ch := watchedSource(t, configPath, "")
	swapConfigMap(t, dir, "..2026_09_09_12_00_01.000000002", edited)

	u := nextUpdate(t, ch)
	require.NoError(t, u.Err)
	require.Equal(t, string(edited), string(u.State.ConfigRaw))
}

// The chart mounts the topology ConfigMap in its own directory, so it needs a
// watch of its own rather than riding along on the profile's.
func TestFileSource_EmitsOnTopologyEdit(t *testing.T) {
	t.Parallel()

	original, _ := profileBytes(t)
	configDir, topologyDir := t.TempDir(), t.TempDir()

	configPath := filepath.Join(configDir, "config.yaml")
	require.NoError(t, os.WriteFile(configPath, original, 0o600))
	topologyPath := filepath.Join(topologyDir, "topology.yaml")
	require.NoError(t, os.WriteFile(topologyPath, []byte(topologyDoc), 0o600))

	ch := watchedSource(t, configPath, topologyPath)

	updated := topologyDoc + "      - id: 2\n        nodes: [worker-1]\n"
	require.NoError(t, os.WriteFile(topologyPath, []byte(updated), 0o600))

	u := nextUpdate(t, ch)
	require.NoError(t, u.Err)
	require.Equal(t, updated, string(u.State.TopologyRaw))
}

// Repointing the symlink is how a host switches profiles, and it moves the
// directory later edits arrive in, so the watch has to move with it. Only
// inotify reports a symlink being replaced in a watched directory; kqueue
// follows the link when it opens the entry and never sees the swap, so on a
// developer's macOS the resync is what catches this.
func TestFileSource_FollowsARetargetedSymlink(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("only inotify reports a symlink swap inside a watched directory")
	}
	t.Parallel()

	original, edited := profileBytes(t)
	replacement := []byte(string(original) + "\n# replacement\n")
	etc, before, after := t.TempDir(), t.TempDir(), t.TempDir()

	initial := filepath.Join(before, "gb200.yaml")
	require.NoError(t, os.WriteFile(initial, original, 0o600))
	configPath := filepath.Join(etc, "config.yaml")
	require.NoError(t, os.Symlink(initial, configPath))

	ch := watchedSource(t, configPath, "")

	// Retarget into a directory nothing has watched yet.
	target := filepath.Join(after, "h100.yaml")
	require.NoError(t, os.WriteFile(target, replacement, 0o600))
	tmp := filepath.Join(etc, "config.yaml.tmp")
	require.NoError(t, os.Symlink(target, tmp))
	require.NoError(t, os.Rename(tmp, configPath))

	u := nextUpdate(t, ch)
	require.NoError(t, u.Err)
	require.Equal(t, string(replacement), string(u.State.ConfigRaw))

	// Editing the new target reaches the source only if the reload above moved
	// the watch to the directory holding it.
	require.NoError(t, os.WriteFile(target, edited, 0o600))

	u = nextUpdate(t, ch)
	require.NoError(t, u.Err)
	require.Equal(t, string(edited), string(u.State.ConfigRaw))
}
