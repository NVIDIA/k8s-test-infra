// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package gpudriver

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"sigs.k8s.io/yaml"

	"github.com/NVIDIA/k8s-test-infra/internal/agent"
	"github.com/NVIDIA/k8s-test-infra/internal/agent/source"
	"github.com/NVIDIA/k8s-test-infra/pkg/gpu/mocknvml/engine"
)

// procFSPath joins rel onto the staged /proc/driver/nvidia directory under root.
func procFSPath(root string, rel ...string) string {
	return filepath.Join(append([]string{root, "driver/proc/driver/nvidia"}, rel...)...)
}

// parseParamsLikeNvidiaModprobe mirrors the loop nvidia-modprobe runs over
// /proc/driver/nvidia/params in modprobe-utils/nvidia-modprobe-utils.c:
//
//	while (fscanf(fp, "%31[^:]: %u\n", name, &value) == 2)
//
// Two properties of that loop decide whether a params file works at all: keys
// are matched unprefixed, and the first line whose value is not a bare unsigned
// integer ends the scan, leaving every later key unreachable. Reproducing the
// parser keeps these tests honest about what the real consumer can see, rather
// than asserting on a string this package controls.
func parseParamsLikeNvidiaModprobe(content string) map[string]uint64 {
	reachable := map[string]uint64{}

	for line := range strings.SplitSeq(content, "\n") {
		name, rawValue, found := strings.Cut(line, ":")
		// A name past the 31-char field width leaves its tail in the stream, so
		// the following ": " never matches and the scan ends there.
		if !found || len(name) > 31 {
			return reachable
		}

		value, err := strconv.ParseUint(strings.TrimSpace(rawValue), 10, 64)
		if err != nil {
			return reachable
		}

		reachable[name] = value
	}

	return reachable
}

// TestWriteProcFS_DeviceFileParamsAreReachable is the regression test for both
// params defects: NVreg_-prefixed keys match nothing in nvidia-modprobe, and an
// empty-valued param early in the file strands every key after it.
func TestWriteProcFS_DeviceFileParamsAreReachable(t *testing.T) {
	t.Parallel()

	h := testHost(t)
	state := testState(t)

	require.NoError(t, writeProcFS(context.Background(), h, state))

	content, err := os.ReadFile(procFSPath(h.Root, "params"))
	require.NoError(t, err)

	reachable := parseParamsLikeNvidiaModprobe(string(content))

	// The four keys nvidia-modprobe acts on, under the names it matches.
	require.Equal(t, uint64(1), reachable["ModifyDeviceFiles"])
	require.Equal(t, uint64(0), reachable["DeviceFileUID"])
	require.Equal(t, uint64(0), reachable["DeviceFileGID"])
	require.Equal(t, uint64(0o666), reachable["DeviceFileMode"])
}

// TestWriteProcFS_ConsumedParamsPrecedeTheScanStopper pins the ordering
// invariant so a later contributor cannot strand the device-file keys by
// inserting a param above them. Both kinds of scan-stopping line are checked,
// since the real driver's key set contains one of each: a name wider than the
// 31-char field (InitializeSystemMemoryAllocations) and a quoted value
// (RegistryDwords).
func TestWriteProcFS_ConsumedParamsPrecedeTheScanStopper(t *testing.T) {
	t.Parallel()

	h := testHost(t)
	state := testState(t)

	require.NoError(t, writeProcFS(context.Background(), h, state))

	content, err := os.ReadFile(procFSPath(h.Root, "params"))
	require.NoError(t, err)

	consumed := map[string]bool{
		"ModifyDeviceFiles": true,
		"DeviceFileUID":     true,
		"DeviceFileGID":     true,
		"DeviceFileMode":    true,
	}
	stopper := ""

	for line := range strings.SplitSeq(strings.TrimSuffix(string(content), "\n"), "\n") {
		name, rawValue, found := strings.Cut(line, ":")
		require.True(t, found, "every params line is Key: value, got %q", line)

		_, numeric := strconv.ParseUint(strings.TrimSpace(rawValue), 10, 64)
		if len(name) > 31 || numeric != nil {
			if stopper == "" {
				stopper = name
			}

			continue
		}

		require.False(t, consumed[name] && stopper != "",
			"nvidia-modprobe stops scanning at %q, so it never reaches %q", stopper, name)
		delete(consumed, name)
	}

	require.Empty(t, consumed, "params must carry every key nvidia-modprobe consumes")
}

