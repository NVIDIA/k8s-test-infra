// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package agent

import (
	"context"
	"strings"
	"time"
)

// State is the compiled desired simulation state the agent reconciles toward.
// It maps 1:1 to what MEP-0001's Control Plane will emit.
type State struct {
	Generation int64
	Node       NodeMeta
	Software   SoftwareVersions
	NodeShape  NodeShape
	Devices    []DeviceSpec
	Fabric     FabricState
	IMEX       IMEXState
	// ConfigRaw holds the raw YAML profile bytes so gpudriver can write the
	// engine config without re-deriving it from the narrower State fields.
	// TODO(https://github.com/NVIDIA/k8s-test-infra/issues/717): replace with Profile/Runtime *config.YAMLConfig split — Profile carries
	// the static hardware model, Runtime carries operational overrides (GPU_COUNT,
	// faults, fabric) written by GPU_COUNT env, MEP-0001 CP, and nvml-mock-ctl.
	ConfigRaw []byte
	// TopologyRaw holds the cluster ComputeDomain topology document verbatim.
	// It stays raw because the document describes every node and the mock NVML
	// engine picks this node's entry by NODE_NAME at load time, so there is no
	// per-node view for the agent to compile.
	TopologyRaw []byte
}

// DefaultRootComplexID is the host bridge a synthesized layout hangs every
// device off, matching what a single-socket profile declares explicitly.
const DefaultRootComplexID = "pci0000:00"

// PCITopology returns the root complexes to render, reconciled against the
// devices that exist at runtime rather than taken from the profile as written.
// Devices are the authority because they are what NVML reports, and a served
// tree that disagrees with NVML is worse than no tree: a consumer resolves a
// GPU in one and not the other.
//
// The profile's pcie_topology outlives its device list — GPU_COUNT truncates
// Devices and leaves the layout whole — so declared BDFs no device claims are
// dropped, along with any root that empties out. Conversely a device no root
// claims is adopted by the first one, since an unplaced GPU cannot be resolved
// at all while a misplaced one only misreports its pcieRoot.
//
// Returns nil when no device carries a BDF, which is the signal to render
// nothing.
func (s *State) PCITopology() []RootComplex {
	active := s.activeBDFs()
	if len(active) == 0 {
		return nil
	}

	declared := s.NodeShape.Topology.RootComplexes
	if len(declared) == 0 {
		return []RootComplex{{ID: DefaultRootComplexID, DeviceBDFs: active}}
	}

	roots, placed := placeDeclared(declared, active)

	var orphans []string
	for _, bdf := range active {
		if !placed[bdf] {
			orphans = append(orphans, bdf)
		}
	}

	switch {
	case len(orphans) == 0:
		return roots
	case len(roots) == 0:
		return []RootComplex{{ID: DefaultRootComplexID, DeviceBDFs: orphans}}
	default:
		roots[0].DeviceBDFs = append(roots[0].DeviceBDFs, orphans...)
		return roots
	}
}

// activeBDFs returns the BDFs of the devices that exist at runtime, lowercased
// to match the paths the renderer writes, in device order and deduplicated.
func (s *State) activeBDFs() []string {
	seen := make(map[string]bool, len(s.Devices))
	out := make([]string, 0, len(s.Devices))

	for _, d := range s.Devices {
		if d.PCIBusID == "" {
			continue
		}
		bdf := strings.ToLower(d.PCIBusID)
		if seen[bdf] {
			continue
		}
		seen[bdf] = true
		out = append(out, bdf)
	}

	return out
}

