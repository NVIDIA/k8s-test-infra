// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import (
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// SGPURackProfile is the static shape of a simulated GPU rack.
//
// +genclient
// +genclient:nonNamespaced
// +kubebuilder:metadata:annotations=helm.sh/resource-policy=keep
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster,categories=mokka,shortName=srprof
// +kubebuilder:printcolumn:name="Nodes/Rack",type=integer,JSONPath=`.spec.rack.nodesPerRack`
// +kubebuilder:printcolumn:name="GPUs/Node",type=integer,JSONPath=`.spec.node.gpus.count`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
type SGPURackProfile struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// Spec describes the static rack shape and GPU defaults.
	Spec SGPURackProfileSpec `json:"spec"`
}

// SGPURackProfileList is the list wrapper for SGPURackProfile.
// +kubebuilder:object:root=true
type SGPURackProfileList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []SGPURackProfile `json:"items"`
}

// SGPURackProfileSpec is one logical rack profile.
type SGPURackProfileSpec struct {
	// Rack is the logical rack shape described by this profile.
	Rack SGPURackShape `json:"rack"`

	// Node is the homogeneous node template for the rack.
	Node SGPUNode `json:"node"`

	// +optional
	// Software contains simulated driver and accelerator versions.
	Software *SGPUSoftware `json:"software,omitempty"`

	// +optional
	// Defaults contains initial runtime values for newly created GPUs.
	Defaults *SGPURackProfileDefaults `json:"defaults,omitempty"`
}

// SGPURackShape defines the dimensions of a logical rack template. It is
// nested profile data rather than a materialized SGPURack resource.
type SGPURackShape struct {
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=1024
	// NodesPerRack is the number of logical nodes in each rack.
	NodesPerRack int32 `json:"nodesPerRack"`
}

// SGPUNode is a homogeneous logical node in the rack.
// +kubebuilder:validation:XValidation:rule="size(self.topology.gpuSlots) == self.gpus.count && self.topology.gpuSlots.all(slot, slot.index < self.gpus.count)",message="topology.gpuSlots must contain one slot per GPU with indexes in range"
type SGPUNode struct {
	// GPUs describes the GPU template repeated on each node.
	GPUs SGPUGPUs `json:"gpus"`

	// +optional
	// Host describes the simulated host CPU and memory.
	Host *SGPUHost `json:"host,omitempty"`

	// +required
	// Topology describes PCIe, fabric, and network placement.
	Topology *SGPUTopology `json:"topology"`
}

// SGPUGPUs is the shared template for every GPU on the node.
type SGPUGPUs struct {
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=64
	// Count is the number of GPUs on each node.
	Count int32 `json:"count"`

	// Model describes the vendor and product identity.
	Model GPUModel `json:"model"`

	// Memory describes capacity and bus characteristics.
	Memory GPUMemory `json:"memory"`

	// PCI describes the simulated PCI identity and link.
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
	// Vendor is the GPU vendor name.
	Vendor string `json:"vendor,omitempty"`

	// +optional
	// Product is the short GPU product identifier.
	Product string `json:"product,omitempty"`

	// +optional
	// ProductName is the human-readable GPU product name.
	ProductName string `json:"productName,omitempty"`

	// +optional
	// Architecture is the GPU architecture name.
	Architecture string `json:"architecture,omitempty"`

	// +optional
	// ComputeCapability is the CUDA compute capability.
	ComputeCapability *ComputeCapability `json:"computeCapability,omitempty"`

	// +optional
	// Cores contains programmable core counts.
	Cores *GPUCores `json:"cores,omitempty"`

	// +optional
	// Board contains board-level identity.
	Board *GPUBoard `json:"board,omitempty"`

	// +optional
	// Firmware contains firmware and InfoROM versions.
	Firmware *GPUFirmware `json:"firmware,omitempty"`
}

// ComputeCapability is the CUDA compute capability.
type ComputeCapability struct {
	// Major is the CUDA compute capability major version.
	Major int32 `json:"major"`
	// Minor is the CUDA compute capability minor version.
	Minor int32 `json:"minor"`
}

// GPUCores is programmable core counts.
type GPUCores struct {
	// +optional
	// CUDA is the number of programmable CUDA cores.
	CUDA int32 `json:"cuda,omitempty"`
}

// GPUBoard is board-level identity.
type GPUBoard struct {
	// +optional
	// PartNumber is the board part number.
	PartNumber string `json:"partNumber,omitempty"`
}

