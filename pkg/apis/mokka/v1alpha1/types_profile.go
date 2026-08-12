// Copyright (c) 2026, NVIDIA CORPORATION.  All rights reserved.
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

package v1alpha1

import (
	resource "k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +genclient
// +genclient:nonNamespaced
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster,shortName=sgpup

// SGPUProfile describes the static hardware and software shape of a simulated GPU rack.
type SGPUProfile struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec SGPUProfileSpec `json:"spec"`
}

// +kubebuilder:object:root=true
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// SGPUProfileList contains a list of SGPUProfile resources.
type SGPUProfileList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []SGPUProfile `json:"items"`
}

// SGPUProfileSpec is the desired static rack shape.
type SGPUProfileSpec struct {
	Rack     SGPUProfileRack `json:"rack"`
	Node     SGPUProfileNode `json:"node"`
	Software SGPUSoftware    `json:"software"`
}

// SGPUProfileRack describes the number of logical Nodes in one rack.
type SGPUProfileRack struct {
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=1024
	NodesPerRack int32 `json:"nodesPerRack"`
}

// +kubebuilder:validation:XValidation:rule="self.gpus.count == size(self.topology.gpuSlots)",message="gpuSlots length must equal gpus.count"
// +kubebuilder:validation:XValidation:rule="self.topology.gpuSlots.all(slot, slot.index < self.gpus.count)",message="gpu slot indexes must be contiguous from zero"
// +kubebuilder:validation:XValidation:rule="self.topology.gpuSlots.all(slot, self.topology.gpuSlots.exists_one(other, other.pciAddress == slot.pciAddress))",message="gpu slot PCI addresses must be unique"

// SGPUProfileNode describes one homogeneous logical Node.
type SGPUProfileNode struct {
	GPUs     SGPUHardware     `json:"gpus"`
	Host     *SGPUHost        `json:"host,omitempty"`
	Topology SGPUNodeTopology `json:"topology"`
}

// SGPUHardware is the hardware template shared by every GPU in a profile.
type SGPUHardware struct {
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=64
	Count        int32           `json:"count"`
	Model        GPUModel        `json:"model"`
	Memory       GPUMemory       `json:"memory"`
	PCI          GPUPCI          `json:"pci"`
	Power        GPUPower        `json:"power"`
	Thermal      GPUThermal      `json:"thermal"`
	Clocks       GPUClocks       `json:"clocks"`
	Capabilities GPUCapabilities `json:"capabilities"`
}

// GPUModel identifies a GPU product and its compute shape.
type GPUModel struct {
	// +kubebuilder:validation:MinLength=1
	Vendor string `json:"vendor"`
	// +kubebuilder:validation:MinLength=1
	Product string `json:"product"`
	// +kubebuilder:validation:MinLength=1
	ProductName string `json:"productName"`
	// +kubebuilder:validation:MinLength=1
	Architecture      string               `json:"architecture"`
	ComputeCapability GPUComputeCapability `json:"computeCapability"`
	Cores             GPUCores             `json:"cores"`
	Board             GPUBoard             `json:"board"`
	Firmware          GPUFirmware          `json:"firmware"`
}

// GPUComputeCapability is the CUDA compute capability exposed by the mock.
type GPUComputeCapability struct {
	// +kubebuilder:validation:Minimum=1
	Major int32 `json:"major"`
	// +kubebuilder:validation:Minimum=0
	Minor int32 `json:"minor"`
}

// GPUCores contains GPU processor counts.
type GPUCores struct {
	// +kubebuilder:validation:Minimum=1
	CUDA int32 `json:"cuda"`
}

// GPUBoard contains board-identifying static data.
type GPUBoard struct {
	// +kubebuilder:validation:MinLength=1
	PartNumber string `json:"partNumber"`
}

// GPUFirmware contains firmware versions exposed by the mock.
type GPUFirmware struct {
	// +kubebuilder:validation:MinLength=1
	VBIOSVersion string `json:"vbiosVersion"`
	// +kubebuilder:validation:MinLength=1
	GSPVersion string     `json:"gspVersion"`
	InfoROM    GPUInfoROM `json:"infoROM"`
}

// GPUInfoROM contains the static InfoROM object versions.
type GPUInfoROM struct {
	// +kubebuilder:validation:MinLength=1
	ImageVersion string `json:"imageVersion"`
	// +kubebuilder:validation:MinLength=1
	OEMObjectVersion string `json:"oemObjectVersion"`
	// +kubebuilder:validation:MinLength=1
	ECCObjectVersion string `json:"eccObjectVersion"`
	// +kubebuilder:validation:MinLength=1
	PowerObjectVersion string `json:"powerObjectVersion"`
}

