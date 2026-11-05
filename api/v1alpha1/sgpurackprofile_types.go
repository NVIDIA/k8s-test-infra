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

	// Spec defines the hardware shape and default software/runtime metadata.
	Spec SGPURackProfileSpec `json:"spec"`
}

// Reference returns the reference that pins this profile observation and the
// given content revision.
func (p *SGPURackProfile) Reference(revision string) SGPURackProfileReference {
	return SGPURackProfileReference{
		Name:       p.Name,
		UID:        p.UID,
		Generation: p.Generation,
		Revision:   revision,
	}
}

// SGPURackProfileList is the list wrapper for SGPURackProfile.
// +kubebuilder:object:root=true
type SGPURackProfileList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	// Items contains the profiles in this list.
	Items []SGPURackProfile `json:"items"`
}

// SGPURackProfileSpec is one logical rack profile.
type SGPURackProfileSpec struct {
	// Rack is the logical rack shape described by this profile.
	Rack SGPURackShape `json:"rack"`

	// Node describes the shared simulated hardware for each rack Node.
	Node SGPUNode `json:"node"`

	// +optional
	// Software records optional driver, NVML, and CUDA versions.
	Software *SGPUSoftware `json:"software,omitempty"`

	// +optional
	// Defaults provides initial runtime settings for newly materialized GPUs.
	Defaults *SGPURackProfileDefaults `json:"defaults,omitempty"`
}

// SGPURackShape defines the dimensions of a logical rack template. It is
// nested profile data rather than a materialized SGPURack resource.
type SGPURackShape struct {
	// NodesPerRack is the number of logical Nodes in each rack.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=1024
	NodesPerRack int32 `json:"nodesPerRack"`
}

// SGPUNode is a homogeneous logical node in the rack.
// +kubebuilder:validation:XValidation:rule="size(self.topology.gpuSlots) == self.gpus.count && self.topology.gpuSlots.all(slot, slot.index < self.gpus.count)",message="topology.gpuSlots must contain one slot per GPU with indexes in range"
type SGPUNode struct {
	// GPUs defines the homogeneous simulated GPU set on each Node.
	GPUs SGPUGPUs `json:"gpus"`

	// +optional
	// Host describes the CPU and memory presented by each Node.
	Host *SGPUHost `json:"host,omitempty"`

	// +required
	// Topology describes PCIe slots and optional GPU/network fabrics.
	Topology *SGPUTopology `json:"topology"`
}

// SGPUGPUs is the shared template for every GPU on the node.
type SGPUGPUs struct {
	// Count is the number of GPUs exposed on each Node.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=64
	Count int32 `json:"count"`

	// Model describes the GPU vendor, product, and architecture.
	Model GPUModel `json:"model"`

	// Memory describes device memory capacity and bus width.
	Memory GPUMemory `json:"memory"`

	// PCI describes device identifiers and maximum link properties.
	PCI GPUPCI `json:"pci"`

	// +optional
	// Power describes power-management support and device limits.
	Power *GPUPower `json:"power,omitempty"`

	// +optional
	// Thermal describes device temperature thresholds.
	Thermal *GPUThermal `json:"thermal,omitempty"`

	// +optional
	// Clocks describes maximum and supported device clock rates.
	Clocks *GPUClocks `json:"clocks,omitempty"`

	// +optional
	// Capabilities describes MIG support and additional named capabilities.
	Capabilities *GPUCapabilities `json:"capabilities,omitempty"`
}

// GPUModel is the vendor/product identity.
type GPUModel struct {
	// +optional
	// Vendor is the GPU manufacturer name.
	Vendor string `json:"vendor,omitempty"`

	// +optional
	// Product is the vendor's product identifier.
	Product string `json:"product,omitempty"`

	// +optional
	// ProductName is the human-readable product name.
	ProductName string `json:"productName,omitempty"`

	// +optional
	// Architecture is the GPU architecture name.
	Architecture string `json:"architecture,omitempty"`

	// +optional
	// ComputeCapability is the CUDA compute capability version.
	ComputeCapability *ComputeCapability `json:"computeCapability,omitempty"`

	// +optional
	// Cores contains programmable core counts.
	Cores *GPUCores `json:"cores,omitempty"`

	// +optional
	// Board contains board-level identity.
	Board *GPUBoard `json:"board,omitempty"`

	// +optional
	// Firmware contains firmware and information-ROM versions.
	Firmware *GPUFirmware `json:"firmware,omitempty"`
}

