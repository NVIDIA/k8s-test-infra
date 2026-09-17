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

// Package engine is the pure-Go mock NVML runtime driving the mocknvml bridge.
package engine

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"

	"sigs.k8s.io/yaml"
)

var (
	configCache     *Config
	configCachePath string
	configCacheMu   sync.Mutex
)

// ClearConfigCache clears the cached configuration.
// Use in tests to ensure clean state between test runs.
func ClearConfigCache() {
	configCacheMu.Lock()
	defer configCacheMu.Unlock()
	configCache = nil
	configCachePath = ""
}

// Config holds configuration for the mock engine
type Config struct {
	NumDevices    int
	DriverVersion string

	// YAMLConfig holds the parsed YAML configuration (nil if not using YAML)
	YAMLConfig *YAMLConfig
}

// DefaultConfig returns the default configuration
func DefaultConfig() *Config {
	return &Config{
		NumDevices:    8,            // Default to DGX A100 behavior
		DriverVersion: "550.163.01", // Must match nvidia-smi version
	}
}

// LoadConfig loads configuration from YAML file (if specified) or environment variables.
// Results are cached - subsequent calls with the same config path return cached config.
//
// Config resolution order:
//  1. MOCK_NVML_CONFIG env var (explicit path)
//  2. Auto-discover from /proc/self/maps (Linux only)
//  3. Fall back to env vars / defaults
func LoadConfig() *Config {
	configPath := os.Getenv("MOCK_NVML_CONFIG")
	if configPath == "" {
		configPath = discoverConfigPath()
	}

	configCacheMu.Lock()
	defer configCacheMu.Unlock()

	// Return cached config if path matches
	if configCache != nil && configCachePath == configPath {
		return configCache
	}

	config := DefaultConfig()

	// Check for YAML config file first
	if configPath != "" {
		yamlConfig, err := LoadYAMLConfig(configPath)
		if err == nil {
			applyYAMLConfig(config, yamlConfig, configPath)
			return config
		}
		// Log visible warning since user explicitly requested this config file
		warnLog("Failed to load YAML config from %s: %v, falling back to defaults\n", configPath, err)
	}

	// Fall back to environment variable overrides
	if num := os.Getenv("MOCK_NVML_NUM_DEVICES"); num != "" {
		if val, err := strconv.Atoi(num); err == nil && val >= 0 {
			config.NumDevices = val
		}
	}

	if ver := os.Getenv("MOCK_NVML_DRIVER_VERSION"); ver != "" {
		config.DriverVersion = ver
	}

	debugLog("[CONFIG] Using env/default config: %d devices, driver %s\n", config.NumDevices, config.DriverVersion)

	// Cache the config
	configCache = config
	configCachePath = configPath
	return config
}

// applyYAMLConfig populates config from a successfully-loaded YAMLConfig and
// primes the LoadConfig cache. Callers only reach this once LoadYAMLConfig
// returned nil error, so the YAML values are trusted here — the fall-back /
// env-var branches stay in LoadConfig itself.
func applyYAMLConfig(config *Config, yamlConfig *YAMLConfig, configPath string) {
	config.YAMLConfig = yamlConfig
	// Apply system-level config from YAML
	config.DriverVersion = yamlConfig.System.DriverVersion
	config.NumDevices = len(yamlConfig.Devices)
	if config.NumDevices == 0 {
		config.NumDevices = 8 // Default if no devices specified
	}

	// system.num_devices overrides the device list count.
	// The node agent stamps it from the runtime GPU count so the .so knows the
	// desired count without consumers needing to set env vars.
	if yamlConfig.System.NumDevices > 0 {
		config.NumDevices = yamlConfig.System.NumDevices
	}

	// Topology overlay: when a cluster-level topology ConfigMap is
	// mounted into the pod we look up the current Kubernetes node
	// (NODE_NAME) and override the fabric cluster UUID / clique ID
	// on the YAML defaults so every device on this node reports
	// the correct ComputeDomain identity. Nodes not present in the
	// topology fall through to the YAML-default fabric config (or
	// to NOT_SUPPORTED when none is set, matching non-GB200 GPUs).
	applyTopologyOverlay(yamlConfig)

	debugLog("[CONFIG] Loaded YAML config: %d devices, driver %s\n", config.NumDevices, config.DriverVersion)

	configCache = config
	configCachePath = configPath
}

// ConfigOverridePathFor resolves the runtime overrides file path from the resolved
// config path. MOCK_NVML_OVERRIDES wins; otherwise overrides.yaml sits next to
// config.yaml. Returns "" when no config path is known.
func ConfigOverridePathFor(configPath string) string {
	if p := os.Getenv("MOCK_NVML_OVERRIDES"); p != "" {
		return p
	}
	if configPath == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(configPath), "overrides.yaml")
}