// GPUFirmware is firmware versions.
type GPUFirmware struct {
	// +optional
	// VBIOSVersion is the GPU VBIOS version.
	VBIOSVersion string `json:"vbiosVersion,omitempty"`

	// +optional
	// GSPVersion is the GPU System Processor firmware version.
	GSPVersion string `json:"gspVersion,omitempty"`

	// +optional
	// InfoROM contains InfoROM sub-object versions.
	InfoROM *InfoROM `json:"infoROM,omitempty"`
}

// InfoROM is NVML inforom sub-object versions.
type InfoROM struct {
	// +optional
	// ImageVersion is the InfoROM image version.
	ImageVersion string `json:"imageVersion,omitempty"`

	// +optional
	// OEMObjectVersion is the InfoROM OEM object version.
	OEMObjectVersion string `json:"oemObjectVersion,omitempty"`

	// +optional
	// ECCObjectVersion is the InfoROM ECC object version.
	ECCObjectVersion string `json:"eccObjectVersion,omitempty"`

	// +optional
	// PowerObjectVersion is the InfoROM power object version.
	PowerObjectVersion string `json:"powerObjectVersion,omitempty"`
}

// GPUMemory is on-device memory.
type GPUMemory struct {
	// Capacity is the total on-device memory capacity.
	Capacity resource.Quantity `json:"capacity"`

	// +optional
	// Reserved is memory reserved for system use.
	Reserved *resource.Quantity `json:"reserved,omitempty"`

	// +optional
	// Bar1Capacity is the BAR1 aperture capacity.
	Bar1Capacity *resource.Quantity `json:"bar1Capacity,omitempty"`

	// +optional
	// BusWidthBits is the memory bus width in bits.
	BusWidthBits int32 `json:"busWidthBits,omitempty"`
}

// GPUPCI is PCI identity and link characteristics.
type GPUPCI struct {
	// +optional
	// VendorID is the PCI vendor identifier.
	VendorID string `json:"vendorID,omitempty"`

	// +optional
	// DeviceID is the PCI device identifier.
	DeviceID string `json:"deviceID,omitempty"`

	// +optional
	// SubsystemVendorID is the PCI subsystem vendor identifier.
	SubsystemVendorID string `json:"subsystemVendorID,omitempty"`

	// +optional
	// SubsystemDeviceID is the PCI subsystem device identifier.
	SubsystemDeviceID string `json:"subsystemDeviceID,omitempty"`

	// +optional
	// MaxLink is the maximum PCIe link configuration.
	MaxLink *PCILink `json:"maxLink,omitempty"`
}

// PCILink is the PCIe max link generation and width.
type PCILink struct {
	// +optional
	// Generation is the maximum PCIe generation.
	Generation int32 `json:"generation,omitempty"`

	// +optional
	// Width is the maximum PCIe link width.
	Width int32 `json:"width,omitempty"`
}

// GPUPower is the power envelope.
type GPUPower struct {
	// +optional
	// ManagementSupported indicates whether power management is available.
	ManagementSupported bool `json:"managementSupported,omitempty"`

	// +optional
	// LimitsMilliWatts contains the power cap range in milliwatts.
	LimitsMilliWatts *PowerLimits `json:"limitsMilliWatts,omitempty"`
}

// PowerLimits is min/default/max power caps in milliwatts.
type PowerLimits struct {
	// +optional
	// Minimum is the minimum power limit in milliwatts.
	Minimum int64 `json:"minimum,omitempty"`

	// +optional
	// Default is the default power limit in milliwatts.
	Default int64 `json:"default,omitempty"`

	// +optional
	// Maximum is the maximum power limit in milliwatts.
	Maximum int64 `json:"maximum,omitempty"`
}

// GPUThermal is thermal thresholds.
type GPUThermal struct {
	// +optional
	// TargetCelsius is the target operating temperature.
	TargetCelsius int32 `json:"targetCelsius,omitempty"`

	// +optional
	// MaxOperatingCelsius is the maximum operating temperature.
	MaxOperatingCelsius int32 `json:"maxOperatingCelsius,omitempty"`

	// +optional
	// SlowdownThresholdCelsius is the thermal slowdown threshold.
	SlowdownThresholdCelsius int32 `json:"slowdownThresholdCelsius,omitempty"`

	// +optional
	// ShutdownThresholdCelsius is the thermal shutdown threshold.
	ShutdownThresholdCelsius int32 `json:"shutdownThresholdCelsius,omitempty"`
}