// ComputeCapability is the CUDA compute capability.
type ComputeCapability struct {
	// Major is the major CUDA compute capability version.
	Major int32 `json:"major"`
	// Minor is the minor CUDA compute capability version.
	Minor int32 `json:"minor"`
}

// GPUCores is programmable core counts.
type GPUCores struct {
	// +optional
	// CUDA is the number of CUDA cores per GPU.
	CUDA int32 `json:"cuda,omitempty"`
}

// GPUBoard is board-level identity.
type GPUBoard struct {
	// +optional
	// PartNumber is the board part number reported for the GPU.
	PartNumber string `json:"partNumber,omitempty"`
}

// GPUFirmware is firmware versions.
type GPUFirmware struct {
	// +optional
	// VBIOSVersion is the video BIOS version.
	VBIOSVersion string `json:"vbiosVersion,omitempty"`

	// +optional
	// GSPVersion is the GPU System Processor firmware version.
	GSPVersion string `json:"gspVersion,omitempty"`

	// +optional
	// InfoROM contains versions of the GPU information-ROM objects.
	InfoROM *InfoROM `json:"infoROM,omitempty"`
}

// InfoROM is NVML inforom sub-object versions.
type InfoROM struct {
	// +optional
	// ImageVersion is the information-ROM image version.
	ImageVersion string `json:"imageVersion,omitempty"`

	// +optional
	// OEMObjectVersion is the OEM information-ROM object version.
	OEMObjectVersion string `json:"oemObjectVersion,omitempty"`

	// +optional
	// ECCObjectVersion is the ECC information-ROM object version.
	ECCObjectVersion string `json:"eccObjectVersion,omitempty"`

	// +optional
	// PowerObjectVersion is the power information-ROM object version.
	PowerObjectVersion string `json:"powerObjectVersion,omitempty"`
}

// GPUMemory is on-device memory.
type GPUMemory struct {
	// Capacity is the total device memory capacity.
	Capacity resource.Quantity `json:"capacity"`

	// +optional
	// Reserved is the amount of memory reserved from workloads.
	Reserved *resource.Quantity `json:"reserved,omitempty"`

	// +optional
	// Bar1Capacity is the device BAR1 aperture size.
	Bar1Capacity *resource.Quantity `json:"bar1Capacity,omitempty"`

	// +optional
	// BusWidthBits is the memory bus width in bits.
	BusWidthBits int32 `json:"busWidthBits,omitempty"`
}

// GPUPCI is PCI identity and link characteristics.
type GPUPCI struct {
	// +optional
	// VendorID is the PCI vendor identifier in hexadecimal.
	VendorID string `json:"vendorID,omitempty"`

	// +optional
	// DeviceID is the PCI device identifier in hexadecimal.
	DeviceID string `json:"deviceID,omitempty"`

	// +optional
	// SubsystemVendorID is the PCI subsystem vendor identifier.
	SubsystemVendorID string `json:"subsystemVendorID,omitempty"`

	// +optional
	// SubsystemDeviceID is the PCI subsystem device identifier.
	SubsystemDeviceID string `json:"subsystemDeviceID,omitempty"`

	// +optional
	// MaxLink is the maximum supported PCIe generation and width.
	MaxLink *PCILink `json:"maxLink,omitempty"`
}

// PCILink is the PCIe max link generation and width.
type PCILink struct {
	// +optional
	// Generation is the maximum PCIe generation.
	Generation int32 `json:"generation,omitempty"`

	// +optional
	// Width is the maximum PCIe link width in lanes.
	Width int32 `json:"width,omitempty"`
}

