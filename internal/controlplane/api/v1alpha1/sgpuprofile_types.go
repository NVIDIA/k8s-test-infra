// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import (
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// SGPUProfile is the static ground truth for a simulated GPU rack shape:
// hardware capabilities, host topology, software versions, and the baseline
// runtime state SGPURuntimePolicy overrides sparsely.
//
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster,categories=mokka,shortName=sprof
// +kubebuilder:printcolumn:name="Nodes/Rack",type=integer,JSONPath=`.spec.rack.nodesPerRack`
// +kubebuilder:printcolumn:name="GPUs/Node",type=integer,JSONPath=`.spec.node.gpus.count`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
type SGPUProfile struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec SGPUProfileSpec `json:"spec"`
}

// SGPUProfileList is the list wrapper for SGPUProfile.
// +kubebuilder:object:root=true
type SGPUProfileList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []SGPUProfile `json:"items"`
}

// SGPUProfileSpec models rack shape, per-node topology, software footprint,
// and runtime defaults for one logical rack profile.
type SGPUProfileSpec struct {
	Rack SGPURack `json:"rack"`

	Node SGPUNode `json:"node"`

	// +optional
	Software *SGPUSoftware `json:"software,omitempty"`

	// +optional
	Defaults *SGPUProfileDefaults `json:"defaults,omitempty"`
}

// SGPURack is the logical rack shape; SGPUInventory multiplies by its rack count.
type SGPURack struct {
	// +kubebuilder:validation:Minimum=1
	NodesPerRack int32 `json:"nodesPerRack"`
}

// SGPUNode models one homogeneous logical node inside the rack.
type SGPUNode struct {
	GPUs SGPUGPUs `json:"gpus"`

	// +optional
	Host *SGPUHost `json:"host,omitempty"`

	// +optional
	Topology *SGPUTopology `json:"topology,omitempty"`
}

// SGPUGPUs is the shared GPU template every logical GPU on the node inherits.
type SGPUGPUs struct {
	// +kubebuilder:validation:Minimum=1
	Count int32 `json:"count"`

	Model GPUModel `json:"model"`

	Memory GPUMemory `json:"memory"`

	PCI GPUPCI `json:"pci"`

	// +optional
	Power *GPUPower `json:"power,omitempty"`

	// +optional
	Thermal *GPUThermal `json:"thermal,omitempty"`

	// +optional
	Clocks *GPUClocks `json:"clocks,omitempty"`

	// +optional
	Capabilities *GPUCapabilities `json:"capabilities,omitempty"`
}

// GPUModel captures vendor/product identity plus static compute/board details.
type GPUModel struct {
	// +optional
	Vendor string `json:"vendor,omitempty"`

	// +optional
	Product string `json:"product,omitempty"`

	// +optional
	ProductName string `json:"productName,omitempty"`

	// +optional
	Architecture string `json:"architecture,omitempty"`

	// +optional
	ComputeCapability *ComputeCapability `json:"computeCapability,omitempty"`

	// +optional
	Cores *GPUCores `json:"cores,omitempty"`

	// +optional
	Board *GPUBoard `json:"board,omitempty"`

	// +optional
	Firmware *GPUFirmware `json:"firmware,omitempty"`
}

// ComputeCapability is the CUDA compute capability (major.minor).
type ComputeCapability struct {
	Major int32 `json:"major"`
	Minor int32 `json:"minor"`
}

// GPUCores enumerates programmable core counts NVML exposes.
type GPUCores struct {
	// +optional
	CUDA int32 `json:"cuda,omitempty"`
}

// GPUBoard captures board-level identity fields.
type GPUBoard struct {
	// +optional
	PartNumber string `json:"partNumber,omitempty"`
}

// GPUFirmware groups firmware versions surfaced by nvidia-smi -q.
type GPUFirmware struct {
	// +optional
	VBIOSVersion string `json:"vbiosVersion,omitempty"`

	// +optional
	GSPVersion string `json:"gspVersion,omitempty"`

	// +optional
	InfoROM *InfoROM `json:"infoROM,omitempty"`
}

// InfoROM mirrors NVML's inforom sub-object versions.
type InfoROM struct {
	// +optional
	ImageVersion string `json:"imageVersion,omitempty"`

	// +optional
	OEMObjectVersion string `json:"oemObjectVersion,omitempty"`

	// +optional
	ECCObjectVersion string `json:"eccObjectVersion,omitempty"`

	// +optional
	PowerObjectVersion string `json:"powerObjectVersion,omitempty"`
}