// placeDeclared keeps the BDFs of each declared root that an active device
// claims, dropping any root left with none. It reports which BDFs it placed so
// the caller can find the devices no root accounts for.
func placeDeclared(declared []RootComplex, active []string) ([]RootComplex, map[string]bool) {
	wanted := make(map[string]bool, len(active))
	for _, bdf := range active {
		wanted[bdf] = true
	}

	placed := make(map[string]bool, len(active))
	out := make([]RootComplex, 0, len(declared))

	for _, rc := range declared {
		kept := make([]string, 0, len(rc.DeviceBDFs))
		for _, bdf := range rc.DeviceBDFs {
			bdf = strings.ToLower(bdf)
			if wanted[bdf] && !placed[bdf] {
				placed[bdf] = true
				kept = append(kept, bdf)
			}
		}
		if len(kept) == 0 {
			continue
		}
		rc.DeviceBDFs = kept
		out = append(out, rc)
	}

	return out, placed
}

// HasPCITopology reports whether a PCI sysfs tree will be rendered for this
// state. Whoever serves that tree gates on this, so it answers from the same
// reconciliation the renderer reads rather than from a filesystem probe: a bind
// mount whose source is missing fails container creation for the whole pod.
func (s *State) HasPCITopology() bool {
	return len(s.PCITopology()) > 0
}

// NodeMeta carries node identity fields.
type NodeMeta struct {
	NodeName string
	Hostname string
	HostRoot string
}

// SoftwareVersions holds driver / NVML / CUDA version strings.
type SoftwareVersions struct {
	DriverVersion string
	NVMLVersion   string
	CUDAVersion   string
}

// NodeShape describes the simulated node's topology.
type NodeShape struct {
	NumGPUs  int
	Topology PCIeTopology
	Network  NetworkShape
}

// NetworkShape describes the simulated InfiniBand HCAs attached to the node.
// Every field is resolved at compile time so simulators never re-derive
// defaults. All fields are comparable, so == detects a real IB change.
type NetworkShape struct {
	IBEnabled bool
	// HCACount is already resolved from hca_count or hcas_per_gpu * NumGPUs.
	HCACount         int
	HCAType          string
	FWVersion        string
	HWRev            string
	BoardID          string
	NodeDescTemplate string
	LinkLayer        string
	RateGbps         int
	PortState        string
	PhysState        string
	GUIDPrefix       string
}

// PCIeTopology describes the PCIe root-complex / NUMA layout.
type PCIeTopology struct {
	RootComplexes []RootComplex
	CoresPerNUMA  int
}

// RootComplex is one PCI host bridge with its attached GPU BDFs and NUMA node.
type RootComplex struct {
	ID          string
	NUMANode    int
	DeviceBDFs  []string
	CPUAffinity string
}

// DeviceSpec carries per-GPU identity and static hardware properties.
type DeviceSpec struct {
	Index            int
	UUID             string
	MinorNumber      int
	Serial           string
	PCIBusID         string
	Name             string
	Architecture     string
	ComputeCapMajor  int
	ComputeCapMinor  int
	MemoryTotalBytes uint64
	PCIDeviceID      uint32
	PCISubsystemID   uint32
}

// FabricState describes the NVLink / NVSwitch fabric configuration.
type FabricState struct {
	// Profile declares NVLink; does not imply fabricmanager runs.
	Enabled bool
	// Fabricmanager readiness marker directory; empty when the daemon is off.
	ManagerStateDir      string
	ClusterUUID          string
	CliqueID             uint32
	LinksPerGPU          int
	BandwidthPerLinkMbps int
}

// IMEXState describes the IMEX capability surface for the DRA compute-domain plugin.
type IMEXState struct {
	Enabled      bool
	IMEXMajor    int
	CapsMajor    int
	ChannelCount int
}

// StateSource emits State observations.
// Watch sends the current State immediately on subscribe and pushes on change.
// A closed channel means the source is terminally done; the agent stops.
type StateSource interface {
	Watch(ctx context.Context) <-chan Update
	Close() error
}

// Update is one observation from a StateSource.
// Exactly one of State or Err is set.
// Err leaves the last good State in force; the agent does not reconcile.
type Update struct {
	State *State
	Err   error
	At    time.Time
}
