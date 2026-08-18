// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import (
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// SGPUProfile is the static shape of a simulated GPU rack.
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

// SGPUProfileSpec is one logical rack profile.
type SGPUProfileSpec struct {
	Rack SGPURack `json:"rack"`

	Node SGPUNode `json:"node"`

	// +optional
	Software *SGPUSoftware `json:"software,omitempty"`

	// +optional
	Defaults *SGPUProfileDefaults `json:"defaults,omitempty"`
}

// SGPURack is the logical rack shape.
type SGPURack struct {
	// +kubebuilder:validation:Minimum=1
	NodesPerRack int32 `json:"nodesPerRack"`
}

// SGPUNode is a homogeneous logical node in the rack.
type SGPUNode struct {
	GPUs SGPUGPUs `json:"gpus"`

	// +optional
	Host *SGPUHost `json:"host,omitempty"`

	// +optional
	Topology *SGPUTopology `json:"topology,omitempty"`
}

// SGPUGPUs is the shared template for every GPU on the node.
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

// GPUModel is the vendor/product identity.
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

// ComputeCapability is the CUDA compute capability.
type ComputeCapability struct {
	Major int32 `json:"major"`
	Minor int32 `json:"minor"`
}

// GPUCores is programmable core counts.
type GPUCores struct {
	// +optional
	CUDA int32 `json:"cuda,omitempty"`
}

// GPUBoard is board-level identity.
type GPUBoard struct {
	// +optional
	PartNumber string `json:"partNumber,omitempty"`
}

// GPUFirmware is firmware versions.
type GPUFirmware struct {
	// +optional
	VBIOSVersion string `json:"vbiosVersion,omitempty"`

	// +optional
	GSPVersion string `json:"gspVersion,omitempty"`

	// +optional
	InfoROM *InfoROM `json:"infoROM,omitempty"`
}

// InfoROM is NVML inforom sub-object versions.
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

// GPUMemory is on-device memory.
type GPUMemory struct {
	Capacity resource.Quantity `json:"capacity"`

	// +optional
	Reserved *resource.Quantity `json:"reserved,omitempty"`

	// +optional
	Bar1Capacity *resource.Quantity `json:"bar1Capacity,omitempty"`

	// +optional
	BusWidthBits int32 `json:"busWidthBits,omitempty"`
}

// GPUPCI is PCI identity and link characteristics.
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

// PCILink is the PCIe max link generation and width.
type PCILink struct {
	// +optional
	Generation int32 `json:"generation,omitempty"`

	// +optional
	Width int32 `json:"width,omitempty"`
}

// GPUPower is the power envelope.
type GPUPower struct {
	// +optional
	ManagementSupported bool `json:"managementSupported,omitempty"`

	// +optional
	LimitsMilliWatts *PowerLimits `json:"limitsMilliWatts,omitempty"`
}

// PowerLimits is min/default/max power caps in milliwatts.
type PowerLimits struct {
	// +optional
	Minimum int64 `json:"minimum,omitempty"`

	// +optional
	Default int64 `json:"default,omitempty"`

	// +optional
	Maximum int64 `json:"maximum,omitempty"`
}

// GPUThermal is thermal thresholds.
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

// GPUClocks is clock ceilings and per-memory-clock schedules.
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

// SupportedClocks pairs a memory clock with supported graphics clocks.
type SupportedClocks struct {
	MemoryMHz int32 `json:"memoryMHz"`

	// +listType=set
	GraphicsMHz []int32 `json:"graphicsMHz"`
}

// GPUCapabilities is GPU feature toggles and extensible attributes.
type GPUCapabilities struct {
	// +optional
	MIG *MIGCapability `json:"mig,omitempty"`

	// Extensible capability flags keyed by qualified name.
	// +optional
	Attributes map[string]CapabilityAttribute `json:"attributes,omitempty"`
}

// MIGCapability is MIG partitioning support.
type MIGCapability struct {
	// +optional
	Supported bool `json:"supported,omitempty"`

	// +optional
	MaxGPUInstances int32 `json:"maxGPUInstances,omitempty"`
}

// CapabilityAttribute is a typed variant; set exactly one field.
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

// SGPUHost is host CPU + memory.
type SGPUHost struct {
	// +optional
	CPU *HostCPU `json:"cpu,omitempty"`

	// +optional
	Memory *HostMemory `json:"memory,omitempty"`
}

// HostCPU is host CPU identity.
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

// HostMemory is host memory capacity.
type HostMemory struct {
	Capacity resource.Quantity `json:"capacity"`

	// +optional
	CoherentWithGPU bool `json:"coherentWithGPU,omitempty"`
}

// SGPUTopology is PCIe slots, GPU fabric, and network visible to the node.
type SGPUTopology struct {
	// +listType=map
	// +listMapKey=index
	GPUSlots []GPUSlot `json:"gpuSlots"`

	// +optional
	GPUFabric *GPUFabric `json:"gpuFabric,omitempty"`

	// +optional
	Network *NetworkTopology `json:"network,omitempty"`
}

// GPUSlot pins one GPU to a PCI address + NUMA/root-complex/host CPU.
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

// GPUFabric is NVLink/NVSwitch fabric.
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

// FabricDomain is the fabric compute domain.
type FabricDomain struct {
	// +optional
	// +kubebuilder:validation:Enum=Node;Rack;Cluster
	Scope string `json:"scope,omitempty"`

	// +optional
	GPUCount int32 `json:"gpuCount,omitempty"`
}

// FabricSwitches is fabric switches visible per node.
type FabricSwitches struct {
	// +optional
	VisiblePerNode int32 `json:"visiblePerNode,omitempty"`
}

// NetworkTopology is out-of-band adapters.
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

// SGPUSoftware is driver/NVML/CUDA versions.
type SGPUSoftware struct {
	// +optional
	DriverVersion string `json:"driverVersion,omitempty"`

	// +optional
	NVMLVersion string `json:"nvmlVersion,omitempty"`

	// +optional
	CUDAVersion string `json:"cudaVersion,omitempty"`
}

// SGPUProfileDefaults is initial runtime state for fresh sGPUs.
type SGPUProfileDefaults struct {
	// +optional
	Runtime *RuntimeState `json:"runtime,omitempty"`
}