// TestWriteProcFS_ParamsCarryTheDriverKeySet pins the file to the key set and
// order of the real module, captured from an 8-GPU H100 node running the
// 580.105.08 open kernel module.
//
// The ordering invariant above is the property that matters, and this is where
// the provenance lives: the order is only defensible because it is the driver's
// own, and a reader who wonders why InitializeSystemMemoryAllocations sits
// seventh — stranding the 35 keys below it for nvidia-modprobe, on real
// hardware too — can see that we did not choose it.
func TestWriteProcFS_ParamsCarryTheDriverKeySet(t *testing.T) {
	t.Parallel()

	h := testHost(t)
	state := testState(t)

	require.NoError(t, writeProcFS(context.Background(), h, state))

	content, err := os.ReadFile(procFSPath(h.Root, "params"))
	require.NoError(t, err)

	var keys []string
	for line := range strings.SplitSeq(strings.TrimSuffix(string(content), "\n"), "\n") {
		key, _, found := strings.Cut(line, ":")
		require.True(t, found, "every params line is Key: value, got %q", line)
		keys = append(keys, key)
	}

	require.Equal(t, []string{
		"ResmanDebugLevel",
		"RmLogonRC",
		"ModifyDeviceFiles",
		"DeviceFileUID",
		"DeviceFileGID",
		"DeviceFileMode",
		"InitializeSystemMemoryAllocations",
		"UsePageAttributeTable",
		"EnableMSI",
		"EnablePCIeGen3",
		"MemoryPoolSize",
		"KMallocHeapMaxSize",
		"VMallocHeapMaxSize",
		"IgnoreMMIOCheck",
		"EnableStreamMemOPs",
		"EnableUserNUMAManagement",
		"NvLinkDisable",
		"RmProfilingAdminOnly",
		"PreserveVideoMemoryAllocations",
		"EnableS0ixPowerManagement",
		"S0ixPowerManagementVideoMemoryThreshold",
		"DynamicPowerManagement",
		"DynamicPowerManagementVideoMemoryThreshold",
		"RegisterPCIDriver",
		"EnablePCIERelaxedOrderingMode",
		"EnableResizableBar",
		"EnableGpuFirmware",
		"EnableGpuFirmwareLogs",
		"RmNvlinkBandwidthLinkCount",
		"EnableDbgBreakpoint",
		"OpenRmEnableUnsupportedGpus",
		"DmaRemapPeerMmio",
		"ImexChannelCount",
		"CreateImexChannel0",
		"GrdmaPciTopoCheckOverride",
		"CoherentGPUMemoryMode",
		"RegistryDwords",
		"RegistryDwordsPerDevice",
		"RmMsg",
		"GpuBlacklist",
		"TemporaryFilePath",
		"ExcludedGpus",
	}, keys)
}

// TestWriteProcFS_DeviceFilePermissionsComeFromState covers the reason to fix
// params at all: a profile asking for non-default device-node permissions must
// reach the file, so permission-failure scenarios become expressible.
func TestWriteProcFS_DeviceFilePermissionsComeFromState(t *testing.T) {
	t.Parallel()

	h := testHost(t)
	state := testState(t)
	// 0660 root:video — the configuration behind the "Insufficient Permissions"
	// class of NVML failures under rootless container runtimes.
	state.DriverParams = agent.DriverParams{
		DeviceFileUID:     0,
		DeviceFileGID:     27,
		DeviceFileMode:    0o660,
		ModifyDeviceFiles: true,
	}

	require.NoError(t, writeProcFS(context.Background(), h, state))

	content, err := os.ReadFile(procFSPath(h.Root, "params"))
	require.NoError(t, err)

	reachable := parseParamsLikeNvidiaModprobe(string(content))
	require.Equal(t, uint64(0), reachable["DeviceFileUID"])
	require.Equal(t, uint64(27), reachable["DeviceFileGID"])
	require.Equal(t, uint64(0o660), reachable["DeviceFileMode"])
	require.Equal(t, uint64(1), reachable["ModifyDeviceFiles"])
}

// informationFields parses one gpus/<BDF>/information file into key/value
// pairs. The real driver pads with tabs after the colon, so values are trimmed.
func informationFields(t *testing.T, path string) map[string]string {
	t.Helper()

	content, err := os.ReadFile(path)
	require.NoError(t, err)

	fields := map[string]string{}

	for line := range strings.SplitSeq(strings.TrimSuffix(string(content), "\n"), "\n") {
		key, value, found := strings.Cut(line, ":")
		require.True(t, found, "every information line is Key: value, got %q", line)
		fields[strings.TrimSpace(key)] = strings.TrimSpace(value)
	}

	return fields
}