// GPUMemory describes the on-device memory footprint the mock advertises.
type GPUMemory struct {
	Capacity resource.Quantity `json:"capacity"`

	// +optional
	Reserved *resource.Quantity `json:"reserved,omitempty"`

	// +optional
	Bar1Capacity *resource.Quantity `json:"bar1Capacity,omitempty"`

	// +optional
	BusWidthBits int32 `json:"busWidthBits,omitempty"`
}

// GPUPCI captures PCI vendor/device identity and link characteristics.
type GPUPCI struct {
	// +optional
	VendorID string `json:"vendorID,omitempty"`

	// +optional
	DeviceID string `json:"deviceID,omitempty"`

	// +optional
	SubsystemVendorID string `json:"subsystemVendorID,omitempty"`

	// +optional
	SubsystemDeviceID string `json:"subsystemDeviceID,omitempty"`

	// +optional
	MaxLink *PCILink `json:"maxLink,omitempty"`
}

// PCILink models the PCIe max link generation and width.
type PCILink struct {
	// +optional
	Generation int32 `json:"generation,omitempty"`

	// +optional
	Width int32 `json:"width,omitempty"`
}

// GPUPower advertises the power envelope the mock exposes through NVML.
type GPUPower struct {
	// +optional
	ManagementSupported bool `json:"managementSupported,omitempty"`

	// +optional
	LimitsMilliWatts *PowerLimits `json:"limitsMilliWatts,omitempty"`
}

// PowerLimits carries min/default/max power caps in milliwatts.
type PowerLimits struct {
	// +optional
	Minimum int64 `json:"minimum,omitempty"`

	// +optional
	Default int64 `json:"default,omitempty"`

	// +optional
	Maximum int64 `json:"maximum,omitempty"`
}

// GPUThermal captures target and slowdown/shutdown thermal thresholds.
type GPUThermal struct {
	// +optional
	TargetCelsius int32 `json:"targetCelsius,omitempty"`

	// +optional
	MaxOperatingCelsius int32 `json:"maxOperatingCelsius,omitempty"`

	// +optional
	SlowdownThresholdCelsius int32 `json:"slowdownThresholdCelsius,omitempty"`

	// +optional
	ShutdownThresholdCelsius int32 `json:"shutdownThresholdCelsius,omitempty"`
}

// GPUClocks lists the static clock ceilings and per-memory-clock schedules.
type GPUClocks struct {
	// +optional
	MaximumMHz *ClockRates `json:"maximumMHz,omitempty"`

	// +optional
	Supported []SupportedClocks `json:"supported,omitempty"`
}

// ClockRates is a graphics/SM/memory/video clock tuple in MHz.
type ClockRates struct {
	// +optional
	Graphics int32 `json:"graphics,omitempty"`

	// +optional
	SM int32 `json:"sm,omitempty"`

	// +optional
	Memory int32 `json:"memory,omitempty"`

	// +optional
	Video int32 `json:"video,omitempty"`
}

// SupportedClocks pairs a memory clock with the graphics clocks it supports.
type SupportedClocks struct {
	MemoryMHz int32 `json:"memoryMHz"`

	// +listType=set
	GraphicsMHz []int32 `json:"graphicsMHz"`
}

// GPUCapabilities toggles on optional GPU features (MIG etc.) and holds an
// extensible attribute map keyed by qualified name.
type GPUCapabilities struct {
	// +optional
	MIG *MIGCapability `json:"mig,omitempty"`

	// Attributes carries extensible capability flags keyed by qualified name
	// (e.g. `nvidia.com/transformer-engine`). Values are typed via
	// CapabilityAttribute to avoid an opaque `any`.
	// +optional
	Attributes map[string]CapabilityAttribute `json:"attributes,omitempty"`
}

// MIGCapability advertises whether the profile supports MIG partitioning.
type MIGCapability struct {
	// +optional
	Supported bool `json:"supported,omitempty"`

	// +optional
	MaxGPUInstances int32 `json:"maxGPUInstances,omitempty"`
}

// CapabilityAttribute holds one of several typed values so map[string]any
// isn't needed in the schema. Only one field should be set per attribute.
type CapabilityAttribute struct {
	// +optional
	Bool *bool `json:"bool,omitempty"`

	// +optional
	Int *int64 `json:"int,omitempty"`

	// +optional
	String string `json:"string,omitempty"`

	// +optional
	// +listType=set
	Strings []string `json:"strings,omitempty"`
}

