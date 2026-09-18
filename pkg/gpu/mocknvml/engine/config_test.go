// Copyright (c) 2025, NVIDIA CORPORATION.  All rights reserved.
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
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"

	"github.com/NVIDIA/go-nvml/pkg/nvml/mock/dgxa100"
	mockserver "github.com/NVIDIA/go-nvml/pkg/nvml/mock/server"
	"github.com/stretchr/testify/require"
	"sigs.k8s.io/yaml"
)

func TestDefaultConfig(t *testing.T) {
	config := DefaultConfig()
	require.NotNil(t, config, "DefaultConfig returned nil")
	require.Equal(t, 8, config.NumDevices, "Expected default NumDevices 8")
	require.Equal(t, "550.163.01", config.DriverVersion, "Expected default DriverVersion 550.163.01")
}

func TestLoadConfig_Defaults(t *testing.T) {
	// Clear config cache to ensure clean state
	ClearConfigCache()

	config := LoadConfig()
	require.NotNil(t, config, "LoadConfig returned nil")
	require.Equal(t, 8, config.NumDevices, "Expected default NumDevices 8")
	require.Equal(t, "550.163.01", config.DriverVersion, "Expected default DriverVersion")
}

func TestLoadConfig_NumDevices(t *testing.T) {
	tests := []struct {
		name     string
		envValue string
		setEnv   bool
		expected int
	}{
		{"Valid number", "4", true, 4},
		{"Zero devices", "0", true, 0},
		{"Max devices", "8", true, 8},
		{"Invalid string", "abc", true, 8}, // Should use default
		{"Negative number", "-1", true, 8}, // Should use default
		{"Empty string", "", false, 8},     // Should use default
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Clear config cache to ensure env vars take effect
			ClearConfigCache()

			if tt.setEnv {
				t.Setenv("MOCK_NVML_NUM_DEVICES", tt.envValue)
			}

			config := LoadConfig()
			require.Equal(t, tt.expected, config.NumDevices, "Expected NumDevices %d", tt.expected)
		})
	}
}

func TestLoadConfig_DriverVersion(t *testing.T) {
	// Clear config cache to ensure env vars take effect
	ClearConfigCache()

	customVersion := "999.99.99"
	t.Setenv("MOCK_NVML_DRIVER_VERSION", customVersion)

	config := LoadConfig()
	require.Equal(t, customVersion, config.DriverVersion, "Expected DriverVersion %s", customVersion)
}

func TestLoadConfig_AllEnvVars(t *testing.T) {
	// Clear config cache to ensure env vars take effect
	ClearConfigCache()

	t.Setenv("MOCK_NVML_NUM_DEVICES", "6")
	t.Setenv("MOCK_NVML_DRIVER_VERSION", "600.00.00")

	config := LoadConfig()
	require.Equal(t, 6, config.NumDevices, "NumDevices not set correctly")
	require.Equal(t, "600.00.00", config.DriverVersion, "DriverVersion not set correctly")
}

func TestLoadConfig_EmptyEnvVars(t *testing.T) {
	// Clear config cache to ensure env vars take effect
	ClearConfigCache()

	t.Setenv("MOCK_NVML_NUM_DEVICES", "")
	t.Setenv("MOCK_NVML_DRIVER_VERSION", "")

	config := LoadConfig()
	// Empty strings should result in defaults
	require.Equal(t, 8, config.NumDevices, "Expected default NumDevices 8")
	require.Equal(t, "550.163.01", config.DriverVersion, "Expected default DriverVersion")
}

func TestLoadConfig_YAMLNumDevices(t *testing.T) {
	// Create a temp config YAML with system.num_devices set
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	// Config has 2 devices listed but system.num_devices=4
	yamlContent := `version: "1.0"
system:
  driver_version: "550.163.01"
  num_devices: 4
device_defaults:
  name: "NVIDIA A100-SXM4-40GB"
devices:
  - index: 0
    uuid: "GPU-aaaa"
  - index: 1
    uuid: "GPU-bbbb"
`
	require.NoError(t, os.WriteFile(configPath, []byte(yamlContent), 0o644), "Failed to write config file")

	ClearConfigCache()
	t.Setenv("MOCK_NVML_CONFIG", configPath)

	config := LoadConfig()
	require.Equal(t, 4, config.NumDevices, "Expected NumDevices=4 from system.num_devices")
}

func TestLoadConfig_YAMLNumDevicesZero(t *testing.T) {
	// When system.num_devices is 0 (or unset), fall back to device list count
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	yamlContent := `version: "1.0"
system:
  driver_version: "550.163.01"
device_defaults:
  name: "NVIDIA A100-SXM4-40GB"
devices:
  - index: 0
    uuid: "GPU-aaaa"
  - index: 1
    uuid: "GPU-bbbb"
  - index: 2
    uuid: "GPU-cccc"
`
	require.NoError(t, os.WriteFile(configPath, []byte(yamlContent), 0o644), "Failed to write config file")

	ClearConfigCache()
	t.Setenv("MOCK_NVML_CONFIG", configPath)

	config := LoadConfig()
	require.Equal(t, 3, config.NumDevices, "Expected NumDevices=3 from device list")
}

func TestDiscoverConfigPath_NonLinux(t *testing.T) {
	if runtime.GOOS == "linux" {
		t.Skip("Test only applies to non-Linux platforms")
	}
	result := discoverConfigPath()
	require.Empty(t, result, "Expected empty string on non-Linux")
}

func TestDiscoverConfigPath_Linux(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Test only applies to Linux")
	}
	// On Linux without a mock .so loaded, should return empty
	result := discoverConfigPath()
	require.Empty(t, result, "Expected empty string when no libnvidia-ml.so is mapped")
}

func TestLoadConfig_AutoDiscoverFallback(t *testing.T) {
	// When MOCK_NVML_CONFIG is not set and auto-discovery fails,
	// should fall back to env vars / defaults
	ClearConfigCache()

	config := LoadConfig()
	require.NotNil(t, config, "LoadConfig returned nil")
	require.Equal(t, 8, config.NumDevices, "Expected default NumDevices 8")
}