// discoverConfigPath attempts to locate the config file by reading /proc/self/maps
// to find the path of the loaded mock NVML .so, then navigating to the config directory.
//
// Expected layout:
//
//	.so at:     <driver_root>/usr/lib64/libnvidia-ml.so.<version>
//	config at:  <driver_root>/config/config.yaml
//
// Returns empty string if auto-discovery is not possible (non-Linux, file not found).
//
//nolint:cyclop // existing complexity; refactor deferred
func discoverConfigPath() string {
	if runtime.GOOS != "linux" {
		return ""
	}

	f, err := os.Open("/proc/self/maps")
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.Contains(line, "libnvidia-ml.so") {
			continue
		}
		// /proc/self/maps format: addr-addr perms offset dev inode   pathname
		fields := strings.Fields(line)
		if len(fields) < 6 {
			continue
		}
		// The last field is normally the pathname, but after file replacement
		// (e.g. library upgrade) it can be "pathname (deleted)". In that case
		// the absolute path is the second-to-last field.
		soPath := fields[len(fields)-1]
		if soPath == "(deleted)" && len(fields) >= 7 {
			soPath = fields[len(fields)-2]
		}
		if !strings.HasPrefix(soPath, "/") {
			continue
		}
		// Navigate from <driver_root>/usr/lib64/libnvidia-ml.so.* to <driver_root>/config/config.yaml
		libDir := filepath.Dir(soPath)                   // .../usr/lib64
		driverRoot := filepath.Dir(filepath.Dir(libDir)) // .../driver_root
		configPath := filepath.Join(driverRoot, "config", "config.yaml")
		if _, err := os.Stat(configPath); err == nil {
			debugLog("[CONFIG] Auto-discovered config at %s\n", configPath)
			return configPath
		}
	}
	if err := scanner.Err(); err != nil {
		debugLog("[CONFIG] Error scanning /proc/self/maps: %v\n", err)
	}
	return ""
}

// LoadYAMLConfig loads and parses a YAML configuration file
func LoadYAMLConfig(path string) (*YAMLConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config file: %w", err)
	}

	var config YAMLConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("parsing YAML config: %w", err)
	}

	// Validate config
	if err := validateYAMLConfig(&config); err != nil {
		return nil, fmt.Errorf("validating config: %w", err)
	}

	return &config, nil
}

// validateYAMLConfig performs basic validation on the loaded config
func validateYAMLConfig(config *YAMLConfig) error {
	if config.Version == "" {
		return errors.New("config version is required")
	}

	if config.System.DriverVersion == "" {
		return errors.New("system.driver_version is required")
	}

	// Validate device indices are unique
	seen := make(map[int]bool)
	for _, dev := range config.Devices {
		if seen[dev.Index] {
			return fmt.Errorf("duplicate device index: %d", dev.Index)
		}
		seen[dev.Index] = true
	}

	if err := ValidateMinorNumbers(config); err != nil {
		return err
	}

	if err := validateDeviceUUIDs(config); err != nil {
		return err
	}

	if err := validateMIGConfig(config.DeviceDefaults.MIG); err != nil {
		return fmt.Errorf("device_defaults.mig: %w", err)
	}
	for _, dev := range config.Devices {
		if err := validateMIGConfig(dev.MIG); err != nil {
			return fmt.Errorf("devices[index=%d].mig: %w", dev.Index, err)
		}
	}

	return nil
}

// validateMIGConfig rejects a MIG block that cannot mean anything, so a typo
// in a profile is a load error the operator sees rather than a device that
// quietly comes up unpartitioned.
//
// Whether the board actually offers a named profile is not checked here: that
// needs the device's resolved name and memory, which only exist once the
// device is built.
func validateMIGConfig(mig *MIGConfig) error {
	if mig == nil {
		return nil
	}

	if err := validateMIGSupportedProfiles(mig.SupportedProfiles, mig.MaxGPUInstances); err != nil {
		return err
	}

	if err := validateMIGMode("mode_current", mig.ModeCurrent); err != nil {
		return err
	}
	if err := validateMIGMode("mode_pending", mig.ModePending); err != nil {
		return err
	}
	if mig.MaxGPUInstances < 0 {
		return errors.New("max_gpu_instances cannot be negative")
	}

	if err := validateMIGGPUInstanceDecls(mig.GPUInstances); err != nil {
		return err
	}
	if mig.Instances != nil {
		if err := validateMIGInstances(*mig.Instances); err != nil {
			return err
		}
	}
	return nil
}