// GPUPower is the power envelope.
type GPUPower struct {
	// +optional
	// ManagementSupported indicates whether power management is available.
	ManagementSupported bool `json:"managementSupported,omitempty"`

	// +optional
	// LimitsMilliWatts gives the minimum, default, and maximum power limits.
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
	// TargetCelsius is the target GPU temperature in degrees Celsius.
	TargetCelsius int32 `json:"targetCelsius,omitempty"`

	// +optional
	// MaxOperatingCelsius is the maximum rated operating temperature.
	MaxOperatingCelsius int32 `json:"maxOperatingCelsius,omitempty"`

	// +optional
	// SlowdownThresholdCelsius is the temperature at which thermal slowdown begins.
	SlowdownThresholdCelsius int32 `json:"slowdownThresholdCelsius,omitempty"`

	// +optional
	// ShutdownThresholdCelsius is the temperature at which shutdown is triggered.
	ShutdownThresholdCelsius int32 `json:"shutdownThresholdCelsius,omitempty"`
}

// GPUClocks is clock ceilings and per-memory-clock schedules.
type GPUClocks struct {
	// +optional
	// MaximumMHz gives the maximum graphics, SM, memory, and video clock rates.
	MaximumMHz *ClockRates `json:"maximumMHz,omitempty"`

	// +optional
	// Supported lists graphics clock rates available at each memory clock rate.
	Supported []SupportedClocks `json:"supported,omitempty"`
}

// ClockRates is a graphics/SM/memory/video clock tuple in MHz.
type ClockRates struct {
	// +optional
	// Graphics is the graphics clock rate in MHz.
	Graphics int32 `json:"graphics,omitempty"`

	// +optional
	// SM is the streaming-multiprocessor clock rate in MHz.
	SM int32 `json:"sm,omitempty"`

	// +optional
	// Memory is the memory clock rate in MHz.
	Memory int32 `json:"memory,omitempty"`

	// +optional
	// Video is the video clock rate in MHz.
	Video int32 `json:"video,omitempty"`
}

// SupportedClocks pairs a memory clock with supported graphics clocks.
type SupportedClocks struct {
	// MemoryMHz is a memory clock rate paired with GraphicsMHz values.
	MemoryMHz int32 `json:"memoryMHz"`

	// +listType=set
	// GraphicsMHz lists graphics clock rates supported at MemoryMHz.
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
	// Supported indicates whether MIG is available on the GPU.
	Supported bool `json:"supported,omitempty"`

	// +optional
	// MaxGPUInstances is the maximum number of GPU instances supported.
	MaxGPUInstances int32 `json:"maxGPUInstances,omitempty"`
}

// CapabilityAttribute is a typed variant; set exactly one field.
type CapabilityAttribute struct {
	// +optional
	// Bool stores a boolean capability value.
	Bool *bool `json:"bool,omitempty"`

	// +optional
	// Int stores an integer capability value.
	Int *int64 `json:"int,omitempty"`

	// +optional
	// String stores a string capability value.
	String string `json:"string,omitempty"`

	// +optional
	// +listType=set
	// Strings stores a set of string capability values.
	Strings []string `json:"strings,omitempty"`
}

// SGPUHost is host CPU + memory.
type SGPUHost struct {
	// +optional
	// CPU describes the host processor presented by the simulated Node.
	CPU *HostCPU `json:"cpu,omitempty"`

	// +optional
	// Memory describes host memory presented by the simulated Node.
	Memory *HostMemory `json:"memory,omitempty"`
}

// HostCPU is host CPU identity.
type HostCPU struct {
	// +optional
	// Vendor is the processor manufacturer.
	Vendor string `json:"vendor,omitempty"`

	// +optional
	// Product is the processor product identifier.
	Product string `json:"product,omitempty"`

	// +optional
	// Architecture is the host processor architecture.
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
	// CoherentWithGPU indicates whether host and GPU memory are coherent.
	CoherentWithGPU bool `json:"coherentWithGPU,omitempty"`
}

// SGPUTopology is PCIe slots, GPU fabric, and network visible to the node.
type SGPUTopology struct {
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=64
	// +listType=map
	// +listMapKey=index
	// GPUSlots maps each GPU index to its PCI and host topology coordinates.
	GPUSlots []GPUSlot `json:"gpuSlots"`

	// +optional
	// GPUFabric describes optional NVLink/NVSwitch connectivity.
	GPUFabric *GPUFabric `json:"gpuFabric,omitempty"`

	// +optional
	// Network describes optional out-of-band network adapters.
	Network *NetworkTopology `json:"network,omitempty"`
}