// SGPUHost describes optional host (CPU + host memory) characteristics.
type SGPUHost struct {
	// +optional
	CPU *HostCPU `json:"cpu,omitempty"`

	// +optional
	Memory *HostMemory `json:"memory,omitempty"`
}

// HostCPU captures host CPU vendor/product/arch/core count.
type HostCPU struct {
	// +optional
	Vendor string `json:"vendor,omitempty"`

	// +optional
	Product string `json:"product,omitempty"`

	// +optional
	Architecture string `json:"architecture,omitempty"`

	// +optional
	Cores int32 `json:"cores,omitempty"`
}

// HostMemory captures host memory capacity and GPU-coherency flag.
type HostMemory struct {
	Capacity resource.Quantity `json:"capacity"`

	// +optional
	CoherentWithGPU bool `json:"coherentWithGPU,omitempty"`
}

// SGPUTopology describes structural placement — PCIe slots, GPU fabric, and
// out-of-band network — visible to each logical node.
type SGPUTopology struct {
	// +listType=map
	// +listMapKey=index
	GPUSlots []GPUSlot `json:"gpuSlots"`

	// +optional
	GPUFabric *GPUFabric `json:"gpuFabric,omitempty"`

	// +optional
	Network *NetworkTopology `json:"network,omitempty"`
}

// GPUSlot pins one logical GPU to a PCI address plus NUMA/root-complex/host
// processor lineage.
type GPUSlot struct {
	Index int32 `json:"index"`

	// +optional
	PCIAddress string `json:"pciAddress,omitempty"`

	// +optional
	RootComplex string `json:"rootComplex,omitempty"`

	// +optional
	NumaNode int32 `json:"numaNode,omitempty"`

	// +optional
	HostProcessorIndex int32 `json:"hostProcessorIndex,omitempty"`
}

// GPUFabric characterises NVLink/NVSwitch fabric visible to this node.
type GPUFabric struct {
	// +optional
	Type string `json:"type,omitempty"`

	// +optional
	Generation int32 `json:"generation,omitempty"`

	// +optional
	LinksPerGPU int32 `json:"linksPerGPU,omitempty"`

	// +optional
	BandwidthPerLinkMBps int32 `json:"bandwidthPerLinkMBps,omitempty"`

	// +optional
	C2CSupported bool `json:"c2cSupported,omitempty"`

	// +optional
	Domain *FabricDomain `json:"domain,omitempty"`

	// +optional
	Switches *FabricSwitches `json:"switches,omitempty"`
}

// FabricDomain describes the fabric compute domain size and scope.
type FabricDomain struct {
	// +optional
	// +kubebuilder:validation:Enum=Node;Rack;Cluster
	Scope string `json:"scope,omitempty"`

	// +optional
	GPUCount int32 `json:"gpuCount,omitempty"`
}

// FabricSwitches records how many fabric switches each node sees.
type FabricSwitches struct {
	// +optional
	VisiblePerNode int32 `json:"visiblePerNode,omitempty"`
}

// NetworkTopology describes out-of-band adapters (typically InfiniBand).
type NetworkTopology struct {
	// +optional
	Type string `json:"type,omitempty"`

	// +optional
	AdapterModel string `json:"adapterModel,omitempty"`

	// +optional
	FirmwareVersion string `json:"firmwareVersion,omitempty"`

	// +optional
	LinkSpeedGbps int32 `json:"linkSpeedGbps,omitempty"`

	// +optional
	AdaptersPerGPU int32 `json:"adaptersPerGPU,omitempty"`
}

// SGPUSoftware fixes the driver/NVML/CUDA versions the mock advertises.
type SGPUSoftware struct {
	// +optional
	DriverVersion string `json:"driverVersion,omitempty"`

	// +optional
	NVMLVersion string `json:"nvmlVersion,omitempty"`

	// +optional
	CUDAVersion string `json:"cudaVersion,omitempty"`
}

// SGPUProfileDefaults holds the initial runtime state a fresh sGPU boots into.
// MEP0001 requires this to reuse RuntimeState verbatim so SGPURuntimePolicy
// can layer sparse overrides field-by-field.
type SGPUProfileDefaults struct {
	// +optional
	Runtime *RuntimeState `json:"runtime,omitempty"`
}
