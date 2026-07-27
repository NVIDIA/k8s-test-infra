// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package profile is the single, pure source of truth for the GPU-profile
// facts the e2e harness asserts against. It decodes the *chart* profile YAML
// (deployments/nvml-mock/helm/nvml-mock/profiles/<name>.yaml) — i.e. exactly
// what the Helm chart deploys — into a typed Profile and derives the expected
// GPU count, InfiniBand HCA count, NVLink NV# token and fabricmanager state.
//
// Derivations mirror the chart helpers in
// deployments/nvml-mock/helm/nvml-mock/templates/_helpers.tpl so the harness
// can never disagree with what was rendered onto the node:
//
//   - ExpectedGPUs  = len(devices)                         (nvml-mock.gpuCount)
//   - ExpectedHCAs  = infiniband.enabled ? GPUs*hcas_per_gpu : 0
//   - ExpectedNV    = len(nvlink.switches) > 0 ? links_per_gpu : 0
//   - FabricMgr     = len(nvlink.switches) > 0 || device_defaults.fabric.state == "auto"
//
// NOTE on ExpectedNV: the signal is the PRESENCE of an NVSwitch list, NOT
// links_per_gpu on its own. b200 ships links_per_gpu: 18 but switch_support:
// false and no switches: list, so it MUST derive NV0 (standalone negative
// control). This package is intentionally free of any Kubernetes or exec
// imports so it compiles and unit-tests in the normal `go test ./...` run
// (it carries no build tag).
package profile

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"sigs.k8s.io/yaml"
)

// driverVersionRE matches the same character set the mock-driver's shell
// guard (drl_require_version) accepts: digits and dots only. Keeping the
// regex here means an invalid version fails profile.Load rather than at
// insmod / helm render time.
var driverVersionRE = regexp.MustCompile(`^[0-9.]+$`)

// KnownProfiles is the full set of chart profiles shipped in the repo. The
// required CI matrix is a subset chosen by the workflow input; this list is
// only used by All() and the cross-check unit test.
var KnownProfiles = []string{"a100", "h100", "b200", "gb200", "gb300", "l40s", "t4"}

// rawProfile decodes only the fields the harness needs from a chart profile
// YAML. sigs.k8s.io/yaml maps via JSON tags, so the tags are the snake_case
// keys used in the profile files.
type rawProfile struct {
	System struct {
		DriverVersion string `json:"driver_version"`
	} `json:"system"`
	DeviceDefaults struct {
		Name   string `json:"name"`
		Fabric *struct {
			State string `json:"state"`
		} `json:"fabric"`
	} `json:"device_defaults"`
	Devices []struct {
		Index int `json:"index"`
	} `json:"devices"`
	NVLink struct {
		LinksPerGPU int `json:"links_per_gpu"`
		Switches    []struct {
			BDF string `json:"bdf"`
		} `json:"switches"`
	} `json:"nvlink"`
	Infiniband struct {
		Enabled    bool `json:"enabled"`
		HCAsPerGPU int  `json:"hcas_per_gpu"`
	} `json:"infiniband"`
	PCIeTopology struct {
		RootComplexes []struct {
			ID string `json:"id"`
		} `json:"root_complexes"`
	} `json:"pcie_topology"`
}

// Profile is the typed, validated view of a chart GPU profile.
type Profile struct {
	// Name is the profile id (file basename), e.g. "a100".
	Name string
	// DisplayName is device_defaults.name, e.g. "NVIDIA A100-SXM4-40GB".
	DisplayName string
	// DriverVersion is system.driver_version, e.g. "550.163.01". Consumed by
	// the managed-driver GPU Operator scenario to derive the mock-driver
	// image tag and MOCK_KMOD prebuild version.
	DriverVersion string

	gpuCount    int
	ibEnabled   bool
	hcasPerGPU  int
	linksPerGPU int
	hasSwitches bool
	fabricAuto  bool
	hasFabric   bool
	pciRoots    int
}