// GPUMemory contains static GPU memory characteristics.
type GPUMemory struct {
	// +kubebuilder:validation:XValidation:rule="quantity(self).isGreaterThan(quantity('0'))",message="capacity must be positive"
	Capacity resource.Quantity `json:"capacity"`
	// +kubebuilder:validation:XValidation:rule="quantity(self).isGreaterThan(quantity('0'))",message="reserved must be positive"
	Reserved resource.Quantity `json:"reserved"`
	// +kubebuilder:validation:XValidation:rule="quantity(self).isGreaterThan(quantity('0'))",message="bar1Capacity must be positive"
	BAR1Capacity resource.Quantity `json:"bar1Capacity"`
	// +kubebuilder:validation:Minimum=1
	BusWidthBits int32 `json:"busWidthBits"`
}

// GPUPCI contains PCI identity and maximum-link data shared by profile GPUs.
type GPUPCI struct {
	// +kubebuilder:validation:Pattern=`^[0-9a-f]{4}$`
	VendorID string `json:"vendorID"`
	// +kubebuilder:validation:Pattern=`^[0-9a-f]{4}$`
	DeviceID string `json:"deviceID"`
	// +kubebuilder:validation:Pattern=`^[0-9a-f]{4}$`
	SubsystemVendorID string `json:"subsystemVendorID"`
	// +kubebuilder:validation:Pattern=`^[0-9a-f]{4}$`
	SubsystemDeviceID string     `json:"subsystemDeviceID"`
	MaxLink           GPUPCILink `json:"maxLink"`
}

// GPUPCILink describes a PCI link width and generation.
type GPUPCILink struct {
	// +kubebuilder:validation:Minimum=1
	Generation int32 `json:"generation"`
	// +kubebuilder:validation:Minimum=1
	Width int32 `json:"width"`
}

// GPUPower contains static power-management limits.
type GPUPower struct {
	ManagementSupported bool           `json:"managementSupported"`
	LimitsMilliWatts    GPUPowerLimits `json:"limitsMilliWatts"`
}

// GPUPowerLimits contains supported power limits in milliwatts.
type GPUPowerLimits struct {
	// +kubebuilder:validation:Minimum=1
	Minimum int64 `json:"minimum"`
	// +kubebuilder:validation:Minimum=1
	Default int64 `json:"default"`
	// +kubebuilder:validation:Minimum=1
	Maximum int64 `json:"maximum"`
}

// GPUThermal contains static temperature limits in Celsius.
type GPUThermal struct {
	TargetCelsius            int32 `json:"targetCelsius"`
	MaxOperatingCelsius      int32 `json:"maxOperatingCelsius"`
	SlowdownThresholdCelsius int32 `json:"slowdownThresholdCelsius"`
	ShutdownThresholdCelsius int32 `json:"shutdownThresholdCelsius"`
}

// GPUClocks contains maximum and supported clock combinations.
type GPUClocks struct {
	MaximumMHz GPUClockSet         `json:"maximumMHz"`
	Supported  []GPUSupportedClock `json:"supported"`
}

// GPUClockSet contains device clocks in megahertz.
type GPUClockSet struct {
	// +kubebuilder:validation:Minimum=1
	Graphics int32 `json:"graphics"`
	// +kubebuilder:validation:Minimum=1
	SM int32 `json:"sm"`
	// +kubebuilder:validation:Minimum=1
	Memory int32 `json:"memory"`
	// +kubebuilder:validation:Minimum=1
	Video int32 `json:"video"`
}

// GPUSupportedClock pairs a memory clock with supported graphics clocks.
type GPUSupportedClock struct {
	// +kubebuilder:validation:Minimum=1
	MemoryMHz int32 `json:"memoryMHz"`
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=128
	// +listType=set
	GraphicsMHz []int32 `json:"graphicsMHz"`
}

// GPUCapabilities contains structured and extensible static capabilities.
type GPUCapabilities struct {
	MIG GPUMIGCapability `json:"mig"`
	// +kubebuilder:validation:MaxProperties=64
	Attributes map[string]GPUCapabilityAttribute `json:"attributes,omitempty"`
}

// GPUMIGCapability describes MIG support.
type GPUMIGCapability struct {
	Supported bool `json:"supported"`
	// +kubebuilder:validation:Minimum=1
	MaxGPUInstances int32 `json:"maxGPUInstances"`
}

// +kubebuilder:validation:MinProperties=1
// +kubebuilder:validation:XValidation:rule="has(self.bool) != has(self.strings)",message="exactly one capability value must be set"

// GPUCapabilityAttribute is one typed extension value.
type GPUCapabilityAttribute struct {
	Bool *bool `json:"bool,omitempty"`
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=64
	// +listType=set
	// +kubebuilder:validation:items:MaxLength=256
	Strings []string `json:"strings,omitempty"`
}

