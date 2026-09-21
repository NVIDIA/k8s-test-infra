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
	"encoding/xml"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// testdataDir returns the absolute path to the profiles directory.
func testdataDir() string {
	_, filename, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(filename), "..", "..", "..", "..", "deployments", "nvml-mock", "helm", "nvml-mock", "profiles")
}

func TestLoadConfig_L40SProfile(t *testing.T) {
	profilePath := filepath.Join(testdataDir(), "l40s.yaml")

	yamlCfg, err := LoadYAMLConfig(profilePath)
	require.NoError(t, err, "Failed to load L40S profile")

	// Verify device count: L40S typically 8 GPUs in a server
	require.Len(t, yamlCfg.Devices, 8, "L40S device count")

	// Verify architecture
	require.Equal(t, "ada_lovelace", yamlCfg.DeviceDefaults.Architecture, "L40S architecture")

	// Verify compute capability 8.9
	cc := yamlCfg.DeviceDefaults.ComputeCapability
	require.NotNil(t, cc, "L40S compute_capability is nil")
	require.Equal(t, 8, cc.Major, "L40S compute capability major")
	require.Equal(t, 9, cc.Minor, "L40S compute capability minor")

	// Verify memory: 48 GiB = 51539607552 bytes
	mem := yamlCfg.DeviceDefaults.Memory
	require.NotNil(t, mem, "L40S memory config is nil")
	expectedMemBytes := uint64(51539607552) // 48 GiB
	require.Equal(t, expectedMemBytes, mem.TotalBytes, "L40S memory total_bytes")

	// Verify PCI device ID: 0x26B910DE, the L40S board's. 0x26b5 is an L40,
	// which is what this asserted until the capture cross-check caught it.
	pci := yamlCfg.DeviceDefaults.PCI
	require.NotNil(t, pci, "L40S PCI config is nil")
	expectedDeviceID := uint32(0x26B910DE)
	require.Equal(t, expectedDeviceID, pci.DeviceID, "L40S PCI device_id")

	// Verify GPU name
	require.Equal(t, "NVIDIA L40S", yamlCfg.DeviceDefaults.Name, "L40S name")

	// Verify no NVLink (L40S is PCIe only)
	require.Nil(t, yamlCfg.NVLink, "L40S should not have NVLink configuration")

	// Verify PCIe Gen4
	pcie := yamlCfg.DeviceDefaults.PCIe
	require.NotNil(t, pcie, "L40S PCIe config is nil")
	require.Equal(t, 4, pcie.MaxLinkGen, "L40S PCIe max_link_gen")

	// Verify power: 350W TDP
	power := yamlCfg.DeviceDefaults.Power
	require.NotNil(t, power, "L40S power config is nil")
	require.Equal(t, uint32(350000), power.DefaultLimitMW, "L40S power default_limit_mw")
}

func TestLoadConfig_T4Profile(t *testing.T) {
	profilePath := filepath.Join(testdataDir(), "t4.yaml")

	yamlCfg, err := LoadYAMLConfig(profilePath)
	require.NoError(t, err, "Failed to load T4 profile")

	// Verify device count: T4 typically 4 GPUs
	require.Len(t, yamlCfg.Devices, 4, "T4 device count")

	// Verify architecture
	require.Equal(t, "turing", yamlCfg.DeviceDefaults.Architecture, "T4 architecture")

	// Verify compute capability 7.5
	cc := yamlCfg.DeviceDefaults.ComputeCapability
	require.NotNil(t, cc, "T4 compute_capability is nil")
	require.Equal(t, 7, cc.Major, "T4 compute capability major")
	require.Equal(t, 5, cc.Minor, "T4 compute capability minor")

	// Verify memory: 16 GiB = 17179869184 bytes
	mem := yamlCfg.DeviceDefaults.Memory
	require.NotNil(t, mem, "T4 memory config is nil")
	expectedMemBytes := uint64(17179869184) // 16 GiB
	require.Equal(t, expectedMemBytes, mem.TotalBytes, "T4 memory total_bytes")

	// Verify PCI device ID: 0x1EB810DE
	pci := yamlCfg.DeviceDefaults.PCI
	require.NotNil(t, pci, "T4 PCI config is nil")
	expectedDeviceID := uint32(0x1EB810DE)
	require.Equal(t, expectedDeviceID, pci.DeviceID, "T4 PCI device_id")

	// Verify GPU name
	require.Equal(t, "NVIDIA T4", yamlCfg.DeviceDefaults.Name, "T4 name")

	// Verify no NVLink (T4 is PCIe only)
	require.Nil(t, yamlCfg.NVLink, "T4 should not have NVLink configuration")

	// Verify PCIe Gen3
	pcie := yamlCfg.DeviceDefaults.PCIe
	require.NotNil(t, pcie, "T4 PCIe config is nil")
	require.Equal(t, 3, pcie.MaxLinkGen, "T4 PCIe max_link_gen")

	// Verify power: 70W TDP
	power := yamlCfg.DeviceDefaults.Power
	require.NotNil(t, power, "T4 power config is nil")
	require.Equal(t, uint32(70000), power.DefaultLimitMW, "T4 power default_limit_mw")
}