// validateMIGSupportedProfiles rejects a declared profile table that cannot
// describe the board it is declared on.
//
// Every rejection refuses the whole document rather than dropping the row, and
// names the offending value. This is the rationale the checks below and their
// tests share: a table one row short is a board that partitions differently
// from what it advertises, behind an NVML surface that still answers every
// call successfully, and nothing in that surface leads a consumer back to the
// profile YAML.
func validateMIGSupportedProfiles(profiles []MIGProfileSpec, maxGPUInstances int) error {
	if len(profiles) == 0 {
		return nil
	}

	// A declared table needs a board width to place its partitions on, and
	// NVML's widest GPU instance profile spans eight slices. Beyond that no
	// profile could occupy the extra slices, so the document is malformed
	// rather than describing a board the code has yet to learn about.
	// Validating the width first is what lets the rows below be bounded
	// against it.
	if maxGPUInstances < 1 || maxGPUInstances > maxGPUInstanceSlices {
		return fmt.Errorf("max_gpu_instances must be between 1 and %d when supported_profiles is declared, got %d",
			maxGPUInstanceSlices, maxGPUInstances)
	}

	seenProfiles := make(map[string]struct{}, len(profiles))
	seenNames := make(map[string]struct{}, len(profiles))
	seenIDs := make(map[int]string, len(profiles))
	for i, spec := range profiles {
		// The name is checked here rather than alongside the other required
		// fields because it is what locates every other rejection of the row.
		if spec.Name == "" {
			return fmt.Errorf("supported_profiles[%d]: name is required", i)
		}
		if err := validateMIGProfileSpec(spec, maxGPUInstances); err != nil {
			return fmt.Errorf("supported_profiles[%d] (%s): %w", i, spec.Name, err)
		}
		if _, dup := seenProfiles[spec.NVMLProfile]; dup {
			return fmt.Errorf("supported_profiles[%d] (%s): nvml_profile %q is declared twice",
				i, spec.Name, spec.NVMLProfile)
		}
		// Display names are deduplicated as well as profiles, even though the
		// table itself is keyed on the profile: go-nvlib derives the
		// nvidia.com/mig-<name> resource name from the display name, so two
		// rows sharing one publish a Kubernetes resource that resolves to
		// either of them.
		if _, dup := seenNames[spec.Name]; dup {
			return fmt.Errorf("supported_profiles[%d]: name %q is declared twice", i, spec.Name)
		}
		// The reported id is what `nvidia-smi mig -cgi <id>` names, so two
		// rows sharing one leave that command creating either partition. The
		// ids used to come from a table per published listing, which could not
		// collide; now each row states its own, and the id a row that forgets
		// the key publishes — 0 — is the one a real full-board profile
		// publishes too.
		if other, dup := seenIDs[spec.ProfileID]; dup {
			return fmt.Errorf("supported_profiles[%d] (%s): profile_id %d is already declared by %q",
				i, spec.Name, spec.ProfileID, other)
		}
		seenProfiles[spec.NVMLProfile] = struct{}{}
		seenNames[spec.Name] = struct{}{}
		seenIDs[spec.ProfileID] = spec.Name
	}
	return nil
}

// maxGPUInstanceSlices is the widest board a profile document may declare.
// NVML's widest GPU instance profile is GPU_INSTANCE_PROFILE_8_SLICE, so a
// board claiming more compute slices than that is a malformed document rather
// than a board the code has yet to learn about — there is no profile that
// could occupy the extra slices.
//
// It is also what keeps the doubling in nextPowerOfTwo below in range for any
// width a document can contain.
const maxGPUInstanceSlices = 8

// validateMIGProfileSpec rejects a row that advertises a profile nothing can be
// created from: a partition wider than its board or holding no memory has
// nowhere to sit, and a row claiming more instances than fit claims partitions
// that cannot all exist.
//
// The span a row occupies is read off its NVML profile rather than declared,
// so what is bounded here is the board: a 7-slice partition has no room on a
// 4-slice board, whatever NVML could address.
//
// The caller locates the row it passes, so these messages carry the offending
// value alone.
func validateMIGProfileSpec(spec MIGProfileSpec, maxGPUInstances int) error {
	// The enum is resolved before anything derived from it is checked, so that
	// a row naming a profile NVML has never had is reported as that, rather
	// than as a geometry the name never had in the first place.
	profileEnum, ok := gpuInstanceProfileEnum(spec.NVMLProfile)
	if !ok {
		return fmt.Errorf("unknown nvml_profile %q", spec.NVMLProfile)
	}
	span, mapped := gpuInstanceSliceCount(profileEnum)
	// No document reaches this while gpuInstanceProfileEnums and
	// gpuInstanceSliceCount agree on these profiles, which the enum table's
	// guard test asserts. It stays because a later edit to either can break
	// that agreement, and because it is what keeps the divisor below nonzero.
	if !mapped {
		return fmt.Errorf("nvml_profile %q has no known slice count", spec.NVMLProfile)
	}
	// The enum itself confines the span to 1..8, so the open question is
	// whether the profile fits this board: a partition spanning the whole
	// board is legal — 7g.40gb is the A100's widest profile — and one slice
	// wider than the board has nowhere to sit.
	if span > maxGPUInstances {
		return fmt.Errorf("nvml_profile %q spans %d slices, more than the %d max_gpu_instances this board is wide",
			spec.NVMLProfile, span, maxGPUInstances)
	}
	if err := validateDeclaredSliceCount(spec.Slices, span, spec.NVMLProfile); err != nil {
		return err
	}
	if spec.MemoryMB == 0 {
		return errors.New("memory_mb must be greater than 0")
	}
	if err := validateMIGProfileMemoryMatchesName(spec); err != nil {
		return err
	}
	// A 2-slice profile fits a 7-slice board three times, not seven.
	if fits := maxGPUInstances / span; spec.Instances < 1 || spec.Instances > fits {
		return fmt.Errorf("instances must be between 1 and %d for a %d-slice profile on a %d-slice board, got %d",
			fits, span, maxGPUInstances, spec.Instances)
	}
	return validateMIGProfileGeometry(spec, span, maxGPUInstances)
}

