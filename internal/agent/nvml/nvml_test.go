// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package nvml

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/NVIDIA/k8s-test-infra/internal/fsutil"

	"github.com/stretchr/testify/require"
	"sigs.k8s.io/yaml"

	"github.com/NVIDIA/k8s-test-infra/internal/agent"
	"github.com/NVIDIA/k8s-test-infra/internal/agent/host"
	"github.com/NVIDIA/k8s-test-infra/pkg/gpu/mocknvml/engine"
)

// testState returns a minimal State for nvml tests with a real engine YAML.
func testState(t *testing.T) *agent.State {
	t.Helper()
	cfgPath := filepath.Join("..", "..", "..", "pkg", "gpu", "mocknvml", "configs", "mock-nvml-config-a100.yaml")
	data, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	return &agent.State{
		Software:  agent.SoftwareVersions{DriverVersion: "550.163.01"},
		Devices:   []agent.DeviceSpec{{Index: 0, MinorNumber: 0}},
		ConfigRaw: data,
	}
}

func testHost(t *testing.T) *host.Host {
	t.Helper()
	return host.New(t.TempDir())
}

// skipUnlessRootLinux skips the test on any platform where mknod requires root.
func skipUnlessRootLinux(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "linux" || os.Getuid() != 0 {
		t.Skip("requires root on Linux (mknod)")
	}
}

// skipUnlessNVMLLib skips the test when the NVML shim .so is not installed.
func skipUnlessNVMLLib(t *testing.T) {
	t.Helper()
	matches, _ := filepath.Glob("/usr/local/lib/libnvidia-ml.so.*.*.*")
	if len(matches) == 0 {
		t.Skip("libnvidia-ml.so not installed")
	}
}

// ─── individual surface tests ────────────────────────────────────────────────

func TestWriteEngineConfig_WritesBothLocations(t *testing.T) {
	h := testHost(t)
	state := testState(t)

	require.NoError(t, writeEngineConfig(t.Context(), h, state))

	for _, rel := range []string{"config/config.yaml", "driver/config/config.yaml"} {
		_, err := os.Stat(h.RootPath(rel))
		require.NoError(t, err, "%s must exist", rel)
	}
}

// NVML's CPU affinity and the driver's local_cpulist are read by different
// consumers (Slurm's AutoDetect=nvml and AutoDetect=nvidia) for the same fact,
// so the staged config carries the same host-limited CPU set the PCI stager
// serves; see TestBuildTopology_CPUListKeepsOnlyTheHostsCPUs.
func TestWriteEngineConfig_LimitsCPUAffinityToTheHostsCPUs(t *testing.T) {
	t.Parallel()

	stagedAffinity := func(t *testing.T, online string) []string {
		t.Helper()
		h := testHost(t)
		if online != "" {
			path := h.SysPath("devices/system/cpu/online")
			require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
			require.NoError(t, os.WriteFile(path, []byte(online+"\n"), 0o644))
		}
		data, err := os.ReadFile(filepath.Join("..", "..", "..", "pkg", "gpu", "mocknvml", "configs", "mock-nvml-config-gb300.yaml"))
		require.NoError(t, err)
		state := &agent.State{Devices: []agent.DeviceSpec{{Index: 0}}, ConfigRaw: data}

		require.NoError(t, writeEngineConfig(t.Context(), h, state))

		staged, err := os.ReadFile(h.RootPath("config/config.yaml"))
		require.NoError(t, err)
		var cfg engine.YAMLConfig
		require.NoError(t, yaml.Unmarshal(staged, &cfg))
		got := make([]string, 0, len(cfg.PCIeTopology.RootComplexes))
		for _, rc := range cfg.PCIeTopology.RootComplexes {
			got = append(got, rc.CPUAffinity)
		}
		return got
	}

	require.Equal(t, []string{"0-13", "0-13"}, stagedAffinity(t, "0-13"),
		"no NUMA 1 CPU exists, so every CPU is local")
	require.Equal(t, []string{"0-63", "64-99"}, stagedAffinity(t, "0-99"))
	require.Equal(t, []string{"", ""}, stagedAffinity(t, ""),
		"unknown host CPUs leave the profile's synthesized set to the engine")
}

func TestWriteEngineConfig_EmptyConfigRawErrors(t *testing.T) {
	h := testHost(t)
	state := &agent.State{}

	err := writeEngineConfig(t.Context(), h, state)
	require.Error(t, err)
}