// GPUSlot pins one GPU to a PCI address + NUMA/root-complex/host CPU.
type GPUSlot struct {
	// Index is the zero-based GPU index on the Node.
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=63
	Index int32 `json:"index"`

	// +required
	// PCIAddress is the PCI bus address of this GPU.
	// +kubebuilder:validation:Pattern=`^[0-9a-f]{4}:[0-9a-f]{2}:[0-9a-f]{2}\.[0-7]$`
	PCIAddress string `json:"pciAddress"`

	// +required
	// RootComplex identifies the PCI root complex connected to this GPU.
	// +kubebuilder:validation:Pattern=`^pci[0-9a-f]{4}:[0-9a-f]{2}$`
	RootComplex string `json:"rootComplex"`

	// +optional
	// NumaNode is the host NUMA node associated with this GPU.
	NumaNode int32 `json:"numaNode,omitempty"`

	// +optional
	// HostProcessorIndex is the host CPU index associated with this GPU.
	HostProcessorIndex int32 `json:"hostProcessorIndex,omitempty"`
}

// GPUFabric is NVLink/NVSwitch fabric.
type GPUFabric struct {
	// +optional
	// Type names the GPU interconnect fabric.
	Type string `json:"type,omitempty"`

	// +optional
	// Generation is the fabric generation.
	Generation int32 `json:"generation,omitempty"`

	// +optional
	// LinksPerGPU is the number of fabric links available per GPU.
	LinksPerGPU int32 `json:"linksPerGPU,omitempty"`

	// +optional
	// BandwidthPerLinkMBps is the bandwidth of each link in megabytes per second.
	BandwidthPerLinkMBps int32 `json:"bandwidthPerLinkMBps,omitempty"`

	// +optional
	// C2CSupported indicates whether coherent CPU-to-GPU links are supported.
	C2CSupported bool `json:"c2cSupported,omitempty"`

	// +optional
	// Domain describes the scope and size of the GPU fabric domain.
	Domain *FabricDomain `json:"domain,omitempty"`

	// +optional
	// Switches describes the fabric switches visible to each Node.
	Switches *FabricSwitches `json:"switches,omitempty"`
}

// FabricDomain is the fabric compute domain.
type FabricDomain struct {
	// +optional
	// +kubebuilder:validation:Enum=Node;Rack;Cluster
	// Scope is the level at which GPUs share a fabric domain.
	Scope string `json:"scope,omitempty"`

	// +optional
	// GPUCount is the number of GPUs in the domain.
	GPUCount int32 `json:"gpuCount,omitempty"`
}

// FabricSwitches is fabric switches visible per node.
type FabricSwitches struct {
	// +optional
	// VisiblePerNode is the number of fabric switches visible from each Node.
	VisiblePerNode int32 `json:"visiblePerNode,omitempty"`
}

// NetworkTopology is out-of-band adapters.
type NetworkTopology struct {
	// +optional
	// Type names the out-of-band network adapter type.
	Type string `json:"type,omitempty"`

	// +optional
	// AdapterModel is the adapter model name.
	AdapterModel string `json:"adapterModel,omitempty"`

	// +optional
	// FirmwareVersion is the adapter firmware version.
	FirmwareVersion string `json:"firmwareVersion,omitempty"`

	// +optional
	// LinkSpeedGbps is the adapter link speed in gigabits per second.
	LinkSpeedGbps int32 `json:"linkSpeedGbps,omitempty"`

	// +optional
	// AdaptersPerGPU is the number of network adapters associated with each GPU.
	AdaptersPerGPU int32 `json:"adaptersPerGPU,omitempty"`
}

// SGPUSoftware is driver/NVML/CUDA versions.
type SGPUSoftware struct {
	// +optional
	// DriverVersion is the NVIDIA driver version.
	DriverVersion string `json:"driverVersion,omitempty"`

	// +optional
	// NVMLVersion is the NVIDIA Management Library version.
	NVMLVersion string `json:"nvmlVersion,omitempty"`

	// +optional
	// CUDAVersion is the CUDA version.
	CUDAVersion string `json:"cudaVersion,omitempty"`
}

// SGPURackProfileDefaults is initial runtime state for fresh sGPUs.
type SGPURackProfileDefaults struct {
	// +optional
	// Runtime provides initial sparse runtime settings for newly materialized GPUs.
	Runtime *RuntimeState `json:"runtime,omitempty"`
}