func TestLoadConfig_GB300Profile(t *testing.T) {
	profilePath := filepath.Join(testdataDir(), "gb300.yaml")

	yamlCfg, err := LoadYAMLConfig(profilePath)
	require.NoError(t, err, "Failed to load GB300 profile")

	// 4 GPUs: one NVL72 compute tray, 2 Grace-Blackwell Ultra superchips × 2
	// B300 GPUs each, which is what a real node reports (see
	// tests/e2e/go/assertions/nvidiasmi/testdata/hardware/qx-gb300.xml).
	require.Len(t, yamlCfg.Devices, 4, "GB300 device count")

	require.Equal(t, "NVIDIA GB300 NVL", yamlCfg.DeviceDefaults.Name, "GB300 name")

	// 288 GiB HBM3e per GPU is the headline GB300 vs. GB200 delta — make
	// sure a regression in the YAML can never quietly drop us back to 192.
	mem := yamlCfg.DeviceDefaults.Memory
	require.NotNil(t, mem, "GB300 memory config is nil")
	expectedMemBytes := uint64(288) * 1024 * 1024 * 1024
	require.Equal(t, expectedMemBytes, mem.TotalBytes, "GB300 memory total_bytes (288 GiB)")

	// Blackwell Ultra uses the 570.x driver line; the chart's
	// driverVersion helper relies on this value being consistent.
	require.Equal(t, "570.124.06", yamlCfg.System.DriverVersion, "GB300 driver_version")

	// PCIe Gen6 (or NVLink-C2C to Grace).
	pcie := yamlCfg.DeviceDefaults.PCIe
	require.NotNil(t, pcie, "GB300 PCIe config is nil")
	require.Equal(t, 6, pcie.MaxLinkGen, "GB300 PCIe max_link_gen")

	// 1400W default TDP (vs. GB200's 1000W).
	power := yamlCfg.DeviceDefaults.Power
	require.NotNil(t, power, "GB300 power config is nil")
	require.Equal(t, uint32(1400000), power.DefaultLimitMW, "GB300 power default_limit_mw")

	// Grace pairing must be wired up — GB300 is a superchip part.
	cpu := yamlCfg.DeviceDefaults.CPU
	require.NotNil(t, cpu, "GB300 cpu config is nil")
	require.Equal(t, "grace", cpu.Type, "GB300 cpu.type")

	// NVLink v5, 18 links @ 100 GB/s (same fabric as GB200).
	require.NotNil(t, yamlCfg.NVLink, "GB300 NVLink config is nil")
	require.Equal(t, 5, yamlCfg.NVLink.Version, "GB300 nvlink.version")
	require.Equal(t, 18, yamlCfg.NVLink.LinksPerGPU, "GB300 nvlink.links_per_gpu")
}