// validateMIGProfileGeometry refuses a row that does not describe where its
// partition sits and what can run inside it.
//
// A row describes its partition completely or not at all. Nothing computes the
// geometry a row leaves out any more, so an empty list is not a default — it
// is a partition with nowhere to sit, or a GPU instance that can hold no
// compute instance, behind an NVML surface that still answers every call
// successfully. Naming the missing key is what a half-written board gets
// instead of one that looks fine until something tries to partition it.
func validateMIGProfileGeometry(spec MIGProfileSpec, span, maxGPUInstances int) error {
	if len(spec.Placements) == 0 {
		return errors.New("placements is required: a profile that declares no placement has nowhere on the board to sit")
	}
	if err := validateMIGPlacements(spec.Placements, maxGPUInstances); err != nil {
		return err
	}
	if len(spec.ComputeInstances) == 0 {
		return errors.New("compute_instances is required: a profile that declares none offers no compute instance to run on")
	}
	return validateMIGComputeInstances(spec.ComputeInstances, span)
}

// validateDeclaredSliceCount cross-checks a declared width against the width
// the NVML enum it is declared beside already carries.
//
// The enum knows its own span, so the declared count is a second reading of
// one fact rather than a second source for it. Without the check a row could
// bind the 7-slice enum and call itself one slice wide: every bound computed
// from the declared count would treat it as a 1g while the partition it
// creates takes seven sevenths of the board.
//
// Zero means nothing is declared, and the span the enum already carries
// stands in. The width is the one piece of geometry an enum does state, so
// reading it from there is a lookup rather than a derivation.
func validateDeclaredSliceCount(declared, span int, nvmlProfile string) error {
	if declared != 0 && declared != span {
		return fmt.Errorf("slices %d disagrees with the %d slices nvml_profile %q spans",
			declared, span, nvmlProfile)
	}
	return nil
}

// validateMIGPlacements refuses a placement list that would put a partition
// where the board cannot hold it. It computes nothing the row left out; the
// caller has already refused a row that declared no placement at all.
//
// The checks encode the geometry a board partitions by:
//
//   - Start and Size are memory units, of which a board has the next power of
//     two at or above its width, so the bound is eight on a 7-slice board and
//     a 7g placement of size 8 is in range while a 1g at start 6 of size 4 is
//     not. The sum is widened before the comparison so a start near the top of
//     uint32 cannot wrap past the bound.
//   - Size is a power of two because the units divide the board exactly and
//     every start is aligned to its own size. That is what lets a 1g, a 2g and
//     a 3g placement coexist without a partial overlap, and it makes a size of
//     zero — a slot holding nothing — fall out of the same check.
//   - Two placements at one offset advertise a single slot twice, so a
//     consumer enumerating them sees room for two partitions where the board
//     has room for one.
func validateMIGPlacements(placements []MIGPlacementSpec, maxGPUInstances int) error {
	memoryUnits := nextPowerOfTwo(maxGPUInstances)
	seen := make(map[uint32]struct{}, len(placements))
	for i, p := range placements {
		if p.Size == 0 || p.Size&(p.Size-1) != 0 {
			return fmt.Errorf("placements[%d]: size %d is not a power of two", i, p.Size)
		}
		if uint64(p.Start)+uint64(p.Size) > uint64(memoryUnits) {
			return fmt.Errorf("placements[%d]: start %d plus size %d runs past the %d memory units a %d-slice board has",
				i, p.Start, p.Size, memoryUnits, maxGPUInstances)
		}
		if _, dup := seen[p.Start]; dup {
			return fmt.Errorf("placements[%d]: start %d is declared twice", i, p.Start)
		}
		seen[p.Start] = struct{}{}
	}
	return nil
}

// nextPowerOfTwo returns the smallest power of two at or above n, and 1 for
// any n below 1. It converts a board's compute-slice count into the number of
// memory units its placements are measured in.
//
// Callers must bound n. The result is reached by doubling up from 1, so for an
// n within a factor of two of the largest int the doubling overflows to
// negative and then to zero and the loop never ends. Computing it as
// 1 << bits.Len(uint(n-1)) overflows on the same inputs, so the bound has to
// come from the caller rather than from here — which is what
// maxGPUInstanceSlices is checked before any of this runs.
func nextPowerOfTwo(n int) int {
	p := 1
	for p < n {
		p *= 2
	}
	return p
}