// informationKeys returns the field names of one information file in the order
// the file lists them.
func informationKeys(t *testing.T, path string) []string {
	t.Helper()

	content, err := os.ReadFile(path)
	require.NoError(t, err)

	var keys []string
	for line := range strings.SplitSeq(strings.TrimSuffix(string(content), "\n"), "\n") {
		key, _, found := strings.Cut(line, ":")
		require.True(t, found, "every information line is Key: value, got %q", line)
		keys = append(keys, strings.TrimSpace(key))
	}

	return keys
}

// twoGPUState returns a state whose two devices carry full PCI identity, with
// the profile's upper-case bus IDs left as written so the directory naming is
// exercised.
func twoGPUState(t *testing.T) *agent.State {
	t.Helper()

	state := testState(t)
	state.Devices = []agent.DeviceSpec{
		{
			Index: 0, MinorNumber: 0,
			UUID:             "GPU-12345678-1234-1234-1234-123456780000",
			PCIBusID:         "0000:07:00.0",
			Name:             "NVIDIA A100-SXM4-40GB",
			VBIOSVersion:     "92.00.45.00.03",
			ComputeCapMajor:  8,
			MemoryTotalBytes: 42949672960,
		},
		{
			Index: 1, MinorNumber: 1,
			UUID:             "GPU-12345678-1234-1234-1234-123456780001",
			PCIBusID:         "0000:0F:00.0",
			Name:             "NVIDIA A100-SXM4-40GB",
			VBIOSVersion:     "92.00.45.00.03",
			ComputeCapMajor:  8,
			MemoryTotalBytes: 42949672960,
		},
	}

	return state
}

// TestWriteProcFS_WritesPerGPUInformation covers the GPU enumeration path that
// bypasses NVML: a consumer resolving a PCI address to a device minor through
// procfs, which is also the only path that works from inside a container.
func TestWriteProcFS_WritesPerGPUInformation(t *testing.T) {
	t.Parallel()

	h := testHost(t)
	state := twoGPUState(t)

	require.NoError(t, writeProcFS(context.Background(), h, state))

	// Directories carry the lower-case sysfs spelling of the BDF, whatever
	// case the profile used, so a consumer can join the name straight onto
	// /sys/bus/pci/devices.
	fields := informationFields(t, procFSPath(h.Root, "gpus", "0000:0f:00.0", "information"))

	require.Equal(t, "NVIDIA A100-SXM4-40GB", fields["Model"])
	require.Equal(t, "GPU-12345678-1234-1234-1234-123456780001", fields["GPU UUID"])
	require.Equal(t, "0000:0f:00.0", fields["Bus Location"])
	require.Equal(t, "1", fields["Device Minor"])
	require.Equal(t, "92.00.45.00.03", fields["Video BIOS"])
	require.Equal(t, "550.163.01", fields["GPU Firmware"])
	require.Equal(t, "No", fields["GPU Excluded"])

	require.DirExists(t, procFSPath(h.Root, "gpus", "0000:07:00.0"))
}

// TestWriteProcFS_InformationCarriesTheDriverFieldSet pins the file to the
// fields the real driver prints, in its order, captured from an 8-GPU H100 node
// running the 580.105.08 open kernel module.
//
// Exact rather than "contains", because the first version of this file invented
// three fields — Architecture, Memory and a Blacklisted line the driver dropped
// when it renamed the concept to GPU Excluded — and grew them from a written
// description rather than a capture. A consumer parsing by field name is not
// harmed by an extra line, but a mock that reports fields no driver has is
// telling the reader something false about the interface.
func TestWriteProcFS_InformationCarriesTheDriverFieldSet(t *testing.T) {
	t.Parallel()

	h := testHost(t)
	state := twoGPUState(t)

	require.NoError(t, writeProcFS(context.Background(), h, state))

	keys := informationKeys(t, procFSPath(h.Root, "gpus", "0000:07:00.0", "information"))

	require.Equal(t, []string{
		"Model",
		"IRQ",
		"GPU UUID",
		"Video BIOS",
		"Bus Type",
		"DMA Size",
		"DMA Mask",
		"Bus Location",
		"Device Minor",
		"GPU Firmware",
		"GPU Excluded",
	}, keys)
}