// migProfileTable is the partition table shipped beside a board's config.
func migProfileTable(t *testing.T, board string) []byte {
	t.Helper()

	data, err := os.ReadFile(filepath.Join("..", "..", "..", "pkg", "gpu", "mocknvml",
		"configs", "mock-nvml-config-"+board+".mig.yaml"))
	require.NoError(t, err)
	return data
}

// A consumer process reads the config the agent stages with no environment of
// its own to name the partition table, so the table has to be staged beside it.
// Staging the config alone leaves that process seeing a board that cannot
// partition — the device plugin then publishes whole GPUs while NVML inside the
// mock reports partitions, and the two disagree with no error anywhere.
func TestWriteEngineConfig_StagesThePartitionTableBesideTheConfig(t *testing.T) {
	// The sibling rule is all a consumer has; an explicit path would mask it.
	t.Setenv(engine.EnvMIGProfilesConfig, "")

	h := testHost(t)
	state := testState(t)
	state.MIGProfilesRaw = migProfileTable(t, "a100")

	require.NoError(t, writeEngineConfig(t.Context(), h, state))

	for _, rel := range []string{"config/config.yaml", "driver/config/config.yaml"} {
		staged, err := engine.LoadYAMLConfig(h.RootPath(rel))
		require.NoError(t, err, "%s must load the way a consumer loads it", rel)
		require.NotNil(t, staged.DeviceDefaults.MIG, "%s: board should be MIG-capable", rel)
		require.NotEmpty(t, staged.DeviceDefaults.MIG.SupportedProfiles,
			"a consumer loading %s must resolve the board's partition table", rel)
	}
}

// Withdrawal matters as much as writing. A node reconfigured onto a board that
// cannot partition keeps the previous board's table beside the new config
// otherwise, and the sibling rule would then hand a board that has no
// partitions at all someone else's.
func TestWriteEngineConfig_WithdrawsAStalePartitionTable(t *testing.T) {
	t.Setenv(engine.EnvMIGProfilesConfig, "")

	h := testHost(t)
	state := testState(t)
	state.MIGProfilesRaw = migProfileTable(t, "a100")
	require.NoError(t, writeEngineConfig(t.Context(), h, state))

	state.MIGProfilesRaw = nil
	require.NoError(t, writeEngineConfig(t.Context(), h, state))

	for _, rel := range []string{"config/config.mig.yaml", "driver/config/config.mig.yaml"} {
		require.NoFileExists(t, h.RootPath(rel),
			"a board with no table must not keep the previous one")
	}
}

// GFD reads its machine type from a file, so the mock has to serve one: under
// kind the DMI path it defaults to is either absent or owned by the node image.
func TestWriteMachineType_ServesTheProductName(t *testing.T) {
	h := testHost(t)
	state := testState(t)
	state.Devices[0].Name = "NVIDIA GB300 NVL"

	require.NoError(t, writeMachineType(t.Context(), h, state))

	data, err := os.ReadFile(filepath.Join(h.Root, machineTypeRel))
	require.NoError(t, err)
	require.Equal(t, "NVIDIA GB300 NVL\n", string(data),
		"trailing newline mirrors how the kernel renders product_name")
}

// A missing file leaves GFD on its own default, which is the better failure:
// an empty one would label the node with the empty string.
func TestWriteMachineType_NoFileWithoutAProductName(t *testing.T) {
	h := testHost(t)
	state := testState(t)
	state.Devices[0].Name = ""

	require.NoError(t, writeMachineType(t.Context(), h, state))

	_, err := os.Stat(filepath.Join(h.Root, machineTypeRel))
	require.True(t, os.IsNotExist(err), "no product name means no file")
}

func TestWriteMachineType_NoFileWithoutDevices(t *testing.T) {
	h := testHost(t)
	state := testState(t)
	state.Devices = nil

	require.NoError(t, writeMachineType(t.Context(), h, state))

	_, err := os.Stat(filepath.Join(h.Root, machineTypeRel))
	require.True(t, os.IsNotExist(err))
}

// The file is served through the same mount as config.yaml, so it has to sit
// beside it — a path outside driver/config would never reach a container.
func TestWriteMachineType_LandsInTheServedConfigDir(t *testing.T) {
	require.Equal(t, "driver/config", filepath.Dir(machineTypeRel))
}