// validateMIGComputeInstances refuses a compute-instance listing that could
// not exist inside the GPU instance declaring it.
//
// Widths go through validateDeclaredSliceCount like the GPU instance's own,
// and are then bounded by the GPU instance: a 4c compute
// instance inside a 2g partition has nowhere to sit, and more compute
// instances than fit claim partitions that cannot all exist. The table is
// keyed on the enum, so a profile named twice would collapse to one entry and
// advertise a listing one row short of what the YAML reads as.
//
// As with the placements, the caller has already refused a row whose listing
// is empty, so what is checked here is a listing that exists.
func validateMIGComputeInstances(computeInstances []MIGComputeInstanceSpec, giSlices int) error {
	seen := make(map[string]struct{}, len(computeInstances))
	for i, ci := range computeInstances {
		ciEnum, ok := computeInstanceProfileEnum(ci.NVMLProfile)
		if !ok {
			return fmt.Errorf("compute_instances[%d]: unknown nvml_profile %q", i, ci.NVMLProfile)
		}
		span, mapped := computeInstanceSliceCount(ciEnum)
		// Unreachable while computeInstanceProfileEnums and
		// computeInstanceSliceCount agree, which a guard test asserts. It
		// stays because it is what keeps the divisor below nonzero.
		if !mapped {
			return fmt.Errorf("compute_instances[%d]: nvml_profile %q has no known slice count",
				i, ci.NVMLProfile)
		}
		if err := validateDeclaredSliceCount(ci.Slices, span, ci.NVMLProfile); err != nil {
			return fmt.Errorf("compute_instances[%d]: %w", i, err)
		}
		if span > giSlices {
			return fmt.Errorf("compute_instances[%d]: nvml_profile %q spans %d slices, more than the %d its GPU instance spans",
				i, ci.NVMLProfile, span, giSlices)
		}
		if fits := giSlices / span; ci.Instances < 1 || ci.Instances > fits {
			return fmt.Errorf("compute_instances[%d]: instances must be between 1 and %d for a %d-slice compute instance in a %d-slice GPU instance, got %d",
				i, fits, span, giSlices, ci.Instances)
		}
		if _, dup := seen[ci.NVMLProfile]; dup {
			return fmt.Errorf("compute_instances[%d]: nvml_profile %q is declared twice", i, ci.NVMLProfile)
		}
		seen[ci.NVMLProfile] = struct{}{}
	}
	return nil
}

// migProfileNameSize reads the GB figure out of a profile name — the 139 in
// "3g.139gb", suffixes and all.
var migProfileNameSize = regexp.MustCompile(`^\d+g\.(\d+)gb`)

// validateMIGProfileMemoryMatchesName refuses a row whose declared memory
// contradicts the size its own name advertises.
//
// The two are independent literals in the YAML, and only the full-board row is
// checked arithmetically anywhere else, so without this a row can read
// `name: 3g.139gb` beside `memory_mb: 92160` — a partition advertising 139 GB
// and handing over 90 — with every test still green.
//
// The comparison is loose on purpose. A name is the raw allocation rounded up
// to a fraction of the board, and a board holds some memory back, so the two
// never match exactly: an A100's 1g.5gb holds 4864 MiB and an A30's 4g.24gb
// holds 23344, 1.2 GiB short. The shortfall scales with the partition, so the
// slack does too, with a floor for the small ones.
//
// A name that does not spell a size is left alone: nothing else constrains the
// spelling, and inventing a constraint here would refuse a board whose
// partitions NVIDIA names some other way.
func validateMIGProfileMemoryMatchesName(spec MIGProfileSpec) error {
	match := migProfileNameSize.FindStringSubmatch(spec.Name)
	if match == nil {
		return nil
	}
	namedGB, err := strconv.ParseUint(match[1], 10, 64)
	if err != nil {
		return fmt.Errorf("name %q advertises a size that is not a number", spec.Name)
	}

	namedMB := namedGB * 1024
	slack := max(namedMB/16, 1024)
	if spec.MemoryMB+slack < namedMB || spec.MemoryMB > namedMB+slack {
		return fmt.Errorf("memory_mb %d does not hold the %d GB the name %q advertises",
			spec.MemoryMB, namedGB, spec.Name)
	}
	return nil
}

func validateMIGGPUInstanceDecls(gpuInstances []MIGGPUInstanceConfig) error {
	for i, gi := range gpuInstances {
		if err := validateMIGProfileRef(gi.Profile, gi.ProfileID); err != nil {
			return fmt.Errorf("gpu_instances[%d]: %w", i, err)
		}
		if gi.Count < 0 {
			return fmt.Errorf("gpu_instances[%d]: count cannot be negative, got %d", i, gi.Count)
		}
		for j, ci := range gi.ComputeInstances {
			if err := validateMIGProfileRef(ci.Profile, ci.ProfileID); err != nil {
				return fmt.Errorf("gpu_instances[%d].compute_instances[%d]: %w", i, j, err)
			}
			if ci.Count < 0 {
				return fmt.Errorf("gpu_instances[%d].compute_instances[%d]: count cannot be negative, got %d", i, j, ci.Count)
			}
		}
	}
	return nil
}

func validateMIGInstances(instances []MIGGPUInstanceRecord) error {
	seen := make(map[uint32]bool, len(instances))
	for i, gi := range instances {
		if seen[gi.ID] {
			return fmt.Errorf("instances[%d]: duplicate GPU instance id %d", i, gi.ID)
		}
		seen[gi.ID] = true
		if err := validateMIGProfileRef(gi.Profile, gi.ProfileID); err != nil {
			return fmt.Errorf("instances[%d]: %w", i, err)
		}
		if gi.PlacementStart != nil && *gi.PlacementStart < 0 {
			return fmt.Errorf("instances[%d]: placement_start cannot be negative, got %d", i, *gi.PlacementStart)
		}
		// Scoped rather than skipped with a continue, so a later check on the
		// instance itself still runs for a record that omits the key.
		if gi.ComputeInstances != nil {
			ciSeen := make(map[uint32]bool, len(*gi.ComputeInstances))
			for j, ci := range *gi.ComputeInstances {
				if ciSeen[ci.ID] {
					return fmt.Errorf("instances[%d].compute_instances[%d]: duplicate compute instance id %d", i, j, ci.ID)
				}
				ciSeen[ci.ID] = true
				if err := validateMIGProfileRef(ci.Profile, ci.ProfileID); err != nil {
					return fmt.Errorf("instances[%d].compute_instances[%d]: %w", i, j, err)
				}
			}
		}
	}
	return nil
}