func TestLoadConfig_AllProfilesConsistent(t *testing.T) {
	profiles := []struct {
		name         string
		file         string
		architecture string
		ccMajor      int
		ccMinor      int
		memGiB       uint64
		deviceCount  int
	}{
		{"A100", "a100.yaml", "ampere", 8, 0, 40, 8},
		{"H100", "h100.yaml", "hopper", 9, 0, 80, 8},
		{"B200", "b200.yaml", "blackwell", 10, 0, 192, 8},
		{"GB200", "gb200.yaml", "blackwell", 10, 0, 192, 4},
		{"GB300", "gb300.yaml", "blackwell", 10, 0, 288, 4},
		{"L40S", "l40s.yaml", "ada_lovelace", 8, 9, 48, 8},
		{"T4", "t4.yaml", "turing", 7, 5, 16, 4},
	}

	for _, p := range profiles {
		t.Run(p.name, func(t *testing.T) {
			profilePath := filepath.Join(testdataDir(), p.file)
			yamlCfg, err := LoadYAMLConfig(profilePath)
			require.NoError(t, err, "Failed to load %s profile", p.name)

			require.Equal(t, p.architecture, yamlCfg.DeviceDefaults.Architecture, "%s architecture", p.name)

			cc := yamlCfg.DeviceDefaults.ComputeCapability
			require.NotNil(t, cc, "%s compute_capability is nil", p.name)
			require.Equal(t, p.ccMajor, cc.Major, "%s compute capability major", p.name)
			require.Equal(t, p.ccMinor, cc.Minor, "%s compute capability minor", p.name)

			mem := yamlCfg.DeviceDefaults.Memory
			require.NotNil(t, mem, "%s memory config is nil", p.name)
			expectedBytes := p.memGiB * 1024 * 1024 * 1024
			require.Equal(t, expectedBytes, mem.TotalBytes, "%s memory (%d GiB)", p.name, p.memGiB)

			require.Len(t, yamlCfg.Devices, p.deviceCount, "%s device count", p.name)

			require.NotEmpty(t, yamlCfg.System.DriverVersion, "%s driver_version is empty", p.name)
		})
	}
}

// repoRoot returns the absolute path to the repository root.
func repoRoot() string {
	_, filename, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(filename), "..", "..", "..", "..")
}

// hardwareCaptureDir returns the absolute path to the real-hardware
// `nvidia-smi -q -x` captures the shipped profiles are modelled on.
func hardwareCaptureDir() string {
	return filepath.Join(repoRoot(), "tests", "e2e", "go", "assertions",
		"nvidiasmi", "testdata", "hardware")
}

// profileSource is one of the two directories that ship GPU profiles. The
// chart copy is what a Helm install renders into its ConfigMap; the engine copy
// is what MOCK_NVML_CONFIG points at for a local run. Both are consumed by the
// same loader and both must carry the same PCI identity, so both are globbed.
type profileSource struct {
	label  string
	dir    string
	prefix string
}

func profileSources() []profileSource {
	return []profileSource{
		{
			label: "chart",
			dir:   filepath.Join(repoRoot(), "deployments", "nvml-mock", "helm", "nvml-mock", "profiles"),
		},
		{
			label:  "engine",
			dir:    filepath.Join(repoRoot(), "pkg", "gpu", "mocknvml", "configs"),
			prefix: "mock-nvml-config-",
		},
	}
}

// profiles globs the source directory and returns sku -> absolute path. A
// profile added later is picked up here, so it cannot escape the cross-check by
// being absent from a hand-maintained list.
func (s profileSource) profiles(t *testing.T) map[string]string {
	t.Helper()

	matches, err := filepath.Glob(filepath.Join(s.dir, s.prefix+"*.yaml"))
	require.NoError(t, err, "glob %s profiles", s.label)
	require.NotEmpty(t, matches, "%s profile directory %s holds no profile", s.label, s.dir)

	found := make(map[string]string, len(matches))
	for _, m := range matches {
		sku := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(m), s.prefix), ".yaml")
		found[sku] = m
	}
	return found
}

// capturedPCI is the PCI identity a real board reports through
// `nvidia-smi -q -x`. The captures are the authority for both words.
type capturedPCI struct {
	deviceID    uint32
	subsystemID uint32
}

