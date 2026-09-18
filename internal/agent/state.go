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
	Switches   []SwitchSpec
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
// The GPUs are not the whole tree. An HGX baseboard's NVSwitches sit on the
// node's PCIe bus alongside them, so Switches are rendered too and a consumer
// enumerating the bus finds the fabric silicon real hardware would show it.
//
// The profile's pcie_topology outlives its device list — GPU_COUNT truncates
// Devices and leaves the layout whole — so declared BDFs nothing claims are
// dropped, along with any root that empties out. A device no root claims is
// rendered under a root its own address implies, never folded into a declared
// one: locality is what a consumer reads this tree for, so a GPU carrying the
// NUMA node of a root the profile never put it in is worse than one carrying
// none. See adopt.
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
	if len(orphans) == 0 {
		return roots
	}

	return adopt(roots, orphans)
}

// numaNodeUnknown is what Linux writes to numa_node for a device it has no
// proximity information for. Rendering it says "unknown" in the encoding every
// consumer already handles, rather than asserting a node we do not know.
const numaNodeUnknown = -1

// adopt renders the functions no declared root claims — a profile whose
// pcie_topology omits a BDF its device or switch list carries.
//
// Each lands under the root its own address implies (pciDDDD:BB), joining a
// declared root only when that root is the one the address names, where
// membership is the profile's own arithmetic rather than our guess. Anything
// else gets a root of its own with an unknown NUMA node: the alternative,
// appending to whichever declared root came first, hands a topology-aware
// consumer a specific and wrong answer for something it cannot re-derive.
//
// Rendering them at all is deliberate. A GPU missing from the tree is one no
// consumer can resolve from the BDF NVML hands it, which is the failure this
// whole path exists to fix.
func adopt(roots []RootComplex, orphans []string) []RootComplex {
	index := make(map[string]int, len(roots)+len(orphans))
	for i, rc := range roots {
		index[rc.ID] = i
	}

	for _, bdf := range orphans {
		id := rootIDForBDF(bdf)
		if i, ok := index[id]; ok {
			roots[i].DeviceBDFs = append(roots[i].DeviceBDFs, bdf)
			continue
		}

		index[id] = len(roots)
		roots = append(roots, RootComplex{
			ID:         id,
			NUMANode:   numaNodeUnknown,
			DeviceBDFs: []string{bdf},
		})
	}

	return roots
}

// rootIDForBDF names the root complex a device's address implies, in the
// kernel's pciDDDD:BB form. Requires a BDF validBDF accepted, which is why it
// needs no fallback: guessing the default root here would hand the device that
// root's NUMA node, which every shipped profile declares.
func rootIDForBDF(bdf string) string {
	domain, rest, _ := strings.Cut(bdf, ":")
	bus, _, _ := strings.Cut(rest, ":")

	return "pci" + domain + ":" + bus
}

// ValidBDF reports whether s is a PCI address in the form the kernel names
// sysfs entries with, DDDD:BB:DD.F, lowercase hex.
//
// The check is a boundary, not a nicety: gpu.customConfig authors bus_id by
// hand, and the value becomes both a path component under the rendered tree and
// the basis for a root-complex ID. Anything else is dropped rather than placed
// somewhere it does not belong — a string that is not an address is one no
// consumer can look up either. The 8-digit domain NVML reports through
// nvmlPciInfo_t.busId is not this form; the profile field carries the 4-digit
// one, as busIdLegacy does.
//
// Exported for the simulators that put a device's address somewhere a
// hand-authored string must not reach on trust. The kernel log is the sharpest
// of those: it is node-wide, unnamespaced, and read by health agents, so an
// address carrying a newline would let whoever can edit the profile write
// kernel lines of their own.
func ValidBDF(s string) bool {
	const form = "dddd:bb:dd.f"
	if len(s) != len(form) {
		return false
	}

	for i, want := range form {
		got := s[i]
		switch want {
		case ':', '.':
			if got != byte(want) {
				return false
			}
		default:
			if !isLowerHex(got) {
				return false
			}
		}
	}

	return true
}

func isLowerHex(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')
}

// activeBDFs returns the BDFs of every PCI function to render — the GPUs that
// exist at runtime followed by the NVSwitches on this node's own PCIe bus —
// lowercased to match the paths the renderer writes, in declaration order and
// deduplicated. A function whose bus_id is absent or not an address is left out
// entirely.
//
// No GPU means nothing at all, switches included. The tree exists so that a
// consumer can resolve what NVML reports from the BDF it was handed, and a
// baseboard's switches on their own are not something any of them looks up.
func (s *State) activeBDFs() []string {
	seen := make(map[string]bool, len(s.Devices)+len(s.Switches))
	out := make([]string, 0, len(s.Devices)+len(s.Switches))

	for _, d := range s.Devices {
		out = appendBDF(out, seen, d.PCIBusID)
	}
	if len(out) == 0 {
		return nil
	}

	for _, sw := range s.Switches {
		out = appendBDF(out, seen, sw.PCIBusID)
	}

	return out
}

// appendBDF appends bdf lowercased, unless it is not an address the renderer
// can use as a path component or a previous entry already claimed it.
func appendBDF(out []string, seen map[string]bool, bdf string) []string {
	lower := strings.ToLower(bdf)
	if !ValidBDF(lower) || seen[lower] {
		return out
	}
	seen[lower] = true

	return append(out, lower)
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

// SwitchSpec is one NVSwitch that sits on this node's own PCIe bus, as an HGX
// baseboard's do. It carries only a PCI identity because that is the whole of
// what the mock can simulate: NVML models an NVSwitch as the far end of a GPU's
// NVLink and offers no per-switch API, and the nvmlUnit* chassis calls stay
// stubbed the way they are on real DGX/HGX nodes.
//
// A rack-scale platform has NVSwitches that are not on the node's bus at all —
// on GB200/GB300 NVL they live in their own switch trays, reachable over NVLink
// and invisible to the compute tray's lspci. Those profiles declare the switches
// for NVLink topology and no PCI identity, so nothing is compiled here for them.
type SwitchSpec struct {
	PCIBusID       string
	PCIDeviceID    uint32
	PCISubsystemID uint32
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
