// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package agent

import (
	"context"
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