// TestWriteProcFS_GPUInformationOmitsUnknownUUID keeps the file from inventing
// an identity NVML does not report: a profile without a per-device uuid leaves
// NVML on its own base value, which the agent cannot know.
func TestWriteProcFS_GPUInformationOmitsUnknownUUID(t *testing.T) {
	t.Parallel()

	h := testHost(t)
	state := twoGPUState(t)
	state.Devices[0].UUID = ""

	require.NoError(t, writeProcFS(context.Background(), h, state))

	fields := informationFields(t, procFSPath(h.Root, "gpus", "0000:07:00.0", "information"))
	require.NotContains(t, fields, "GPU UUID")
	require.Equal(t, "0", fields["Device Minor"])
}

// TestWriteProcFS_SkipsDeviceWithoutBusID keeps a device that carries no PCI
// identity from producing an unnameable directory, without failing the whole
// staging run over it.
func TestWriteProcFS_SkipsDeviceWithoutBusID(t *testing.T) {
	t.Parallel()

	h := testHost(t)
	state := twoGPUState(t)
	state.Devices[0].PCIBusID = ""

	require.NoError(t, writeProcFS(context.Background(), h, state))

	entries, err := os.ReadDir(procFSPath(h.Root, "gpus"))
	require.NoError(t, err)
	require.Len(t, entries, 1, "only the device with a bus ID gets a directory")
	require.Equal(t, "0000:0f:00.0", entries[0].Name())
}

// TestWriteProcFS_GPUDirsMatchTheServedPCITree pins the two surfaces together.
// The agent serves the rendered PCI tree at /sys/bus/pci/devices, and procfs
// answers the same "which GPUs exist" question; a consumer that resolves a GPU
// in one and not the other is the failure both paths exist to fix. Exercised
// with a profile declaring more roots than the device list carries, which is
// what a GPU_COUNT-capped node looks like.
func TestWriteProcFS_GPUDirsMatchTheServedPCITree(t *testing.T) {
	t.Parallel()

	h := testHost(t)
	state := twoGPUState(t)
	state.NodeShape.Topology.RootComplexes = []agent.RootComplex{
		{ID: "pci0000:00", NUMANode: 0, DeviceBDFs: []string{"0000:07:00.0", "0000:0f:00.0"}},
		{ID: "pci0000:40", NUMANode: 1, DeviceBDFs: []string{"0000:47:00.0", "0000:4e:00.0"}},
	}

	require.NoError(t, writeProcFS(context.Background(), h, state))

	served := []string{}
	for _, rc := range state.PCITopology() {
		served = append(served, rc.DeviceBDFs...)
	}

	entries, err := os.ReadDir(procFSPath(h.Root, "gpus"))
	require.NoError(t, err)

	dirs := make([]string, 0, len(entries))
	for _, e := range entries {
		dirs = append(dirs, e.Name())
	}

	require.ElementsMatch(t, served, dirs,
		"procfs GPU directories must be exactly the addresses the PCI tree serves")
	require.Len(t, dirs, 2, "the two roots the device list does not fill are dropped")
}

// TestWriteProcFS_RejectsBusIDThatIsNotAnAddress guards the boundary: a bus_id
// is authored by hand in gpu.customConfig and becomes a path component here, so
// one carrying separators or parent references would render outside the tree.
func TestWriteProcFS_RejectsBusIDThatIsNotAnAddress(t *testing.T) {
	t.Parallel()

	for _, busID := range []string{
		"../../../../escaped",
		"0000:07:00.0/../../escaped",
		"not-an-address",
		"07:00.0",          // domainless, the form sysfs never uses
		"00000000:07:00.0", // NVML's 8-digit busId, not the profile's
	} {
		t.Run(busID, func(t *testing.T) {
			t.Parallel()

			h := testHost(t)
			state := twoGPUState(t)
			state.Devices[0].PCIBusID = busID

			require.NoError(t, writeProcFS(context.Background(), h, state))

			// Only the well-formed sibling survives, and nothing landed outside
			// the procfs tree.
			entries, err := os.ReadDir(procFSPath(h.Root, "gpus"))
			require.NoError(t, err)
			require.Len(t, entries, 1)
			require.Equal(t, "0000:0f:00.0", entries[0].Name())

			require.NoDirExists(t, filepath.Join(h.Root, "escaped"))
			require.NoDirExists(t, filepath.Join(h.Root, "driver", "escaped"))
		})
	}
}

