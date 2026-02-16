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
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
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
		NumDevices:    8, // Default to DGX A100 behavior
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
		if err != nil {
			// Log visible warning since user explicitly requested this config file
			warnLog("Failed to load YAML config from %s: %v, falling back to defaults\n", configPath, err)
		} else {
			config.YAMLConfig = yamlConfig
			// Apply system-level config from YAML
			config.DriverVersion = yamlConfig.System.DriverVersion
			config.NumDevices = len(yamlConfig.Devices)
			if config.NumDevices == 0 {
				config.NumDevices = 8 // Default if no devices specified
			}

			// system.num_devices overrides the device list count.
			// setup.sh injects this so the .so knows the desired GPU count
			// without consumers needing to set env vars.
			if yamlConfig.System.NumDevices > 0 {
				config.NumDevices = yamlConfig.System.NumDevices
			}
			debugLog("[CONFIG] Loaded YAML config: %d devices, driver %s\n", config.NumDevices, config.DriverVersion)

			// Cache the config
			configCache = config
			configCachePath = configPath
			return config
		}
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

// discoverConfigPath attempts to locate the config file by reading /proc/self/maps
// to find the path of the loaded mock NVML .so, then navigating to the config directory.
//
// Expected layout:
//
//	.so at:     <driver_root>/usr/lib64/libnvidia-ml.so.<version>
//	config at:  <driver_root>/config/config.yaml
//
// Returns empty string if auto-discovery is not possible (non-Linux, file not found).
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
		soPath := fields[len(fields)-1]
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
		return fmt.Errorf("config version is required")
	}

	if config.System.DriverVersion == "" {
		return fmt.Errorf("system.driver_version is required")
	}

	// Validate device indices are unique
	seen := make(map[int]bool)
	for _, dev := range config.Devices {
		if seen[dev.Index] {
			return fmt.Errorf("duplicate device index: %d", dev.Index)
		}
		seen[dev.Index] = true
	}

	return nil
}

// GetDeviceConfig returns the device configuration for a specific index,
// merging defaults with per-device overrides
func (c *Config) GetDeviceConfig(index int) *DeviceConfig {
	if c.YAMLConfig == nil {
		return nil
	}

	// Start with a copy of defaults
	merged := c.YAMLConfig.DeviceDefaults

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

// GetDeviceMinorNumber returns the minor number for a specific device index
func (c *Config) GetDeviceMinorNumber(index int) int {
	if c.YAMLConfig == nil {
		return index
	}

	for _, dev := range c.YAMLConfig.Devices {
		if dev.Index == index {
			return dev.MinorNumber
		}
	}
	return index
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
	if override.Utilization != nil {
		base.Utilization = override.Utilization
	}
	if override.ECC != nil {
		base.ECC = override.ECC
	}
	// Add more fields as needed
}

// Note: debugLog is defined in utils.go to avoid duplication