// Per-device processes, decoded from real YAML through the inline-embedded
// DeviceConfig and merged: covers override (d0), explicit-clear (d1), inherit (d2).
func TestYAMLConfig_PerDeviceProcesses(t *testing.T) {
	const y = `
device_defaults:
  processes:
    - {pid: 1, type: "C"}
devices:
  - index: 0
    processes:
      - {pid: 4242, type: "C", sm_util: 75}
  - index: 1
    processes: []
`
	var yc YAMLConfig
	require.NoError(t, yaml.Unmarshal([]byte(y), &yc), "yaml decode")
	c := &Config{YAMLConfig: &yc}

	d0 := c.GetDeviceConfig(0)
	require.Len(t, d0.Processes, 1, "device 0 (override) len")
	require.Equal(t, uint32(4242), d0.Processes[0].PID, "device 0 (override) PID")
	require.Equal(t, uint32(75), d0.Processes[0].SmUtil, "device 0 (override) SmUtil")

	d1 := c.GetDeviceConfig(1) // processes: [] clears the default
	require.Empty(t, d1.Processes, "device 1 (explicit clear)")

	d2 := c.GetDeviceConfig(2) // no override -> inherit
	require.Len(t, d2.Processes, 1, "device 2 (inherit) len")
	require.Equal(t, uint32(1), d2.Processes[0].PID, "device 2 (inherit) PID")
}

// A device that does not declare minor_number takes the index, which is what
// the driver assigns when probe order follows PCI enumeration order. Treating
// an omitted key as minor 0 would point every such device at /dev/nvidia0.
func TestGetDeviceMinorNumber_DefaultsToIndex(t *testing.T) {
	t.Parallel()

	y := `
version: "1.0"
system:
  driver_version: "550.163.01"
devices:
  - index: 0
    uuid: "GPU-aaa"
  - index: 1
    uuid: "GPU-bbb"
`
	var yc YAMLConfig
	require.NoError(t, yaml.Unmarshal([]byte(y), &yc), "yaml decode")
	c := &Config{YAMLConfig: &yc}

	require.Equal(t, 0, c.GetDeviceMinorNumber(0))
	require.Equal(t, 1, c.GetDeviceMinorNumber(1))
	require.Equal(t, 2, c.GetDeviceMinorNumber(2), "device without an override entry")
}

// Nodes whose driver probe order does not follow PCI enumeration order report
// minor numbers that do not match the NVML index, so the profile must be able
// to say so — including minor 0 on a device that is not index 0.
func TestGetDeviceMinorNumber_HonorsExplicitValue(t *testing.T) {
	t.Parallel()

	y := `
version: "1.0"
system:
  driver_version: "550.163.01"
devices:
  - index: 0
    minor_number: 2
  - index: 1
    minor_number: 0
`
	var yc YAMLConfig
	require.NoError(t, yaml.Unmarshal([]byte(y), &yc), "yaml decode")
	c := &Config{YAMLConfig: &yc}

	require.Equal(t, 2, c.GetDeviceMinorNumber(0))
	require.Equal(t, 0, c.GetDeviceMinorNumber(1))
}

// Two devices claiming one minor number would collapse onto a single
// /dev/nvidia<N>, silently handing both GPUs the same character device.
func TestValidateYAMLConfig_RejectsDuplicateMinorNumbers(t *testing.T) {
	t.Parallel()

	y := `
version: "1.0"
system:
  driver_version: "550.163.01"
devices:
  - index: 0
    minor_number: 3
  - index: 1
    minor_number: 3
`
	var yc YAMLConfig
	require.NoError(t, yaml.Unmarshal([]byte(y), &yc), "yaml decode")

	require.ErrorContains(t, validateYAMLConfig(&yc), "duplicate device minor number: 3")
}

// A device that omits minor_number still occupies its index as a minor, so an
// explicit value elsewhere can collide with it. Checking only the values that
// were spelled out would let both devices reach the same /dev/nvidia<N>.
func TestValidateYAMLConfig_RejectsCollisionWithADefaultedMinorNumber(t *testing.T) {
	t.Parallel()

	y := `
version: "1.0"
system:
  driver_version: "550.163.01"
devices:
  - index: 0
    minor_number: 1
  - index: 1
`
	var yc YAMLConfig
	require.NoError(t, yaml.Unmarshal([]byte(y), &yc), "yaml decode")

	require.ErrorContains(t, validateYAMLConfig(&yc), "duplicate device minor number: 1")
}

// An implicit device — one the count covers but no entry describes — takes its
// index as a minor and can be collided with just the same.
func TestValidateYAMLConfig_RejectsCollisionWithAnUndeclaredDevice(t *testing.T) {
	t.Parallel()

	y := `
version: "1.0"
system:
  driver_version: "550.163.01"
  num_devices: 4
devices:
  - index: 0
    minor_number: 3
`
	var yc YAMLConfig
	require.NoError(t, yaml.Unmarshal([]byte(y), &yc), "yaml decode")

	require.ErrorContains(t, validateYAMLConfig(&yc), "duplicate device minor number: 3")
}

// A MIG device's UUID is its parent's with the instance ids spliced into the
// fourth group, so two parents that differ only there name the same partition.
// The UUID is how a consumer asks for a specific partition, and how the device
// plugin identifies the one it allocated.
func TestValidateYAMLConfig_RejectsUUIDsThatCollapseOntoOneMIGDevice(t *testing.T) {
	t.Parallel()

	y := `
version: "1.0"
system:
  driver_version: "550.163.01"
devices:
  - index: 0
    uuid: "GPU-01000100-0000-0000-0001-000000000000"
  - index: 1
    uuid: "GPU-01000100-0000-0000-0002-000000000000"
`
	var yc YAMLConfig
	require.NoError(t, yaml.Unmarshal([]byte(y), &yc), "yaml decode")

	require.ErrorContains(t, validateYAMLConfig(&yc),
		"devices 0 and 1 would derive the same MIG device UUIDs")
}

// The same check catches the blunter mistake it generalizes: two devices given
// one UUID collide as full GPUs, before MIG enters into it.
func TestValidateYAMLConfig_RejectsTwoDevicesSharingAUUID(t *testing.T) {
	t.Parallel()

	y := `
version: "1.0"
system:
  driver_version: "550.163.01"
devices:
  - index: 0
    uuid: "GPU-01000100-0000-0000-0000-000000000000"
  - index: 1
    uuid: "GPU-01000100-0000-0000-0000-000000000000"
`
	var yc YAMLConfig
	require.NoError(t, yaml.Unmarshal([]byte(y), &yc), "yaml decode")

	require.ErrorContains(t, validateYAMLConfig(&yc), "duplicate device uuid")
}