// GPUClocks is clock ceilings and per-memory-clock schedules.
type GPUClocks struct {
	// +optional
	// MaximumMHz is the maximum clock tuple.
	MaximumMHz *ClockRates `json:"maximumMHz,omitempty"`

	// +optional
	// Supported lists supported graphics clocks by memory clock.
	Supported []SupportedClocks `json:"supported,omitempty"`
}

// ClockRates is a graphics/SM/memory/video clock tuple in MHz.
type ClockRates struct {
	// +optional
	// Graphics is the maximum graphics clock in MHz.
	Graphics int32 `json:"graphics,omitempty"`

	// +optional
	// SM is the maximum SM clock in MHz.
	SM int32 `json:"sm,omitempty"`

	// +optional
	// Memory is the maximum memory clock in MHz.
	Memory int32 `json:"memory,omitempty"`

	// +optional
	// Video is the maximum video clock in MHz.
	Video int32 `json:"video,omitempty"`
}

// SupportedClocks pairs a memory clock with supported graphics clocks.
type SupportedClocks struct {
	// MemoryMHz is the memory clock for this supported set.
	MemoryMHz int32 `json:"memoryMHz"`

	// +listType=set
	// GraphicsMHz lists supported graphics clocks for MemoryMHz.
	GraphicsMHz []int32 `json:"graphicsMHz"`
}

// GPUCapabilities is GPU feature toggles and extensible attributes.
type GPUCapabilities struct {
	// +optional
	// MIG describes Multi-Instance GPU support.
	MIG *MIGCapability `json:"mig,omitempty"`

	// Extensible capability flags keyed by qualified name.
	// +optional
	Attributes map[string]CapabilityAttribute `json:"attributes,omitempty"`
}

// MIGCapability is MIG partitioning support.
type MIGCapability struct {
	// +optional
	// Supported indicates whether MIG is available.
	Supported bool `json:"supported,omitempty"`

	// +optional
	// MaxGPUInstances is the maximum number of GPU instances.
	MaxGPUInstances int32 `json:"maxGPUInstances,omitempty"`
}

// CapabilityAttribute is a typed variant; set exactly one field.
type CapabilityAttribute struct {
	// +optional
	// Bool is the boolean value when this attribute is boolean-valued.
	Bool *bool `json:"bool,omitempty"`

	// +optional
	// Int is the integer value when this attribute is integer-valued.
	Int *int64 `json:"int,omitempty"`

	// +optional
	// String is the string value when this attribute is string-valued.
	String string `json:"string,omitempty"`

	// +optional
	// +listType=set
	// Strings contains the string values when this attribute is a set.
	Strings []string `json:"strings,omitempty"`
}

// SGPUHost is host CPU + memory.
type SGPUHost struct {
	// +optional
	// CPU describes host processor identity and capacity.
	CPU *HostCPU `json:"cpu,omitempty"`

	// +optional
	// Memory describes host memory capacity and coherence.
	Memory *HostMemory `json:"memory,omitempty"`
}

// HostCPU is host CPU identity.
type HostCPU struct {
	// +optional
	// Vendor is the host CPU vendor.
	Vendor string `json:"vendor,omitempty"`

	// +optional
	// Product is the host CPU product name.
	Product string `json:"product,omitempty"`

	// +optional
	// Architecture is the host CPU architecture.
	Architecture string `json:"architecture,omitempty"`

	// +optional
	// Cores is the number of host CPU cores.
	Cores int32 `json:"cores,omitempty"`
}

// HostMemory is host memory capacity.
type HostMemory struct {
	// Capacity is the host memory capacity.
	Capacity resource.Quantity `json:"capacity"`

	// +optional
	// CoherentWithGPU indicates whether host memory is coherent with GPU memory.
	CoherentWithGPU bool `json:"coherentWithGPU,omitempty"`
}

// SGPUTopology is PCIe slots, GPU fabric, and network visible to the node.
type SGPUTopology struct {
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=64
	// +listType=map
	// +listMapKey=index
	// GPUSlots maps logical GPUs to PCI addresses on the node.
	GPUSlots []GPUSlot `json:"gpuSlots"`

	// +optional
	// GPUFabric describes the node's simulated GPU fabric.
	GPUFabric *GPUFabric `json:"gpuFabric,omitempty"`

	// +optional
	// Network describes the node's simulated network topology.
	Network *NetworkTopology `json:"network,omitempty"`
}