// Load reads profilesDir/<name>.yaml and returns the typed Profile.
func Load(profilesDir, name string) (Profile, error) {
	path := filepath.Join(profilesDir, name+".yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		return Profile{}, fmt.Errorf("read profile %q: %w", path, err)
	}
	var raw rawProfile
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return Profile{}, fmt.Errorf("parse profile %q: %w", path, err)
	}
	if strings.TrimSpace(raw.DeviceDefaults.Name) == "" {
		return Profile{}, fmt.Errorf("profile %q: device_defaults.name is empty", path)
	}
	if len(raw.Devices) == 0 {
		return Profile{}, fmt.Errorf("profile %q: devices list is empty", path)
	}
	driverVersion := strings.TrimSpace(raw.System.DriverVersion)
	if driverVersion == "" {
		return Profile{}, fmt.Errorf("profile %q: system.driver_version is empty", path)
	}
	if !driverVersionRE.MatchString(driverVersion) {
		return Profile{}, fmt.Errorf("profile %q: system.driver_version %q must be digits and dots only", path, driverVersion)
	}

	p := Profile{
		Name:          name,
		DisplayName:   raw.DeviceDefaults.Name,
		DriverVersion: driverVersion,
		gpuCount:      len(raw.Devices),
		ibEnabled:     raw.Infiniband.Enabled,
		hcasPerGPU:    raw.Infiniband.HCAsPerGPU,
		linksPerGPU:   raw.NVLink.LinksPerGPU,
		hasSwitches:   len(raw.NVLink.Switches) > 0,
	}
	if raw.DeviceDefaults.Fabric != nil {
		p.hasFabric = true
		p.fabricAuto = strings.EqualFold(strings.TrimSpace(raw.DeviceDefaults.Fabric.State), "auto")
	}
	// render-pci-sysfs falls back to a flat single-root layout when a profile
	// declares no pcie_topology block, so an empty list still means 1 root.
	p.pciRoots = len(raw.PCIeTopology.RootComplexes)
	if p.pciRoots == 0 {
		p.pciRoots = 1
	}
	// An IB-enabled profile that forgot hcas_per_gpu would silently expect 0
	// HCAs; the shipped profiles all set 1. Default to 1 when enabled but
	// unset so a missing key does not weaken the assertion.
	if p.ibEnabled && p.hcasPerGPU == 0 {
		p.hcasPerGPU = 1
	}
	return p, nil
}

// All loads every KnownProfiles entry from profilesDir.
func All(profilesDir string) ([]Profile, error) {
	out := make([]Profile, 0, len(KnownProfiles))
	for _, name := range KnownProfiles {
		p, err := Load(profilesDir, name)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

// ExpectedGPUs is the number of GPUs the profile exposes (len of devices).
func (p Profile) ExpectedGPUs() int { return p.gpuCount }

// IBEnabled reports whether the profile ships InfiniBand enabled.
func (p Profile) IBEnabled() bool { return p.ibEnabled }

// ExpectedHCAs is the number of InfiniBand HCAs the profile should expose:
// one per GPU when IB is enabled, otherwise 0 (l40s/t4 negative control).
func (p Profile) ExpectedHCAs() int {
	if !p.ibEnabled {
		return 0
	}
	return p.gpuCount * p.hcasPerGPU
}

// ExpectedNV is the NV# link count `nvidia-smi topo -m` should show between
// every GPU pair: links_per_gpu when the profile declares NVSwitches,
// otherwise 0 (b200/l40s/t4 negative controls). Keyed off the switch list,
// NOT links_per_gpu — see the package doc.
func (p Profile) ExpectedNV() int {
	if !p.hasSwitches {
		return 0
	}
	return p.linksPerGPU
}

// ExpectedPCIRoots is the number of distinct PCIe root complexes the rendered
// /sys/bus/pci tree should span (len of pcie_topology.root_complexes, or 1 for
// profiles with no explicit block). Mirrors the demo's EXPECTED_ROOTS: e.g.
// a100/h100/b200/l40s -> 2, gb200/gb300 -> 4, t4 -> 1. A regression that
// collapsed every device onto one root would break NUMA-aware scheduling.
func (p Profile) ExpectedPCIRoots() int { return p.pciRoots }

// FabricMgr reports whether the fake nvidia-fabricmanager daemon runs for this
// profile (NVSwitch baseboard present, or fabric.state: auto). Mirrors
// _helpers.tpl nvml-mock.fabricmanagerEnabled. The harness still reads the
// deployed DaemonSet's MOCK_FABRICMANAGER env at runtime as the authoritative
// gate; this is the profile-derived expectation.
func (p Profile) FabricMgr() bool { return p.hasSwitches || p.fabricAuto }

// HasFabric reports whether the profile declares a device_defaults.fabric block
// (cluster_uuid / clique_id). Only these profiles (h100, gb200, gb300) expose
// ComputeDomain fabric identity via nvmlDeviceGetGpuFabricInfo, so the mock's
// check-fabric consumer succeeds and the topology overlay has something to
// rewrite. This is DISTINCT from FabricMgr: an NVSwitch profile like a100 runs
// the fabricmanager daemon (FabricMgr true) yet reports fabric NOT SUPPORTED
// (HasFabric false).
func (p Profile) HasFabric() bool { return p.hasFabric }