func validateMIGMode(field, value string) error {
	switch value {
	case "", "enabled", "disabled":
		return nil
	}
	return fmt.Errorf("%s must be \"enabled\" or \"disabled\", got %q", field, value)
}

// validateMIGProfileRef enforces that a profile is named exactly one way.
func validateMIGProfileRef(profile string, profileID *int) error {
	switch {
	case profile != "" && profileID != nil:
		return fmt.Errorf("sets both profile %q and profile_id %d; use one", profile, *profileID)
	case profile == "" && profileID == nil:
		return errors.New("must set either profile or profile_id")
	case profileID != nil && *profileID < 0:
		return fmt.Errorf("profile_id cannot be negative, got %d", *profileID)
	}
	return nil
}

// GetDeviceConfig returns the device configuration for a specific index,
// merging defaults with per-device overrides
func (c *Config) GetDeviceConfig(index int) *DeviceConfig {
	if c.YAMLConfig == nil {
		return nil
	}

	// Start with a copy of defaults. The copy is shallow, so every pointer
	// field still aliases the shared defaults. mergeDeviceOverride writes
	// THROUGH the PCI pointer instead of replacing it, so clone PCIConfig here
	// or a per-device PCI override lands on the defaults and leaks into every
	// device merged afterwards (issue #589). mergePlatformOverride clones
	// Platform itself; the remaining branches replace the pointer wholesale.
	merged := c.YAMLConfig.DeviceDefaults
	if merged.PCI != nil {
		pci := *merged.PCI
		merged.PCI = &pci
	}

	// Find and apply per-device overrides
	for _, override := range c.YAMLConfig.Devices {
		if override.Index == index {
			mergeDeviceOverride(&merged, &override)
			break
		}
	}

	return &merged
}

// GetDeviceUUID returns the UUID for a specific device index
func (c *Config) GetDeviceUUID(index int) string {
	if c.YAMLConfig == nil {
		return ""
	}

	for _, dev := range c.YAMLConfig.Devices {
		if dev.Index == index {
			return dev.UUID
		}
	}
	return ""
}

// GetDeviceMinorNumber returns the minor number for a specific device index.
func (c *Config) GetDeviceMinorNumber(index int) int {
	return DeviceMinorNumber(c.YAMLConfig, index)
}

// maxDeviceMinor is the highest minor major 195 leaves for a GPU: the driver
// keeps 255 for nvidiactl.
const maxDeviceMinor = 254

// DeviceMinorNumber returns the /dev/nvidia<N> a device is staged under,
// defaulting to the index for devices that do not declare one — the numbering
// a driver produces when it probes in PCI enumeration order.
//
// The agent and the engine both resolve minors through here so the nodes that
// get staged and the nodes the visibility filter looks for cannot drift apart.
func DeviceMinorNumber(config *YAMLConfig, index int) int {
	if config == nil {
		return index
	}

	for _, dev := range config.Devices {
		if dev.Index == index && dev.MinorNumber != nil {
			return *dev.MinorNumber
		}
	}
	return index
}

// ValidateMinorNumbers rejects minors that no device node can carry and minors
// two devices would end up sharing. Defaulted devices take part: one that never
// declares a minor still occupies its index, so an explicit value elsewhere can
// collide with it.
func ValidateMinorNumbers(config *YAMLConfig) error {
	if config == nil {
		return nil
	}

	for _, dev := range config.Devices {
		if dev.MinorNumber == nil {
			continue
		}
		if *dev.MinorNumber < 0 || *dev.MinorNumber > maxDeviceMinor {
			return fmt.Errorf("device %d: device minor number out of range (0-%d): %d",
				dev.Index, maxDeviceMinor, *dev.MinorNumber)
		}
	}

	seen := make(map[int]int, deviceSpace(config))
	for _, index := range deviceIndices(config) {
		minor := DeviceMinorNumber(config, index)
		if other, dup := seen[minor]; dup {
			return fmt.Errorf("duplicate device minor number: %d (devices %d and %d)", minor, other, index)
		}
		seen[minor] = index
	}

	return nil
}