// Distinguishing parents must stay accepted: the shipped profiles vary the last
// group, which the derivation preserves.
func TestValidateYAMLConfig_AcceptsUUIDsThatDifferOutsideTheSplicedGroup(t *testing.T) {
	t.Parallel()

	y := `
version: "1.0"
system:
  driver_version: "550.163.01"
devices:
  - index: 0
    uuid: "GPU-01000100-0000-0000-0001-000000000000"
  - index: 1
    uuid: "GPU-01000100-0000-0000-0002-000000000001"
`
	var yc YAMLConfig
	require.NoError(t, yaml.Unmarshal([]byte(y), &yc), "yaml decode")

	require.NoError(t, validateYAMLConfig(&yc))
}

// Every profile the project ships has to survive its own validation. The
// checks here reject a config on properties derived from what it declares —
// minor numbers, MIG UUID stems — so tightening one can turn a shipped profile
// into a pod that will not start, with nothing between the change and a
// cluster to say so.
func TestValidateYAMLConfig_AcceptsEveryShippedProfile(t *testing.T) {
	t.Parallel()

	globs := []string{
		"../../../../deployments/nvml-mock/helm/nvml-mock/profiles/*.yaml",
		"../configs/*.yaml",
	}
	for _, glob := range globs {
		paths, err := filepath.Glob(glob)
		require.NoError(t, err)
		require.NotEmptyf(t, paths, "no profiles matched %q", glob)

		for _, path := range paths {
			t.Run(filepath.Base(path), func(t *testing.T) {
				t.Parallel()

				data, err := os.ReadFile(path)
				require.NoError(t, err)

				var yc YAMLConfig
				require.NoError(t, yaml.Unmarshal(data, &yc), "yaml decode")
				require.NoError(t, validateYAMLConfig(&yc))
			})
		}
	}
}

// stageCharDevs formats the node name from the minor and casts it to uint32.
// A negative value names a node the GPU-node pattern cannot match, so it
// survives pruning, and 255 is nvidiactl's.
func TestValidateYAMLConfig_RejectsMinorNumbersOutsideTheDeviceRange(t *testing.T) {
	t.Parallel()

	for name, minor := range map[string]int{"negative": -1, "nvidiactl": 255} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			y := fmt.Sprintf(`
version: "1.0"
system:
  driver_version: "550.163.01"
devices:
  - index: 0
    minor_number: %d
`, minor)
			var yc YAMLConfig
			require.NoError(t, yaml.Unmarshal([]byte(y), &yc), "yaml decode")

			require.ErrorContains(t, validateYAMLConfig(&yc), "device minor number out of range")
		})
	}
}

// The address the node agent announces in a kernel log has to be the one the
// running device serves, so the fallback here must track the base mock the
// engine builds devices from rather than a formula that happens to match today.
func TestBaseDevicePCIBusID_TracksTheBaseMock(t *testing.T) {
	t.Parallel()

	base := dgxa100.New()

	for i := range base.Devices {
		dev, ok := base.Devices[i].(*mockserver.Device)
		require.Truef(t, ok, "base device %d is not a mockserver.Device", i)
		require.Equalf(t, dev.PciBusID, BaseDevicePCIBusID(i),
			"device %d must report the address the base mock carries", i)
	}

	require.Empty(t, BaseDevicePCIBusID(MaxDevices), "no device exists past the base mock")
	require.Empty(t, BaseDevicePCIBusID(-1))
}

func TestValidateMIGConfig_ExplicitInstances(t *testing.T) {
	t.Parallel()

	start := 0
	tests := []struct {
		name    string
		mig     *MIGConfig
		wantErr string
	}{
		{
			name: "valid explicit layout",
			mig: &MIGConfig{
				ModeCurrent: "enabled",
				Instances: &[]MIGGPUInstanceRecord{
					{ID: 0, Profile: "1g.5gb", PlacementStart: &start},
					{ID: 2, Profile: "1g.5gb"},
				},
			},
		},
		{
			name: "empty list is valid and means no partitions",
			mig:  &MIGConfig{ModeCurrent: "enabled", Instances: &[]MIGGPUInstanceRecord{}},
		},
		{
			name: "duplicate instance ids",
			mig: &MIGConfig{
				ModeCurrent: "enabled",
				Instances: &[]MIGGPUInstanceRecord{
					{ID: 1, Profile: "1g.5gb"},
					{ID: 1, Profile: "1g.5gb"},
				},
			},
			wantErr: "duplicate GPU instance id 1",
		},
		{
			name: "neither profile nor profile_id",
			mig: &MIGConfig{
				ModeCurrent: "enabled",
				Instances:   &[]MIGGPUInstanceRecord{{ID: 0}},
			},
			wantErr: "must set either profile or profile_id",
		},
		{
			name: "duplicate compute instance ids",
			mig: &MIGConfig{
				ModeCurrent: "enabled",
				Instances: &[]MIGGPUInstanceRecord{{
					ID: 0, Profile: "1g.5gb",
					ComputeInstances: &[]MIGComputeInstanceRecord{{ID: 0, Profile: "1c"}, {ID: 0, Profile: "1c"}},
				}},
			},
			wantErr: "duplicate compute instance id 0",
		},
		{
			name: "empty compute instance list is valid and means none exist",
			mig: &MIGConfig{
				ModeCurrent: "enabled",
				Instances: &[]MIGGPUInstanceRecord{{
					ID: 0, Profile: "1g.5gb",
					ComputeInstances: &[]MIGComputeInstanceRecord{},
				}},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := validateMIGConfig(tt.mig)
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, tt.wantErr)
		})
	}
}

// The distinction the pointer exists for: absent means "no explicit layout",
// present-and-empty means "an explicit layout with nothing in it".
func TestMIGConfig_EmptyInstancesRoundTripsDistinctFromAbsent(t *testing.T) {
	t.Parallel()

	var absent MIGConfig
	require.NoError(t, yaml.Unmarshal([]byte("mode_current: enabled\n"), &absent))
	require.Nil(t, absent.Instances)

	var empty MIGConfig
	require.NoError(t, yaml.Unmarshal([]byte("mode_current: enabled\ninstances: []\n"), &empty))
	require.NotNil(t, empty.Instances)
	require.Empty(t, *empty.Instances)
}