func TestStageNvidiaSMI_WritesSMIScript(t *testing.T) {
	h := testHost(t)
	state := testState(t)

	require.NoError(t, stageNvidiaSMI(t.Context(), h, state))

	script := h.RootPath("driver/usr/bin/nvidia-smi.sh")
	content, err := os.ReadFile(script)
	require.NoError(t, err)
	require.Contains(t, string(content), state.Software.DriverVersion)

	// Whether nvidia-smi is the ELF or a symlink, it must exist.
	_, err = os.Lstat(h.RootPath("driver/usr/bin/nvidia-smi"))
	require.NoError(t, err, "nvidia-smi must exist (ELF or symlink)")
}

func TestStageCUDAShim_NopWhenNoLib(t *testing.T) {
	matches, _ := filepath.Glob("/usr/local/lib/libcuda.so.*.*.*")
	if len(matches) > 0 {
		t.Skip("libcuda.so is present; this test covers the no-lib path")
	}
	h := testHost(t)
	state := testState(t)

	require.NoError(t, stageCUDAShim(t.Context(), h, state),
		"stageCUDAShim must not error when libcuda.so is absent")
}

func TestStageNVMLShim_CopiesLibAndCreatesLinks(t *testing.T) {
	skipUnlessNVMLLib(t)

	h := testHost(t)
	state := testState(t)

	require.NoError(t, stageNVMLShim(t.Context(), h, state))

	lib64 := h.RootPath("driver/usr/lib64")
	versioned := "libnvidia-ml.so." + state.Software.DriverVersion
	for _, name := range []string{versioned, "libnvidia-ml.so.1", "libnvidia-ml.so"} {
		_, err := os.Lstat(filepath.Join(lib64, name))
		require.NoError(t, err, "%s must exist", name)
	}
}

func TestCharDevsForDevices_UsesConfiguredMinorNumbers(t *testing.T) {
	t.Parallel()

	devices := []agent.DeviceSpec{
		{Index: 0, MinorNumber: 3},
		{Index: 1, MinorNumber: 0},
	}
	require.Equal(t, []charDev{
		{"nvidia3", 195, 3},
		{"nvidia0", 195, 0},
		{"nvidiactl", 195, 255},
		{"nvidia-uvm", 510, 0},
		{"nvidia-uvm-tools", 510, 1},
	}, charDevsForDevices(devices))
}

func TestStageCharDevs_CreatesDeviceNodes(t *testing.T) {
	skipUnlessRootLinux(t)

	h := testHost(t)
	state := testState(t)

	require.NoError(t, stageCharDevs(t.Context(), h, state))

	devRoot := h.RootPath("driver/dev")
	for _, name := range []string{"nvidia0", "nvidiactl", "nvidia-uvm", "nvidia-uvm-tools"} {
		_, err := os.Stat(filepath.Join(devRoot, name))
		require.NoError(t, err, "%s chardev must exist", name)
	}
}

// The node name and the minor it is created with both come from the driver's
// numbering, not from the NVML index, so a device whose two differ gets one
// node under its minor rather than a stray node under its index.
func TestStageCharDevs_UsesConfiguredMinorNumber(t *testing.T) {
	skipUnlessRootLinux(t)

	h := testHost(t)
	state := testState(t)
	state.Devices = []agent.DeviceSpec{{Index: 1, MinorNumber: 3}}

	require.NoError(t, stageCharDevs(t.Context(), h, state))

	devRoot := filepath.Join(h.Root, "driver/dev")
	require.FileExists(t, filepath.Join(devRoot, "nvidia3"))
	require.NoFileExists(t, filepath.Join(devRoot, "nvidia1"))
}