// GPUSlot pins one GPU to a PCI address + NUMA/root-complex/host CPU.
type GPUSlot struct {
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=63
	// Index is the logical GPU index for this slot.
	Index int32 `json:"index"`

	// +required
	// +kubebuilder:validation:Pattern=`^[0-9a-f]{4}:[0-9a-f]{2}:[0-9a-f]{2}\.[0-7]$`
	// PCIAddress is the GPU PCI address.
	PCIAddress string `json:"pciAddress"`

	// +required
	// +kubebuilder:validation:Pattern=`^pci[0-9a-f]{4}:[0-9a-f]{2}$`
	RootComplex string `json:"rootComplex"`

	// +optional
	// NumaNode is the NUMA node associated with the slot.
	NumaNode int32 `json:"numaNode,omitempty"`

	// +optional
	// HostProcessorIndex is the host processor associated with the slot.
	HostProcessorIndex int32 `json:"hostProcessorIndex,omitempty"`
}

// GPUFabric is NVLink/NVSwitch fabric.
type GPUFabric struct {
	// +optional
	// Type identifies the fabric implementation.
	Type string `json:"type,omitempty"`

	// +optional
	// Generation is the fabric generation.
	Generation int32 `json:"generation,omitempty"`

	// +optional
	// LinksPerGPU is the number of fabric links per GPU.
	LinksPerGPU int32 `json:"linksPerGPU,omitempty"`

	// +optional
	// BandwidthPerLinkMBps is the bandwidth of each fabric link.
	BandwidthPerLinkMBps int32 `json:"bandwidthPerLinkMBps,omitempty"`

	// +optional
	// C2CSupported indicates whether chip-to-chip links are available.
	C2CSupported bool `json:"c2cSupported,omitempty"`

	// +optional
	// Domain identifies the fabric compute domain.
	Domain *FabricDomain `json:"domain,omitempty"`

	// +optional
	// Switches describes fabric switches visible to the node.
	Switches *FabricSwitches `json:"switches,omitempty"`
}

// FabricDomain is the fabric compute domain.
type FabricDomain struct {
	// +optional
	// +kubebuilder:validation:Enum=Node;Rack;Cluster
	// Scope is the domain over which the fabric is shared.
	Scope string `json:"scope,omitempty"`

	// +optional
	// GPUCount is the number of GPUs in the fabric domain.
	GPUCount int32 `json:"gpuCount,omitempty"`
}

// FabricSwitches is fabric switches visible per node.
type FabricSwitches struct {
	// +optional
	// VisiblePerNode is the number of fabric switches visible per node.
	VisiblePerNode int32 `json:"visiblePerNode,omitempty"`
}

// NetworkTopology is out-of-band adapters.
type NetworkTopology struct {
	// +optional
	// Type identifies the network fabric.
	Type string `json:"type,omitempty"`

	// +optional
	// AdapterModel is the network adapter model.
	AdapterModel string `json:"adapterModel,omitempty"`

	// +optional
	// FirmwareVersion is the network adapter firmware version.
	FirmwareVersion string `json:"firmwareVersion,omitempty"`

	// +optional
	// LinkSpeedGbps is the network link speed in gigabits per second.
	LinkSpeedGbps int32 `json:"linkSpeedGbps,omitempty"`

	// +optional
	// AdaptersPerGPU is the number of adapters associated with each GPU.
	AdaptersPerGPU int32 `json:"adaptersPerGPU,omitempty"`
}

// SGPUSoftware is driver/NVML/CUDA versions.
type SGPUSoftware struct {
	// +optional
	// DriverVersion is the simulated NVIDIA driver version.
	DriverVersion string `json:"driverVersion,omitempty"`

	// +optional
	// NVMLVersion is the simulated NVML version.
	NVMLVersion string `json:"nvmlVersion,omitempty"`

	// +optional
	// CUDAVersion is the simulated CUDA version.
	CUDAVersion string `json:"cudaVersion,omitempty"`
}

// SGPURackProfileDefaults is initial runtime state for fresh sGPUs.
type SGPURackProfileDefaults struct {
	// +optional
	// Runtime contains initial runtime state for newly created GPUs.
	Runtime *RuntimeState `json:"runtime,omitempty"`
}
