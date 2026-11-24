<!--
SPDX-License-Identifier: Apache-2.0
SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION
-->

# API Reference

## Packages
- [mokka.nvidia.com/v1alpha1](#mokkanvidiacomv1alpha1)


## mokka.nvidia.com/v1alpha1

Package v1alpha1 contains API types for the mokka.nvidia.com group.


### Resource Types
- [SGPUInventory](#sgpuinventory)
- [SGPURack](#sgpurack)
- [SGPURackProfile](#sgpurackprofile)
- [SGPURuntimePolicy](#sgpuruntimepolicy)
- [SNICProfile](#snicprofile)



#### CapabilityAttribute



CapabilityAttribute is a typed variant; set exactly one field.



_Appears in:_
- [GPUCapabilities](#gpucapabilities)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `bool` _boolean_ | Bool stores a boolean capability value. |  | Optional: \{\} <br /> |
| `int` _integer_ | Int stores an integer capability value. |  | Optional: \{\} <br /> |
| `string` _string_ | String stores a string capability value. |  | Optional: \{\} <br /> |
| `strings` _string array_ | Strings stores a set of string capability values. |  | Optional: \{\} <br /> |


#### ClockRates



ClockRates is a graphics/SM/memory/video clock tuple in MHz.



_Appears in:_
- [GPUClocks](#gpuclocks)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `graphics` _integer_ | Graphics is the graphics clock rate in MHz. |  | Optional: \{\} <br /> |
| `sm` _integer_ | SM is the streaming-multiprocessor clock rate in MHz. |  | Optional: \{\} <br /> |
| `memory` _integer_ | Memory is the memory clock rate in MHz. |  | Optional: \{\} <br /> |
| `video` _integer_ | Video is the video clock rate in MHz. |  | Optional: \{\} <br /> |


#### ClocksTelemetry



ClocksTelemetry reports current clock rates.



_Appears in:_
- [RuntimeTelemetry](#runtimetelemetry)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `graphicsMHz` _integer_ | GraphicsMHz is the simulated graphics clock rate in MHz. |  | Minimum: 0 <br />Optional: \{\} <br /> |
| `smMHz` _integer_ | SMMHz is the simulated streaming-multiprocessor clock rate in MHz. |  | Minimum: 0 <br />Optional: \{\} <br /> |
| `memoryMHz` _integer_ | MemoryMHz is the simulated memory clock rate in MHz. |  | Minimum: 0 <br />Optional: \{\} <br /> |
| `videoMHz` _integer_ | VideoMHz is the simulated video clock rate in MHz. |  | Minimum: 0 <br />Optional: \{\} <br /> |


#### ComputeCapability



ComputeCapability is the CUDA compute capability.



_Appears in:_
- [GPUModel](#gpumodel)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `major` _integer_ | Major is the major CUDA compute capability version. |  |  |
| `minor` _integer_ | Minor is the minor CUDA compute capability version. |  |  |


#### DeviceState

_Underlying type:_ _string_

DeviceState is a simulated GPU health state.

_Validation:_
- Enum: [Healthy Degraded Failed]

_Appears in:_
- [RuntimeState](#runtimestate)

| Field | Description |
| --- | --- |
| `Healthy` |  |
| `Degraded` |  |
| `Failed` |  |


#### FabricDomain



FabricDomain is the fabric compute domain.



_Appears in:_
- [GPUFabric](#gpufabric)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `scope` _string_ | Scope is the level at which GPUs share a fabric domain. |  | Enum: [Node Rack Cluster] <br />Optional: \{\} <br /> |
| `gpuCount` _integer_ | GPUCount is the number of GPUs in the domain. |  | Optional: \{\} <br /> |


#### FabricSwitches



FabricSwitches is fabric switches visible per node.



_Appears in:_
- [GPUFabric](#gpufabric)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `visiblePerNode` _integer_ | VisiblePerNode is the number of fabric switches visible from each Node. |  | Optional: \{\} <br /> |


#### GPUBoard



GPUBoard is board-level identity.



_Appears in:_
- [GPUModel](#gpumodel)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `partNumber` _string_ | PartNumber is the board part number reported for the GPU. |  | Optional: \{\} <br /> |


#### GPUCapabilities



GPUCapabilities is GPU feature toggles and extensible attributes.



_Appears in:_
- [SGPUGPUs](#sgpugpus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `mig` _[MIGCapability](#migcapability)_ | MIG describes Multi-Instance GPU support. |  | Optional: \{\} <br /> |
| `attributes` _object (keys:string, values:[CapabilityAttribute](#capabilityattribute))_ | Extensible capability flags keyed by qualified name. |  | Optional: \{\} <br /> |


#### GPUClocks



GPUClocks is clock ceilings and per-memory-clock schedules.



_Appears in:_
- [SGPUGPUs](#sgpugpus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `maximumMHz` _[ClockRates](#clockrates)_ | MaximumMHz gives the maximum graphics, SM, memory, and video clock rates. |  | Optional: \{\} <br /> |
| `supported` _[SupportedClocks](#supportedclocks) array_ | Supported lists graphics clock rates available at each memory clock rate. |  | Optional: \{\} <br /> |


#### GPUCores



GPUCores is programmable core counts.



_Appears in:_
- [GPUModel](#gpumodel)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `cuda` _integer_ | CUDA is the number of CUDA cores per GPU. |  | Optional: \{\} <br /> |


#### GPUFabric



GPUFabric is NVLink/NVSwitch fabric.



_Appears in:_
- [SGPUTopology](#sgputopology)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `type` _string_ | Type names the GPU interconnect fabric. |  | Optional: \{\} <br /> |
| `generation` _integer_ | Generation is the fabric generation. |  | Optional: \{\} <br /> |
| `linksPerGPU` _integer_ | LinksPerGPU is the number of fabric links available per GPU. |  | Optional: \{\} <br /> |
| `bandwidthPerLinkMBps` _integer_ | BandwidthPerLinkMBps is the bandwidth of each link in megabytes per second. |  | Optional: \{\} <br /> |
| `c2cSupported` _boolean_ | C2CSupported indicates whether coherent CPU-to-GPU links are supported. |  | Optional: \{\} <br /> |
| `domain` _[FabricDomain](#fabricdomain)_ | Domain describes the scope and size of the GPU fabric domain. |  | Optional: \{\} <br /> |
| `switches` _[FabricSwitches](#fabricswitches)_ | Switches describes the fabric switches visible to each Node. |  | Optional: \{\} <br /> |


#### GPUFirmware



GPUFirmware is firmware versions.



_Appears in:_
- [GPUModel](#gpumodel)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `vbiosVersion` _string_ | VBIOSVersion is the video BIOS version. |  | Optional: \{\} <br /> |
| `gspVersion` _string_ | GSPVersion is the GPU System Processor firmware version. |  | Optional: \{\} <br /> |
| `infoROM` _[InfoROM](#inforom)_ | InfoROM contains versions of the GPU information-ROM objects. |  | Optional: \{\} <br /> |


#### GPUMemory



GPUMemory is on-device memory.



_Appears in:_
- [SGPUGPUs](#sgpugpus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `capacity` _[Quantity](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#quantity-resource-api)_ | Capacity is the total device memory capacity. |  |  |
| `reserved` _[Quantity](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#quantity-resource-api)_ | Reserved is the amount of memory reserved from workloads. |  | Optional: \{\} <br /> |
| `bar1Capacity` _[Quantity](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#quantity-resource-api)_ | Bar1Capacity is the device BAR1 aperture size. |  | Optional: \{\} <br /> |
| `busWidthBits` _integer_ | BusWidthBits is the memory bus width in bits. |  | Optional: \{\} <br /> |


#### GPUModel



GPUModel is the vendor/product identity.



_Appears in:_
- [SGPUGPUs](#sgpugpus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `vendor` _string_ | Vendor is the GPU manufacturer name. |  | Optional: \{\} <br /> |
| `product` _string_ | Product is the vendor's product identifier. |  | Optional: \{\} <br /> |
| `productName` _string_ | ProductName is the human-readable product name. |  | Optional: \{\} <br /> |
| `architecture` _string_ | Architecture is the GPU architecture name. |  | Optional: \{\} <br /> |
| `computeCapability` _[ComputeCapability](#computecapability)_ | ComputeCapability is the CUDA compute capability version. |  | Optional: \{\} <br /> |
| `cores` _[GPUCores](#gpucores)_ | Cores contains programmable core counts. |  | Optional: \{\} <br /> |
| `board` _[GPUBoard](#gpuboard)_ | Board contains board-level identity. |  | Optional: \{\} <br /> |
| `firmware` _[GPUFirmware](#gpufirmware)_ | Firmware contains firmware and information-ROM versions. |  | Optional: \{\} <br /> |


#### GPUPCI



GPUPCI is PCI identity and link characteristics.



_Appears in:_
- [SGPUGPUs](#sgpugpus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `vendorID` _string_ | VendorID is the PCI vendor identifier in hexadecimal. |  | Optional: \{\} <br /> |
| `deviceID` _string_ | DeviceID is the PCI device identifier in hexadecimal. |  | Optional: \{\} <br /> |
| `subsystemVendorID` _string_ | SubsystemVendorID is the PCI subsystem vendor identifier. |  | Optional: \{\} <br /> |
| `subsystemDeviceID` _string_ | SubsystemDeviceID is the PCI subsystem device identifier. |  | Optional: \{\} <br /> |
| `maxLink` _[PCILink](#pcilink)_ | MaxLink is the maximum supported PCIe generation and width. |  | Optional: \{\} <br /> |


#### GPUPower



GPUPower is the power envelope.



_Appears in:_
- [SGPUGPUs](#sgpugpus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `managementSupported` _boolean_ | ManagementSupported indicates whether power management is available. |  | Optional: \{\} <br /> |
| `limitsMilliWatts` _[PowerLimits](#powerlimits)_ | LimitsMilliWatts gives the minimum, default, and maximum power limits. |  | Optional: \{\} <br /> |


#### GPUSlot



GPUSlot pins one GPU to a PCI address + NUMA/root-complex/host CPU.



_Appears in:_
- [SGPUTopology](#sgputopology)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `index` _integer_ | Index is the zero-based GPU index on the Node. |  | Maximum: 63 <br />Minimum: 0 <br /> |
| `pciAddress` _string_ | PCIAddress is the PCI bus address of this GPU. |  | Pattern: `^[0-9a-f]\{4\}:[0-9a-f]\{2\}:[0-9a-f]\{2\}\.[0-7]$` <br />Required: \{\} <br /> |
| `rootComplex` _string_ | RootComplex identifies the PCI root complex connected to this GPU. |  | Pattern: `^pci[0-9a-f]\{4\}:[0-9a-f]\{2\}$` <br />Required: \{\} <br /> |
| `numaNode` _integer_ | NumaNode is the host NUMA node associated with this GPU. |  | Optional: \{\} <br /> |
| `hostProcessorIndex` _integer_ | HostProcessorIndex is the host CPU index associated with this GPU. |  | Optional: \{\} <br /> |
| `parent` _string_ | Parent is the root port or NIC switch port the GPU sits below, from<br />any slot; empty when unknown. |  | Pattern: `^[0-9a-f]\{4\}:[0-9a-f]\{2\}:[0-9a-f]\{2\}\.[0-7]$` <br />Optional: \{\} <br /> |


#### GPUThermal



GPUThermal is thermal thresholds.



_Appears in:_
- [SGPUGPUs](#sgpugpus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `targetCelsius` _integer_ | TargetCelsius is the target GPU temperature in degrees Celsius. |  | Optional: \{\} <br /> |
| `maxOperatingCelsius` _integer_ | MaxOperatingCelsius is the maximum rated operating temperature. |  | Optional: \{\} <br /> |
| `slowdownThresholdCelsius` _integer_ | SlowdownThresholdCelsius is the temperature at which thermal slowdown begins. |  | Optional: \{\} <br /> |
| `shutdownThresholdCelsius` _integer_ | ShutdownThresholdCelsius is the temperature at which shutdown is triggered. |  | Optional: \{\} <br /> |


#### HostCPU



HostCPU is host CPU identity.



_Appears in:_
- [SGPUHost](#sgpuhost)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `vendor` _string_ | Vendor is the processor manufacturer. |  | Optional: \{\} <br /> |
| `product` _string_ | Product is the processor product identifier. |  | Optional: \{\} <br /> |
| `architecture` _string_ | Architecture is the host processor architecture. |  | Optional: \{\} <br /> |
| `cores` _integer_ | Cores is the number of host CPU cores. |  | Optional: \{\} <br /> |


#### HostMemory



HostMemory is host memory capacity.



_Appears in:_
- [SGPUHost](#sgpuhost)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `capacity` _[Quantity](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#quantity-resource-api)_ | Capacity is the host memory capacity. |  |  |
| `coherentWithGPU` _boolean_ | CoherentWithGPU indicates whether host and GPU memory are coherent. |  | Optional: \{\} <br /> |


#### InfoROM



InfoROM is NVML inforom sub-object versions.



_Appears in:_
- [GPUFirmware](#gpufirmware)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `imageVersion` _string_ | ImageVersion is the information-ROM image version. |  | Optional: \{\} <br /> |
| `oemObjectVersion` _string_ | OEMObjectVersion is the OEM information-ROM object version. |  | Optional: \{\} <br /> |
| `eccObjectVersion` _string_ | ECCObjectVersion is the ECC information-ROM object version. |  | Optional: \{\} <br /> |
| `powerObjectVersion` _string_ | PowerObjectVersion is the power information-ROM object version. |  | Optional: \{\} <br /> |


#### InventoryCapacity



InventoryCapacity is realized rack/node/GPU counts.



_Appears in:_
- [RackGroupStatus](#rackgroupstatus)
- [SGPUInventoryStatus](#sgpuinventorystatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `racks` _integer_ | Racks is the number of materialized racks. |  |  |
| `nodes` _integer_ | Nodes is the number of logical Nodes across those racks. |  |  |
| `gpus` _integer_ | GPUs is the number of simulated GPUs across those Nodes. |  |  |


#### InventoryUsage



InventoryUsage summarizes eligible placement Nodes, bound rack slots, and pending allocations.



_Appears in:_
- [RackGroupStatus](#rackgroupstatus)
- [SGPUInventoryStatus](#sgpuinventorystatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `requestedNodes` _integer_ | RequestedNodes counts eligible Nodes matching placement selectors. Inventory totals<br />deduplicate matches across groups; per-group values count each group's matches. |  |  |
| `allocatedNodes` _integer_ | AllocatedNodes counts rack Node slots with a live matching Kubernetes Node binding. |  |  |
| `availableNodes` _integer_ | AvailableNodes is remaining capacity after bound rack slots, never below zero. |  |  |
| `pendingNodes` _integer_ | PendingNodes counts eligible placement requests awaiting capacity in one matching group. |  |  |


#### LaneSpeed

_Underlying type:_ _string_

LaneSpeed is the per-lane signalling generation of a port.

_Validation:_
- Enum: [SDR DDR QDR FDR10 FDR EDR HDR NDR XDR]

_Appears in:_
- [NICPort](#nicport)



#### MIGCapability



MIGCapability is MIG partitioning support.



_Appears in:_
- [GPUCapabilities](#gpucapabilities)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `supported` _boolean_ | Supported indicates whether MIG is available on the GPU. |  | Optional: \{\} <br /> |
| `maxGPUInstances` _integer_ | MaxGPUInstances is the maximum number of GPU instances supported. |  | Optional: \{\} <br /> |


#### NICConfiguration



NICConfiguration is one firmware configuration, declared as its result: its
ports, the PCI functions it produces and the RDMA devices the driver
creates on them.



_Appears in:_
- [SNICProfileSpec](#snicprofilespec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `name` _string_ | Name is what slots and overrides select. |  | MinLength: 1 <br /> |
| `ports` _[NICPort](#nicport) array_ | Ports are the configuration's logical network ports, in physical order:<br />a port's position is its number, p0 first, and its identifiers and<br />names follow from that number. |  | Optional: \{\} <br /> |
| `functions` _[NICFunction](#nicfunction) array_ | Functions are the PCI functions the configuration produces. |  | MinItems: 1 <br /> |
| `rdmaDevices` _[NICRDMADevice](#nicrdmadevice) array_ | RDMADevices are the RDMA devices the driver creates. |  | Optional: \{\} <br /> |


#### NICDevicePlacement



NICDevicePlacement places one PCI device of a NIC. Function numbers come
from the configuration, so switching configuration never moves a device.



_Appears in:_
- [NICSlot](#nicslot)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `rootComplex` _string_ | RootComplex is the root complex, such as pci0009:00. A GPU slot naming<br />the same root complex shares it. |  | Pattern: `^pci[0-9a-f]\{4\}:[0-9a-f]\{2\}$` <br /> |
| `bridges` _[PCIBridge](#pcibridge) array_ | Bridges are the NIC's switch ports above or beside the device, parents<br />first. A GPU slot may name one as its parent. |  | Optional: \{\} <br /> |
| `address` _string_ | Address is the device's DDDD:BB:DD address. |  | Pattern: `^[0-9a-f]\{4\}:[0-9a-f]\{2\}:[0-9a-f]\{2\}$` <br /> |
| `parent` _string_ | Parent is a root port or a switch port; empty for a device on the root<br />bus. |  | Pattern: `^[0-9a-f]\{4\}:[0-9a-f]\{2\}:[0-9a-f]\{2\}\.[0-7]$` <br />Optional: \{\} <br /> |
| `numaNode` _integer_ | NUMANode is the device's NUMA node; -1 means unknown. |  | Minimum: -1 <br /> |
| `vpdPosition` _string_ | VPDPosition is the suffix of the VPD product name, such as -Prime or<br />-Aux[1]. |  | Optional: \{\} <br /> |


#### NICFunction



NICFunction is one PCI function a configuration produces.



_Appears in:_
- [NICConfiguration](#nicconfiguration)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `id` _string_ | ID names the function within the configuration. |  | MinLength: 1 <br /> |
| `type` _string_ | Type is a key of functionTypes. |  | MinLength: 1 <br /> |
| `pciDevice` _string_ | PCIDevice is the PCI device the function belongs to, which a slot<br />places, such as network or hostA. |  | MinLength: 1 <br /> |
| `function` _integer_ | Function is the PCI function number within the device. |  | Maximum: 7 <br />Minimum: 0 <br /> |
| `class` _string_ | Class is the PCI class code the configuration gives the function, such<br />as "0x020700". |  | Pattern: `^0x[0-9a-f]\{6\}$` <br /> |
| `ports` _string array_ | Ports are the ports the function owns. A port has one owner per PCI<br />device, so a NIC with several host links shows it on one function per<br />link. |  | Optional: \{\} <br /> |


#### NICFunctionType



NICFunctionType is the hardware identity and driver binding of one kind of
PCI function. The class isn't here: the configuration decides it.



_Appears in:_
- [SNICProfileSpec](#snicprofilespec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `vendorID` _string_ | VendorID is four lowercase hex digits, without 0x. |  | Pattern: `^[0-9a-f]\{4\}$` <br /> |
| `deviceID` _string_ | DeviceID is four lowercase hex digits, without 0x. |  | Pattern: `^[0-9a-f]\{4\}$` <br /> |
| `subsystemVendorID` _string_ | SubsystemVendorID is four lowercase hex digits; empty means 0000. |  | Pattern: `^[0-9a-f]\{4\}$` <br />Optional: \{\} <br /> |
| `subsystemDeviceID` _string_ | SubsystemDeviceID is four lowercase hex digits; empty means 0000. |  | Pattern: `^[0-9a-f]\{4\}$` <br />Optional: \{\} <br /> |
| `driver` _string_ | Driver is the bound kernel driver, such as mlx5_core. |  | MinLength: 1 <br /> |
| `rdmaNamePrefix` _string_ | RDMANamePrefix is the kernel's RDMA device name prefix, such as mlx5_. |  | Optional: \{\} <br /> |
| `rdmaNodeType` _string_ | RDMANodeType is the node_type the driver reports; empty means "1: CA". |  | Optional: \{\} <br /> |
| `rdmaOmit` _[RDMAAttribute](#rdmaattribute) array_ | RDMAOmit lists RDMA attributes the driver doesn't create: the file is<br />absent. |  | Enum: [fw_ver hca_type board_id hw_rev] <br />Optional: \{\} <br /> |
| `rdmaEmpty` _[RDMAAttribute](#rdmaattribute) array_ | RDMAEmpty lists RDMA attributes the driver creates with an empty value. |  | Enum: [fw_ver hca_type board_id hw_rev] <br />Optional: \{\} <br /> |
| `totalVFs` _integer_ | TotalVFs is sriov_totalvfs; 0 means no SR-IOV. Enabling VFs is cluster<br />software's job, so the declaration never says how many are enabled. |  | Minimum: 0 <br />Optional: \{\} <br /> |
| `vfType` _string_ | VFType names the function type of the VFs this function enables, a key<br />of functionTypes. It needs totalVFs. |  | Optional: \{\} <br /> |
| `revision` _string_ | Revision is the PCI revision ID, such as "0x00". |  | Pattern: `^0x[0-9a-f]\{2\}$` <br />Optional: \{\} <br /> |
| `pcieLink` _[NICPCIeLink](#nicpcielink)_ | PCIeLink is the link the function trains to. It is omitted when no<br />capture shows it, and then no link attributes are generated. |  | Optional: \{\} <br /> |
| `vpd` _boolean_ | VPD reports whether the function exposes the profile's VPD. |  | Optional: \{\} <br /> |
| `vpdSuffixes` _object (keys:string, values:string)_ | VPDSuffixes are appended to an identity keyword's value on this kind of<br />function. "\{function\}" stands for the PCI function number. |  | Optional: \{\} <br /> |
| `vpdOmit` _string array_ | VPDOmit lists the identity keywords this kind of function doesn't<br />report. |  | Optional: \{\} <br /> |


#### NICIdentity



NICIdentity is the model-level identity a NIC reports.



_Appears in:_
- [SNICProfileSpec](#snicprofilespec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `model` _string_ | Model is the model name, such as ConnectX-8. |  | MinLength: 1 <br /> |
| `hcaType` _string_ | HCAType is the RDMA hca_type, such as MT4131. |  | Optional: \{\} <br /> |
| `boardID` _string_ | BoardID is the RDMA board_id, such as MT_0000001513. |  | Optional: \{\} <br /> |
| `firmwareVersion` _string_ | FirmwareVersion is the RDMA fw_ver; a slot may override it. |  | Optional: \{\} <br /> |
| `partNumber` _string_ | PartNumber is the board part number and the default for VPD PN. |  | Optional: \{\} <br /> |
| `hwRev` _string_ | HWRev is the RDMA hw_rev and the ibv_devinfo hw_ver; empty means 0x0. |  | Pattern: `^0x[0-9a-fA-F]+$` <br />Optional: \{\} <br /> |
| `vendorOUI` _string_ | VendorOUI is the IEEE OUI verbs report as vendor_id, such as "0x02c9". |  | Pattern: `^0x[0-9a-f]+$` <br />Optional: \{\} <br /> |


#### NICOverride



NICOverride changes NIC slots of a rack profile for one rack group.



_Appears in:_
- [RackGroup](#rackgroup)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `slots` _string array_ | Slots are the names of the slots the override applies to. |  | MinItems: 1 <br /> |
| `profile` _string_ | Profile replaces the slot's profile. The slot's configuration, firmware<br />version, VPD and a systemImageGUID that names an RDMA device belong to<br />the old model, so they no longer apply; the new profile's own do, unless<br />this override sets them. A distinct systemImageGUID carries over. |  | Optional: \{\} <br /> |
| `configuration` _string_ | Configuration replaces the slot's configuration. |  | Optional: \{\} <br /> |
| `firmwareVersion` _string_ | FirmwareVersion replaces the slot's firmware version. |  | Optional: \{\} <br /> |
| `vpd` _object (keys:string, values:string)_ | VPD overrides model VPD keywords, on top of the slot's own overrides. |  | Optional: \{\} <br /> |
| `systemImageGUID` _string_ | SystemImageGUID replaces the slot's systemImageGUID: "distinct", or an<br />RDMA device of the selected configuration. |  | Optional: \{\} <br /> |


#### NICPCIeLink



NICPCIeLink is a PCIe link as sysfs reports it.



_Appears in:_
- [NICFunctionType](#nicfunctiontype)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `speed` _string_ | Speed is the link speed as sysfs reports it, such as "64.0 GT/s PCIe". |  | MinLength: 1 <br /> |
| `width` _integer_ | Width is the link width in lanes. |  | Minimum: 0 <br /> |
| `maxWidth` _integer_ | MaxWidth is set only when it differs from width, as on a VF reporting 0<br />of 255. |  | Minimum: 0 <br />Optional: \{\} <br /> |


#### NICPort



NICPort is one logical network port: what a fabric attaches to, not a
physical connector.



_Appears in:_
- [NICConfiguration](#nicconfiguration)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `id` _string_ | ID names the port within the configuration. |  | MinLength: 1 <br /> |
| `technology` _[NICTechnology](#nictechnology)_ | Technology is the port's link technology. |  | Enum: [ib roce efa] <br /> |
| `width` _integer_ | Width is the lane count in the rate text, such as 4 in<br />"800 Gb/sec (4X XDR)". |  | Minimum: 1 <br /> |
| `laneSpeed` _[LaneSpeed](#lanespeed)_ | LaneSpeed is the per-lane signalling generation. |  | Enum: [SDR DDR QDR FDR10 FDR EDR HDR NDR XDR] <br /> |
| `planes` _integer_ | Planes is the number of fabric planes the port carries; 0 is a single<br />endpoint. |  | Minimum: 0 <br /> |
| `capMask` _string_ | CapMask is the capability mask an ib or efa port reports, which its<br />firmware sets. A roce port follows a rule instead. |  | Pattern: `^0x[0-9a-f]\{8\}$` <br />Optional: \{\} <br /> |
| `lmc` _integer_ | LMC is the LID mask count; empty means 0. |  | Maximum: 7 <br />Minimum: 0 <br />Optional: \{\} <br /> |


#### NICRDMADevice



NICRDMADevice is an RDMA device the driver creates on a function.



_Appears in:_
- [NICConfiguration](#nicconfiguration)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `id` _string_ | ID names the RDMA device within the configuration. |  | MinLength: 1 <br /> |
| `function` _string_ | Function is the ID of the function the device is on. |  | MinLength: 1 <br /> |
| `ports` _string array_ | Ports are the device's ports in order: port k of the device is the k-th<br />entry. |  | MinItems: 1 <br /> |


#### NICSlot



NICSlot is one NIC instance on a Node. It names the profile and selects one
of its configurations; configuration data never lives in the slot.



_Appears in:_
- [SGPUTopology](#sgputopology)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `slot` _string_ | Slot names the instance, unique per Node, such as nic0 or frontend0. |  | MaxLength: 63 <br />MinLength: 1 <br /> |
| `profile` _string_ | Profile is the name of an SNICProfile. |  | MinLength: 1 <br /> |
| `configuration` _string_ | Configuration is one of the profile's configurations; empty means its<br />default. |  | Optional: \{\} <br /> |
| `board` _integer_ | Board groups the slots whose ASICs share one physical board. Board<br />numbers are Node-wide. |  | Minimum: 0 <br />Optional: \{\} <br /> |
| `systemImageGUID` _string_ | SystemImageGUID is the system image GUID of the NIC's RDMA devices:<br />empty for the node GUID of its lowest-addressed RDMA device, an RDMA<br />device ID for that device's node GUID, or "distinct" for a GUID that is<br />no device's node GUID. |  | Optional: \{\} <br /> |
| `gpus` _integer array_ | GPUs are the indexes of the GPUs the NIC serves. They record locality;<br />they don't restrict which GPUs may use the NIC. |  | items:Maximum: 63 <br />items:Minimum: 0 <br />Optional: \{\} <br /> |
| `firmwareVersion` _string_ | FirmwareVersion overrides the profile's identity.firmwareVersion. |  | Optional: \{\} <br /> |
| `vpd` _object (keys:string, values:string)_ | VPD overrides model VPD keywords, such as EC. |  | Optional: \{\} <br /> |
| `devices` _object (keys:string, values:[NICDevicePlacement](#nicdeviceplacement))_ | Devices places each PCI device the profile's configurations name. One<br />NIC's devices may sit under different root complexes. |  | MinProperties: 1 <br /> |


#### NICTechnology

_Underlying type:_ _string_

NICTechnology is the link technology of a port.

_Validation:_
- Enum: [ib roce efa]

_Appears in:_
- [NICPort](#nicport)

| Field | Description |
| --- | --- |
| `ib` | NICTechnologyIB is InfiniBand.<br /> |
| `roce` | NICTechnologyRoCE is RDMA over Converged Ethernet.<br /> |
| `efa` | NICTechnologyEFA is AWS Elastic Fabric Adapter.<br /> |


#### NICVPD



NICVPD is the vital product data of a NIC model.



_Appears in:_
- [SNICProfileSpec](#snicprofilespec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `keywords` _object (keys:string, values:string)_ | Keywords are model values by VPD keyword, such as PN, EC or V2. "name"<br />is the product name, to which a device's vpdPosition is appended. PN<br />defaults to identity.partNumber. |  | Optional: \{\} <br /> |
| `identities` _object (keys:string, values:[VPDScope](#vpdscope))_ | Identities are the keywords whose value is a generated identifier,<br />mapped to the scope that shares one value. |  | Optional: \{\} <br /> |


#### PCIBridge



PCIBridge is one exposed port of a NIC's PCIe switch.



_Appears in:_
- [NICDevicePlacement](#nicdeviceplacement)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `address` _string_ | Address is the bridge's address. |  | Pattern: `^[0-9a-f]\{4\}:[0-9a-f]\{2\}:[0-9a-f]\{2\}\.[0-7]$` <br /> |
| `parent` _string_ | Parent is a root port or another switch port. |  | Pattern: `^[0-9a-f]\{4\}:[0-9a-f]\{2\}:[0-9a-f]\{2\}\.[0-7]$` <br /> |


#### PCIID



PCIID is a PCI vendor and device ID pair.



_Appears in:_
- [RootPort](#rootport)
- [SNICProfileSpec](#snicprofilespec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `vendorID` _string_ | VendorID is four lowercase hex digits, without 0x. |  | Pattern: `^[0-9a-f]\{4\}$` <br /> |
| `deviceID` _string_ | DeviceID is four lowercase hex digits, without 0x. |  | Pattern: `^[0-9a-f]\{4\}$` <br /> |


#### PCILink



PCILink is the PCIe max link generation and width.



_Appears in:_
- [GPUPCI](#gpupci)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `generation` _integer_ | Generation is the maximum PCIe generation. |  | Optional: \{\} <br /> |
| `width` _integer_ | Width is the maximum PCIe link width in lanes. |  | Optional: \{\} <br /> |


#### PercentRange



PercentRange is a min/max percentage bound. Both bounds are required, so an
override replaces an inherited range whole.



_Appears in:_
- [UtilizationPattern](#utilizationpattern)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `minimum` _integer_ | Minimum is the lower utilization percentage bound. |  | Maximum: 100 <br />Minimum: 0 <br /> |
| `maximum` _integer_ | Maximum is the upper utilization percentage bound. |  | Maximum: 100 <br />Minimum: 0 <br /> |


#### PolicyTargetRef



PolicyTargetRef selects the simulated GPUs a policy applies to. Each listed
axis narrows the one above it, and an omitted axis selects every index. The
deepest listed axis is the policy's scope: inventory, rack group, rack, Node,
or GPU. A narrower scope overrides a broader one field by field. Every
listed rack group must exist in the inventory, and every listed index must
exist in at least one selected rack group.



_Appears in:_
- [SGPURuntimePolicySpec](#sgpuruntimepolicyspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `group` _string_ | Group is the API group containing the target resource. |  | Enum: [mokka.nvidia.com] <br /> |
| `kind` _string_ | Kind is the target resource kind. |  | Enum: [SGPUInventory] <br /> |
| `name` _string_ | Name is the name of the target SGPUInventory. |  | MinLength: 1 <br /> |
| `rackGroups` _string array_ | RackGroups optionally limits the policy to these rack-group IDs. |  | MaxItems: 64 <br />MinItems: 1 <br />Optional: \{\} <br /> |
| `rackIndexes` _integer array_ | RackIndexes optionally limits the policy to these rack indexes. |  | MaxItems: 64 <br />MinItems: 1 <br />items:Minimum: 0 <br />Optional: \{\} <br /> |
| `nodeIndexes` _integer array_ | NodeIndexes optionally limits the policy to these logical Node indexes. |  | MaxItems: 64 <br />MinItems: 1 <br />items:Minimum: 0 <br />Optional: \{\} <br /> |
| `gpuIndexes` _integer array_ | GPUIndexes optionally limits the policy to these GPU indexes. |  | MaxItems: 64 <br />MinItems: 1 <br />items:Minimum: 0 <br />Optional: \{\} <br /> |


#### PowerLimits



PowerLimits is min/default/max power caps in milliwatts.



_Appears in:_
- [GPUPower](#gpupower)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `minimum` _integer_ | Minimum is the minimum power limit in milliwatts. |  | Optional: \{\} <br /> |
| `default` _integer_ | Default is the default power limit in milliwatts. |  | Optional: \{\} <br /> |
| `maximum` _integer_ | Maximum is the maximum power limit in milliwatts. |  | Optional: \{\} <br /> |


#### PowerTelemetry



PowerTelemetry drives synthetic power draw.



_Appears in:_
- [RuntimeTelemetry](#runtimetelemetry)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `mode` _string_ | Mode selects a constant or generated power draw. |  | Enum: [Fixed Pattern] <br />Optional: \{\} <br /> |
| `drawMilliWatts` _integer_ | DrawMilliWatts is the simulated GPU power draw in milliwatts. |  | Minimum: 0 <br />Optional: \{\} <br /> |


#### ProfileReference



ProfileReference targets an SGPURackProfile by name.



_Appears in:_
- [RackGroup](#rackgroup)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `name` _string_ | Name is the name of the SGPURackProfile to use. |  | MinLength: 1 <br /> |


#### RDMAAttribute

_Underlying type:_ _string_

RDMAAttribute is an RDMA device attribute a driver may leave out or leave
empty.

_Validation:_
- Enum: [fw_ver hca_type board_id hw_rev]

_Appears in:_
- [NICFunctionType](#nicfunctiontype)



#### RDMASoftware



RDMASoftware is what the Node image decides about RDMA, beyond what the NIC
and its driver produce.



_Appears in:_
- [SGPUSoftware](#sgpusoftware)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `umad` _boolean_ | UMAD reports whether ib_umad loads: umad and issm files and the<br />infiniband_mad class exist. |  | Optional: \{\} <br /> |
| `ipoib` _boolean_ | IPoIB reports whether ib_ipoib loads: each InfiniBand port gets an IPoIB<br />netdev. |  | Optional: \{\} <br /> |
| `umadMode` _string_ | UMADMode is the udev file mode of umad and issm files, such as "666";<br />empty means the kernel's 600. |  | Pattern: `^[0-7]\{3,4\}$` <br />Optional: \{\} <br /> |
| `driverVersion` _string_ | DriverVersion is the version the image's NIC driver module reports,<br />such as 26.01-1.0.0 from a driver package. Empty means the module<br />reports none. |  | Optional: \{\} <br /> |


#### RackGroup



RackGroup is a homogeneous group of racks sharing a profile.



_Appears in:_
- [SGPUInventorySpec](#sgpuinventoryspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `id` _string_ | ID uniquely identifies this group within its inventory. |  | MaxLength: 63 <br />MinLength: 1 <br /> |
| `count` _integer_ | Count is the number of racks to materialize from this group. |  | Maximum: 100000 <br />Minimum: 1 <br /> |
| `profileRef` _[ProfileReference](#profilereference)_ | ProfileRef names the rack profile used to materialize each rack. |  |  |
| `placement` _[RackPlacement](#rackplacement)_ | Placement optionally restricts the Nodes eligible for this rack group. |  | Optional: \{\} <br /> |
| `nicOverrides` _[NICOverride](#nicoverride) array_ | NICOverrides replace the profile, configuration, firmware or VPD of NIC<br />slots for this group's racks, so groups sharing a rack profile can run<br />other NICs or modes. Overrides apply in order; a later one wins for a<br />field both set. |  | Optional: \{\} <br /> |


#### RackGroupStatus



RackGroupStatus is the per-group Capacity + Usage projection.



_Appears in:_
- [SGPUInventoryStatus](#sgpuinventorystatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `id` _string_ | ID identifies the rack group summarized by this status. |  |  |
| `profileName` _string_ | ProfileName is the name of the profile used to materialize the group. |  |  |
| `capacity` _[InventoryCapacity](#inventorycapacity)_ | Capacity is the realized capacity for this group. |  |  |
| `usage` _[InventoryUsage](#inventoryusage)_ | Usage summarizes Node allocation for this group. |  |  |


#### RackPlacement



RackPlacement constrains which nodes a rack group may materialize on.



_Appears in:_
- [RackGroup](#rackgroup)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `nodeSelector` _[LabelSelector](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#labelselector-v1-meta)_ | NodeSelector restricts placement to Nodes matching these labels. |  | Optional: \{\} <br /> |


#### RootPort



RootPort is one PCIe root port of the platform.



_Appears in:_
- [SGPUTopology](#sgputopology)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `address` _string_ | Address is the root port's address, such as "0009:00:00.0". |  | Pattern: `^[0-9a-f]\{4\}:[0-9a-f]\{2\}:[0-9a-f]\{2\}\.[0-7]$` <br /> |
| `rootComplex` _string_ | RootComplex is the root complex the port belongs to, such as pci0009:00. |  | Pattern: `^pci[0-9a-f]\{4\}:[0-9a-f]\{2\}$` <br /> |
| `id` _[PCIID](#pciid)_ | ID is the root port's PCI IDs. The IDs above a device are generated<br />only where they are known. |  | Optional: \{\} <br /> |


#### RuntimeModes



RuntimeModes is the persistent NVML/CUDA mode settings.



_Appears in:_
- [RuntimeState](#runtimestate)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `persistence` _string_ | Persistence controls the simulated persistence mode. |  | Enum: [Enabled Disabled] <br />Optional: \{\} <br /> |
| `compute` _string_ | Compute selects the simulated compute mode. |  | Enum: [Default Exclusive Prohibited] <br />Optional: \{\} <br /> |
| `mig` _string_ | MIG controls whether Multi-Instance GPU mode is enabled. |  | Enum: [Enabled Disabled] <br />Optional: \{\} <br /> |
| `ecc` _string_ | ECC controls whether error-correcting code is enabled. |  | Enum: [Enabled Disabled] <br />Optional: \{\} <br /> |
| `accounting` _string_ | Accounting controls whether GPU accounting mode is enabled. |  | Enum: [Enabled Disabled] <br />Optional: \{\} <br /> |


#### RuntimeState



RuntimeState is the effective runtime settings of a simulated GPU.
Sparse: omitted fields inherit; explicit zero is a set value.



_Appears in:_
- [SGPURackProfileDefaults](#sgpurackprofiledefaults)
- [SGPURuntimePolicySpec](#sgpuruntimepolicyspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `deviceState` _[DeviceState](#devicestate)_ | DeviceState is the simulated health state of the GPU. |  | Enum: [Healthy Degraded Failed] <br />Optional: \{\} <br /> |
| `modes` _[RuntimeModes](#runtimemodes)_ | Modes contains persistent NVML/CUDA mode settings. |  | Optional: \{\} <br /> |
| `telemetry` _[RuntimeTelemetry](#runtimetelemetry)_ | Telemetry contains synthetic utilization, power, temperature, and clock values. |  | Optional: \{\} <br /> |


#### RuntimeTelemetry



RuntimeTelemetry is the synthetic NVML telemetry.



_Appears in:_
- [RuntimeState](#runtimestate)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `performanceState` _string_ | NVML P-state, e.g. "P0". |  | Pattern: `^P[0-9]+$` <br />Optional: \{\} <br /> |
| `utilization` _[UtilizationTelemetry](#utilizationtelemetry)_ | Utilization defines synthetic GPU and memory utilization values. |  | Optional: \{\} <br /> |
| `power` _[PowerTelemetry](#powertelemetry)_ | Power defines synthetic power draw values. |  | Optional: \{\} <br /> |
| `temperature` _[TemperatureTelemetry](#temperaturetelemetry)_ | Temperature defines synthetic GPU and memory temperature values. |  | Optional: \{\} <br /> |
| `clocks` _[ClocksTelemetry](#clockstelemetry)_ | Clocks defines the current simulated clock rates. |  | Optional: \{\} <br /> |


#### SGPUGPUs



SGPUGPUs is the shared template for every GPU on the node.



_Appears in:_
- [SGPUNode](#sgpunode)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `count` _integer_ | Count is the number of GPUs exposed on each Node. |  | Maximum: 64 <br />Minimum: 1 <br /> |
| `model` _[GPUModel](#gpumodel)_ | Model describes the GPU vendor, product, and architecture. |  |  |
| `memory` _[GPUMemory](#gpumemory)_ | Memory describes device memory capacity and bus width. |  |  |
| `pci` _[GPUPCI](#gpupci)_ | PCI describes device identifiers and maximum link properties. |  |  |
| `power` _[GPUPower](#gpupower)_ | Power describes power-management support and device limits. |  | Optional: \{\} <br /> |
| `thermal` _[GPUThermal](#gputhermal)_ | Thermal describes device temperature thresholds. |  | Optional: \{\} <br /> |
| `clocks` _[GPUClocks](#gpuclocks)_ | Clocks describes maximum and supported device clock rates. |  | Optional: \{\} <br /> |
| `capabilities` _[GPUCapabilities](#gpucapabilities)_ | Capabilities describes MIG support and additional named capabilities. |  | Optional: \{\} <br /> |


#### SGPUHost



SGPUHost is host CPU + memory.



_Appears in:_
- [SGPUNode](#sgpunode)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `cpu` _[HostCPU](#hostcpu)_ | CPU describes the host processor presented by the simulated Node. |  | Optional: \{\} <br /> |
| `memory` _[HostMemory](#hostmemory)_ | Memory describes host memory presented by the simulated Node. |  | Optional: \{\} <br /> |


#### SGPUInventory



SGPUInventory is a set of simulated GPU racks to distribute across CPU nodes.





| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `mokka.nvidia.com/v1alpha1` | | |
| `kind` _string_ | `SGPUInventory` | | |
| `metadata` _[ObjectMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#objectmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `spec` _[SGPUInventorySpec](#sgpuinventoryspec)_ | Spec defines the simulated rack groups managed by this inventory. |  |  |
| `status` _[SGPUInventoryStatus](#sgpuinventorystatus)_ | Status reports the capacity and usage currently realized by the controller. |  | Optional: \{\} <br /> |


#### SGPUInventorySpec



SGPUInventorySpec is the desired rack composition.



_Appears in:_
- [SGPUInventory](#sgpuinventory)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `rackGroups` _[RackGroup](#rackgroup) array_ | RackGroups declares homogeneous sets of racks. One group can expand to<br />many racks; the controller admits at most 64 groups across all Inventories. |  | MaxItems: 64 <br />MinItems: 1 <br /> |


#### SGPUInventoryStatus



SGPUInventoryStatus is the Control Plane view of realized capacity and usage.



_Appears in:_
- [SGPUInventory](#sgpuinventory)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `rackGroupsSummary` _string_ | RackGroupsSummary is a comma-joined rendering of rack-group IDs. |  | Optional: \{\} <br /> |
| `capacity` _[InventoryCapacity](#inventorycapacity)_ | Capacity is the total rack, Node, and GPU capacity realized by this inventory. |  | Optional: \{\} <br /> |
| `usage` _[InventoryUsage](#inventoryusage)_ | Usage summarizes requested and allocated Nodes across the inventory. |  | Optional: \{\} <br /> |
| `rackGroups` _[RackGroupStatus](#rackgroupstatus) array_ | RackGroups contains per-group capacity and usage summaries. |  | Optional: \{\} <br /> |
| `conditions` _[Condition](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#condition-v1-meta) array_ | Conditions report the latest inventory acceptance and programming results. |  | Optional: \{\} <br /> |


#### SGPUNode



SGPUNode is a homogeneous logical node in the rack.



_Appears in:_
- [SGPURackProfileSpec](#sgpurackprofilespec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `gpus` _[SGPUGPUs](#sgpugpus)_ | GPUs defines the homogeneous simulated GPU set on each Node. |  |  |
| `host` _[SGPUHost](#sgpuhost)_ | Host describes the CPU and memory presented by each Node. |  | Optional: \{\} <br /> |
| `topology` _[SGPUTopology](#sgputopology)_ | Topology describes PCIe slots and optional GPU/network fabrics. |  | Required: \{\} <br /> |


#### SGPUNodeReference



SGPUNodeReference identifies an exact Kubernetes Node instance.



_Appears in:_
- [SGPURackNode](#sgpuracknode)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `name` _string_ | Name is the Kubernetes Node name. |  | MinLength: 1 <br /> |
| `uid` _[UID](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#uid-types-pkg)_ | UID distinguishes this Node instance from a replacement with the same name. |  |  |


#### SGPURack



SGPURack is the controller-owned materialization of one inventory rack.
Its controller owner reference identifies the inventory pinned by
spec.inventoryRef.





| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `mokka.nvidia.com/v1alpha1` | | |
| `kind` _string_ | `SGPURack` | | |
| `metadata` _[ObjectMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#objectmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `spec` _[SGPURackSpec](#sgpurackspec)_ | Spec is the rendered rack definition and its logical Node bindings. |  |  |
| `status` _[SGPURackStatus](#sgpurackstatus)_ | Status summarizes the rack's durable Node assignments. |  | Optional: \{\} <br /> |


#### SGPURackGPU



SGPURackGPU contains deterministic identity and structural placement.



_Appears in:_
- [SGPURackNode](#sgpuracknode)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `index` _integer_ | Index is the zero-based GPU index within the logical Node. |  | Maximum: 63 <br />Minimum: 0 <br /> |
| `uuid` _string_ | UUID is the stable GPU identifier. |  | Pattern: `^GPU-[0-9a-f]\{8\}-[0-9a-f]\{4\}-[0-9a-f]\{4\}-[0-9a-f]\{4\}-[0-9a-f]\{12\}$` <br /> |
| `serial` _string_ | Serial is the GPU serial number. |  | MinLength: 1 <br /> |
| `minorNumber` _integer_ | MinorNumber is the device minor number exposed to workloads. |  | Minimum: 0 <br /> |
| `pciAddress` _string_ | PCIAddress is the GPU's PCI bus address. |  | Pattern: `^[0-9a-f]\{4\}:[0-9a-f]\{2\}:[0-9a-f]\{2\}\.[0-7]$` <br /> |
| `rootComplex` _string_ | RootComplex identifies the PCI root complex connected to the GPU. |  | Pattern: `^pci[0-9a-f]\{4\}:[0-9a-f]\{2\}$` <br /> |
| `numaNode` _integer_ | NUMANode is the host NUMA node associated with the GPU. |  | Minimum: 0 <br /> |
| `hostProcessorIndex` _integer_ | HostProcessorIndex is the host CPU index associated with the GPU. |  | Minimum: 0 <br /> |


#### SGPURackIdentity



SGPURackIdentity contains deterministic rack coordinates and fabric identity.



_Appears in:_
- [SGPURackSpec](#sgpurackspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `rackGroup` _string_ | RackGroup is the rack-group identifier within the source inventory. |  | Format: dns1123Label <br />MaxLength: 63 <br />MinLength: 1 <br /> |
| `rackIndex` _integer_ | RackIndex is the zero-based rack index within its group. |  | Minimum: 0 <br /> |
| `fabricUUID` _string_ | FabricUUID identifies the fabric domain associated with the rack. |  | Format: uuid <br /> |
| `cliqueID` _integer_ | CliqueID is reserved for future multi-clique layouts and remains zero. |  | Maximum: 0 <br />Minimum: 0 <br /> |


#### SGPURackInventoryReference



SGPURackInventoryReference pins a rack to an exact inventory instance.



_Appears in:_
- [SGPURackSpec](#sgpurackspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `name` _string_ | Name is the inventory name. |  | MinLength: 1 <br /> |
| `uid` _[UID](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#uid-types-pkg)_ | UID pins the reference to the exact inventory instance. |  |  |


#### SGPURackNode



SGPURackNode is one logical Node and its optional Kubernetes Node binding.



_Appears in:_
- [SGPURackSpec](#sgpurackspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `index` _integer_ | Index is the zero-based logical Node index within the rack. |  | Maximum: 1023 <br />Minimum: 0 <br /> |
| `nodeRef` _[SGPUNodeReference](#sgpunodereference)_ | NodeRef is absent while the logical Node is unbound. Its UID distinguishes<br />a replacement Kubernetes Node that reuses the same name. |  | Optional: \{\} <br /> |
| `gpus` _[SGPURackGPU](#sgpurackgpu) array_ | GPUs are rendered once for the logical Node and do not depend on its binding. |  | MaxItems: 64 <br />MinItems: 1 <br /> |


#### SGPURackProfile



SGPURackProfile is the static shape of a simulated GPU rack.





| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `mokka.nvidia.com/v1alpha1` | | |
| `kind` _string_ | `SGPURackProfile` | | |
| `metadata` _[ObjectMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#objectmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `spec` _[SGPURackProfileSpec](#sgpurackprofilespec)_ | Spec defines the hardware shape and default software/runtime metadata. |  |  |


#### SGPURackProfileDefaults



SGPURackProfileDefaults is initial runtime state for fresh sGPUs.



_Appears in:_
- [SGPURackProfileSpec](#sgpurackprofilespec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `runtime` _[RuntimeState](#runtimestate)_ | Runtime provides initial sparse runtime settings for newly materialized GPUs. |  | Optional: \{\} <br /> |


#### SGPURackProfileReference



SGPURackProfileReference pins rendered data to an exact profile revision.



_Appears in:_
- [SGPURackSpec](#sgpurackspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `name` _string_ | Name is the profile name. |  | MinLength: 1 <br /> |
| `uid` _[UID](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#uid-types-pkg)_ | UID pins the reference to the exact profile instance. |  |  |
| `generation` _integer_ | Generation records the profile generation used for rendering. |  | Minimum: 1 <br /> |
| `revision` _string_ | Revision identifies the rendered profile content, independent of its name. |  | Pattern: `^[0-9a-f]\{64\}$` <br /> |


#### SGPURackProfileSpec



SGPURackProfileSpec is one logical rack profile.



_Appears in:_
- [SGPURackProfile](#sgpurackprofile)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `rack` _[SGPURackShape](#sgpurackshape)_ | Rack is the logical rack shape described by this profile. |  |  |
| `node` _[SGPUNode](#sgpunode)_ | Node describes the shared simulated hardware for each rack Node. |  |  |
| `software` _[SGPUSoftware](#sgpusoftware)_ | Software records optional driver, NVML, and CUDA versions. |  | Optional: \{\} <br /> |
| `defaults` _[SGPURackProfileDefaults](#sgpurackprofiledefaults)_ | Defaults provides initial runtime settings for newly materialized GPUs. |  | Optional: \{\} <br /> |


#### SGPURackShape



SGPURackShape defines the dimensions of a logical rack template. It is
nested profile data rather than a materialized SGPURack resource.



_Appears in:_
- [SGPURackProfileSpec](#sgpurackprofilespec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `nodesPerRack` _integer_ | NodesPerRack is the number of logical Nodes in each rack. |  | Maximum: 1024 <br />Minimum: 1 <br /> |


#### SGPURackSpec



SGPURackSpec is a rendered rack with durable logical Node bindings.



_Appears in:_
- [SGPURack](#sgpurack)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `inventoryRef` _[SGPURackInventoryReference](#sgpurackinventoryreference)_ | InventoryRef pins ownership to an exact inventory instance so a<br />same-name replacement cannot inherit this rack. |  |  |
| `profileRef` _[SGPURackProfileReference](#sgpurackprofilereference)_ | ProfileRef records the exact profile revision used to render the rack. |  |  |
| `identity` _[SGPURackIdentity](#sgpurackidentity)_ | Identity is the stable coordinate used by agents and topology consumers. |  |  |
| `nodes` _[SGPURackNode](#sgpuracknode) array_ | Nodes retain stable device identities while Kubernetes Node assignments<br />change. Fabric and network capabilities remain in the referenced profile. |  | MaxItems: 1024 <br />MinItems: 1 <br /> |


#### SGPURackStatus



SGPURackStatus summarizes durable logical Node assignments.



_Appears in:_
- [SGPURack](#sgpurack)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `observedGeneration` _integer_ | ObservedGeneration is the latest spec generation reflected by this status. |  | Minimum: 0 <br />Optional: \{\} <br /> |
| `assignedNodes` _integer_ | AssignedNodes is the number of logical Nodes with an exact Kubernetes<br />Node binding. |  | Minimum: 0 <br />Optional: \{\} <br /> |
| `conditions` _[Condition](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#condition-v1-meta) array_ | Conditions report whether the rendered assignment is ready for agents. |  | Optional: \{\} <br /> |


#### SGPURuntimePolicy



SGPURuntimePolicy applies a sparse RuntimeState override to a subset of
an SGPUInventory.





| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `mokka.nvidia.com/v1alpha1` | | |
| `kind` _string_ | `SGPURuntimePolicy` | | |
| `metadata` _[ObjectMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#objectmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `spec` _[SGPURuntimePolicySpec](#sgpuruntimepolicyspec)_ | Spec defines the target selection and runtime settings to apply. |  |  |
| `status` _[SGPURuntimePolicyStatus](#sgpuruntimepolicystatus)_ | Status summarizes the target scope selected for this policy. |  | Optional: \{\} <br /> |


#### SGPURuntimePolicySpec



SGPURuntimePolicySpec pairs a target selector with the RuntimeState to apply.



_Appears in:_
- [SGPURuntimePolicy](#sgpuruntimepolicy)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `targetRef` _[PolicyTargetRef](#policytargetref)_ | TargetRef selects the inventory and optional rack, Node, and GPU subset. |  |  |
| `runtime` _[RuntimeState](#runtimestate)_ | Runtime contains sparse settings to apply to the selected GPUs. |  | Optional: \{\} <br /> |


#### SGPURuntimePolicyStatus



SGPURuntimePolicyStatus reports whether the policy is accepted and holds
comma-joined denormalizations of spec.targetRef axes for print columns.



_Appears in:_
- [SGPURuntimePolicy](#sgpuruntimepolicy)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `rackGroupsSummary` _string_ | RackGroupsSummary is the comma-separated selected rack-group IDs. |  | Optional: \{\} <br /> |
| `rackIndexesSummary` _string_ | RackIndexesSummary is the comma-separated selected rack indexes. |  | Optional: \{\} <br /> |
| `nodeIndexesSummary` _string_ | NodeIndexesSummary is the comma-separated selected logical Node indexes. |  | Optional: \{\} <br /> |
| `gpuIndexesSummary` _string_ | GPUIndexesSummary is the comma-separated selected GPU indexes. |  | Optional: \{\} <br /> |
| `conditions` _[Condition](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#condition-v1-meta) array_ | Conditions report whether the policy is accepted. |  | Optional: \{\} <br /> |


#### SGPUSoftware



SGPUSoftware is driver/NVML/CUDA versions.



_Appears in:_
- [SGPURackProfileSpec](#sgpurackprofilespec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `driverVersion` _string_ | DriverVersion is the NVIDIA driver version. |  | Optional: \{\} <br /> |
| `nvmlVersion` _string_ | NVMLVersion is the NVIDIA Management Library version. |  | Optional: \{\} <br /> |
| `cudaVersion` _string_ | CUDAVersion is the CUDA version. |  | Optional: \{\} <br /> |
| `rdma` _[RDMASoftware](#rdmasoftware)_ | RDMA describes the Node image's RDMA stack: which optional modules it<br />loads and the version its NIC driver reports. |  | Optional: \{\} <br /> |


#### SGPUTopology



SGPUTopology is the PCIe slots, GPU fabric and NICs visible to the node.
GPUs and NICs share one PCIe tree: root ports are declared once, and every
GPU slot, NIC device and NIC switch port names its parent in that tree.



_Appears in:_
- [SGPUNode](#sgpunode)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `rootPorts` _[RootPort](#rootport) array_ | RootPorts are the platform's PCIe root ports, once per Node. |  | MaxItems: 256 <br />Optional: \{\} <br /> |
| `gpuSlots` _[GPUSlot](#gpuslot) array_ | GPUSlots maps each GPU index to its PCI and host topology coordinates. |  | MaxItems: 64 <br />MinItems: 1 <br /> |
| `gpuFabric` _[GPUFabric](#gpufabric)_ | GPUFabric describes optional NVLink/NVSwitch connectivity. |  | Optional: \{\} <br /> |
| `nics` _[NICSlot](#nicslot) array_ | NICs places one NIC instance per entry on the Node: an SNICProfile, the<br />configuration it runs and where each of its PCI devices sits. |  | MaxItems: 64 <br />Optional: \{\} <br /> |


#### SNICProfile



SNICProfile is one NIC model as packaged on a card, and every firmware
configuration it can run, each declared as its result. It holds model data
only: no addresses, identifiers or counts of a particular node. NIC slots in
SGPURackProfile place instances of it on a node.





| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `mokka.nvidia.com/v1alpha1` | | |
| `kind` _string_ | `SNICProfile` | | |
| `metadata` _[ObjectMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#objectmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `spec` _[SNICProfileSpec](#snicprofilespec)_ | Spec describes the NIC model and its configurations. |  |  |


#### SNICProfileSpec



SNICProfileSpec is the model data of an SNICProfile.



_Appears in:_
- [SNICProfile](#snicprofile)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `identity` _[NICIdentity](#nicidentity)_ | Identity is what the NIC reports about itself, the same in every<br />configuration. |  |  |
| `sources` _string array_ | Sources cites the captures or documents the values come from. |  | Optional: \{\} <br /> |
| `switchPort` _[PCIID](#pciid)_ | SwitchPort identifies the ports of the NIC's own PCIe switch, which slots<br />place as bridges. |  | Optional: \{\} <br /> |
| `vpd` _[NICVPD](#nicvpd)_ | VPD is the vital product data of the functions whose type has vpd set. |  | Optional: \{\} <br /> |
| `functionTypes` _object (keys:string, values:[NICFunctionType](#nicfunctiontype))_ | FunctionTypes are the kinds of PCI function the NIC can expose, keyed by<br />the name configurations use. |  | MinProperties: 1 <br /> |
| `defaultConfiguration` _string_ | DefaultConfiguration names the configuration used when neither the<br />cluster nor the slot selects one. |  | MinLength: 1 <br /> |
| `configurations` _[NICConfiguration](#nicconfiguration) array_ | Configurations are the firmware configurations the card can run, each<br />declared as its result. |  | MinItems: 1 <br /> |


#### SupportedClocks



SupportedClocks pairs a memory clock with supported graphics clocks.



_Appears in:_
- [GPUClocks](#gpuclocks)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `memoryMHz` _integer_ | MemoryMHz is a memory clock rate paired with GraphicsMHz values. |  |  |
| `graphicsMHz` _integer array_ | GraphicsMHz lists graphics clock rates supported at MemoryMHz. |  |  |


#### TemperatureTelemetry



TemperatureTelemetry drives synthetic GPU/memory temperature.



_Appears in:_
- [RuntimeTelemetry](#runtimetelemetry)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `mode` _string_ | Mode selects constant or generated temperature values. |  | Enum: [Fixed Pattern] <br />Optional: \{\} <br /> |
| `gpuCelsius` _integer_ | GPUCelsius is the simulated GPU temperature in degrees Celsius. |  | Optional: \{\} <br /> |
| `memoryCelsius` _integer_ | MemoryCelsius is the simulated memory temperature in degrees Celsius. |  | Optional: \{\} <br /> |


#### UtilizationPattern



UtilizationPattern shapes the generated utilization curve.



_Appears in:_
- [UtilizationTelemetry](#utilizationtelemetry)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `type` _string_ | Type selects the shape of the generated utilization curve. |  | Enum: [Steady Bursty Wave] <br />Optional: \{\} <br /> |
| `gpuPercent` _[PercentRange](#percentrange)_ | GPUPercent sets the minimum and maximum GPU utilization percentages. |  | Optional: \{\} <br /> |
| `memoryPercent` _[PercentRange](#percentrange)_ | MemoryPercent sets the minimum and maximum memory utilization percentages. |  | Optional: \{\} <br /> |


#### UtilizationTelemetry



UtilizationTelemetry drives synthetic GPU/memory utilization.



_Appears in:_
- [RuntimeTelemetry](#runtimetelemetry)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `mode` _string_ | Mode selects pattern-generated or fixed utilization values. |  | Enum: [Pattern Fixed] <br />Optional: \{\} <br /> |
| `pattern` _[UtilizationPattern](#utilizationpattern)_ | Pattern defines the utilization values generated over time. |  | Optional: \{\} <br /> |


#### VPDScope

_Underlying type:_ _string_

VPDScope names which NIC functions share one generated VPD value: board,
asic or boardPosition, or another identity keyword whose generated value
this one reuses before its function suffix, as a BlueField's VU starts with
its SN.

_Validation:_
- Pattern: `^(board|asic|boardPosition|[A-Z0-9]{2})$`

_Appears in:_
- [NICVPD](#nicvpd)

| Field | Description |
| --- | --- |
| `board` | VPDScopeBoard shares one value across every slot on the same board.<br /> |
| `asic` | VPDScopeASIC shares one value across the functions of one slot.<br /> |
| `boardPosition` | VPDScopeBoardPosition shares one value across the devices on one board<br />whose placement gives the same VPD position.<br /> |