// TestPruneGPUNodes_RemovesShrunkDeviceSet exercises pruneGPUNodes directly with
// plain files: pruning selects purely by name, and mknod needs root, which CI
// runners do not have.
func TestPruneGPUNodes_RemovesShrunkDeviceSet(t *testing.T) {
	devRoot := t.TempDir()
	for _, n := range []string{
		"nvidia0", "nvidia1", "nvidia2", "nvidia3",
		"nvidiactl", "nvidia-uvm", "nvidia-uvm-tools",
	} {
		require.NoError(t, os.WriteFile(filepath.Join(devRoot, n), nil, 0o600))
	}
	// The imex simulator stages the IMEX channel tree in this same directory.
	imex := filepath.Join(devRoot, "nvidia-caps-imex-channels")
	require.NoError(t, os.MkdirAll(imex, 0o755))

	// The device set shrank from four GPUs to two.
	wanted := map[string]bool{
		"nvidia0": true, "nvidia1": true,
		"nvidiactl": true, "nvidia-uvm": true, "nvidia-uvm-tools": true,
	}
	require.NoError(t, pruneGPUNodes(devRoot, wanted))

	for _, keep := range []string{"nvidia0", "nvidia1", "nvidiactl", "nvidia-uvm", "nvidia-uvm-tools"} {
		require.FileExists(t, filepath.Join(devRoot, keep))
	}
	for _, gone := range []string{"nvidia2", "nvidia3"} {
		require.NoFileExists(t, filepath.Join(devRoot, gone),
			"stale GPU node %s must be pruned", gone)
	}
	// The nvidia prefix alone must not be grounds for deletion — this tree
	// belongs to the imex simulator, not to nvml.
	require.DirExists(t, imex, "IMEX channel tree must survive pruning")
}

// TestStageCharDevs_PrunesShrunkDeviceSet guards the call site, not just the
// helper: stageCharDevs must prune GPU nodes a larger device set left behind.
func TestStageCharDevs_PrunesShrunkDeviceSet(t *testing.T) {
	skipUnlessRootLinux(t)

	h := testHost(t)
	devRoot := h.RootPath("driver/dev")
	require.NoError(t, os.MkdirAll(devRoot, 0o755))

	// A previous, larger device set left four GPU nodes behind.
	for i, n := range []string{"nvidia0", "nvidia1", "nvidia2", "nvidia3"} {
		require.NoError(t, fsutil.Mknod(filepath.Join(devRoot, n), 195, uint32(i)))
	}

	state := &agent.State{Devices: []agent.DeviceSpec{
		{Index: 0, MinorNumber: 0},
		{Index: 1, MinorNumber: 1},
	}}
	require.NoError(t, stageCharDevs(t.Context(), h, state))

	require.FileExists(t, filepath.Join(devRoot, "nvidia0"))
	require.FileExists(t, filepath.Join(devRoot, "nvidia1"))
	require.NoFileExists(t, filepath.Join(devRoot, "nvidia2"))
	require.NoFileExists(t, filepath.Join(devRoot, "nvidia3"))
}

// ─── Discard ─────────────────────────────────────────────────────────────────

func TestDiscard_NopWhenNotStaged(t *testing.T) {
	require.NoError(t, New(testHost(t)).Discard(t.Context()))
}

func TestDiscard_RemovesTheStagedConfig(t *testing.T) {
	h := testHost(t)
	state := testState(t)
	state.Devices[0].Name = "NVIDIA A100-SXM4-40GB"
	require.NoError(t, writeEngineConfig(t.Context(), h, state))
	require.NoError(t, writeMachineType(t.Context(), h, state))

	require.NoError(t, New(h).Discard(t.Context()))

	for _, rel := range []string{"config/config.yaml", "driver/config/config.yaml", machineTypeRel} {
		require.NoFileExists(t, h.RootPath(rel))
	}
}

// ─── full Stage (Linux root + NVML lib required) ─────────────────────────────

func TestStage_WritesAllSurfaces(t *testing.T) {
	skipUnlessRootLinux(t)
	skipUnlessNVMLLib(t)

	h := testHost(t)
	sim := New(h)
	state := testState(t)

	require.NoError(t, sim.Stage(t.Context(), state))
	require.True(t, sim.Ready())

	// chardevs
	_, err := os.Stat(h.RootPath("driver/dev", "nvidiactl"))
	require.NoError(t, err)

	// NVML shim
	_, err = os.Lstat(h.RootPath("driver/usr/lib64", "libnvidia-ml.so.1"))
	require.NoError(t, err)

	// nvidia-smi
	_, err = os.Lstat(h.RootPath("driver/usr/bin/nvidia-smi"))
	require.NoError(t, err)

	// engine config
	_, err = os.Stat(h.RootPath("config/config.yaml"))
	require.NoError(t, err)
}

func TestStage_Idempotent(t *testing.T) {
	skipUnlessRootLinux(t)
	skipUnlessNVMLLib(t)

	h := testHost(t)
	sim := New(h)
	state := testState(t)

	require.NoError(t, sim.Stage(t.Context(), state))
	require.NoError(t, sim.Stage(t.Context(), state), "second Stage must not error")
}
