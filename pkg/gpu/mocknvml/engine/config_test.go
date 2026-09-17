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

func TestValidateMIGConfig_RejectsAnUnknownNVMLProfile(t *testing.T) {
	t.Parallel()

	err := validateMIGConfig(&MIGConfig{
		MaxGPUInstances: 7,
		SupportedProfiles: []MIGProfileSpec{
			{Name: "1g.10gb", NVMLProfile: "1_SLICE", Instances: 7, MemoryMB: 10240},
			{Name: "9g.99gb", NVMLProfile: "9_SLICE", Instances: 1, MemoryMB: 99999},
		},
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
		SupportedProfiles: []MIGProfileSpec{
			{Name: "1g.10gb", NVMLProfile: "1_SLICE", Instances: 7, MemoryMB: 10240},
			{Name: "1g.10gb duplicate", NVMLProfile: "1_SLICE", Instances: 7, MemoryMB: 10240},
		},
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
		SupportedProfiles: []MIGProfileSpec{
			{Name: "1g.10gb", NVMLProfile: "1_SLICE", Instances: 7, MemoryMB: 10240},
			{Name: "1g.10gb", NVMLProfile: "1_SLICE_REV1", Instances: 7, MemoryMB: 10240},
		},
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
		SupportedProfiles: []MIGProfileSpec{
			{Name: "7g.40gb", NVMLProfile: "7_SLICE", Instances: 1, MemoryMB: 40960},
		},
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
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := validateMIGConfig(&MIGConfig{
				MaxGPUInstances:   tt.boardSlices,
				SupportedProfiles: []MIGProfileSpec{tt.spec},
			})

			require.ErrorContains(t, err, tt.wantErr)
			// The row has to be locatable from the message alone.
			require.ErrorContains(t, err, tt.spec.Name)
		})
	}
}