// declaredRows completes each row with the placements and compute-instance
// listing a row is now required to declare, for the tests below whose subject
// is some other field. A row is given one slot at offset 0 wide enough for the
// whole partition and one compute instance spanning it, which is the least a
// row can say and still describe a partition.
//
// Rows that already declare either are left alone, so a test whose subject is
// the geometry still states it. A row NVML has no profile for is left alone
// too — there is no width to complete it from, and that row's own rejection is
// what its test is asserting.
func declaredRows(specs ...MIGProfileSpec) []MIGProfileSpec {
	rows := make([]MIGProfileSpec, 0, len(specs))
	for _, spec := range specs {
		profileEnum, known := gpuInstanceProfileEnum(spec.NVMLProfile)
		span, sized := gpuInstanceSliceCount(profileEnum)
		if known && sized {
			if spec.ProfileID == 0 {
				// The enum, which is distinct per row because a row is keyed
				// on it — so the ids stay unique without a test having to
				// invent numbers for them.
				spec.ProfileID = profileEnum
			}
			if len(spec.Placements) == 0 {
				spec.Placements = []MIGPlacementSpec{
					{Start: 0, Size: uint32(nextPowerOfTwo(span))},
				}
			}
			if len(spec.ComputeInstances) == 0 {
				spec.ComputeInstances = []MIGComputeInstanceSpec{
					{NVMLProfile: strconv.Itoa(span) + "_SLICE", Slices: span, Instances: 1},
				}
			}
		}
		rows = append(rows, spec)
	}
	return rows
}

func TestValidateMIGConfig_RejectsAnUnknownNVMLProfile(t *testing.T) {
	t.Parallel()

	err := validateMIGConfig(&MIGConfig{
		MaxGPUInstances: 7,
		SupportedProfiles: declaredRows(
			MIGProfileSpec{Name: "1g.10gb", NVMLProfile: "1_SLICE", Instances: 7, MemoryMB: 10240},
			MIGProfileSpec{Name: "9g.99gb", NVMLProfile: "9_SLICE", Instances: 1, MemoryMB: 99999},
		),
	})

	require.Error(t, err)
	// The offending value has to appear; validateMIGSupportedProfiles explains
	// why the document is refused rather than the row dropped.
	require.Contains(t, err.Error(), "9_SLICE")
}

func TestValidateMIGConfig_RejectsADuplicateNVMLProfile(t *testing.T) {
	t.Parallel()

	err := validateMIGConfig(&MIGConfig{
		MaxGPUInstances: 7,
		SupportedProfiles: declaredRows(
			MIGProfileSpec{Name: "1g.10gb", NVMLProfile: "1_SLICE", Instances: 7, MemoryMB: 10240},
			MIGProfileSpec{Name: "1g.10gb duplicate", NVMLProfile: "1_SLICE", Instances: 7, MemoryMB: 10240},
		),
	})

	require.Error(t, err)
	require.Contains(t, err.Error(), "1_SLICE")
}

func TestValidateMIGConfig_RejectsADuplicateProfileName(t *testing.T) {
	t.Parallel()

	// Distinct profiles, one display name. go-nvlib turns that name into the
	// nvidia.com/mig-<name> resource the cluster schedules on, so the two rows
	// would publish one ambiguous resource.
	err := validateMIGConfig(&MIGConfig{
		MaxGPUInstances: 7,
		SupportedProfiles: declaredRows(
			MIGProfileSpec{Name: "1g.10gb", NVMLProfile: "1_SLICE", Instances: 7, MemoryMB: 10240},
			MIGProfileSpec{Name: "1g.10gb", NVMLProfile: "1_SLICE_REV1", Instances: 7, MemoryMB: 10240},
		),
	})

	require.ErrorContains(t, err, `name "1g.10gb" is declared twice`)
}

func TestValidateMIGConfig_RejectsAProfileWithNoName(t *testing.T) {
	t.Parallel()

	err := validateMIGConfig(&MIGConfig{
		MaxGPUInstances: 7,
		SupportedProfiles: []MIGProfileSpec{
			{NVMLProfile: "1_SLICE", Instances: 7, MemoryMB: 10240},
		},
	})

	require.Error(t, err)
	require.Contains(t, err.Error(), "name")
}

// TestValidateMIGConfig_RejectsARowThatDeclaresNoGeometry covers the two keys
// a row used to be allowed to omit, when the placements and the
// compute-instance listing were computed from its slice count.
//
// Nothing computes them now, so an omission is a partition with nowhere to sit
// or a GPU instance nothing can run inside — and every NVML call on such a
// board still succeeds, answering with an empty listing. The message names the
// missing key because nothing else leads a contributor from that behaviour
// back to the row.
func TestValidateMIGConfig_RejectsARowThatDeclaresNoGeometry(t *testing.T) {
	t.Parallel()

	row := MIGProfileSpec{Name: "1g.10gb", NVMLProfile: "1_SLICE", Instances: 7, MemoryMB: 10240}

	err := validateMIGConfig(&MIGConfig{
		MaxGPUInstances:   7,
		SupportedProfiles: []MIGProfileSpec{row},
	})
	require.ErrorContains(t, err, "placements is required")
	require.ErrorContains(t, err, row.Name)

	withPlacements := row
	withPlacements.Placements = []MIGPlacementSpec{{Start: 0, Size: 1}}
	err = validateMIGConfig(&MIGConfig{
		MaxGPUInstances:   7,
		SupportedProfiles: []MIGProfileSpec{withPlacements},
	})
	require.ErrorContains(t, err, "compute_instances is required")
	require.ErrorContains(t, err, row.Name)
}

// TestValidateMIGConfig_RejectsADuplicateProfileID refuses two rows publishing
// one reported id. The id is what `nvidia-smi mig -cgi <id>` names, so a board
// declaring it twice leaves that command creating whichever partition the
// table resolves first.
//
// It could not happen while the ids came from a table per published listing,
// which was keyed on the profile enum. Each row states its own now, and the id
// a row that forgets the key publishes — 0 — is one a real full-board profile
// publishes too.
func TestValidateMIGConfig_RejectsADuplicateProfileID(t *testing.T) {
	t.Parallel()

	err := validateMIGConfig(&MIGConfig{
		MaxGPUInstances: 7,
		SupportedProfiles: declaredRows(
			MIGProfileSpec{Name: "1g.10gb", NVMLProfile: "1_SLICE", ProfileID: 19, Instances: 7, MemoryMB: 10240},
			MIGProfileSpec{Name: "2g.20gb", NVMLProfile: "2_SLICE", ProfileID: 19, Instances: 3, MemoryMB: 20480},
		),
	})

	require.ErrorContains(t, err, `profile_id 19 is already declared by "1g.10gb"`)
}