// validateDeviceUUIDs rejects UUIDs that two devices would end up sharing,
// either as full GPUs or once their partitions are named.
//
// A UUID is how a consumer asks for one specific device — nvmlDeviceGetHandleByUUID
// resolves it, and the device plugin reports the UUID of what it allocated — so
// two devices answering to one UUID hand out whichever was found first. A MIG
// partition inherits the problem: its UUID is derived from its parent's, so
// parents that differ only where the derivation writes give corresponding
// partitions identical UUIDs even though the GPUs themselves are distinct.
//
// The collision is detected by asking the derivation itself for one fixed pair
// of instance ids, rather than restating which part of a UUID it writes: the
// id contribution is the same for both parents, so agreeing there is agreeing
// for every partition either board can carry.
//
// Devices that declare no UUID keep the base mock's own, which is already
// distinct per device, so they do not take part.
func validateDeviceUUIDs(config *YAMLConfig) error {
	if config == nil {
		return nil
	}

	type declaration struct {
		index int
		uuid  string
	}
	byUUID := make(map[string]declaration, len(config.Devices))
	byMIGStem := make(map[string]declaration, len(config.Devices))

	for _, dev := range config.Devices {
		if dev.UUID == "" {
			continue
		}
		this := declaration{index: dev.Index, uuid: dev.UUID}

		if other, dup := byUUID[dev.UUID]; dup {
			return fmt.Errorf("duplicate device uuid: %q (devices %d and %d)",
				dev.UUID, other.index, dev.Index)
		}
		byUUID[dev.UUID] = this

		stem := migDeviceUUID(dev.UUID, 0, 0)
		if other, dup := byMIGStem[stem]; dup {
			return fmt.Errorf(
				"device uuids %q and %q differ only where MIG instance ids are spliced in: "+
					"devices %d and %d would derive the same MIG device UUIDs",
				other.uuid, dev.UUID, other.index, dev.Index)
		}
		byMIGStem[stem] = this
	}

	return nil
}

// deviceIndices lists every device the config brings into being: those the
// count covers, plus any the overrides declare beyond it.
func deviceIndices(config *YAMLConfig) []int {
	n := deviceSpace(config)
	indices := make([]int, 0, n)
	seen := make(map[int]bool, n)
	for i := 0; i < n; i++ {
		indices = append(indices, i)
		seen[i] = true
	}
	for _, dev := range config.Devices {
		if !seen[dev.Index] {
			indices = append(indices, dev.Index)
			seen[dev.Index] = true
		}
	}
	sort.Ints(indices)
	return indices
}

// deviceSpace is how many devices the config describes before any runtime cap.
func deviceSpace(config *YAMLConfig) int {
	n := len(config.Devices)
	if config.System.NumDevices > n {
		n = config.System.NumDevices
	}
	return n
}

// GetDevicePCIBusID returns the PCI bus ID for a specific device index
func (c *Config) GetDevicePCIBusID(index int) string {
	if c.YAMLConfig == nil {
		return ""
	}

	for _, dev := range c.YAMLConfig.Devices {
		if dev.Index == index && dev.PCI != nil {
			return dev.PCI.BusID
		}
	}
	return ""
}

// mergeDeviceOverride merges non-zero override values into the base config
//
//nolint:cyclop // existing complexity; refactor deferred
func mergeDeviceOverride(base *DeviceConfig, override *DeviceOverride) {
	if override.Name != "" {
		base.Name = override.Name
	}
	if override.Serial != "" {
		base.Serial = override.Serial
	}
	if override.Brand != "" {
		base.Brand = override.Brand
	}
	if override.BoardPartNumber != "" {
		base.BoardPartNumber = override.BoardPartNumber
	}
	if override.VBIOSVersion != "" {
		base.VBIOSVersion = override.VBIOSVersion
	}
	if override.Architecture != "" {
		base.Architecture = override.Architecture
	}
	if override.PCI != nil {
		if base.PCI == nil {
			base.PCI = &PCIConfig{}
		}
		if override.PCI.BusID != "" {
			base.PCI.BusID = override.PCI.BusID
		}
		if override.PCI.DeviceID != 0 {
			base.PCI.DeviceID = override.PCI.DeviceID
		}
		if override.PCI.SubsystemID != 0 {
			base.PCI.SubsystemID = override.PCI.SubsystemID
		}
	}
	if override.Memory != nil {
		base.Memory = override.Memory
	}
	if override.BAR1Memory != nil {
		base.BAR1Memory = override.BAR1Memory
	}
	if override.Power != nil {
		base.Power = override.Power
	}
	if override.Thermal != nil {
		base.Thermal = override.Thermal
	}
	if override.Clocks != nil {
		base.Clocks = override.Clocks
	}
	if override.ClocksThrottleReasons != nil {
		base.ClocksThrottleReasons = override.ClocksThrottleReasons
	}
	if override.Utilization != nil {
		base.Utilization = override.Utilization
	}
	if override.ECC != nil {
		base.ECC = override.ECC
	}
	if override.DynamicMetrics != nil {
		base.DynamicMetrics = override.DynamicMetrics
	}
	if override.Failure != nil {
		base.Failure = override.Failure
	}
	if override.Fabric != nil {
		base.Fabric = override.Fabric
	}
	if override.NVLinkError != nil {
		base.NVLinkError = override.NVLinkError
	}
	if override.Processes != nil {
		base.Processes = override.Processes // nil = not overridden; [] = explicit clear
	}
	if override.MIG != nil {
		base.MIG = override.MIG
	}
	if override.Platform != nil {
		mergePlatformOverride(base, override.Platform)
	}
	// Add more fields as needed
}

