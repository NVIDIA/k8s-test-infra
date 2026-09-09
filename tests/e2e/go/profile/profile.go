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
	"slices"
	"strconv"
	"strings"

	"sigs.k8s.io/yaml"

	"github.com/NVIDIA/k8s-test-infra/internal/gpuarch"
)

// KnownProfiles is the full set of chart profiles shipped in the repo. The
// required CI matrix is a subset chosen by the workflow input; this list is
// only used by All() and the cross-check unit test.
var KnownProfiles = []string{"a100", "h100", "b200", "gb200", "gb300", "l40s", "t4"}

// rawProfile decodes only the fields the harness needs from a chart profile
// YAML. sigs.k8s.io/yaml maps via JSON tags, so the tags are the snake_case
// keys used in the profile files.
type rawProfile struct {
	DeviceDefaults struct {
		Name         string `json:"name"`
		Architecture string `json:"architecture"`
		Fabric       *struct {
			State string `json:"state"`
		} `json:"fabric"`
		Memory struct {
			TotalBytes int64 `json:"total_bytes"`
		} `json:"memory"`
		Thermal *struct {
			ShutdownThresholdC int `json:"shutdown_threshold_c"`
			SlowdownThresholdC int `json:"slowdown_threshold_c"`
			MaxOperatingC      int `json:"max_operating_c"`
		} `json:"thermal"`
		Utilization *struct {
			JPEG int `json:"jpeg"`
			OFA  int `json:"ofa"`
		} `json:"utilization"`
		PCIe *struct {
			MaxLinkGen int `json:"max_link_gen"`
		} `json:"pcie"`
		Clocks *struct {
			GraphicsMax int `json:"graphics_max"`
		} `json:"clocks"`
		Power *struct {
			WorkloadProfiles *struct {
				Supported []struct {
					ID        int   `json:"id"`
					Priority  int   `json:"priority"`
					Conflicts []int `json:"conflicts"`
				} `json:"supported"`
				Requested []int `json:"requested"`
			} `json:"workload_power_profiles"`
		} `json:"power"`
		Platform     *rawPlatform `json:"platform"`
		RemappedRows *struct {
			AvailabilityHistogram *struct {
				Max int `json:"max"`
			} `json:"availability_histogram"`
		} `json:"remapped_rows"`
		MIG *struct {
			GPUInstances []rawMIGGpuInstance `json:"gpu_instances"`
		} `json:"mig"`
	} `json:"device_defaults"`
	Devices []struct {
		Index    int          `json:"index"`
		Platform *rawPlatform `json:"platform"`
	} `json:"devices"`
	NVLink struct {
		LinksPerGPU int  `json:"links_per_gpu"`
		C2CEnabled  bool `json:"c2c_enabled"`
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
	System struct {
		DriverVersion string `json:"driver_version"`
	} `json:"system"`
}

// rawPlatform decodes a platform block, which appears both under
// device_defaults (the node's location) and per device (its module id).
type rawPlatform struct {
	ChassisSerialNumber string `json:"chassis_serial_number"`
	SlotNumber          int    `json:"slot_number"`
	TrayIndex           int    `json:"tray_index"`
	HostID              int    `json:"host_id"`
	PeerType            string `json:"peer_type"`
	ModuleID            int    `json:"module_id"`
}

// PlatformIdentity is the platform identity a profile configures — where its
// node sits in a rack. ModuleIDs is indexed by device; the rest describe the
// node and are shared by all its GPUs. PeerType keeps the profile spelling
// ("switch_connected"), leaving the rendering nvidia-smi uses to the assertion.
type PlatformIdentity struct {
	ChassisSerialNumber string
	SlotNumber          int
	TrayIndex           int
	HostID              int
	PeerType            string
	ModuleIDs           []int
}

// Profile is the typed, validated view of a chart GPU profile.
type Profile struct {
	// Name is the profile id (file basename), e.g. "a100".
	Name string
	// DisplayName is device_defaults.name, e.g. "NVIDIA A100-SXM4-40GB".
	DisplayName string

	gpuCount    int
	ibEnabled   bool
	hcasPerGPU  int
	linksPerGPU int
	hasSwitches bool
	c2cEnabled  bool
	fabricAuto  bool
	hasFabric   bool
	pciRoots    int
	memoryBytes int64

	arch               gpuarch.Arch
	shutdownThresholdC int
	slowdownThresholdC int
	maxOperatingC      int
	jpegUtilizationPct int
	ofaUtilizationPct  int
	maxPCIeLinkGen     int
	graphicsMaxMHz     int
	rowRemapHistogram  bool
	rowRemapBanks      int

	platform    PlatformIdentity
	hasPlatform bool

	driverVersion             string
	workloadProfiles          []WorkloadPowerProfile
	workloadProfilesRequested []int
	workloadProfilesDeclared  bool

	migPartitions    int
	migDeviceProfile string
}

// WorkloadPowerProfile is one profile a board advertises through
// `nvidia-smi power-profiles`. ID is an NVML_POWER_PROFILE_* index, which is
// also what nvidia-smi renders as the profile's name.
type WorkloadPowerProfile struct {
	ID        int
	Priority  int
	Conflicts []int
}

// bytesPerMiB is the divisor GPU Feature Discovery uses when it publishes
// nvidia.com/gpu.memory, which is reported in MiB.
const bytesPerMiB = 1024 * 1024

// GFDProductName is DisplayName in the form GPU Feature Discovery publishes as
// nvidia.com/gpu.product: spaces become dashes. For example "NVIDIA GB200"
// becomes "NVIDIA-GB200".
func (p Profile) GFDProductName() string {
	return strings.ReplaceAll(p.DisplayName, " ", "-")
}

// MemoryMiB is per-device memory in MiB, matching what GFD publishes as
// nvidia.com/gpu.memory.
func (p Profile) MemoryMiB() int { return int(p.memoryBytes / bytesPerMiB) }

// Load reads profilesDir/<name>.yaml and returns the typed Profile.
//
// device_defaults.architecture is required and must name a generation
// gpuarch.Parse recognizes: the harness keys its "this generation and newer"
// expectations off it, so an absent or misspelled value would leave them
// answering from a default with the suite green.
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
	if strings.TrimSpace(raw.DeviceDefaults.Architecture) == "" {
		return Profile{}, fmt.Errorf("profile %q: device_defaults.architecture is empty", path)
	}
	parsedArch, ok := gpuarch.Parse(raw.DeviceDefaults.Architecture)
	if !ok {
		return Profile{}, fmt.Errorf("profile %q: unrecognized device_defaults.architecture %q",
			path, raw.DeviceDefaults.Architecture)
	}

	p := Profile{
		Name:        name,
		DisplayName: raw.DeviceDefaults.Name,
		gpuCount:    len(raw.Devices),
		ibEnabled:   raw.Infiniband.Enabled,
		hcasPerGPU:  raw.Infiniband.HCAsPerGPU,
		linksPerGPU: raw.NVLink.LinksPerGPU,
		hasSwitches: len(raw.NVLink.Switches) > 0,
		c2cEnabled:  raw.NVLink.C2CEnabled,
		memoryBytes: raw.DeviceDefaults.Memory.TotalBytes,
		arch:        parsedArch,

		driverVersion: strings.TrimSpace(raw.System.DriverVersion),
	}
	p.applyOptionalDeviceDefaults(raw)
	// pcibus simulator falls back to a flat single-root layout when a profile
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

// applyOptionalDeviceDefaults copies the device_defaults sub-blocks a profile
// may omit. Each absent block leaves its fields at the zero value, which is
// what the accessors document.
func (p *Profile) applyOptionalDeviceDefaults(raw rawProfile) {
	if t := raw.DeviceDefaults.Thermal; t != nil {
		p.shutdownThresholdC = t.ShutdownThresholdC
		p.slowdownThresholdC = t.SlowdownThresholdC
		p.maxOperatingC = t.MaxOperatingC
	}
	if u := raw.DeviceDefaults.Utilization; u != nil {
		p.jpegUtilizationPct = u.JPEG
		p.ofaUtilizationPct = u.OFA
	}
	if pcie := raw.DeviceDefaults.PCIe; pcie != nil {
		p.maxPCIeLinkGen = pcie.MaxLinkGen
	}
	if c := raw.DeviceDefaults.Clocks; c != nil {
		p.graphicsMaxMHz = c.GraphicsMax
	}
	p.applyWorkloadPowerProfiles(raw)
	if r := raw.DeviceDefaults.RemappedRows; r != nil && r.AvailabilityHistogram != nil {
		p.rowRemapHistogram = true
		p.rowRemapBanks = r.AvailabilityHistogram.Max
	}
	if f := raw.DeviceDefaults.Fabric; f != nil {
		p.hasFabric = true
		p.fabricAuto = strings.EqualFold(strings.TrimSpace(f.State), "auto")
	}
	if mig := raw.DeviceDefaults.MIG; mig != nil {
		p.migPartitions, p.migDeviceProfile = migLayout(mig.GPUInstances)
	}
	if pl := raw.DeviceDefaults.Platform; pl != nil {
		p.hasPlatform = true
		p.platform = PlatformIdentity{
			ChassisSerialNumber: pl.ChassisSerialNumber,
			SlotNumber:          pl.SlotNumber,
			TrayIndex:           pl.TrayIndex,
			HostID:              pl.HostID,
			PeerType:            pl.PeerType,
			ModuleIDs:           deviceModuleIDs(raw, pl.ModuleID),
		}
	}
}

// rawMIGGpuInstance is one entry of a profile's declared MIG partitioning.
type rawMIGGpuInstance struct {
	Profile string `json:"profile"`
	Count   int    `json:"count"`
}

// migLayout totals a declared partitioning and reports the profile every
// partition shares, or "" when they differ. The uniform case is the one the
// device plugin's migStrategy=single accepts, so collapsing a mixed layout to
// "" keeps a caller from asserting on a resource name that would never be
// published.
func migLayout(instances []rawMIGGpuInstance) (partitions int, uniform string) {
	for i, gi := range instances {
		// An omitted count means one instance, matching how the engine reads
		// the same field.
		count := gi.Count
		if count == 0 {
			count = 1
		}
		partitions += count
		switch {
		case i == 0:
			uniform = gi.Profile
		case uniform != gi.Profile:
			uniform = ""
		}
	}
	return partitions, uniform
}

// deviceModuleIDs collects each device's module id, keyed by the declared
// device index so the result lines up with NVML's device order however the YAML
// lists them. A device that declares no module id falls back to the default,
// mirroring the mock's per-device merge, which treats zero as unset.
func deviceModuleIDs(raw rawProfile, defaultModuleID int) []int {
	ids := make([]int, len(raw.Devices))
	for i := range ids {
		ids[i] = defaultModuleID
	}
	for i, dev := range raw.Devices {
		at := dev.Index
		if at < 0 || at >= len(ids) {
			at = i
		}
		if dev.Platform != nil && dev.Platform.ModuleID != 0 {
			ids[at] = dev.Platform.ModuleID
		}
	}
	return ids
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

// MIGPartitionsPerGPU is how many GPU instances each of the profile's boards
// declares. Profiles ship this layout inert (mode_current: disabled) and the
// chart's gpu.mig.enabled turns it on, so the count describes what the board
// partitions into once MIG is switched on, not what it exposes by default.
func (p Profile) MIGPartitionsPerGPU() int { return p.migPartitions }

// MIGDeviceProfile is the profile name every declared partition shares, e.g.
// "1g.5gb", or "" when the profile declares none or declares a mix.
func (p Profile) MIGDeviceProfile() string { return p.migDeviceProfile }

// MIGCapable reports whether the profile declares a MIG partitioning at all.
func (p Profile) MIGCapable() bool { return p.migPartitions > 0 }

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
// a100/h100/b200/l40s/gb200/gb300 -> 2, t4 -> 1. A regression that
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

// C2CEnabled reports whether the profile declares an NVLink-C2C link to the
// host CPU (nvlink.c2c_enabled). True only on the Grace-Blackwell profiles;
// nvidia-smi -q renders it as "GPU C2C Mode : Enabled" there and N/A
// elsewhere. Absent key means false, i.e. N/A.
func (p Profile) C2CEnabled() bool { return p.c2cEnabled }

// PlatformIdentity returns the platform identity the profile configures and
// whether it declares one at all. Only the rack-scale profiles (gb200, gb300)
// do: NVML answers nvmlDeviceGetPlatformInfo for a board whose platform can
// report a physical location, and nvidia-smi renders N/A for every other one, so
// the absent case is the negative control that keeps the populated case from
// being satisfiable by constants.
func (p Profile) PlatformIdentity() (PlatformIdentity, bool) { return p.platform, p.hasPlatform }

// Architecture is the profile's generation as an ordered value, for
// expectations of the form "this generation and newer". It formats itself as
// the canonical lowercase name, so the l40s profile's "ada_lovelace" spelling
// prints as "ada".
func (p Profile) Architecture() gpuarch.Arch { return p.arch }

// ShutdownThresholdC is thermal.shutdown_threshold_c from the profile.
func (p Profile) ShutdownThresholdC() int { return p.shutdownThresholdC }

// SlowdownThresholdC is thermal.slowdown_threshold_c from the profile.
func (p Profile) SlowdownThresholdC() int { return p.slowdownThresholdC }

// MaxOperatingC is thermal.max_operating_c from the profile.
func (p Profile) MaxOperatingC() int { return p.maxOperatingC }

// JPEGUtilizationPct is utilization.jpeg from the profile, the percentage
// nvidia-smi -q -x must report in jpeg_util.
func (p Profile) JPEGUtilizationPct() int { return p.jpegUtilizationPct }

// OFAUtilizationPct is utilization.ofa from the profile, the percentage
// nvidia-smi -q -x must report in ofa_util.
func (p Profile) OFAUtilizationPct() int { return p.ofaUtilizationPct }

// MaxPCIeLinkGen is device_defaults.pcie.max_link_gen from the profile — the
// PCIe generation nvidia-smi must report for the "Max" and "Device Max" rows.
// Ranges from 3 (t4) to 6 (Blackwell), so asserting against it pins the value
// to config rather than a hardcoded constant.
func (p Profile) MaxPCIeLinkGen() int { return p.maxPCIeLinkGen }

// GraphicsMaxClockMHz is device_defaults.clocks.graphics_max, the boost maximum
// nvidia-smi renders in Max Clocks. The mock reports the same value as the
// OEM ceiling in Max Customer Boost Clocks, which is what every real board does
// (#712). 0 when the profile declares no clocks block, in which case both rows
// read N/A.
func (p Profile) GraphicsMaxClockMHz() int { return p.graphicsMaxMHz }

// applyWorkloadPowerProfiles decodes power.workload_power_profiles. Declaring
// the block is what separates a board that models the feature from one that
// declines it, so the flag is recorded even when the profile list is empty.
func (p *Profile) applyWorkloadPowerProfiles(raw rawProfile) {
	pw := raw.DeviceDefaults.Power
	if pw == nil || pw.WorkloadProfiles == nil {
		return
	}
	p.workloadProfilesDeclared = true
	for _, sp := range pw.WorkloadProfiles.Supported {
		p.workloadProfiles = append(p.workloadProfiles, WorkloadPowerProfile{
			ID:        sp.ID,
			Priority:  sp.Priority,
			Conflicts: sp.Conflicts,
		})
	}
	p.workloadProfilesRequested = pw.WorkloadProfiles.Requested
}

// workloadPowerProfileMinDriver is the driver that introduced
// nvmlDeviceWorkloadPowerProfileGet*. On anything older the mock's version
// registry reports the symbol as absent, so nvidia-smi fails to find the
// function rather than being told the device declines.
const workloadPowerProfileMinDriver = 570

// WorkloadPowerProfiles returns the profiles the board advertises, ascending by
// id, and whether it declares the feature at all. An empty slice with declared
// true is a board that supports the feature and lists nothing.
func (p Profile) WorkloadPowerProfiles() ([]WorkloadPowerProfile, bool) {
	out := slices.Clone(p.workloadProfiles)
	slices.SortFunc(out, func(a, b WorkloadPowerProfile) int { return a.ID - b.ID })
	return out, p.workloadProfilesDeclared
}

// RequestedWorkloadPowerProfiles is the profile ids the board asks for.
// Normally empty: every captured board reports no requested profile.
func (p Profile) RequestedWorkloadPowerProfiles() []int {
	return slices.Clone(p.workloadProfilesRequested)
}

// SupportsWorkloadPowerProfiles reports whether `nvidia-smi power-profiles`
// should succeed on this profile. Both axes have to hold, and they fail
// differently: a board that declares no profiles is told the device does not
// support the feature, while one on a pre-570 driver cannot find the function
// at all.
func (p Profile) SupportsWorkloadPowerProfiles() bool {
	return p.workloadProfilesDeclared && p.DriverMajor() >= workloadPowerProfileMinDriver
}

// IndependentWorkloadProfilePair returns two advertised profiles that do not
// conflict with each other, so requesting both leaves both enforced. Used to
// tell a cleared profile from its neighbour: clearing one must leave the other.
func (p Profile) IndependentWorkloadProfilePair() (first, second int, ok bool) {
	profiles, _ := p.WorkloadPowerProfiles()
	for i, a := range profiles {
		for _, b := range profiles[i+1:] {
			if !workloadProfilesConflict(a, b) {
				return a.ID, b.ID, true
			}
		}
	}
	return 0, 0, false
}

// ConflictingWorkloadProfilePair returns two advertised profiles that cannot be
// enforced together, the higher-priority one first. Requesting both is how the
// difference between the requested and the enforced set becomes observable.
func (p Profile) ConflictingWorkloadProfilePair() (winner, loser int, ok bool) {
	profiles, _ := p.WorkloadPowerProfiles()
	for i, a := range profiles {
		for _, b := range profiles[i+1:] {
			// Equal priorities would leave the outcome to the tie-break
			// rather than to priority, which is not what this pair is for.
			if !workloadProfilesConflict(a, b) || a.Priority == b.Priority {
				continue
			}
			if a.Priority < b.Priority {
				return a.ID, b.ID, true
			}
			return b.ID, a.ID, true
		}
	}
	return 0, 0, false
}

// workloadProfilesConflict reports whether two profiles exclude each other.
// Either direction counts, so a config that names the conflict on one side only
// is still treated as a conflict.
func workloadProfilesConflict(a, b WorkloadPowerProfile) bool {
	return slices.Contains(a.Conflicts, b.ID) || slices.Contains(b.Conflicts, a.ID)
}

// DriverMajor is the major component of system.driver_version, or 0 when the
// profile declares none or it does not parse.
func (p Profile) DriverMajor() int {
	major, _, _ := strings.Cut(p.driverVersion, ".")
	n, err := strconv.Atoi(major)
	if err != nil {
		return 0
	}
	return n
}

// ReportsDetailedSramECC is true when nvidia-smi renders the Ampere-and-later
// SRAM breakdown for this architecture: the uncorrectable count split into
// parity and SEC-DED, plus the per-unit source list and the threshold flag.
// Pre-Ampere output carries a single combined SRAM Uncorrectable row and none of
// the rest, so the expectation is an architecture axis rather than a config one
// — nvidia-smi picks the layout from the reported architecture, not from what
// the profile configures (#641).
func (p Profile) ReportsDetailedSramECC() bool { return p.arch.AtLeast(gpuarch.Ampere) }

// ReportsRowRemapHistogram reports whether the profile configures
// remapped_rows.availability_histogram, i.e. whether nvidia-smi must render bank
// counts rather than N/A for the Bank Remap Availability Histogram. Row
// remapping is Ampere and later, so pre-Ampere profiles leave the block out and
// the mock answers unsupported the way that hardware does (#641).
func (p Profile) ReportsRowRemapHistogram() bool { return p.rowRemapHistogram }

// RowRemapHistogramBanks is remapped_rows.availability_histogram.max: how many
// banks a never-remapped GPU reports with their full complement of spare rows.
// Zero when the profile configures no histogram.
func (p Profile) RowRemapHistogramBanks() int { return p.rowRemapBanks }

// ReportsTLimitTemp is true when real hardware of this architecture reports the
// GPU T.Limit temperature field IDs (Ada and later). Pre-Ada profiles keep the
// legacy absolute threshold rows via nvmlDeviceGetTemperatureThreshold.
func (p Profile) ReportsTLimitTemp() bool { return p.arch.AtLeast(gpuarch.Ada) }