// readCapture parses qx-<sku>.xml and returns the board's PCI identity,
// requiring every GPU in the capture to agree on it.
func readCapture(t *testing.T, sku string) capturedPCI {
	t.Helper()

	var doc struct {
		GPUs []struct {
			PCI struct {
				DeviceID    string `xml:"pci_device_id"`
				SubsystemID string `xml:"pci_sub_system_id"`
			} `xml:"pci"`
		} `xml:"gpu"`
	}

	raw, err := os.ReadFile(filepath.Join(hardwareCaptureDir(), "qx-"+sku+".xml"))
	require.NoErrorf(t, err, "no hardware capture for profile %q: every profile must be modelled on a captured board", sku)
	require.NoError(t, xml.Unmarshal(raw, &doc), "parse hardware capture for %s", sku)
	require.NotEmpty(t, doc.GPUs, "capture for %s declares no GPU", sku)

	parse := func(field, value string) uint32 {
		t.Helper()
		v, err := strconv.ParseUint(value, 16, 32)
		require.NoErrorf(t, err, "capture %s %s %q", sku, field, value)
		return uint32(v)
	}

	first := doc.GPUs[0].PCI
	got := capturedPCI{
		deviceID:    parse("pci_device_id", first.DeviceID),
		subsystemID: parse("pci_sub_system_id", first.SubsystemID),
	}
	for i, gpu := range doc.GPUs {
		require.Equalf(t, first.DeviceID, gpu.PCI.DeviceID, "capture %s GPU %d disagrees on pci_device_id", sku, i)
		require.Equalf(t, first.SubsystemID, gpu.PCI.SubsystemID, "capture %s GPU %d disagrees on pci_sub_system_id", sku, i)
	}
	return got
}

// TestProfilePCIDeviceIDMatchesHardwareCapture holds every shipped profile's
// PCI identity, in BOTH copies, to the board it claims to model.
//
// The captures are the authority: their README names each one's node and tells
// whoever authors a profile to check it against what the real board reports.
// Nothing enforced it, and four profiles had drifted their device_id - `gb300`
// reported an HGX GB200 ID, `gb200` and `b200` reported IDs inside the Hopper
// range that no NVIDIA board carries, and `l40s` reported an L40 - while six of
// seven carried a subsystem_id belonging to some other board entirely.
//
// Neither word is cosmetic. device_id and subsystem_id both reach the rendered
// PCI sysfs tree (internal/pcisysfs/render.go), where `lspci` resolves them
// against the system's pci.ids and names a different GPU and a different board
// vendor than the one the mock claims to be; subsystem_id also reaches NVML
// callers as nvmlPciInfo_t.pciSubSystemId (pkg/gpu/mocknvml/engine/device.go).
//
// The profile set is globbed, not listed, so a profile added later is checked
// on its first run - and a profile with no matching capture fails rather than
// passing unnoticed.
func TestProfilePCIDeviceIDMatchesHardwareCapture(t *testing.T) {
	t.Parallel()

	sources := profileSources()

	// Both copies must ship the same SKUs; a profile added to one copy only is
	// a half-landed change, and the missing half would otherwise go unchecked.
	var skuSets []map[string]string
	for _, src := range sources {
		skuSets = append(skuSets, src.profiles(t))
	}
	for i := 1; i < len(skuSets); i++ {
		require.Equal(t, sortedKeys(skuSets[0]), sortedKeys(skuSets[i]),
			"%s and %s must ship the same profile SKUs", sources[0].label, sources[i].label)
	}

	for i, src := range sources {
		for _, sku := range sortedKeys(skuSets[i]) {
			path := skuSets[i][sku]
			t.Run(src.label+"/"+sku, func(t *testing.T) {
				t.Parallel()

				want := readCapture(t, sku)

				cfg, err := LoadYAMLConfig(path)
				require.NoError(t, err, "load profile %s", path)
				require.NotNil(t, cfg.DeviceDefaults.PCI, "profile %s declares no pci block", path)

				require.Equal(t, want.deviceID, cfg.DeviceDefaults.PCI.DeviceID,
					"%s %s device_defaults.pci.device_id must be the captured board's", src.label, sku)
				require.Equal(t, want.subsystemID, cfg.DeviceDefaults.PCI.SubsystemID,
					"%s %s device_defaults.pci.subsystem_id must be the captured board's", src.label, sku)
			})
		}
	}
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