// mergePlatformOverride merges per-field rather than replacing the block, so a
// device can set its own module_id — the one field that varies between the GPUs
// of a node — without restating the chassis, slot, tray, and host id it shares
// with them.
//
// The block is copied before it is written to: GetDeviceConfig copies
// DeviceDefaults shallowly, so every device's merge starts out pointing at the
// same PlatformConfig. Editing that in place would give all of them whichever
// module id was merged last.
func mergePlatformOverride(base *DeviceConfig, override *PlatformConfig) {
	if base.Platform == nil {
		base.Platform = &PlatformConfig{}
	} else {
		clone := *base.Platform
		base.Platform = &clone
	}
	if override.ChassisSerialNumber != "" {
		base.Platform.ChassisSerialNumber = override.ChassisSerialNumber
	}
	if override.SlotNumber != 0 {
		base.Platform.SlotNumber = override.SlotNumber
	}
	if override.TrayIndex != 0 {
		base.Platform.TrayIndex = override.TrayIndex
	}
	if override.HostID != 0 {
		base.Platform.HostID = override.HostID
	}
	if override.PeerType != "" {
		base.Platform.PeerType = override.PeerType
	}
	if override.ModuleID != 0 {
		base.Platform.ModuleID = override.ModuleID
	}
}

// applyTopologyOverlay rewrites yamlConfig.DeviceDefaults.Fabric (and any
// per-device override that already carries a Fabric block) based on a
// cluster-level topology document. The lookup key is the Kubernetes node
// name supplied through the NODE_NAME environment variable (set via the
// downward API in the DaemonSet). When either the topology file or the
// NODE_NAME is unset, this is a no-op.
//
// Resolution order for the topology path:
//  1. MOCK_TOPOLOGY_CONFIG env var (explicit path)
//  2. /config/topology.yaml (canonical helm mount)
//
//nolint:cyclop // existing complexity; refactor deferred
func applyTopologyOverlay(yamlConfig *YAMLConfig) {
	nodeName := os.Getenv("NODE_NAME")
	if nodeName == "" {
		return
	}
	topoPath := os.Getenv("MOCK_TOPOLOGY_CONFIG")
	if topoPath == "" {
		topoPath = "/config/topology.yaml"
	}
	if _, err := os.Stat(topoPath); err != nil {
		// No topology mounted — leave fabric config untouched.
		return
	}
	data, err := os.ReadFile(topoPath)
	if err != nil {
		warnLog("Failed to read topology %s: %v\n", topoPath, err)
		return
	}
	var topo TopologyDocument
	if err := yaml.Unmarshal(data, &topo); err != nil {
		warnLog("Failed to parse topology %s: %v\n", topoPath, err)
		return
	}
	// The profile is the gate: if the loaded YAML has no fabric block on
	// its DeviceDefaults, the GPU type modelled by this profile is not
	// fabric-attached (e.g. A100). Synthesising a FabricConfig here would
	// silently start reporting GB200-style fabric info on every device,
	// which is exactly what real NVML does *not* do on those GPUs.
	if yamlConfig.DeviceDefaults.Fabric == nil {
		debugLog("[CONFIG] Topology overlay: profile has no fabric defaults, skipping overlay for node=%s\n", nodeName)
		return
	}
	for _, domain := range topo.Domains {
		for _, clique := range domain.Cliques {
			for _, n := range clique.Nodes {
				if n != nodeName {
					continue
				}
				overrideFabric(yamlConfig, domain.UUID, clique.ID)
				debugLog("[CONFIG] Topology overlay: node=%s domain=%s clique=%d\n",
					nodeName, domain.UUID, clique.ID)
				return
			}
		}
	}
	debugLog("[CONFIG] Topology overlay: node=%s not in topology, leaving fabric defaults\n", nodeName)
}

// overrideFabric pins the supplied cluster UUID / clique ID onto the
// already-present DeviceDefaults.Fabric (the caller guarantees it is
// non-nil — see applyTopologyOverlay). Per-device overrides that carry
// their own Fabric block get the same treatment so the entire node
// reports a consistent fabric identity.
func overrideFabric(yamlConfig *YAMLConfig, clusterUUID string, cliqueID uint32) {
	yamlConfig.DeviceDefaults.Fabric.ClusterUUID = clusterUUID
	yamlConfig.DeviceDefaults.Fabric.CliqueID = cliqueID
	if yamlConfig.DeviceDefaults.Fabric.State == "" {
		yamlConfig.DeviceDefaults.Fabric.State = "completed"
	}
	// forward-compat: no profile currently ships per-device fabric blocks,
	// but if one does in the future we keep the whole node coherent by
	// rewriting those too rather than silently letting them diverge.
	for i := range yamlConfig.Devices {
		if yamlConfig.Devices[i].Fabric == nil {
			continue
		}
		yamlConfig.Devices[i].Fabric.ClusterUUID = clusterUUID
		yamlConfig.Devices[i].Fabric.CliqueID = cliqueID
		if yamlConfig.Devices[i].Fabric.State == "" {
			yamlConfig.Devices[i].Fabric.State = "completed"
		}
	}
}

// Note: debugLog is defined in utils.go to avoid duplication
