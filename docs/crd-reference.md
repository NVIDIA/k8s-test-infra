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



#### CapabilityAttribute



CapabilityAttribute is a typed variant; set exactly one field.



_Appears in:_
- [GPUCapabilities](#gpucapabilities)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `bool` _boolean_ | Bool is the boolean value when this attribute is boolean-valued. |  | Optional: \{\} <br /> |
| `int` _integer_ | Int is the integer value when this attribute is integer-valued. |  | Optional: \{\} <br /> |
| `string` _string_ | String is the string value when this attribute is string-valued. |  | Optional: \{\} <br /> |
| `strings` _string array_ | Strings contains the string values when this attribute is a set. |  | Optional: \{\} <br /> |


#### ClockRates



ClockRates is a graphics/SM/memory/video clock tuple in MHz.



_Appears in:_
- [GPUClocks](#gpuclocks)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `graphics` _integer_ | Graphics is the maximum graphics clock in MHz. |  | Optional: \{\} <br /> |
| `sm` _integer_ | SM is the maximum SM clock in MHz. |  | Optional: \{\} <br /> |
| `memory` _integer_ | Memory is the maximum memory clock in MHz. |  | Optional: \{\} <br /> |
| `video` _integer_ | Video is the maximum video clock in MHz. |  | Optional: \{\} <br /> |


#### ClocksTelemetry



ClocksTelemetry reports current clock rates.



_Appears in:_
- [RuntimeTelemetry](#runtimetelemetry)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `graphicsMHz` _integer_ | GraphicsMHz is the graphics clock in MHz. |  | Minimum: 0 <br />Optional: \{\} <br /> |
| `smMHz` _integer_ | SMMHz is the SM clock in MHz. |  | Minimum: 0 <br />Optional: \{\} <br /> |
| `memoryMHz` _integer_ | MemoryMHz is the memory clock in MHz. |  | Minimum: 0 <br />Optional: \{\} <br /> |
| `videoMHz` _integer_ | VideoMHz is the video clock in MHz. |  | Minimum: 0 <br />Optional: \{\} <br /> |


#### ComputeCapability



ComputeCapability is the CUDA compute capability.



_Appears in:_
- [GPUModel](#gpumodel)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `major` _integer_ | Major is the CUDA compute capability major version. |  |  |
| `minor` _integer_ | Minor is the CUDA compute capability minor version. |  |  |


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
| `scope` _string_ | Scope is the domain over which the fabric is shared. |  | Enum: [Node Rack Cluster] <br />Optional: \{\} <br /> |
| `gpuCount` _integer_ | GPUCount is the number of GPUs in the fabric domain. |  | Optional: \{\} <br /> |


#### FabricSwitches



FabricSwitches is fabric switches visible per node.



_Appears in:_
- [GPUFabric](#gpufabric)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `visiblePerNode` _integer_ | VisiblePerNode is the number of fabric switches visible per node. |  | Optional: \{\} <br /> |


#### GPUBoard



GPUBoard is board-level identity.



_Appears in:_
- [GPUModel](#gpumodel)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `partNumber` _string_ | PartNumber is the board part number. |  | Optional: \{\} <br /> |


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
| `maximumMHz` _[ClockRates](#clockrates)_ | MaximumMHz is the maximum clock tuple. |  | Optional: \{\} <br /> |
| `supported` _[SupportedClocks](#supportedclocks) array_ | Supported lists supported graphics clocks by memory clock. |  | Optional: \{\} <br /> |


#### GPUCores



GPUCores is programmable core counts.



_Appears in:_
- [GPUModel](#gpumodel)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `cuda` _integer_ | CUDA is the number of programmable CUDA cores. |  | Optional: \{\} <br /> |


#### GPUFabric



GPUFabric is NVLink/NVSwitch fabric.



_Appears in:_
- [SGPUTopology](#sgputopology)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `type` _string_ | Type identifies the fabric implementation. |  | Optional: \{\} <br /> |
| `generation` _integer_ | Generation is the fabric generation. |  | Optional: \{\} <br /> |
| `linksPerGPU` _integer_ | LinksPerGPU is the number of fabric links per GPU. |  | Optional: \{\} <br /> |
| `bandwidthPerLinkMBps` _integer_ | BandwidthPerLinkMBps is the bandwidth of each fabric link. |  | Optional: \{\} <br /> |
| `c2cSupported` _boolean_ | C2CSupported indicates whether chip-to-chip links are available. |  | Optional: \{\} <br /> |
| `domain` _[FabricDomain](#fabricdomain)_ | Domain identifies the fabric compute domain. |  | Optional: \{\} <br /> |
| `switches` _[FabricSwitches](#fabricswitches)_ | Switches describes fabric switches visible to the node. |  | Optional: \{\} <br /> |


#### GPUFirmware



GPUFirmware is firmware versions.



_Appears in:_
- [GPUModel](#gpumodel)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `vbiosVersion` _string_ | VBIOSVersion is the GPU VBIOS version. |  | Optional: \{\} <br /> |
| `gspVersion` _string_ | GSPVersion is the GPU System Processor firmware version. |  | Optional: \{\} <br /> |
| `infoROM` _[InfoROM](#inforom)_ | InfoROM contains InfoROM sub-object versions. |  | Optional: \{\} <br /> |


#### GPUMemory



GPUMemory is on-device memory.



_Appears in:_
- [SGPUGPUs](#sgpugpus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `capacity` _[Quantity](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#quantity-resource-api)_ | Capacity is the total on-device memory capacity. |  |  |
| `reserved` _[Quantity](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#quantity-resource-api)_ | Reserved is memory reserved for system use. |  | Optional: \{\} <br /> |
| `bar1Capacity` _[Quantity](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#quantity-resource-api)_ | Bar1Capacity is the BAR1 aperture capacity. |  | Optional: \{\} <br /> |
| `busWidthBits` _integer_ | BusWidthBits is the memory bus width in bits. |  | Optional: \{\} <br /> |


#### GPUModel



GPUModel is the vendor/product identity.



_Appears in:_
- [SGPUGPUs](#sgpugpus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `vendor` _string_ | Vendor is the GPU vendor name. |  | Optional: \{\} <br /> |
| `product` _string_ | Product is the short GPU product identifier. |  | Optional: \{\} <br /> |
| `productName` _string_ | ProductName is the human-readable GPU product name. |  | Optional: \{\} <br /> |
| `architecture` _string_ | Architecture is the GPU architecture name. |  | Optional: \{\} <br /> |
| `computeCapability` _[ComputeCapability](#computecapability)_ | ComputeCapability is the CUDA compute capability. |  | Optional: \{\} <br /> |
| `cores` _[GPUCores](#gpucores)_ | Cores contains programmable core counts. |  | Optional: \{\} <br /> |
| `board` _[GPUBoard](#gpuboard)_ | Board contains board-level identity. |  | Optional: \{\} <br /> |
| `firmware` _[GPUFirmware](#gpufirmware)_ | Firmware contains firmware and InfoROM versions. |  | Optional: \{\} <br /> |


#### GPUPCI



GPUPCI is PCI identity and link characteristics.



_Appears in:_
- [SGPUGPUs](#sgpugpus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `vendorID` _string_ | VendorID is the PCI vendor identifier. |  | Optional: \{\} <br /> |
| `deviceID` _string_ | DeviceID is the PCI device identifier. |  | Optional: \{\} <br /> |
| `subsystemVendorID` _string_ | SubsystemVendorID is the PCI subsystem vendor identifier. |  | Optional: \{\} <br /> |
| `subsystemDeviceID` _string_ | SubsystemDeviceID is the PCI subsystem device identifier. |  | Optional: \{\} <br /> |
| `maxLink` _[PCILink](#pcilink)_ | MaxLink is the maximum PCIe link configuration. |  | Optional: \{\} <br /> |


#### GPUPower



GPUPower is the power envelope.



_Appears in:_
- [SGPUGPUs](#sgpugpus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `managementSupported` _boolean_ | ManagementSupported indicates whether power management is available. |  | Optional: \{\} <br /> |
| `limitsMilliWatts` _[PowerLimits](#powerlimits)_ | LimitsMilliWatts contains the power cap range in milliwatts. |  | Optional: \{\} <br /> |


#### GPUSlot



GPUSlot pins one GPU to a PCI address + NUMA/root-complex/host CPU.



_Appears in:_
- [SGPUTopology](#sgputopology)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `index` _integer_ | Index is the logical GPU index for this slot. |  | Maximum: 63 <br />Minimum: 0 <br /> |
| `pciAddress` _string_ | PCIAddress is the GPU PCI address. |  | Pattern: `^[0-9a-f]\{4\}:[0-9a-f]\{2\}:[0-9a-f]\{2\}\.[0-7]$` <br />Required: \{\} <br /> |
| `rootComplex` _string_ |  |  | Pattern: `^pci[0-9a-f]\{4\}:[0-9a-f]\{2\}$` <br />Required: \{\} <br /> |
| `numaNode` _integer_ | NumaNode is the NUMA node associated with the slot. |  | Optional: \{\} <br /> |
| `hostProcessorIndex` _integer_ | HostProcessorIndex is the host processor associated with the slot. |  | Optional: \{\} <br /> |


#### GPUThermal



GPUThermal is thermal thresholds.



_Appears in:_
- [SGPUGPUs](#sgpugpus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `targetCelsius` _integer_ | TargetCelsius is the target operating temperature. |  | Optional: \{\} <br /> |
| `maxOperatingCelsius` _integer_ | MaxOperatingCelsius is the maximum operating temperature. |  | Optional: \{\} <br /> |
| `slowdownThresholdCelsius` _integer_ | SlowdownThresholdCelsius is the thermal slowdown threshold. |  | Optional: \{\} <br /> |
| `shutdownThresholdCelsius` _integer_ | ShutdownThresholdCelsius is the thermal shutdown threshold. |  | Optional: \{\} <br /> |


#### HostCPU



HostCPU is host CPU identity.



_Appears in:_
- [SGPUHost](#sgpuhost)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `vendor` _string_ | Vendor is the host CPU vendor. |  | Optional: \{\} <br /> |
| `product` _string_ | Product is the host CPU product name. |  | Optional: \{\} <br /> |
| `architecture` _string_ | Architecture is the host CPU architecture. |  | Optional: \{\} <br /> |
| `cores` _integer_ | Cores is the number of host CPU cores. |  | Optional: \{\} <br /> |


#### HostMemory



HostMemory is host memory capacity.



_Appears in:_
- [SGPUHost](#sgpuhost)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `capacity` _[Quantity](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#quantity-resource-api)_ | Capacity is the host memory capacity. |  |  |
| `coherentWithGPU` _boolean_ | CoherentWithGPU indicates whether host memory is coherent with GPU memory. |  | Optional: \{\} <br /> |


#### InfoROM



InfoROM is NVML inforom sub-object versions.



_Appears in:_
- [GPUFirmware](#gpufirmware)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `imageVersion` _string_ | ImageVersion is the InfoROM image version. |  | Optional: \{\} <br /> |
| `oemObjectVersion` _string_ | OEMObjectVersion is the InfoROM OEM object version. |  | Optional: \{\} <br /> |
| `eccObjectVersion` _string_ | ECCObjectVersion is the InfoROM ECC object version. |  | Optional: \{\} <br /> |
| `powerObjectVersion` _string_ | PowerObjectVersion is the InfoROM power object version. |  | Optional: \{\} <br /> |


#### InventoryCapacity



InventoryCapacity is realized rack/node/GPU counts.



_Appears in:_
- [RackGroupStatus](#rackgroupstatus)
- [SGPUInventoryStatus](#sgpuinventorystatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `racks` _integer_ | Racks is the number of realized racks. |  |  |
| `nodes` _integer_ | Nodes is the number of realized logical nodes. |  |  |
| `gpus` _integer_ | GPUs is the number of realized GPUs. |  |  |


#### InventoryUsage



InventoryUsage is node-level request/allocation counts.



_Appears in:_
- [RackGroupStatus](#rackgroupstatus)
- [SGPUInventoryStatus](#sgpuinventorystatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `requestedNodes` _integer_ | RequestedNodes is the number of nodes requested by consumers. |  |  |
| `allocatedNodes` _integer_ | AllocatedNodes is the number of nodes assigned to consumers. |  |  |
| `availableNodes` _integer_ | AvailableNodes is the number of currently available nodes. |  |  |
| `pendingNodes` _integer_ | PendingNodes is the number of requests awaiting allocation. |  |  |


#### MIGCapability



MIGCapability is MIG partitioning support.



_Appears in:_
- [GPUCapabilities](#gpucapabilities)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `supported` _boolean_ | Supported indicates whether MIG is available. |  | Optional: \{\} <br /> |
| `maxGPUInstances` _integer_ | MaxGPUInstances is the maximum number of GPU instances. |  | Optional: \{\} <br /> |


#### NetworkTopology



NetworkTopology is out-of-band adapters.



_Appears in:_
- [SGPUTopology](#sgputopology)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `type` _string_ | Type identifies the network fabric. |  | Optional: \{\} <br /> |
| `adapterModel` _string_ | AdapterModel is the network adapter model. |  | Optional: \{\} <br /> |
| `firmwareVersion` _string_ | FirmwareVersion is the network adapter firmware version. |  | Optional: \{\} <br /> |
| `linkSpeedGbps` _integer_ | LinkSpeedGbps is the network link speed in gigabits per second. |  | Optional: \{\} <br /> |
| `adaptersPerGPU` _integer_ | AdaptersPerGPU is the number of adapters associated with each GPU. |  | Optional: \{\} <br /> |


#### PCILink



PCILink is the PCIe max link generation and width.



_Appears in:_
- [GPUPCI](#gpupci)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `generation` _integer_ | Generation is the maximum PCIe generation. |  | Optional: \{\} <br /> |
| `width` _integer_ | Width is the maximum PCIe link width. |  | Optional: \{\} <br /> |


#### PercentRange



PercentRange is a min/max percentage bound.



_Appears in:_
- [UtilizationPattern](#utilizationpattern)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `minimum` _integer_ | Minimum is the lower percentage bound. |  | Maximum: 100 <br />Minimum: 0 <br /> |
| `maximum` _integer_ | Maximum is the upper percentage bound. |  | Maximum: 100 <br />Minimum: 0 <br /> |


#### PolicyTargetRef



PolicyTargetRef selects the fan-out scope. Each optional slice narrows
the level above.



_Appears in:_
- [SGPURuntimePolicySpec](#sgpuruntimepolicyspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `group` _string_ | Group is the API group of the selected inventory. |  | Enum: [mokka.nvidia.com] <br /> |
| `kind` _string_ | Kind is the resource kind being selected. |  | Enum: [SGPUInventory] <br /> |
| `name` _string_ | Name is the selected SGPUInventory resource name. |  | MinLength: 1 <br /> |
| `rackGroups` _string array_ | RackGroups limits the policy to these rack-group IDs. |  | MaxItems: 64 <br />MinItems: 1 <br />Optional: \{\} <br /> |
| `rackIndexes` _integer array_ | RackIndexes limits the policy to these rack indexes. |  | MaxItems: 64 <br />MinItems: 1 <br />Optional: \{\} <br /> |
| `nodeIndexes` _integer array_ | NodeIndexes limits the policy to these node indexes. |  | MaxItems: 64 <br />MinItems: 1 <br />Optional: \{\} <br /> |
| `gpuIndexes` _integer array_ | GPUIndexes limits the policy to these GPU indexes. |  | MaxItems: 64 <br />MinItems: 1 <br />Optional: \{\} <br /> |


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
| `mode` _string_ | Mode selects fixed or patterned power draw. |  | Enum: [Fixed Pattern] <br />Optional: \{\} <br /> |
| `drawMilliWatts` _integer_ | DrawMilliWatts is the synthetic power draw in milliwatts. |  | Minimum: 0 <br />Optional: \{\} <br /> |


#### ProfileReference



ProfileReference targets an SGPURackProfile by name.



_Appears in:_
- [RackGroup](#rackgroup)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `name` _string_ | Name is the SGPURackProfile resource name. |  | MinLength: 1 <br /> |


#### RackGroup



RackGroup is a homogeneous group of racks sharing a profile.



_Appears in:_
- [SGPUInventorySpec](#sgpuinventoryspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `id` _string_ | ID is the stable identifier for this rack group. |  | MaxLength: 63 <br />MinLength: 1 <br /> |
| `count` _integer_ | Count is the number of racks to materialize. |  | Maximum: 100000 <br />Minimum: 1 <br /> |
| `profileRef` _[ProfileReference](#profilereference)_ | ProfileRef selects the rack profile used for this group. |  |  |
| `placement` _[RackPlacement](#rackplacement)_ |  |  | Optional: \{\} <br /> |


#### RackGroupStatus



RackGroupStatus is the per-group Capacity + Usage projection.



_Appears in:_
- [SGPUInventoryStatus](#sgpuinventorystatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `id` _string_ | ID identifies the rack group. |  |  |
| `profileName` _string_ | ProfileName is the profile used by the group. |  |  |
| `capacity` _[InventoryCapacity](#inventorycapacity)_ | Capacity is the realized capacity for the group. |  |  |
| `usage` _[InventoryUsage](#inventoryusage)_ | Usage is the current allocation state for the group. |  |  |


#### RackPlacement



RackPlacement constrains which nodes a rack group may materialize on.



_Appears in:_
- [RackGroup](#rackgroup)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `nodeSelector` _[LabelSelector](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#labelselector-v1-meta)_ | NodeSelector limits placement to matching Kubernetes nodes. |  | Optional: \{\} <br /> |


#### RuntimeModes



RuntimeModes is the persistent NVML/CUDA mode settings.



_Appears in:_
- [RuntimeState](#runtimestate)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `persistence` _string_ | Persistence controls persistence mode. |  | Enum: [Enabled Disabled] <br />Optional: \{\} <br /> |
| `compute` _string_ | Compute controls compute mode. |  | Enum: [Default Exclusive Prohibited] <br />Optional: \{\} <br /> |
| `mig` _string_ | MIG controls MIG mode. |  | Enum: [Enabled Disabled] <br />Optional: \{\} <br /> |
| `ecc` _string_ | ECC controls ECC mode. |  | Enum: [Enabled Disabled] <br />Optional: \{\} <br /> |
| `accounting` _string_ | Accounting controls accounting mode. |  | Enum: [Enabled Disabled] <br />Optional: \{\} <br /> |


#### RuntimeState



RuntimeState is the effective runtime settings of a simulated GPU.
Sparse: omitted fields inherit; explicit zero is a set value.



_Appears in:_
- [SGPURackProfileDefaults](#sgpurackprofiledefaults)
- [SGPURuntimePolicySpec](#sgpuruntimepolicyspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `deviceState` _[DeviceState](#devicestate)_ | DeviceState is the simulated GPU health state. |  | Enum: [Healthy Degraded Failed] <br />Optional: \{\} <br /> |
| `modes` _[RuntimeModes](#runtimemodes)_ | Modes contains persistent NVML/CUDA mode settings. |  | Optional: \{\} <br /> |
| `telemetry` _[RuntimeTelemetry](#runtimetelemetry)_ | Telemetry contains synthetic NVML telemetry values. |  | Optional: \{\} <br /> |


#### RuntimeTelemetry



RuntimeTelemetry is the synthetic NVML telemetry.



_Appears in:_
- [RuntimeState](#runtimestate)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `performanceState` _string_ | NVML P-state, e.g. "P0".<br />PerformanceState is the NVML performance state, such as P0. |  | Pattern: `^P[0-9]+$` <br />Optional: \{\} <br /> |
| `utilization` _[UtilizationTelemetry](#utilizationtelemetry)_ |  |  | Optional: \{\} <br /> |
| `power` _[PowerTelemetry](#powertelemetry)_ |  |  | Optional: \{\} <br /> |
| `temperature` _[TemperatureTelemetry](#temperaturetelemetry)_ |  |  | Optional: \{\} <br /> |
| `clocks` _[ClocksTelemetry](#clockstelemetry)_ |  |  | Optional: \{\} <br /> |


#### SGPUGPUs



SGPUGPUs is the shared template for every GPU on the node.



_Appears in:_
- [SGPUNode](#sgpunode)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `count` _integer_ | Count is the number of GPUs on each node. |  | Maximum: 64 <br />Minimum: 1 <br /> |
| `model` _[GPUModel](#gpumodel)_ | Model describes the vendor and product identity. |  |  |
| `memory` _[GPUMemory](#gpumemory)_ | Memory describes capacity and bus characteristics. |  |  |
| `pci` _[GPUPCI](#gpupci)_ | PCI describes the simulated PCI identity and link. |  |  |
| `power` _[GPUPower](#gpupower)_ |  |  | Optional: \{\} <br /> |
| `thermal` _[GPUThermal](#gputhermal)_ |  |  | Optional: \{\} <br /> |
| `clocks` _[GPUClocks](#gpuclocks)_ |  |  | Optional: \{\} <br /> |
| `capabilities` _[GPUCapabilities](#gpucapabilities)_ |  |  | Optional: \{\} <br /> |


#### SGPUHost



SGPUHost is host CPU + memory.



_Appears in:_
- [SGPUNode](#sgpunode)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `cpu` _[HostCPU](#hostcpu)_ | CPU describes host processor identity and capacity. |  | Optional: \{\} <br /> |
| `memory` _[HostMemory](#hostmemory)_ | Memory describes host memory capacity and coherence. |  | Optional: \{\} <br /> |


#### SGPUInventory



SGPUInventory is a set of simulated GPU racks to distribute across CPU nodes.





| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `mokka.nvidia.com/v1alpha1` | | |
| `kind` _string_ | `SGPUInventory` | | |
| `metadata` _[ObjectMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#objectmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `spec` _[SGPUInventorySpec](#sgpuinventoryspec)_ | Spec describes the desired rack composition. |  |  |
| `status` _[SGPUInventoryStatus](#sgpuinventorystatus)_ | Status reports realized capacity and allocation state. |  | Optional: \{\} <br /> |


#### SGPUInventorySpec



SGPUInventorySpec is the desired rack composition.



_Appears in:_
- [SGPUInventory](#sgpuinventory)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `rackGroups` _[RackGroup](#rackgroup) array_ | RackGroups declares homogeneous sets of racks. One group can expand to<br />many racks; the controller admits at most 64 groups across all Inventories.<br />RackGroups declares the homogeneous rack groups in the inventory. |  | MaxItems: 64 <br />MinItems: 1 <br /> |


#### SGPUInventoryStatus



SGPUInventoryStatus is the Control Plane view of realized capacity and usage.



_Appears in:_
- [SGPUInventory](#sgpuinventory)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `rackGroupsSummary` _string_ | RackGroupsSummary is a comma-joined rendering of rack-group IDs. |  | Optional: \{\} <br /> |
| `capacity` _[InventoryCapacity](#inventorycapacity)_ | Capacity reports the realized rack, node, and GPU counts. |  | Optional: \{\} <br /> |
| `usage` _[InventoryUsage](#inventoryusage)_ | Usage reports requested, allocated, available, and pending nodes. |  | Optional: \{\} <br /> |
| `rackGroups` _[RackGroupStatus](#rackgroupstatus) array_ | RackGroups is the current per-group status projection. |  | Optional: \{\} <br /> |
| `conditions` _[Condition](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#condition-v1-meta) array_ | Conditions report inventory acceptance and programming state. |  | Optional: \{\} <br /> |


#### SGPUNode



SGPUNode is a homogeneous logical node in the rack.



_Appears in:_
- [SGPURackProfileSpec](#sgpurackprofilespec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `gpus` _[SGPUGPUs](#sgpugpus)_ | GPUs describes the GPU template repeated on each node. |  |  |
| `host` _[SGPUHost](#sgpuhost)_ | Host describes the simulated host CPU and memory. |  | Optional: \{\} <br /> |
| `topology` _[SGPUTopology](#sgputopology)_ | Topology describes PCIe, fabric, and network placement. |  | Required: \{\} <br /> |


#### SGPUNodeReference



SGPUNodeReference identifies an exact Kubernetes Node instance.



_Appears in:_
- [SGPURackNode](#sgpuracknode)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `name` _string_ | Name is the Kubernetes Node name. |  | MinLength: 1 <br /> |
| `uid` _[UID](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#uid-types-pkg)_ | UID identifies the exact Kubernetes Node instance. |  |  |


#### SGPURack



SGPURack is the controller-owned materialization of one inventory rack.
Its controller owner reference identifies the inventory pinned by
spec.inventoryRef.





| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `mokka.nvidia.com/v1alpha1` | | |
| `kind` _string_ | `SGPURack` | | |
| `metadata` _[ObjectMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#objectmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `spec` _[SGPURackSpec](#sgpurackspec)_ | Spec is the controller-owned rendered rack specification. |  |  |
| `status` _[SGPURackStatus](#sgpurackstatus)_ | Status reports node binding and readiness state. |  | Optional: \{\} <br /> |


#### SGPURackGPU



SGPURackGPU contains deterministic identity and structural placement.



_Appears in:_
- [SGPURackNode](#sgpuracknode)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `index` _integer_ | Index is the logical GPU index within the node. |  | Maximum: 63 <br />Minimum: 0 <br /> |
| `uuid` _string_ | UUID is the stable GPU identifier. |  | Pattern: `^GPU-[0-9a-f]\{8\}-[0-9a-f]\{4\}-[0-9a-f]\{4\}-[0-9a-f]\{4\}-[0-9a-f]\{12\}$` <br /> |
| `serial` _string_ | Serial is the simulated GPU serial number. |  | MinLength: 1 <br /> |
| `minorNumber` _integer_ | MinorNumber is the simulated device minor number. |  | Minimum: 0 <br /> |
| `pciAddress` _string_ |  |  | Pattern: `^[0-9a-f]\{4\}:[0-9a-f]\{2\}:[0-9a-f]\{2\}\.[0-7]$` <br /> |
| `rootComplex` _string_ | RootComplex identifies the PCI root complex. |  | Pattern: `^pci[0-9a-f]\{4\}:[0-9a-f]\{2\}$` <br /> |
| `numaNode` _integer_ | NUMANode is the host NUMA node containing the GPU. |  | Minimum: 0 <br /> |
| `hostProcessorIndex` _integer_ | HostProcessorIndex is the host processor index associated with the GPU. |  | Minimum: 0 <br /> |


#### SGPURackIdentity



SGPURackIdentity contains deterministic rack coordinates and fabric identity.



_Appears in:_
- [SGPURackSpec](#sgpurackspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `rackGroup` _string_ | RackGroup is the stable group identifier. |  | Format: dns1123Label <br />MaxLength: 63 <br />MinLength: 1 <br /> |
| `rackIndex` _integer_ | RackIndex is the zero-based index within the group. |  | Minimum: 0 <br /> |
| `fabricUUID` _string_ | FabricUUID identifies the fabric clique represented by this rack. |  | Format: uuid <br /> |
| `cliqueID` _integer_ | CliqueID remains zero while each rack represents one fabric clique.<br />CliqueID is reserved for future multi-clique racks and is currently zero. |  | Maximum: 0 <br />Minimum: 0 <br /> |


#### SGPURackInventoryReference



SGPURackInventoryReference pins a rack to an exact inventory instance.



_Appears in:_
- [SGPURackSpec](#sgpurackspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `name` _string_ | Name is the inventory resource name. |  | MinLength: 1 <br /> |
| `uid` _[UID](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#uid-types-pkg)_ | UID identifies the exact inventory instance. |  |  |


#### SGPURackNode



SGPURackNode is one logical Node and its optional Kubernetes Node binding.



_Appears in:_
- [SGPURackSpec](#sgpurackspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `index` _integer_ | Index is the logical node index within the rack. |  | Maximum: 1023 <br />Minimum: 0 <br /> |
| `nodeRef` _[SGPUNodeReference](#sgpunodereference)_ | NodeRef is absent while the logical Node is unbound. Its UID distinguishes<br />a replacement Kubernetes Node that reuses the same name. |  | Optional: \{\} <br /> |
| `gpus` _[SGPURackGPU](#sgpurackgpu) array_ | GPUs are rendered once for the logical Node and do not depend on its binding. |  | MaxItems: 64 <br />MinItems: 1 <br /> |


#### SGPURackProfile



SGPURackProfile is the static shape of a simulated GPU rack.





| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `mokka.nvidia.com/v1alpha1` | | |
| `kind` _string_ | `SGPURackProfile` | | |
| `metadata` _[ObjectMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#objectmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `spec` _[SGPURackProfileSpec](#sgpurackprofilespec)_ | Spec describes the static rack shape and GPU defaults. |  |  |


#### SGPURackProfileDefaults



SGPURackProfileDefaults is initial runtime state for fresh sGPUs.



_Appears in:_
- [SGPURackProfileSpec](#sgpurackprofilespec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `runtime` _[RuntimeState](#runtimestate)_ | Runtime contains initial runtime state for newly created GPUs. |  | Optional: \{\} <br /> |


#### SGPURackProfileReference



SGPURackProfileReference pins rendered data to an exact profile revision.



_Appears in:_
- [SGPURackSpec](#sgpurackspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `name` _string_ | Name is the profile resource name. |  | MinLength: 1 <br /> |
| `uid` _[UID](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#uid-types-pkg)_ | UID identifies the exact profile instance. |  |  |
| `generation` _integer_ | Generation is the profile resource generation used to render the rack. |  | Minimum: 1 <br /> |
| `revision` _string_ | Revision identifies the rendered profile content, independent of its name. |  | Pattern: `^[0-9a-f]\{64\}$` <br /> |


#### SGPURackProfileSpec



SGPURackProfileSpec is one logical rack profile.



_Appears in:_
- [SGPURackProfile](#sgpurackprofile)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `rack` _[SGPURackShape](#sgpurackshape)_ | Rack is the logical rack shape described by this profile. |  |  |
| `node` _[SGPUNode](#sgpunode)_ | Node is the homogeneous node template for the rack. |  |  |
| `software` _[SGPUSoftware](#sgpusoftware)_ | Software contains simulated driver and accelerator versions. |  | Optional: \{\} <br /> |
| `defaults` _[SGPURackProfileDefaults](#sgpurackprofiledefaults)_ | Defaults contains initial runtime values for newly created GPUs. |  | Optional: \{\} <br /> |


#### SGPURackShape



SGPURackShape defines the dimensions of a logical rack template. It is
nested profile data rather than a materialized SGPURack resource.



_Appears in:_
- [SGPURackProfileSpec](#sgpurackprofilespec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `nodesPerRack` _integer_ | NodesPerRack is the number of logical nodes in each rack. |  | Maximum: 1024 <br />Minimum: 1 <br /> |


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
| `conditions` _[Condition](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#condition-v1-meta) array_ | Conditions report whether the rendered assignment is ready for agents.<br />Conditions report whether the rack assignment is ready. |  | Optional: \{\} <br /> |


#### SGPURuntimePolicy



SGPURuntimePolicy applies a sparse RuntimeState override to a subset of
an SGPUInventory.





| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `mokka.nvidia.com/v1alpha1` | | |
| `kind` _string_ | `SGPURuntimePolicy` | | |
| `metadata` _[ObjectMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#objectmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `spec` _[SGPURuntimePolicySpec](#sgpuruntimepolicyspec)_ | Spec selects the inventory scope and runtime override. |  |  |
| `status` _[SGPURuntimePolicyStatus](#sgpuruntimepolicystatus)_ | Status contains denormalized target axes for print columns. |  | Optional: \{\} <br /> |


#### SGPURuntimePolicySpec



SGPURuntimePolicySpec pairs a target selector with the RuntimeState to apply.



_Appears in:_
- [SGPURuntimePolicy](#sgpuruntimepolicy)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `targetRef` _[PolicyTargetRef](#policytargetref)_ | TargetRef identifies the inventory and optional rack, node, and GPU axes. |  |  |
| `runtime` _[RuntimeState](#runtimestate)_ | Runtime is the sparse runtime override to apply. |  | Optional: \{\} <br /> |


#### SGPURuntimePolicyStatus



SGPURuntimePolicyStatus holds comma-joined denormalizations of
spec.targetRef axes for print columns.



_Appears in:_
- [SGPURuntimePolicy](#sgpuruntimepolicy)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `rackGroupsSummary` _string_ | RackGroupsSummary lists the selected rack groups. |  | Optional: \{\} <br /> |
| `rackIndexesSummary` _string_ | RackIndexesSummary lists the selected rack indexes. |  | Optional: \{\} <br /> |
| `nodeIndexesSummary` _string_ | NodeIndexesSummary lists the selected node indexes. |  | Optional: \{\} <br /> |
| `gpuIndexesSummary` _string_ | GPUIndexesSummary lists the selected GPU indexes. |  | Optional: \{\} <br /> |


#### SGPUSoftware



SGPUSoftware is driver/NVML/CUDA versions.



_Appears in:_
- [SGPURackProfileSpec](#sgpurackprofilespec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `driverVersion` _string_ | DriverVersion is the simulated NVIDIA driver version. |  | Optional: \{\} <br /> |
| `nvmlVersion` _string_ | NVMLVersion is the simulated NVML version. |  | Optional: \{\} <br /> |
| `cudaVersion` _string_ | CUDAVersion is the simulated CUDA version. |  | Optional: \{\} <br /> |


#### SGPUTopology



SGPUTopology is PCIe slots, GPU fabric, and network visible to the node.



_Appears in:_
- [SGPUNode](#sgpunode)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `gpuSlots` _[GPUSlot](#gpuslot) array_ | GPUSlots maps logical GPUs to PCI addresses on the node. |  | MaxItems: 64 <br />MinItems: 1 <br /> |
| `gpuFabric` _[GPUFabric](#gpufabric)_ | GPUFabric describes the node's simulated GPU fabric. |  | Optional: \{\} <br /> |
| `network` _[NetworkTopology](#networktopology)_ | Network describes the node's simulated network topology. |  | Optional: \{\} <br /> |


#### SupportedClocks



SupportedClocks pairs a memory clock with supported graphics clocks.



_Appears in:_
- [GPUClocks](#gpuclocks)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `memoryMHz` _integer_ | MemoryMHz is the memory clock for this supported set. |  |  |
| `graphicsMHz` _integer array_ | GraphicsMHz lists supported graphics clocks for MemoryMHz. |  |  |


#### TemperatureTelemetry



TemperatureTelemetry drives synthetic GPU/memory temperature.



_Appears in:_
- [RuntimeTelemetry](#runtimetelemetry)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `mode` _string_ | Mode selects fixed or patterned temperature. |  | Enum: [Fixed Pattern] <br />Optional: \{\} <br /> |
| `gpuCelsius` _integer_ | GPUCelsius is the GPU temperature in Celsius. |  | Optional: \{\} <br /> |
| `memoryCelsius` _integer_ | MemoryCelsius is the memory temperature in Celsius. |  | Optional: \{\} <br /> |


#### UtilizationPattern



UtilizationPattern shapes the generated utilization curve.



_Appears in:_
- [UtilizationTelemetry](#utilizationtelemetry)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `type` _string_ | Type selects the utilization curve shape. |  | Enum: [Steady Bursty Wave] <br />Optional: \{\} <br /> |
| `gpuPercent` _[PercentRange](#percentrange)_ | GPUPercent is the GPU utilization range. |  | Optional: \{\} <br /> |
| `memoryPercent` _[PercentRange](#percentrange)_ | MemoryPercent is the memory utilization range. |  | Optional: \{\} <br /> |


#### UtilizationTelemetry



UtilizationTelemetry drives synthetic GPU/memory utilization.



_Appears in:_
- [RuntimeTelemetry](#runtimetelemetry)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `mode` _string_ | Mode selects fixed or patterned utilization. |  | Enum: [Pattern Fixed] <br />Optional: \{\} <br /> |
| `pattern` _[UtilizationPattern](#utilizationpattern)_ | Pattern describes the utilization curve when Mode is Pattern. |  | Optional: \{\} <br /> |