func TestValidateMIGConfig_AcceptsABoardThatDeclaresNoProfiles(t *testing.T) {
	t.Parallel()

	// t4 and l40s: not MIG-capable, and that is expressed by declaring nothing.
	require.NoError(t, validateMIGConfig(&MIGConfig{}))
}

func TestValidateMIGConfig_RejectsABoardWiderThanNVMLCanAddress(t *testing.T) {
	t.Parallel()

	// Nine slices is past NVML's widest profile, so the rows below cannot be
	// bounded against a width like this; validateMIGSupportedProfiles refuses
	// it before reading them.
	err := validateMIGConfig(&MIGConfig{
		MaxGPUInstances: 9,
		SupportedProfiles: []MIGProfileSpec{
			{Name: "1g.10gb", NVMLProfile: "1_SLICE", Instances: 7, MemoryMB: 10240},
		},
	})

	require.Error(t, err)
	require.Contains(t, err.Error(), "max_gpu_instances")
}

// The json tags are the contract with every shipped profile YAML, and a
// misspelled one drops its column silently — a decode short of a field is the
// one malformed table validateMIGSupportedProfiles cannot see.
//
// Every value below is deliberately nonzero and distinct from the others. A
// field asserted as zero would pass with its tag deleted, and two fields
// sharing a value would pass with their tags transposed, which would make the
// assertion cover the field in name only.
func TestMIGConfig_SupportedProfilesDecodeFromYAML(t *testing.T) {
	t.Parallel()

	var mig MIGConfig
	require.NoError(t, yaml.Unmarshal([]byte(`
max_gpu_instances: 7
supported_profiles:
  - name: 1g.10gb
    nvml_profile: 1_SLICE
    instances: 7
    memory_mb: 10240
    multiprocessors: 16
    copy_engines: 3
    decoders: 4
    encoders: 2
    jpeg: 5
    ofa: 6
`), &mig))

	require.Equal(t, []MIGProfileSpec{{
		Name:            "1g.10gb",
		NVMLProfile:     "1_SLICE",
		Instances:       7,
		MemoryMB:        10240,
		Multiprocessors: 16,
		CopyEngines:     3,
		Decoders:        4,
		Encoders:        2,
		JPEG:            5,
		OFA:             6,
	}}, mig.SupportedProfiles)

	// The row above states no geometry, which is a document validation
	// refuses; the tags of the fields that carry it are covered by the decode
	// test below. Completing the row is what leaves this one asserting that
	// the engine counts above are otherwise a valid document.
	mig.SupportedProfiles = declaredRows(mig.SupportedProfiles...)
	require.NoError(t, validateMIGConfig(&mig))
}

// A partition spanning its whole board is the one every MIG board ships: 7g on
// an A100 or H100, 4g on an A30. It is also the boundary the board-fit check
// sits on, and the rejecting cases below only reach that check from above the
// boundary, so a check written as >= would leave every one of them green while
// refusing the full-board profile of every MIG board.
func TestValidateMIGConfig_AcceptsAProfileSpanningTheWholeBoard(t *testing.T) {
	t.Parallel()

	require.NoError(t, validateMIGConfig(&MIGConfig{
		MaxGPUInstances: 7,
		SupportedProfiles: declaredRows(
			MIGProfileSpec{Name: "7g.40gb", NVMLProfile: "7_SLICE", Instances: 1, MemoryMB: 40960},
		),
	}))
}

// TestValidateMIGConfig_AcceptsTheRoundingRealBoardsShow guards the loose end
// of the name/memory cross-check. Real allocations fall short of the size
// their name advertises, by a margin that grows with the partition: an A100's
// 1g.5gb holds 4864 MiB and an A30's 4g.24gb holds 23344, 1.2 GiB short. A
// check tight enough to be exact would refuse both.
func TestValidateMIGConfig_AcceptsTheRoundingRealBoardsShow(t *testing.T) {
	t.Parallel()

	require.NoError(t, validateMIGConfig(&MIGConfig{
		MaxGPUInstances: 7,
		SupportedProfiles: declaredRows(
			MIGProfileSpec{Name: "1g.5gb", NVMLProfile: "1_SLICE", Instances: 7, MemoryMB: 4864},
			MIGProfileSpec{Name: "1g.23gb", NVMLProfile: "1_SLICE_REV2", Instances: 4, MemoryMB: 23040},
			MIGProfileSpec{Name: "7g.40gb", NVMLProfile: "7_SLICE", Instances: 1, MemoryMB: 40192},
		),
	}))

	require.NoError(t, validateMIGConfig(&MIGConfig{
		MaxGPUInstances: 4,
		SupportedProfiles: declaredRows(
			MIGProfileSpec{Name: "4g.24gb", NVMLProfile: "4_SLICE", Instances: 1, MemoryMB: 23344},
		),
	}))
}

// Every row here decodes cleanly and was accepted before validateMIGProfileSpec
// existed, which would have put it in the table `nvidia-smi mig -lgip` prints.
// See that function for what makes each of them impossible.
func TestValidateMIGConfig_RejectsAProfileThatCannotBeCreated(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		boardSlices int
		spec        MIGProfileSpec
		wantErr     string
	}{
		"wider than its own board": {
			boardSlices: 4,
			spec:        MIGProfileSpec{Name: "7g.40gb", NVMLProfile: "7_SLICE", Instances: 1, MemoryMB: 40960},
			wantErr:     `nvml_profile "7_SLICE" spans 7 slices, more than the 4 max_gpu_instances this board is wide`,
		},
		"no memory": {
			boardSlices: 7,
			spec:        MIGProfileSpec{Name: "1g.0gb", NVMLProfile: "1_SLICE", Instances: 7},
			wantErr:     "memory_mb must be greater than 0",
		},
		"no instances": {
			boardSlices: 7,
			spec:        MIGProfileSpec{Name: "1g.10gb", NVMLProfile: "1_SLICE", Instances: 0, MemoryMB: 10240},
			wantErr:     "instances must be between 1 and 7 for a 1-slice profile on a 7-slice board, got 0",
		},
		"more instances than fit the board": {
			boardSlices: 7,
			spec:        MIGProfileSpec{Name: "2g.20gb", NVMLProfile: "2_SLICE", Instances: 7, MemoryMB: 20480},
			wantErr:     "instances must be between 1 and 3 for a 2-slice profile on a 7-slice board, got 7",
		},
		// The shape the gb300 memory defect had: a row advertising one size
		// in its name and holding another. Name and memory are independent
		// literals, so nothing else in the row contradicts it.
		"memory that contradicts its own name": {
			boardSlices: 7,
			spec:        MIGProfileSpec{Name: "3g.139gb", NVMLProfile: "3_SLICE", Instances: 2, MemoryMB: 92160},
			wantErr:     `memory_mb 92160 does not hold the 139 GB the name "3g.139gb" advertises`,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := validateMIGConfig(&MIGConfig{
				MaxGPUInstances:   tt.boardSlices,
				SupportedProfiles: declaredRows(tt.spec),
			})

			require.ErrorContains(t, err, tt.wantErr)
			// The row has to be locatable from the message alone.
			require.ErrorContains(t, err, tt.spec.Name)
		})
	}
}

