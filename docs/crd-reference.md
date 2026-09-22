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
| `bool` _boolean_ |  |  | Optional: \{\} <br /> |
| `int` _integer_ |  |  | Optional: \{\} <br /> |
| `string` _string_ |  |  | Optional: \{\} <br /> |
| `strings` _string array_ |  |  | Optional: \{\} <br /> |


#### ClockRates



ClockRates is a graphics/SM/memory/video clock tuple in MHz.



_Appears in:_
- [GPUClocks](#gpuclocks)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `graphics` _integer_ |  |  | Optional: \{\} <br /> |
| `sm` _integer_ |  |  | Optional: \{\} <br /> |
| `memory` _integer_ |  |  | Optional: \{\} <br /> |
| `video` _integer_ |  |  | Optional: \{\} <br /> |


#### ClocksTelemetry



ClocksTelemetry reports current clock rates.



_Appears in:_
- [RuntimeTelemetry](#runtimetelemetry)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `graphicsMHz` _integer_ |  |  | Minimum: 0 <br />Optional: \{\} <br /> |
| `smMHz` _integer_ |  |  | Minimum: 0 <br />Optional: \{\} <br /> |
| `memoryMHz` _integer_ |  |  | Minimum: 0 <br />Optional: \{\} <br /> |
| `videoMHz` _integer_ |  |  | Minimum: 0 <br />Optional: \{\} <br /> |


#### ComputeCapability



ComputeCapability is the CUDA compute capability.



_Appears in:_
- [GPUModel](#gpumodel)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `major` _integer_ |  |  |  |
| `minor` _integer_ |  |  |  |


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
| `scope` _string_ |  |  | Enum: [Node Rack Cluster] <br />Optional: \{\} <br /> |
| `gpuCount` _integer_ |  |  | Optional: \{\} <br /> |


#### FabricSwitches



FabricSwitches is fabric switches visible per node.



_Appears in:_
- [GPUFabric](#gpufabric)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `visiblePerNode` _integer_ |  |  | Optional: \{\} <br /> |


#### GPUBoard



GPUBoard is board-level identity.



_Appears in:_
- [GPUModel](#gpumodel)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `partNumber` _string_ |  |  | Optional: \{\} <br /> |


#### GPUCapabilities



GPUCapabilities is GPU feature toggles and extensible attributes.



_Appears in:_
- [SGPUGPUs](#sgpugpus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `mig` _[MIGCapability](#migcapability)_ |  |  | Optional: \{\} <br /> |
| `attributes` _object (keys:string, values:[CapabilityAttribute](#capabilityattribute))_ | Extensible capability flags keyed by qualified name. |  | Optional: \{\} <br /> |


#### GPUClocks



GPUClocks is clock ceilings and per-memory-clock schedules.



_Appears in:_
- [SGPUGPUs](#sgpugpus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `maximumMHz` _[ClockRates](#clockrates)_ |  |  | Optional: \{\} <br /> |
| `supported` _[SupportedClocks](#supportedclocks) array_ |  |  | Optional: \{\} <br /> |


#### GPUCores



GPUCores is programmable core counts.



_Appears in:_
- [GPUModel](#gpumodel)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `cuda` _integer_ |  |  | Optional: \{\} <br /> |


#### GPUFabric



GPUFabric is NVLink/NVSwitch fabric.



_Appears in:_
- [SGPUTopology](#sgputopology)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `type` _string_ |  |  | Optional: \{\} <br /> |
| `generation` _integer_ |  |  | Optional: \{\} <br /> |
| `linksPerGPU` _integer_ |  |  | Optional: \{\} <br /> |
| `bandwidthPerLinkMBps` _integer_ |  |  | Optional: \{\} <br /> |
| `c2cSupported` _boolean_ |  |  | Optional: \{\} <br /> |
| `domain` _[FabricDomain](#fabricdomain)_ |  |  | Optional: \{\} <br /> |
| `switches` _[FabricSwitches](#fabricswitches)_ |  |  | Optional: \{\} <br /> |


#### GPUFirmware



GPUFirmware is firmware versions.



_Appears in:_
- [GPUModel](#gpumodel)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `vbiosVersion` _string_ |  |  | Optional: \{\} <br /> |
| `gspVersion` _string_ |  |  | Optional: \{\} <br /> |
| `infoROM` _[InfoROM](#inforom)_ |  |  | Optional: \{\} <br /> |


#### GPUMemory



GPUMemory is on-device memory.



_Appears in:_
- [SGPUGPUs](#sgpugpus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `capacity` _[Quantity](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#quantity-resource-api)_ |  |  |  |
| `reserved` _[Quantity](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#quantity-resource-api)_ |  |  | Optional: \{\} <br /> |
| `bar1Capacity` _[Quantity](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#quantity-resource-api)_ |  |  | Optional: \{\} <br /> |
| `busWidthBits` _integer_ |  |  | Optional: \{\} <br /> |


#### GPUModel



GPUModel is the vendor/product identity.



_Appears in:_
- [SGPUGPUs](#sgpugpus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `vendor` _string_ |  |  | Optional: \{\} <br /> |
| `product` _string_ |  |  | Optional: \{\} <br /> |
| `productName` _string_ |  |  | Optional: \{\} <br /> |
| `architecture` _string_ |  |  | Optional: \{\} <br /> |
| `computeCapability` _[ComputeCapability](#computecapability)_ |  |  | Optional: \{\} <br /> |
| `cores` _[GPUCores](#gpucores)_ |  |  | Optional: \{\} <br /> |
| `board` _[GPUBoard](#gpuboard)_ |  |  | Optional: \{\} <br /> |
| `firmware` _[GPUFirmware](#gpufirmware)_ |  |  | Optional: \{\} <br /> |


#### GPUPCI



GPUPCI is PCI identity and link characteristics.



_Appears in:_
- [SGPUGPUs](#sgpugpus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `vendorID` _string_ |  |  | Optional: \{\} <br /> |
| `deviceID` _string_ |  |  | Optional: \{\} <br /> |
| `subsystemVendorID` _string_ |  |  | Optional: \{\} <br /> |
| `subsystemDeviceID` _string_ |  |  | Optional: \{\} <br /> |
| `maxLink` _[PCILink](#pcilink)_ |  |  | Optional: \{\} <br /> |


#### GPUPower



GPUPower is the power envelope.



_Appears in:_
- [SGPUGPUs](#sgpugpus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `managementSupported` _boolean_ |  |  | Optional: \{\} <br /> |
| `limitsMilliWatts` _[PowerLimits](#powerlimits)_ |  |  | Optional: \{\} <br /> |


#### GPUSlot



GPUSlot pins one GPU to a PCI address + NUMA/root-complex/host CPU.



_Appears in:_
- [SGPUTopology](#sgputopology)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `index` _integer_ |  |  | Maximum: 63 <br />Minimum: 0 <br /> |
| `pciAddress` _string_ |  |  | Pattern: `^[0-9a-f]\{4\}:[0-9a-f]\{2\}:[0-9a-f]\{2\}\.[0-7]$` <br />Required: \{\} <br /> |
| `rootComplex` _string_ |  |  | Pattern: `^pci[0-9a-f]\{4\}:[0-9a-f]\{2\}$` <br />Required: \{\} <br /> |
| `numaNode` _integer_ |  |  | Optional: \{\} <br /> |
| `hostProcessorIndex` _integer_ |  |  | Optional: \{\} <br /> |


#### GPUThermal



GPUThermal is thermal thresholds.



_Appears in:_
- [SGPUGPUs](#sgpugpus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `targetCelsius` _integer_ |  |  | Optional: \{\} <br /> |
| `maxOperatingCelsius` _integer_ |  |  | Optional: \{\} <br /> |
| `slowdownThresholdCelsius` _integer_ |  |  | Optional: \{\} <br /> |
| `shutdownThresholdCelsius` _integer_ |  |  | Optional: \{\} <br /> |


#### HostCPU



HostCPU is host CPU identity.



_Appears in:_
- [SGPUHost](#sgpuhost)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `vendor` _string_ |  |  | Optional: \{\} <br /> |
| `product` _string_ |  |  | Optional: \{\} <br /> |
| `architecture` _string_ |  |  | Optional: \{\} <br /> |
| `cores` _integer_ |  |  | Optional: \{\} <br /> |


#### HostMemory



HostMemory is host memory capacity.



_Appears in:_
- [SGPUHost](#sgpuhost)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `capacity` _[Quantity](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#quantity-resource-api)_ |  |  |  |
| `coherentWithGPU` _boolean_ |  |  | Optional: \{\} <br /> |


#### InfoROM



InfoROM is NVML inforom sub-object versions.



_Appears in:_
- [GPUFirmware](#gpufirmware)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `imageVersion` _string_ |  |  | Optional: \{\} <br /> |
| `oemObjectVersion` _string_ |  |  | Optional: \{\} <br /> |
| `eccObjectVersion` _string_ |  |  | Optional: \{\} <br /> |
| `powerObjectVersion` _string_ |  |  | Optional: \{\} <br /> |


#### InventoryCapacity



InventoryCapacity is realized rack/node/GPU counts.



_Appears in:_
- [RackGroupStatus](#rackgroupstatus)
- [SGPUInventoryStatus](#sgpuinventorystatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `racks` _integer_ |  |  |  |
| `nodes` _integer_ |  |  |  |
| `gpus` _integer_ |  |  |  |


#### InventoryUsage



InventoryUsage is node-level request/allocation counts.



_Appears in:_
- [RackGroupStatus](#rackgroupstatus)
- [SGPUInventoryStatus](#sgpuinventorystatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `requestedNodes` _integer_ |  |  |  |
| `allocatedNodes` _integer_ |  |  |  |
| `availableNodes` _integer_ |  |  |  |
| `pendingNodes` _integer_ |  |  |  |


#### MIGCapability



MIGCapability is MIG partitioning support.



_Appears in:_
- [GPUCapabilities](#gpucapabilities)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `supported` _boolean_ |  |  | Optional: \{\} <br /> |
| `maxGPUInstances` _integer_ |  |  | Optional: \{\} <br /> |


#### NetworkTopology



NetworkTopology is out-of-band adapters.



_Appears in:_
- [SGPUTopology](#sgputopology)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `type` _string_ |  |  | Optional: \{\} <br /> |
| `adapterModel` _string_ |  |  | Optional: \{\} <br /> |
| `firmwareVersion` _string_ |  |  | Optional: \{\} <br /> |
| `linkSpeedGbps` _integer_ |  |  | Optional: \{\} <br /> |
| `adaptersPerGPU` _integer_ |  |  | Optional: \{\} <br /> |


#### PCILink



PCILink is the PCIe max link generation and width.



_Appears in:_
- [GPUPCI](#gpupci)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `generation` _integer_ |  |  | Optional: \{\} <br /> |
| `width` _integer_ |  |  | Optional: \{\} <br /> |


#### PercentRange



PercentRange is a min/max percentage bound.



_Appears in:_
- [UtilizationPattern](#utilizationpattern)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `minimum` _integer_ |  |  | Maximum: 100 <br />Minimum: 0 <br /> |
| `maximum` _integer_ |  |  | Maximum: 100 <br />Minimum: 0 <br /> |


#### PolicyTargetRef



PolicyTargetRef selects the fan-out scope. Each optional slice narrows
the level above.



_Appears in:_
- [SGPURuntimePolicySpec](#sgpuruntimepolicyspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `group` _string_ |  |  | Enum: [mokka.nvidia.com] <br /> |
| `kind` _string_ |  |  | Enum: [SGPUInventory] <br /> |
| `name` _string_ |  |  | MinLength: 1 <br /> |
| `rackGroups` _string array_ |  |  | MaxItems: 64 <br />MinItems: 1 <br />Optional: \{\} <br /> |
| `rackIndexes` _integer array_ |  |  | MaxItems: 64 <br />MinItems: 1 <br />Optional: \{\} <br /> |
| `nodeIndexes` _integer array_ |  |  | MaxItems: 64 <br />MinItems: 1 <br />Optional: \{\} <br /> |
| `gpuIndexes` _integer array_ |  |  | MaxItems: 64 <br />MinItems: 1 <br />Optional: \{\} <br /> |


#### PowerLimits



PowerLimits is min/default/max power caps in milliwatts.



_Appears in:_
- [GPUPower](#gpupower)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `minimum` _integer_ |  |  | Optional: \{\} <br /> |
| `default` _integer_ |  |  | Optional: \{\} <br /> |
| `maximum` _integer_ |  |  | Optional: \{\} <br /> |


#### PowerTelemetry



PowerTelemetry drives synthetic power draw.



_Appears in:_
- [RuntimeTelemetry](#runtimetelemetry)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `mode` _string_ |  |  | Enum: [Fixed Pattern] <br />Optional: \{\} <br /> |
| `drawMilliWatts` _integer_ |  |  | Minimum: 0 <br />Optional: \{\} <br /> |


#### ProfileReference



ProfileReference targets an SGPURackProfile by name.



_Appears in:_
- [RackGroup](#rackgroup)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `name` _string_ |  |  | MinLength: 1 <br /> |


#### RackGroup



RackGroup is a homogeneous group of racks sharing a profile.



_Appears in:_
- [SGPUInventorySpec](#sgpuinventoryspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `id` _string_ |  |  | MaxLength: 63 <br />MinLength: 1 <br /> |
| `count` _integer_ |  |  | Maximum: 100000 <br />Minimum: 1 <br /> |
| `profileRef` _[ProfileReference](#profilereference)_ |  |  |  |
| `placement` _[RackPlacement](#rackplacement)_ |  |  | Optional: \{\} <br /> |


#### RackGroupStatus



RackGroupStatus is the per-group Capacity + Usage projection.



_Appears in:_
- [SGPUInventoryStatus](#sgpuinventorystatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `id` _string_ |  |  |  |
| `profileName` _string_ |  |  |  |
| `capacity` _[InventoryCapacity](#inventorycapacity)_ |  |  |  |
| `usage` _[InventoryUsage](#inventoryusage)_ |  |  |  |


#### RackPlacement



RackPlacement constrains which nodes a rack group may materialize on.



_Appears in:_
- [RackGroup](#rackgroup)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `nodeSelector` _[LabelSelector](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#labelselector-v1-meta)_ |  |  | Optional: \{\} <br /> |


#### RuntimeModes



RuntimeModes is the persistent NVML/CUDA mode settings.



_Appears in:_
- [RuntimeState](#runtimestate)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `persistence` _string_ |  |  | Enum: [Enabled Disabled] <br />Optional: \{\} <br /> |
| `compute` _string_ |  |  | Enum: [Default Exclusive Prohibited] <br />Optional: \{\} <br /> |
| `mig` _string_ |  |  | Enum: [Enabled Disabled] <br />Optional: \{\} <br /> |
| `ecc` _string_ |  |  | Enum: [Enabled Disabled] <br />Optional: \{\} <br /> |
| `accounting` _string_ |  |  | Enum: [Enabled Disabled] <br />Optional: \{\} <br /> |


#### RuntimeState



RuntimeState is the effective runtime settings of a simulated GPU.
Sparse: omitted fields inherit; explicit zero is a set value.



_Appears in:_
- [SGPURackProfileDefaults](#sgpurackprofiledefaults)
- [SGPURuntimePolicySpec](#sgpuruntimepolicyspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `deviceState` _[DeviceState](#devicestate)_ |  |  | Enum: [Healthy Degraded Failed] <br />Optional: \{\} <br /> |
| `modes` _[RuntimeModes](#runtimemodes)_ |  |  | Optional: \{\} <br /> |
| `telemetry` _[RuntimeTelemetry](#runtimetelemetry)_ |  |  | Optional: \{\} <br /> |


#### RuntimeTelemetry



RuntimeTelemetry is the synthetic NVML telemetry.



_Appears in:_
- [RuntimeState](#runtimestate)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `performanceState` _string_ | NVML P-state, e.g. "P0". |  | Pattern: `^P[0-9]+$` <br />Optional: \{\} <br /> |
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
| `count` _integer_ |  |  | Maximum: 64 <br />Minimum: 1 <br /> |
| `model` _[GPUModel](#gpumodel)_ |  |  |  |
| `memory` _[GPUMemory](#gpumemory)_ |  |  |  |
| `pci` _[GPUPCI](#gpupci)_ |  |  |  |
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
| `cpu` _[HostCPU](#hostcpu)_ |  |  | Optional: \{\} <br /> |
| `memory` _[HostMemory](#hostmemory)_ |  |  | Optional: \{\} <br /> |


#### SGPUInventory



SGPUInventory is a set of simulated GPU racks to distribute across CPU nodes.





| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `mokka.nvidia.com/v1alpha1` | | |
| `kind` _string_ | `SGPUInventory` | | |
| `metadata` _[ObjectMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#objectmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `spec` _[SGPUInventorySpec](#sgpuinventoryspec)_ |  |  |  |
| `status` _[SGPUInventoryStatus](#sgpuinventorystatus)_ |  |  | Optional: \{\} <br /> |


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
| `capacity` _[InventoryCapacity](#inventorycapacity)_ |  |  | Optional: \{\} <br /> |
| `usage` _[InventoryUsage](#inventoryusage)_ |  |  | Optional: \{\} <br /> |
| `rackGroups` _[RackGroupStatus](#rackgroupstatus) array_ |  |  | Optional: \{\} <br /> |
| `conditions` _[Condition](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#condition-v1-meta) array_ |  |  | Optional: \{\} <br /> |


#### SGPUNode



SGPUNode is a homogeneous logical node in the rack.



_Appears in:_
- [SGPURackProfileSpec](#sgpurackprofilespec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `gpus` _[SGPUGPUs](#sgpugpus)_ |  |  |  |
| `host` _[SGPUHost](#sgpuhost)_ |  |  | Optional: \{\} <br /> |
| `topology` _[SGPUTopology](#sgputopology)_ |  |  | Required: \{\} <br /> |


#### SGPUNodeReference



SGPUNodeReference identifies an exact Kubernetes Node instance.



_Appears in:_
- [SGPURackNode](#sgpuracknode)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `name` _string_ |  |  | MinLength: 1 <br /> |
| `uid` _[UID](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#uid-types-pkg)_ |  |  |  |


#### SGPURack



SGPURack is the controller-owned materialization of one inventory rack.
Its controller owner reference identifies the inventory pinned by
spec.inventoryRef.





| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `mokka.nvidia.com/v1alpha1` | | |
| `kind` _string_ | `SGPURack` | | |
| `metadata` _[ObjectMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#objectmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `spec` _[SGPURackSpec](#sgpurackspec)_ |  |  |  |
| `status` _[SGPURackStatus](#sgpurackstatus)_ |  |  | Optional: \{\} <br /> |


#### SGPURackGPU



SGPURackGPU contains deterministic identity and structural placement.



_Appears in:_
- [SGPURackNode](#sgpuracknode)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `index` _integer_ |  |  | Maximum: 63 <br />Minimum: 0 <br /> |
| `uuid` _string_ |  |  | Pattern: `^GPU-[0-9a-f]\{8\}-[0-9a-f]\{4\}-[0-9a-f]\{4\}-[0-9a-f]\{4\}-[0-9a-f]\{12\}$` <br /> |
| `serial` _string_ |  |  | MinLength: 1 <br /> |
| `minorNumber` _integer_ |  |  | Minimum: 0 <br /> |
| `pciAddress` _string_ |  |  | Pattern: `^[0-9a-f]\{4\}:[0-9a-f]\{2\}:[0-9a-f]\{2\}\.[0-7]$` <br /> |
| `rootComplex` _string_ |  |  | Pattern: `^pci[0-9a-f]\{4\}:[0-9a-f]\{2\}$` <br /> |
| `numaNode` _integer_ |  |  | Minimum: 0 <br /> |
| `hostProcessorIndex` _integer_ |  |  | Minimum: 0 <br /> |


#### SGPURackIdentity



SGPURackIdentity contains deterministic rack coordinates and fabric identity.



_Appears in:_
- [SGPURackSpec](#sgpurackspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `rackGroup` _string_ |  |  | Format: dns1123Label <br />MaxLength: 63 <br />MinLength: 1 <br /> |
| `rackIndex` _integer_ |  |  | Minimum: 0 <br /> |
| `fabricUUID` _string_ |  |  | Format: uuid <br /> |
| `cliqueID` _integer_ | CliqueID remains zero while each rack represents one fabric clique. |  | Maximum: 0 <br />Minimum: 0 <br /> |


#### SGPURackInventoryReference



SGPURackInventoryReference pins a rack to an exact inventory instance.



_Appears in:_
- [SGPURackSpec](#sgpurackspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `name` _string_ |  |  | MinLength: 1 <br /> |
| `uid` _[UID](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#uid-types-pkg)_ |  |  |  |


#### SGPURackNode



SGPURackNode is one logical Node and its optional Kubernetes Node binding.



_Appears in:_
- [SGPURackSpec](#sgpurackspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `index` _integer_ |  |  | Maximum: 1023 <br />Minimum: 0 <br /> |
| `nodeRef` _[SGPUNodeReference](#sgpunodereference)_ | NodeRef is absent while the logical Node is unbound. Its UID distinguishes<br />a replacement Kubernetes Node that reuses the same name. |  | Optional: \{\} <br /> |
| `gpus` _[SGPURackGPU](#sgpurackgpu) array_ | GPUs are rendered once for the logical Node and do not depend on its binding. |  | MaxItems: 64 <br />MinItems: 1 <br /> |


#### SGPURackProfile



SGPURackProfile is the static shape of a simulated GPU rack.





| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `mokka.nvidia.com/v1alpha1` | | |
| `kind` _string_ | `SGPURackProfile` | | |
| `metadata` _[ObjectMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#objectmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `spec` _[SGPURackProfileSpec](#sgpurackprofilespec)_ |  |  |  |


#### SGPURackProfileDefaults



SGPURackProfileDefaults is initial runtime state for fresh sGPUs.



_Appears in:_
- [SGPURackProfileSpec](#sgpurackprofilespec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `runtime` _[RuntimeState](#runtimestate)_ |  |  | Optional: \{\} <br /> |


#### SGPURackProfileReference



SGPURackProfileReference pins rendered data to an exact profile revision.



_Appears in:_
- [SGPURackSpec](#sgpurackspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `name` _string_ |  |  | MinLength: 1 <br /> |
| `uid` _[UID](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#uid-types-pkg)_ |  |  |  |
| `generation` _integer_ |  |  | Minimum: 1 <br /> |
| `revision` _string_ | Revision identifies the rendered profile content, independent of its name. |  | Pattern: `^[0-9a-f]\{64\}$` <br /> |


#### SGPURackProfileSpec



SGPURackProfileSpec is one logical rack profile.



_Appears in:_
- [SGPURackProfile](#sgpurackprofile)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `rack` _[SGPURackShape](#sgpurackshape)_ | Rack is the logical rack shape described by this profile. |  |  |
| `node` _[SGPUNode](#sgpunode)_ |  |  |  |
| `software` _[SGPUSoftware](#sgpusoftware)_ |  |  | Optional: \{\} <br /> |
| `defaults` _[SGPURackProfileDefaults](#sgpurackprofiledefaults)_ |  |  | Optional: \{\} <br /> |


#### SGPURackShape



SGPURackShape defines the dimensions of a logical rack template. It is
nested profile data rather than a materialized SGPURack resource.



_Appears in:_
- [SGPURackProfileSpec](#sgpurackprofilespec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `nodesPerRack` _integer_ |  |  | Maximum: 1024 <br />Minimum: 1 <br /> |


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
| `spec` _[SGPURuntimePolicySpec](#sgpuruntimepolicyspec)_ |  |  |  |
| `status` _[SGPURuntimePolicyStatus](#sgpuruntimepolicystatus)_ |  |  | Optional: \{\} <br /> |


#### SGPURuntimePolicySpec



SGPURuntimePolicySpec pairs a target selector with the RuntimeState to apply.



_Appears in:_
- [SGPURuntimePolicy](#sgpuruntimepolicy)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `targetRef` _[PolicyTargetRef](#policytargetref)_ |  |  |  |
| `runtime` _[RuntimeState](#runtimestate)_ |  |  | Optional: \{\} <br /> |


#### SGPURuntimePolicyStatus



SGPURuntimePolicyStatus holds comma-joined denormalizations of
spec.targetRef axes for print columns.



_Appears in:_
- [SGPURuntimePolicy](#sgpuruntimepolicy)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `rackGroupsSummary` _string_ |  |  | Optional: \{\} <br /> |
| `rackIndexesSummary` _string_ |  |  | Optional: \{\} <br /> |
| `nodeIndexesSummary` _string_ |  |  | Optional: \{\} <br /> |
| `gpuIndexesSummary` _string_ |  |  | Optional: \{\} <br /> |


#### SGPUSoftware



SGPUSoftware is driver/NVML/CUDA versions.



_Appears in:_
- [SGPURackProfileSpec](#sgpurackprofilespec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `driverVersion` _string_ |  |  | Optional: \{\} <br /> |
| `nvmlVersion` _string_ |  |  | Optional: \{\} <br /> |
| `cudaVersion` _string_ |  |  | Optional: \{\} <br /> |


#### SGPUTopology



SGPUTopology is PCIe slots, GPU fabric, and network visible to the node.



_Appears in:_
- [SGPUNode](#sgpunode)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `gpuSlots` _[GPUSlot](#gpuslot) array_ |  |  | MaxItems: 64 <br />MinItems: 1 <br /> |
| `gpuFabric` _[GPUFabric](#gpufabric)_ |  |  | Optional: \{\} <br /> |
| `network` _[NetworkTopology](#networktopology)_ |  |  | Optional: \{\} <br /> |


#### SupportedClocks



SupportedClocks pairs a memory clock with supported graphics clocks.



_Appears in:_
- [GPUClocks](#gpuclocks)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `memoryMHz` _integer_ |  |  |  |
| `graphicsMHz` _integer array_ |  |  |  |


#### TemperatureTelemetry



TemperatureTelemetry drives synthetic GPU/memory temperature.



_Appears in:_
- [RuntimeTelemetry](#runtimetelemetry)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `mode` _string_ |  |  | Enum: [Fixed Pattern] <br />Optional: \{\} <br /> |
| `gpuCelsius` _integer_ |  |  | Optional: \{\} <br /> |
| `memoryCelsius` _integer_ |  |  | Optional: \{\} <br /> |


#### UtilizationPattern



UtilizationPattern shapes the generated utilization curve.



_Appears in:_
- [UtilizationTelemetry](#utilizationtelemetry)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `type` _string_ |  |  | Enum: [Steady Bursty Wave] <br />Optional: \{\} <br /> |
| `gpuPercent` _[PercentRange](#percentrange)_ |  |  | Optional: \{\} <br /> |
| `memoryPercent` _[PercentRange](#percentrange)_ |  |  | Optional: \{\} <br /> |


#### UtilizationTelemetry



UtilizationTelemetry drives synthetic GPU/memory utilization.



_Appears in:_
- [RuntimeTelemetry](#runtimetelemetry)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `mode` _string_ |  |  | Enum: [Pattern Fixed] <br />Optional: \{\} <br /> |
| `pattern` _[UtilizationPattern](#utilizationpattern)_ |  |  | Optional: \{\} <br /> |