// SGPUHost contains optional host characteristics exposed by the mock.
type SGPUHost struct {
	CPU    *SGPUHostCPU    `json:"cpu,omitempty"`
	Memory *SGPUHostMemory `json:"memory,omitempty"`
}

// SGPUHostCPU contains host processor characteristics.
type SGPUHostCPU struct {
	// +kubebuilder:validation:MinLength=1
	Vendor string `json:"vendor"`
	// +kubebuilder:validation:MinLength=1
	Product string `json:"product"`
	// +kubebuilder:validation:MinLength=1
	Architecture string `json:"architecture"`
	// +kubebuilder:validation:Minimum=1
	Cores int32 `json:"cores"`
}

// SGPUHostMemory contains host memory characteristics.
type SGPUHostMemory struct {
	// +kubebuilder:validation:XValidation:rule="quantity(self).isGreaterThan(quantity('0'))",message="capacity must be positive"
	Capacity        resource.Quantity `json:"capacity"`
	CoherentWithGPU bool              `json:"coherentWithGPU"`
}

// SGPUNodeTopology describes structural GPU, fabric, and network placement.
type SGPUNodeTopology struct {
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=64
	// +listType=map
	// +listMapKey=index
	GPUSlots  []SGPUGPUSlot  `json:"gpuSlots"`
	GPUFabric *SGPUGPUFabric `json:"gpuFabric,omitempty"`
	Network   *SGPUNetwork   `json:"network,omitempty"`
}

// SGPUGPUSlot defines a GPU's stable structural coordinate within a Node.
type SGPUGPUSlot struct {
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=63
	Index int32 `json:"index"`
	// +kubebuilder:validation:Pattern=`^[0-9a-f]{4}:[0-9a-f]{2}:[0-9a-f]{2}\.[0-7]$`
	// +kubebuilder:validation:MaxLength=12
	PCIAddress string `json:"pciAddress"`
	// +kubebuilder:validation:Pattern=`^pci[0-9a-f]{4}:[0-9a-f]{2}$`
	RootComplex string `json:"rootComplex"`
	// +kubebuilder:validation:Minimum=0
	NUMANode int32 `json:"numaNode"`
	// +kubebuilder:validation:Minimum=0
	HostProcessorIndex int32 `json:"hostProcessorIndex"`
}

// SGPUGPUFabric describes rack-local GPU fabric topology.
type SGPUGPUFabric struct {
	// +kubebuilder:validation:MinLength=1
	Type string `json:"type"`
	// +kubebuilder:validation:Minimum=1
	Generation int32 `json:"generation"`
	// +kubebuilder:validation:Minimum=1
	LinksPerGPU int32 `json:"linksPerGPU"`
	// +kubebuilder:validation:Minimum=1
	BandwidthPerLinkMBps int64                  `json:"bandwidthPerLinkMBps"`
	C2CSupported         bool                   `json:"c2cSupported"`
	Domain               SGPUGPUFabricDomain    `json:"domain"`
	Switches             *SGPUGPUFabricSwitches `json:"switches,omitempty"`
}

// SGPUGPUFabricDomain defines the fabric domain shape.
type SGPUGPUFabricDomain struct {
	// +kubebuilder:validation:Enum=Rack
	Scope string `json:"scope"`
	// +kubebuilder:validation:Minimum=1
	GPUCount int32 `json:"gpuCount"`
}

// SGPUGPUFabricSwitches contains fabric-switch visibility.
type SGPUGPUFabricSwitches struct {
	// +kubebuilder:validation:Minimum=1
	VisiblePerNode int32 `json:"visiblePerNode"`
}

// SGPUNetwork describes rack-local network topology.
type SGPUNetwork struct {
	// +kubebuilder:validation:MinLength=1
	Type string `json:"type"`
	// +kubebuilder:validation:MinLength=1
	AdapterModel string `json:"adapterModel"`
	// +kubebuilder:validation:MinLength=1
	FirmwareVersion string `json:"firmwareVersion"`
	// +kubebuilder:validation:Minimum=1
	LinkSpeedGbps int64 `json:"linkSpeedGbps"`
	// +kubebuilder:validation:Minimum=1
	AdaptersPerGPU int32 `json:"adaptersPerGPU"`
}

// SGPUSoftware contains versions exposed through NVML and CUDA queries.
type SGPUSoftware struct {
	// +kubebuilder:validation:MinLength=1
	DriverVersion string `json:"driverVersion"`
	// +kubebuilder:validation:MinLength=1
	NVMLVersion string `json:"nvmlVersion"`
	// +kubebuilder:validation:MinLength=1
	CUDAVersion string `json:"cudaVersion"`
}