// The declared geometry carries four facts the Go code used to derive, and its
// json tags are the contract with every shipped profile YAML on the same terms
// as TestMIGConfig_SupportedProfilesDecodeFromYAML: a misspelled tag drops a
// column silently.
//
// Every numeric value below is nonzero and distinct within its struct, so a
// transposed pair of tags cannot pass either. The row is a 2-slice profile
// because that is the narrowest shape whose placements have a nonzero start —
// a full-board row places only at 0, where a dropped `start` would decode to
// the value the assertion expects.
func TestMIGConfig_DeclaredGeometryDecodesFromYAML(t *testing.T) {
	t.Parallel()

	var mig MIGConfig
	require.NoError(t, yaml.Unmarshal([]byte(`
max_gpu_instances: 7
supported_profiles:
  - name: 2g.20gb
    nvml_profile: 2_SLICE
    slices: 2
    profile_id: 21
    instances: 3
    memory_mb: 20480
    multiprocessors: 42
    copy_engines: 12
    decoders: 13
    encoders: 14
    jpeg: 15
    ofa: 16
    placements:
      - {start: 0, size: 2}
      - {start: 4, size: 2}
    compute_instances:
      - nvml_profile: 1_SLICE
        slices: 1
        instances: 2
        multiprocessors: 21
        shared_copy_engines: 3
        decoders: 4
        encoders: 5
        jpeg: 6
        ofa: 7
      - nvml_profile: 2_SLICE
        slices: 2
        instances: 1
        multiprocessors: 42
        shared_copy_engines: 8
        decoders: 9
        encoders: 10
        jpeg: 11
        ofa: 17
`), &mig))

	require.Equal(t, []MIGProfileSpec{{
		Name:            "2g.20gb",
		NVMLProfile:     "2_SLICE",
		Slices:          2,
		ProfileID:       21,
		Instances:       3,
		MemoryMB:        20480,
		Multiprocessors: 42,
		CopyEngines:     12,
		Decoders:        13,
		Encoders:        14,
		JPEG:            15,
		OFA:             16,
		Placements: []MIGPlacementSpec{
			{Start: 0, Size: 2},
			{Start: 4, Size: 2},
		},
		ComputeInstances: []MIGComputeInstanceSpec{
			{
				NVMLProfile:       "1_SLICE",
				Slices:            1,
				Instances:         2,
				Multiprocessors:   21,
				SharedCopyEngines: 3,
				Decoders:          4,
				Encoders:          5,
				JPEG:              6,
				OFA:               7,
			},
			{
				NVMLProfile:       "2_SLICE",
				Slices:            2,
				Instances:         1,
				Multiprocessors:   42,
				SharedCopyEngines: 8,
				Decoders:          9,
				Encoders:          10,
				JPEG:              11,
				OFA:               17,
			},
		},
	}}, mig.SupportedProfiles)
	require.NoError(t, validateMIGConfig(&mig))
}