// TestWriteProcFS_PrunesStaleGPUDirectories covers a shrinking device set:
// staging is additive, so a directory left by a larger set would keep
// advertising a GPU that NVML no longer reports.
func TestWriteProcFS_PrunesStaleGPUDirectories(t *testing.T) {
	t.Parallel()

	h := testHost(t)
	ctx := context.Background()

	require.NoError(t, writeProcFS(ctx, h, twoGPUState(t)))

	shrunk := twoGPUState(t)
	shrunk.Devices = shrunk.Devices[:1]
	require.NoError(t, writeProcFS(ctx, h, shrunk))

	require.DirExists(t, procFSPath(h.Root, "gpus", "0000:07:00.0"))
	require.NoDirExists(t, procFSPath(h.Root, "gpus", "0000:0f:00.0"),
		"the directory of a removed GPU must not survive")
}

// TestDeviceMinorAgreesAcrossSurfaces is the cross-check this procfs work
// exists for. /dev/nvidiaN, nvmlDeviceGetMinorNumber and
// /proc/driver/nvidia/gpus/<BDF>/information each answer "which minor is this
// GPU", and a consumer picks one of the three. They must not disagree. Driven
// from the shipped profiles through the real FileSource so the compiler is in
// the loop rather than a hand-built State.
func TestDeviceMinorAgreesAcrossSurfaces(t *testing.T) {
	t.Parallel()

	configs, err := filepath.Glob("../../../pkg/gpu/mocknvml/configs/mock-nvml-config-*.yaml")
	require.NoError(t, err)
	require.NotEmpty(t, configs, "no config YAMLs found")

	for _, path := range configs {
		t.Run(filepath.Base(path), func(t *testing.T) {
			t.Parallel()

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			src := source.NewFileSource(path, "", slog.New(slog.NewTextHandler(io.Discard, nil)))
			defer func() { require.NoError(t, src.Close()) }()

			update := <-src.Watch(ctx)
			require.NoError(t, update.Err)
			require.NotNil(t, update.State)

			state := update.State
			h := testHost(t)
			require.NoError(t, writeProcFS(ctx, h, state))

			// The minor NVML answers with, read from the same profile bytes the
			// shim loads at dlopen time.
			var yamlConfig engine.YAMLConfig
			require.NoError(t, yaml.Unmarshal(state.ConfigRaw, &yamlConfig))
			nvmlConfig := &engine.Config{YAMLConfig: &yamlConfig}

			// The nodes stageCharDevs creates.
			nodes := gpuCharDevs(state)
			require.Len(t, nodes, len(state.Devices))

			for i, d := range state.Devices {
				minor := nvmlConfig.GetDeviceMinorNumber(i)

				fields := informationFields(t,
					procFSPath(h.Root, "gpus", strings.ToLower(d.PCIBusID), "information"))
				require.Equal(t, strconv.Itoa(minor), fields["Device Minor"],
					"procfs disagrees with nvmlDeviceGetMinorNumber for device %d", i)

				require.Equal(t, fmt.Sprintf("nvidia%d", minor), nodes[i].name,
					"device node name disagrees with nvmlDeviceGetMinorNumber for device %d", i)
				require.Equal(t, uint32(minor), nodes[i].minor, //nolint:gosec // profile minors are small
					"device node minor disagrees with nvmlDeviceGetMinorNumber for device %d", i)
			}
		})
	}
}

// TestWriteProcFS_ImexChannelCountFollowsState keeps params from disagreeing
// with the channel devices the imex simulator actually stages. Read by line
// rather than through the parser mimic: this key sits below the scan stopper
// in the real driver's ordering, so only a reader of the whole file sees it.
func TestWriteProcFS_ImexChannelCountFollowsState(t *testing.T) {
	t.Parallel()

	h := testHost(t)
	state := testState(t)
	state.IMEX = agent.IMEXState{Enabled: true, ChannelCount: 64}

	require.NoError(t, writeProcFS(context.Background(), h, state))

	content, err := os.ReadFile(procFSPath(h.Root, "params"))
	require.NoError(t, err)
	require.Contains(t, string(content), "ImexChannelCount: 64\n")
}

// TestWriteProcFS_ModifyDeviceFilesDisabled guards the false case: a profile
// that turns off device-node management must report 0, not omit the key.
func TestWriteProcFS_ModifyDeviceFilesDisabled(t *testing.T) {
	t.Parallel()

	h := testHost(t)
	state := testState(t)
	state.DriverParams = agent.DriverParams{ModifyDeviceFiles: false}

	require.NoError(t, writeProcFS(context.Background(), h, state))

	content, err := os.ReadFile(procFSPath(h.Root, "params"))
	require.NoError(t, err)

	reachable := parseParamsLikeNvidiaModprobe(string(content))
	require.Contains(t, reachable, "ModifyDeviceFiles")
	require.Equal(t, uint64(0), reachable["ModifyDeviceFiles"])
}