// Every row here decodes cleanly and describes a board that would partition
// wrongly: a placement the board has no room for, a compute instance that
// cannot sit inside its GPU instance, or a declared width contradicting the
// one its own NVML enum carries.
//
// The width cases are why declaring `slices` is safe rather than a return to
// two sources for one fact: the enum still knows its own span, so the declared
// count is cross-checked against it instead of either being trusted alone.
func TestValidateMIGConfig_RejectsDeclaredGeometryThatCannotPartitionTheBoard(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		boardSlices int
		spec        MIGProfileSpec
		wantErr     string
	}{
		"a placement running past the board's memory units": {
			boardSlices: 7,
			spec: MIGProfileSpec{
				Name: "1g.10gb", NVMLProfile: "1_SLICE", Instances: 7, MemoryMB: 10240,
				Placements: []MIGPlacementSpec{{Start: 6, Size: 4}},
			},
			wantErr: "placements[0]: start 6 plus size 4 runs past the 8 memory units a 7-slice board has",
		},
		"a placement size that is not a power of two": {
			boardSlices: 7,
			spec: MIGProfileSpec{
				Name: "3g.20gb", NVMLProfile: "3_SLICE", Instances: 2, MemoryMB: 20480,
				Placements: []MIGPlacementSpec{{Start: 0, Size: 3}},
			},
			wantErr: "placements[0]: size 3 is not a power of two",
		},
		"a placement of no size": {
			boardSlices: 7,
			spec: MIGProfileSpec{
				Name: "1g.10gb", NVMLProfile: "1_SLICE", Instances: 7, MemoryMB: 10240,
				Placements: []MIGPlacementSpec{{Start: 2}},
			},
			wantErr: "placements[0]: size 0 is not a power of two",
		},
		"two placements at one offset": {
			boardSlices: 7,
			spec: MIGProfileSpec{
				Name: "2g.20gb", NVMLProfile: "2_SLICE", Instances: 3, MemoryMB: 20480,
				Placements: []MIGPlacementSpec{{Start: 2, Size: 2}, {Start: 2, Size: 2}},
			},
			wantErr: "placements[1]: start 2 is declared twice",
		},
		"a declared width that disagrees with its own enum": {
			boardSlices: 7,
			spec: MIGProfileSpec{
				Name: "1g.10gb", NVMLProfile: "1_SLICE", Slices: 2, Instances: 7, MemoryMB: 10240,
			},
			wantErr: `slices 2 disagrees with the 1 slices nvml_profile "1_SLICE" spans`,
		},
		"a compute instance naming an enum NVML does not have": {
			boardSlices: 7,
			spec: MIGProfileSpec{
				Name: "2g.20gb", NVMLProfile: "2_SLICE", Instances: 3, MemoryMB: 20480,
				ComputeInstances: []MIGComputeInstanceSpec{
					{NVMLProfile: "9_SLICE", Slices: 9, Instances: 1},
				},
			},
			wantErr: `compute_instances[0]: unknown nvml_profile "9_SLICE"`,
		},
		"a compute instance wider than its GPU instance": {
			boardSlices: 7,
			spec: MIGProfileSpec{
				Name: "2g.20gb", NVMLProfile: "2_SLICE", Instances: 3, MemoryMB: 20480,
				ComputeInstances: []MIGComputeInstanceSpec{
					{NVMLProfile: "4_SLICE", Slices: 4, Instances: 1},
				},
			},
			wantErr: `compute_instances[0]: nvml_profile "4_SLICE" spans 4 slices, more than the 2 its GPU instance spans`,
		},
		"a compute instance width that disagrees with its own enum": {
			boardSlices: 7,
			spec: MIGProfileSpec{
				Name: "2g.20gb", NVMLProfile: "2_SLICE", Instances: 3, MemoryMB: 20480,
				ComputeInstances: []MIGComputeInstanceSpec{
					{NVMLProfile: "2_SLICE", Slices: 1, Instances: 1},
				},
			},
			wantErr: `compute_instances[0]: slices 1 disagrees with the 2 slices nvml_profile "2_SLICE" spans`,
		},
		"more compute instances than fit the GPU instance": {
			boardSlices: 7,
			spec: MIGProfileSpec{
				Name: "2g.20gb", NVMLProfile: "2_SLICE", Instances: 3, MemoryMB: 20480,
				ComputeInstances: []MIGComputeInstanceSpec{
					{NVMLProfile: "1_SLICE", Slices: 1, Instances: 3},
				},
			},
			wantErr: "compute_instances[0]: instances must be between 1 and 2 for a 1-slice compute instance in a 2-slice GPU instance, got 3",
		},
		// The compute-instance table is keyed on the enum, so two rows naming
		// one profile collapse into a single entry rather than being reported.
		"a compute instance declared twice": {
			boardSlices: 7,
			spec: MIGProfileSpec{
				Name: "2g.20gb", NVMLProfile: "2_SLICE", Instances: 3, MemoryMB: 20480,
				ComputeInstances: []MIGComputeInstanceSpec{
					{NVMLProfile: "1_SLICE", Slices: 1, Instances: 2},
					{NVMLProfile: "1_SLICE", Slices: 1, Instances: 2},
				},
			},
			wantErr: `compute_instances[1]: nvml_profile "1_SLICE" is declared twice`,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := validateMIGConfig(&MIGConfig{
				MaxGPUInstances:   tt.boardSlices,
				SupportedProfiles: declaredRows(tt.spec),
			})

			require.ErrorContains(t, err, tt.wantErr)
			// The row has to be locatable from the message alone.
			require.ErrorContains(t, err, tt.spec.Name)
		})
	}
}

// TestValidateMIGConfig_AcceptsTheGeometryItReports feeds each shipped board's
// assembled tables back into the config as declared geometry and asserts
// validation accepts it.
//
// It closes the loop between the two halves: what a board reports has to be
// something a board may declare. A validation rule that refuses geometry the
// transcription hands out would refuse a board the mock already supports, and
// the spelling of every compute-instance enum has to survive the round trip
// for a contributor to be able to write the row at all.
func TestValidateMIGConfig_AcceptsTheGeometryItReports(t *testing.T) {
	t.Parallel()

	ciProfileNames := make(map[int]string, len(computeInstanceProfileEnums))
	for name, ciEnum := range computeInstanceProfileEnums {
		ciProfileNames[ciEnum] = name
	}

	for _, board := range migGoldenBoards {
		t.Run(board.profile, func(t *testing.T) {
			t.Parallel()

			migCfg := migConfigOfShippedProfile(t, board.profile)
			profiles, ids, supported := migProfilesFromConfig(migCfg, board.memoryBytes)
			require.True(t, supported, "%s must declare a MIG profile table", board.profile)

			declared := *migCfg
			declared.SupportedProfiles = nil
			for _, spec := range migCfg.SupportedProfiles {
				profileEnum, ok := gpuInstanceProfileEnum(spec.NVMLProfile)
				require.True(t, ok)
				sliceCount, ok := gpuInstanceSliceCount(profileEnum)
				require.True(t, ok)

				spec.Slices = sliceCount
				spec.ProfileID = ids.reported(profileEnum)
				// The row it came from declares this geometry too, so the
				// reported values replace what is there rather than adding to
				// it.
				spec.Placements = nil
				spec.ComputeInstances = nil
				for _, p := range profiles.GpuInstancePlacements[profileEnum] {
					spec.Placements = append(spec.Placements,
						MIGPlacementSpec{Start: p.Start, Size: p.Size})
				}
				for ciEnum, ci := range profiles.ComputeInstanceProfiles[profileEnum] {
					name, named := ciProfileNames[ciEnum]
					require.True(t, named,
						"compute instance profile %d has no YAML spelling", ciEnum)
					spec.ComputeInstances = append(spec.ComputeInstances, MIGComputeInstanceSpec{
						NVMLProfile:       name,
						Slices:            int(ci.SliceCount),
						Instances:         int(ci.InstanceCount),
						Multiprocessors:   int(ci.MultiprocessorCount),
						SharedCopyEngines: int(ci.SharedCopyEngineCount),
						Decoders:          int(ci.SharedDecoderCount),
						Encoders:          int(ci.SharedEncoderCount),
						JPEG:              int(ci.SharedJpegCount),
						OFA:               int(ci.SharedOfaCount),
					})
				}
				declared.SupportedProfiles = append(declared.SupportedProfiles, spec)
			}

			require.NoError(t, validateMIGConfig(&declared))
		})
	}
}

// migBoardDeclaringNoProfiles is a board whose mig: block carries the
// properties of the silicon and leaves the profile table to be resolved from
// elsewhere — the shape every shipped profile takes once its table has moved
// out.
const migBoardDeclaringNoProfiles = `
version: "1.0"
system:
  driver_version: "550.163.01"
device_defaults:
  mig:
    mode_current: enabled
    max_gpu_instances: 7
`

const migExternalTable1g = `
version: 1
supported_profiles:
  - name: 1g.5gb
    nvml_profile: 1_SLICE
    profile_id: 19
    instances: 7
    memory_mb: 4864
    placements:
      - start: 0
        size: 1
    compute_instances:
      - nvml_profile: 1_SLICE
        instances: 1
`

const migExternalTable7g = `
version: 1
supported_profiles:
  - name: 7g.40gb
    nvml_profile: 7_SLICE
    profile_id: 0
    instances: 1
    memory_mb: 40192
    placements:
      - start: 0
        size: 8
    compute_instances:
      - nvml_profile: 7_SLICE
        instances: 1
`

// stageMIGProfileFixtures writes a config, and optionally a sibling table
// beside it, into a fresh directory and returns the config path.
func stageMIGProfileFixtures(t *testing.T, config, sibling string) string {
	t.Helper()
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte(config), 0o600))
	if sibling != "" {
		require.NoError(t, os.WriteFile(filepath.Join(dir, "config.mig.yaml"), []byte(sibling), 0o600))
	}
	return configPath
}

// writeMIGProfileTable stages a table on its own, away from any config, so a
// test can point MOCK_MIG_PROFILES_CONFIG at a path the sibling rule would
// never reach.
func writeMIGProfileTable(t *testing.T, table string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "mig-table.yaml")
	require.NoError(t, os.WriteFile(path, []byte(table), 0o600))
	return path
}

// The tests below set MOCK_MIG_PROFILES_CONFIG — to "" where they mean "no
// env var" — so an inherited value cannot decide the outcome. t.Setenv rules
// out t.Parallel for them.

func TestLoadYAMLConfig_AttachesAnExternalMIGProfileTable(t *testing.T) {
	t.Setenv("MOCK_MIG_PROFILES_CONFIG", "")

	cfg, err := LoadYAMLConfig(stageMIGProfileFixtures(t, migBoardDeclaringNoProfiles, migExternalTable1g))

	require.NoError(t, err)
	require.Len(t, cfg.DeviceDefaults.MIG.SupportedProfiles, 1)
	require.Equal(t, "1g.5gb", cfg.DeviceDefaults.MIG.SupportedProfiles[0].Name)
	require.Equal(t, 19, cfg.DeviceDefaults.MIG.SupportedProfiles[0].ProfileID)
}

func TestLoadYAMLConfig_MIGProfilesEnvVarWinsOverTheSibling(t *testing.T) {
	configPath := stageMIGProfileFixtures(t, migBoardDeclaringNoProfiles, migExternalTable1g)
	t.Setenv("MOCK_MIG_PROFILES_CONFIG", writeMIGProfileTable(t, migExternalTable7g))

	cfg, err := LoadYAMLConfig(configPath)

	require.NoError(t, err)
	require.Len(t, cfg.DeviceDefaults.MIG.SupportedProfiles, 1)
	require.Equal(t, "7g.40gb", cfg.DeviceDefaults.MIG.SupportedProfiles[0].Name)
}

// A board with no table anywhere is not MIG-capable, which is what a board
// declaring no profiles has always meant. It is not a load error: the mount
// the chart provides is the thing that can be absent, and refusing the config
// would take the whole board down with it.
func TestLoadYAMLConfig_NoMIGProfileTableLeavesTheBoardNotMIGCapable(t *testing.T) {
	t.Setenv("MOCK_MIG_PROFILES_CONFIG", "")

	cfg, err := LoadYAMLConfig(stageMIGProfileFixtures(t, migBoardDeclaringNoProfiles, ""))

	require.NoError(t, err)
	require.Empty(t, cfg.DeviceDefaults.MIG.SupportedProfiles)
}

// Absent and present-but-wrong are different failures. A table that does not
// parse is an operator mistake, and dropping it would present as the board
// above — deliberately non-MIG — when it is in fact broken.
func TestLoadYAMLConfig_RefusesAMalformedExternalMIGProfileTable(t *testing.T) {
	t.Setenv("MOCK_MIG_PROFILES_CONFIG", "")

	_, err := LoadYAMLConfig(stageMIGProfileFixtures(t, migBoardDeclaringNoProfiles, "supported_profiles: [oh: dear\n"))

	require.Error(t, err)
	require.Contains(t, err.Error(), "config.mig.yaml")
}

// Splitting the file must not split the guarantees: a table validated as
// inline data is validated identically once it arrives from its own file.
func TestLoadYAMLConfig_ExternalMIGProfileTableFacesTheSameValidation(t *testing.T) {
	t.Setenv("MOCK_MIG_PROFILES_CONFIG", "")
	narrowBoard := `
version: "1.0"
system:
  driver_version: "550.163.01"
device_defaults:
  mig:
    max_gpu_instances: 4
`

	_, err := LoadYAMLConfig(stageMIGProfileFixtures(t, narrowBoard, migExternalTable7g))

	require.Error(t, err)
	require.Contains(t, err.Error(), "spans 7 slices, more than the 4 max_gpu_instances this board is wide")
}

// Two authoritative tables for one board is an ambiguity, not a precedence
// question: whichever source loses is a table an operator wrote and the mock
// silently ignored. The config refuses to load and names both, the way
// validateMIGProfileRef already refuses a profile named two ways.
func TestLoadYAMLConfig_RefusesAMIGProfileTableDeclaredInlineAndExternally(t *testing.T) {
	t.Setenv("MOCK_MIG_PROFILES_CONFIG", "")
	inline := migBoardDeclaringNoProfiles + `    supported_profiles:
      - name: 1g.5gb
        nvml_profile: 1_SLICE
        profile_id: 19
        instances: 7
        memory_mb: 4864
        placements:
          - start: 0
            size: 1
        compute_instances:
          - nvml_profile: 1_SLICE
            instances: 1
`

	_, err := LoadYAMLConfig(stageMIGProfileFixtures(t, inline, migExternalTable7g))

	require.Error(t, err)
	require.Contains(t, err.Error(), "supported_profiles")
	require.Contains(t, err.Error(), "config.mig.yaml")
}
