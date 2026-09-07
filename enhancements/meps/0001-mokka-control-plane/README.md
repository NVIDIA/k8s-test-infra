# MEP-0001: Mokka Control Plane

Author: [Roman Hlushko](https://github.com/roma-glushko)

<!-- toc -->
- [Summary](#summary)
- [Motivation](#motivation)
  - [Goals](#goals)
  - [Non-Goals](#non-goals)
- [Proposal](#proposal)
  - [User Stories](#user-stories)
    - [S1: Precise Multi-GPU Distribution Simulation](#s1-precise-multi-gpu-distribution-simulation)
    - [S2: Dynamic Failure Injection](#s2-dynamic-failure-injection)
  - [Notes/Constraints/Caveats (Optional)](#notesconstraintscaveats-optional)
  - [Risks and Mitigations](#risks-and-mitigations)
- [Design Details](#design-details)
- [Drawbacks](#drawbacks)
- [Alternatives](#alternatives)
<!-- /toc -->

## Summary

This proposal introduces a new component called Mokka Control Plane that centralizes:
- distribution of virtual GPU inventory (a new concept) across a K8s cluster 
- management of simulated GPU runtime state (for example, useful for chaos testing and fault injection)

## Motivation

The current architecture includes:
- a single central NVML mock component that acts as a node agent that applies NVML mock configuration and topology from configmaps
- each NVML instance is an independent component that knows nothing about the existence of any other NVML mock instances
- if you want to have different GPU profiles for different nodes, you need to label those nodes differently and deploy multiple Mokka helm charts with different node selectors.

![todays-architecture.png](./img/todays-architecture.png)

This architecture has given us a chance to focus on developing the core simulation logic so far.
However, we are approaching use cases that push it to its limits:

- there is no way to simulate capacity distribution of GPU platforms like GB300. For example, if you want to simulate two GB300 instances in the cluster, you can get at most 2 × 18 = 36 GPU nodes, each with 4 GPUs. Today it's the responsibility of Mokka users to enforce that cap, which may lead to unrealistic cluster topologies.
- we would like to have a simple way to change simulated GPU runtime state like temperature, failure modes, etc., so that a cluster operator can quickly propagate a failure across thousands of nodes.
- we ask cluster administrators to set `nvidia.com/gpu.clique` labels while it should be based on the GPU rack the node belongs to.
- we ask cluster administrators to provide cross-rack network topology.

### Goals

We aim for:
- decoupling of the simulated GPU profiles and runtime state from NVML mocks in order to control them centrally
- being able to schedule only a realistic number of GPU nodes based on provided configuration.
- automatically infer networking as much as we can.

### Non-Goals

<!--
What is out of scope for this MEP? Listing non-goals helps to focus discussion
and make progress.
-->

## Proposal

Before talking about the actual proposal, we need to refine our mental model. 
This should help us and our users reason about Mokka better.

The proposed way of thinking about our domain is one of the key pieces of the proposal.

### Context

When you buy a GPU platform like GB300 in clouds like AWS,
it becomes reserved for you.
You can then distribute the available number of GPUs across accounts and clusters as much as that capacity allows.

The reservation takes time and this is where Mokka can be useful.

### Mental Model

Let's think about the GPU infrastructure we want to simulate in terms of simulated GPU racks (we use the term *sGPU* to avoid confusion with *vGPU*, which already has an established meaning in the field) that the user is expecting to have
and networking between them. That's our simulated GPU inventory.

The GPU inventory holds the capacity that we can distribute in the cluster. 
For example, if we have 2× GB300 racks in our simulated GPU inventory, this gives 2 × 18 = 36 GPU nodes, each with 4 sGPUs attached.
If we wanted to schedule 40 GPU nodes with that inventory, 40 − 36 = 4 would be without GPUs.

The sGPU racks are characterized by:

- sGPU rack profile that contains physical and operational features and facts about the real GPU we simulate. This is our static ground truth. This includes expected node-level GPU profile (e.g. each of 18 GB300 nodes has 4 GPUs).
- sGPU runtime state which is a dynamic set of simulated sGPU characteristics like temperature, failure modes, fan state, etc. This is not sGPU rack-level information, it's sGPU-level information.

### Architecture

We propose to transform the current system state into a classic control-data plane architecture where:
- Control Plane centralizes sGPU inventory information and network topology
- Data Plane is a node-level agent that applies the sGPU node information, runtime state, and network topology.

![proposed-architecture.png](./img/proposed-architecture.png)

With this, NVML mock becomes a node-level agent (or Mokka Node Agent).
It makes sure sGPU and networking reflect the desired state.
In that sense, it can be thought of as a virtual device driver (for GPU and network card).

The current controller stage implements the Kubernetes control-plane foundation.
The controller watches `SGPURackProfile`, `SGPUInventory`, `SGPURack`, and eligible
Kubernetes `Node` objects. It materializes one controller-owned `SGPURack` per
inventory rack and assigns eligible Nodes to the logical Node slots stored in
those racks. Kubernetes objects are authoritative in this stage; controller
restart state is rebuilt from informer caches, and Redis is not used.

Each rack records the exact inventory name and UID, plus the exact profile name,
UID, generation, and canonical content revision used to render it. Rack group,
rack index, logical Node index, and GPU index form stable coordinates. Fabric
UUIDs, GPU UUIDs, and serials are generated deterministically from the inventory
UID and those coordinates; minor numbers and topology placement come from the
profile's indexed GPU slots. They remain stable across reconciliation and
Kubernetes Node rebinding. Recreating an inventory with the same name produces
a new identity because its UID changes.

Logical Nodes may be unbound. A bound slot records the exact Kubernetes Node
name and UID, so a same-name Node replacement is a different assignment. The
controller preserves valid bindings and allocates free slots deterministically.
It projects successful assignments onto the bound Node without forcing field
ownership:

- `mokka.nvidia.com/sgpu-assigned: "true"`
- `nvidia.com/gpu.clique: <fabric UUID>.<clique ID>`
- `mokka.nvidia.com/sgpu-assignment`: a versioned compact annotation containing
  the exact inventory, rack, profile revision, logical coordinates, and Node UID

The controller does not overwrite incompatible values or fields owned by
another manager. Capacity shrink, group removal, selector mismatch, Node
ineligibility, inventory deletion, and rack deletion first remove compatible
controller-owned Node metadata for the exact binding. Finalizers keep the
binding or rack until that cleanup is acknowledged. Missing Nodes and same-name
replacements are treated as the exact old UID being absent. Controller-owned
racks use an inventory controller owner reference with
`blockOwnerDeletion: false`; finalizers, rather than a blocking owner reference,
coordinate projection cleanup. Racks owned by another object are not adopted.

#### Runtime State

**Future design.** Runtime-policy evaluation, node-agent heartbeats, simulated
telemetry, and fault injection are not implemented by the current controller
stage. The `SGPURuntimePolicy` API describes the intended policy surface, but no
controller currently applies it. The durable Node-to-rack-slot assignment is
already stored in `SGPURack`; it is not an `SGPUNodeAllocation` resource.

Earlier versions of this proposal explored Redis for high-churn agent and
simulated runtime state. Redis is not a dependency of the implemented
controller, and any runtime backend and concurrency design remains future work.
A future design must preserve the Kubernetes resources as the authoritative
inventory, profile, rack, and assignment state described above.

### User Stories

#### S1: Precise Multi-GPU Distribution Simulation

As a cluster administrator, I don't need to worry about GPU rack architecture details
in order to simulate the GPU capacity my team expects.

I just specify the GPU racks we expect to have and rough network topology between them, and my cluster topology will be representative.

#### S2: Dynamic Failure Injection

**Future runtime-policy story.** As a cluster administrator, I can manage runtime state of my simulated GPU inventory in one place via Kubernetes Custom Resources.
I can write an automation that modifies the Kubernetes CR state and expect it to be propagated to all nodes without being concerned with cluster topology.

### Notes/Constraints/Caveats (Optional)

<!--
What are the caveats to the proposal?
What are some important details that didn't come across above?
Go in to as much detail as necessary here.
This might be a good place to talk about core concepts and how they relate.
-->

### Risks and Mitigations

<!--
What are the risks of this proposal, and how do we mitigate? Think broadly.
For example, consider operational overhead, resource consumption, and how
this will impact our users.
-->

## Design Details

### sGPU Inventory Distribution

The controller resolves every `SGPUInventory.spec.rackGroups[].profileRef` and
materializes the declared rack count as `SGPURack` resources. Each materialized
rack contains rendered logical Nodes and GPUs, stable identities, topology
placement, and optional exact Kubernetes Node bindings. Inventory and rack
status summarize realized capacity, allocation, pending requests, projection,
and conflicts.

A Kubernetes Node requests capacity by carrying
`mokka.nvidia.com/sgpu-node: "true"`. If a rack group has
`placement.nodeSelector`, the Node must also match that selector. A Node that
matches more than one rack group is left unassigned until the placement is
unambiguous. Existing valid bindings remain stable; pending Nodes and free
logical slots are ordered deterministically before assignment.

### Node Agent

**Future design.** The node agent will focus on applying expected NVML and
networking state. The current controller stage only materializes and projects
its authoritative assignment data; it does not yet expose the agent protocol,
heartbeat processing, or runtime-state delivery.

### Control Plane State

In the current stage, Kubernetes resources are authoritative:

- `SGPURackProfile` stores reusable static rack and GPU shape.
- `SGPUInventory` declares rack groups, counts, profiles, and placement.
- Controller-owned `SGPURack` stores rendered identity, topology placement, and
  exact optional Node bindings.
- Kubernetes Node labels and the assignment annotation are a derived projection
  of those bindings, not a second source of truth.

The controller runs as a Kubernetes controller with leader election. It keeps
only rebuildable informer indexes and work queues in memory. Redis, agent
liveness, and mutable simulated GPU runtime state are outside the current stage.

### CRD Design

The current controller stage uses these cluster-scoped CRDs:

- [Admin/GitOps] sGPU Profile: specifies reusable sGPU rack profiles.
- [Admin/GitOps] sGPU Inventory: specifies rack groups, counts, profiles, and
  Node placement constraints.
- [Control Plane] sGPU Rack: controller-owned materialization of one inventory
  rack, including stable logical identities and optional exact Node bindings.

`SGPURuntimePolicy` is a future control-plane surface. Its API is present, but
policy evaluation and runtime delivery are not implemented in this stage.
`SGPUNodeAllocation` is superseded by the binding stored directly in
`SGPURack.spec.nodes[].nodeRef` and is not a current CRD.

All CRDs are cluster-scoped.

#### SGPURackProfile

The current profile YAML format is a mix of multiple things:
- hardware capabilities
- node/rack topology
- software and firmware versions
- generated identities such as UUIDs and serial numbers
- initial runtime state
- live counters and telemetry
- implementation details needed to reproduce nvidia-smi -q.

Mapping between the existing YAML configuration and proposed SGPURackProfile:

| Existing section                      | New location                           |
| ------------------------------------- | -------------------------------------- |
| `system`                              | `spec.software`                        |
| Static parts of `device_defaults`     | `spec.node.gpus`                       |
| Current values in `device_defaults`   | `spec.defaults.runtime`                |
| `devices[]` identities                | Generated during materialization       |
| `devices[].pci.bus_id`                | `spec.node.topology.gpuSlots[]`        |
| `pcie_topology`                       | Derived from `gpuSlots[]`              |
| `nvlink`                              | `spec.node.topology.gpuFabric`         |
| `infiniband`                          | `spec.node.topology.network`           |
| `dynamic_metrics_defaults`            | `spec.defaults.runtime.telemetry`      |
| `fabric.cluster_uuid` and `clique_id` | Generated from the fabric domain       |
| `processes`                           | Runtime state only; never profile spec |

Not all of that belongs to SGPURackProfile. We propose the following information hierarchy:

```
SGPURackProfile
├── rack                         Rack shape
├── node
│   ├── gpus                     Homogeneous GPU template
│   ├── host                     CPU/host characteristics
│   └── topology                 PCIe, NUMA, GPU fabric, network
├── software                     Driver/NVML/CUDA presentation
└── defaults.runtime             Initial state, same schema as runtime policy
```

```yaml
apiVersion: mokka.nvidia.com/v1alpha1
kind: SGPURackProfile
metadata:
  name: gb300-nvl72
  labels:
    mokka.nvidia.com/family: gb300
    mokka.nvidia.com/platform: nvl72
    mokka.nvidia.com/profile-source: builtin
spec:
  # Logical rack shape. SGPUInventory multiplies this by its rack count.
  rack:
    nodesPerRack: 18

  # Every logical node in this profile has the same shape.
  node:
    gpus:
      count: 4

      model:
        vendor: nvidia.com
        product: GB300
        productName: NVIDIA GB300 NVL
        architecture: blackwell

        computeCapability:
          major: 10
          minor: 0

        cores:
          cuda: 21632

        board:
          partNumber: 699-2G530-0300-000

        firmware:
          vbiosVersion: 97.00.41.00.01
          gspVersion: 570.124.06

          infoROM:
            imageVersion: G530.0300.00.01
            oemObjectVersion: "2.1"
            eccObjectVersion: "7.20"
            powerObjectVersion: "1.0"

      memory:
        capacity: 288Gi
        reserved: 1536Mi
        bar1Capacity: 768Gi
        busWidthBits: 8192

      pci:
        vendorID: "10de"
        deviceID: "2941"
        subsystemVendorID: "10de"
        subsystemDeviceID: "1830"

        maxLink:
          generation: 6
          width: 16

      power:
        managementSupported: true

        limitsMilliWatts:
          minimum: 500000
          default: 1400000
          maximum: 1600000

      thermal:
        targetCelsius: 85
        maxOperatingCelsius: 85
        slowdownThresholdCelsius: 90
        shutdownThresholdCelsius: 95

      clocks:
        maximumMHz:
          graphics: 2200
          sm: 2200
          memory: 2625
          video: 2200

        supported:
          - memoryMHz: 2625
            graphicsMHz:
              - 345
              - 690
              - 1035
              - 1380
              - 1725
              - 1980
              - 2200

      capabilities:
        mig:
          supported: true
          maxGPUInstances: 7

        # Extensible capabilities that do not justify permanent top-level
        # fields. Keys must be qualified names.
        attributes:
          nvidia.com/transformer-engine:
            bool: true

          nvidia.com/confidential-compute:
            bool: true

          nvidia.com/nvlink-c2c:
            bool: true

          nvidia.com/tensor-precisions:
            strings:
              - fp4
              - fp6
              - fp8

          nvidia.com/decompression-engine:
            bool: true

    # Optional host characteristics exposed by the mock.
    host:
      cpu:
        vendor: nvidia.com
        product: Grace
        architecture: arm64
        cores: 72

      memory:
        capacity: 480Gi
        coherentWithGPU: true

    topology:
      # Describes structural PCIe/NUMA placement. UUIDs, serials and minor
      # numbers are generated per allocated logical node.
      gpuSlots:
        - index: 0
          pciAddress: "0000:0a:00.0"
          rootComplex: pci0000:00
          numaNode: 0
          hostProcessorIndex: 0

        - index: 1
          pciAddress: "0000:0b:00.0"
          rootComplex: pci0000:00
          numaNode: 0
          hostProcessorIndex: 0

        - index: 2
          pciAddress: "0000:4a:00.0"
          rootComplex: pci0000:40
          numaNode: 1
          hostProcessorIndex: 1

        - index: 3
          pciAddress: "0000:4b:00.0"
          rootComplex: pci0000:40
          numaNode: 1
          hostProcessorIndex: 1

      gpuFabric:
        type: NVLink
        generation: 5
        linksPerGPU: 18
        bandwidthPerLinkMBps: 53125
        c2cSupported: true

        domain:
          scope: Rack
          gpuCount: 72

        switches:
          # Optional: number visible to each node's NVML view.
          visiblePerNode: 4

      network:
        type: InfiniBand
        adapterModel: MT4129
        firmwareVersion: 28.43.1000
        linkSpeedGbps: 400
        adaptersPerGPU: 1

  # Versions that the simulator exposes through NVML/CUDA queries.
  software:
    driverVersion: 570.124.06
    nvmlVersion: 12.570.124.06
    cudaVersion: "12.8"

  # Initial effective runtime state. This should use exactly the same Go/API
  # type as SGPURuntimePolicy.spec.runtime.
  defaults:
    runtime:
      deviceState: Healthy

      modes:
        persistence: Enabled
        compute: Default
        mig: Disabled
        ecc: Enabled
        accounting: Disabled

      telemetry:
        performanceState: P0

        utilization:
          mode: Pattern
          pattern:
            type: Steady
            gpuPercent:
              minimum: 10
              maximum: 45
            memoryPercent:
              minimum: 5
              maximum: 25

        power:
          mode: Fixed
          drawMilliWatts: 175000

        temperature:
          mode: Fixed
          gpuCelsius: 38
          memoryCelsius: 36

        clocks:
          graphicsMHz: 345
          smMHz: 345
          memoryMHz: 2625
          videoMHz: 1200
```

Validation semantics:

```yaml
gpuSlots:
  x-kubernetes-list-type: map
  x-kubernetes-list-map-keys:
    - index

capabilities.attributes.*.strings:
  x-kubernetes-list-type: set
```

- We should represent the out-of-the-box profiles just as custom resources of `SGPURackProfile` type,
so there is a distinction between vanilla and custom profiles.

#### SGPUInventory

```yaml
apiVersion: mokka.nvidia.com/v1alpha1
kind: SGPUInventory
metadata:
  name: dev
spec:
  rackGroups:
    - id: training
      count: 2
      profileRef:
        name: gb300-nvl72

      placement:
        nodeSelector:
          matchLabels:
            mokka.nvidia.com/sgpu: training

    - id: ci
      count: 3
      profileRef:
        name: custom-vera-rubin-300
        
      placement:
        nodeSelector:
          matchLabels:
            mokka.nvidia.com/sgpu: ci

# information added by Control Plane
# This is useful for troubleshooting and understanding simulated capacity usage
status:
  capacity:
    racks: 5
    nodes: 48
    gpus: 168

  usage:
    requestedNodes: 45
    allocatedNodes: 40
    availableNodes: 8
    pendingNodes: 5

  rackGroups:
    - id: training
      profileName: gb300-nvl72
      
      capacity:
        racks: 2
        nodes: 36
        gpus: 144

      usage:
        requestedNodes: 37
        allocatedNodes: 32
        availableNodes: 4
        pendingNodes: 5

    - id: ci
      profileName: custom-vera-rubin-300
      capacity:
        racks: 3
        nodes: 12
        gpus: 24

      usage:
        requestedNodes: 8
        allocatedNodes: 8
        availableNodes: 4
        pendingNodes: 0

  conditions:
    - type: Accepted
      status: "True"
      reason: Accepted
      message: The inventory configuration is valid.
      observedGeneration: 3
      lastTransitionTime: "2026-08-05T10:40:00Z"

    - type: ResolvedRefs
      status: "True"
      reason: ProfilesResolved
      message: All referenced profiles are resolved.
      observedGeneration: 3
      lastTransitionTime: "2026-08-05T10:40:20Z"

    - type: Programmed
      status: "True"
      reason: Programmed
      message: All desired racks and Node projections are programmed.
      observedGeneration: 3
      lastTransitionTime: "2026-08-05T10:40:40Z"

    - type: RequestsSatisfied
      status: "False"
      reason: PendingNodes
      message: 5 requested Nodes are pending capacity.
      observedGeneration: 3
      lastTransitionTime: "2026-08-05T10:45:12Z"
```

For Server-Side Apply friendliness, `spec.rackGroups` should be defined in the CRD schema as an associative list keyed by id:

```yaml
x-kubernetes-list-type: map
x-kubernetes-list-map-keys:
  - id
```

The controller admits at most 64 rack groups across all inventories (as
[Gateway API does for listeners](https://www.romaglushko.com/blog/k8s-gateway-api/#listenerset)).
One group can expand to many racks through `count`, so this bounds selector
classification without reducing the 100,000-rack topology limit.

Rack-local fabric and clique identity is materialized in `SGPURack`. Mutable
simulated GPU state remains part of the [future runtime design](#runtime-state).

#### SGPURuntimePolicy (Future Controller Stage)

```yaml
apiVersion: mokka.nvidia.com/v1alpha1
kind: SGPURuntimePolicy
metadata:
  name: dev-busy-gpus
spec:
  targetRef:
    group: mokka.nvidia.com
    kind: SGPUInventory 
    name: development-cluster

    rackGroups: # optional
      - training 
    rackIndexes: # optional
      - 0
    nodeIndexes: # optional
      - 5
    gpuIndexes: # optional
      - 2

  runtime: # the same configuration as in SGPURackProfile.defaults.runtime
    telemetry:
      temperature:
        mode: Fixed
        gpuCelsius: 80
        memoryCelsius: 50
```

Validation semantics for `*Indexes` fields:

```yaml
x-kubernetes-list-type: set
minItems: 1
maxItems: 64
```

A future runtime-policy controller must perform reference-dependent validation of `targetRef`:

- Does the inventory exist?
- Does rackGroup exist?
- Is rackIndex < count?
- Is nodeIndex < nodesPerRack?
- Is gpuIndex < devicesPerNode?

A policy should contain only the fields it wants to control. It's a sparse override.

- Omitted field means inherit
- Explicit zero is a real value to set e.g. empty list [] or 0 int.
- A more-specific list replaces a less-specific list

#### SGPURack

`SGPURack` supersedes the earlier `SGPUNodeAllocation` design. Cluster
administrators do not create these resources. The controller renders them from
an exact inventory instance and profile observation, then updates only the
optional `nodeRef` bindings while retaining stable logical identities.

The following illustrative one-Node, one-GPU rack uses the current `v1alpha1`
fields:

```yaml
apiVersion: mokka.nvidia.com/v1alpha1
kind: SGPURack
metadata:
  name: dev-training-0-cf4957078095
  uid: 7f6db22d-f91b-4f98-a0d2-a408bc3b78a2
  labels:
    mokka.nvidia.com/inventory: dev
    mokka.nvidia.com/rack-group: training
    mokka.nvidia.com/rack-index: "0"
  annotations:
    mokka.nvidia.com/inventory-uid: 2d50c972-39d4-4f63-ae42-ea2d639a17a1
  finalizers:
    - mokka.nvidia.com/rack-cleanup
  ownerReferences:
    - apiVersion: mokka.nvidia.com/v1alpha1
      kind: SGPUInventory
      name: dev
      uid: 2d50c972-39d4-4f63-ae42-ea2d639a17a1
      controller: true
      blockOwnerDeletion: false
spec:
  inventoryRef:
    name: dev
    uid: 2d50c972-39d4-4f63-ae42-ea2d639a17a1
  profileRef:
    name: one-gpu-ci
    uid: 26fc3c9b-a857-4320-863d-334af5a5d768
    generation: 3
    revision: 5a683f0d9ad8d10e5a683f0d9ad8d10e5a683f0d9ad8d10e5a683f0d9ad8d10e
  identity:
    rackGroup: training
    rackIndex: 0
    fabricUUID: 612ee4ea-82ff-5045-bb7e-04c5f9617370
    cliqueID: 0
  nodes:
    - index: 0
      nodeRef:
        name: worker-06
        uid: 33427206-1021-4bd9-a6fb-c23737696e98
      gpus:
        - index: 0
          uuid: GPU-18c43468-a120-56a8-acce-00213dc46934
          serial: "13714374014097840417"
          minorNumber: 0
          pciAddress: "0000:0a:00.0"
          rootComplex: pci0000:00
          numaNode: 0
          hostProcessorIndex: 0
status:
  observedGeneration: 2
  assignedNodes: 1
  conditions:
    - type: Ready
      status: "True"
      reason: Ready
      message: Rack bindings are valid.
      observedGeneration: 2
      lastTransitionTime: "2026-08-05T10:40:00Z"
```

The corresponding controller-owned Node projection is compact and pins every
object that can be replaced by UID:

```yaml
apiVersion: v1
kind: Node
metadata:
  name: worker-06
  uid: 33427206-1021-4bd9-a6fb-c23737696e98
  labels:
    mokka.nvidia.com/sgpu-assigned: "true"
    nvidia.com/gpu.clique: 612ee4ea-82ff-5045-bb7e-04c5f9617370.0
  annotations:
    mokka.nvidia.com/sgpu-assignment: >-
      {"v":1,"inventory":{"name":"dev","uid":"2d50c972-39d4-4f63-ae42-ea2d639a17a1"},"rack":{"name":"dev-training-0-cf4957078095","uid":"7f6db22d-f91b-4f98-a0d2-a408bc3b78a2"},"profile":{"name":"one-gpu-ci","uid":"26fc3c9b-a857-4320-863d-334af5a5d768","revision":"5a683f0d9ad8d10e5a683f0d9ad8d10e5a683f0d9ad8d10e5a683f0d9ad8d10e"},"rackGroup":"training","rackIndex":0,"nodeIndex":0,"nodeUID":"33427206-1021-4bd9-a6fb-c23737696e98"}
```

### Future SGPURuntimePolicy Apply Strategy

A policy applies to the target itself and all descendants below:

```
Inventory
└── rack group
    └── rack
        └── node
            └── GPU
```

The effective configuration is assembled in this order:

```
SGPURackProfile defaults
      ↓
inventory policy
      ↓
rack-group policy
      ↓
rack policy
      ↓
node policy
      ↓
GPU policy
```

A more specific policy overrides a less specific policy field by field.

Specificity can be represented as target depth:

| Depth | Target     |
| ----: | ---------- |
|     0 | Inventory  |
|     1 | Rack group |
|     2 | Rack       |
|     3 | Node       |
|     4 | GPU        |

Conflicting policies are policies that:
- are applied at the same level (neither is more specific than the other)
- modify the same fields

In this case, we keep the oldest policy in place based on `creationTime` and reject all challenger policies as conflicting.

Deleting a policy would trigger recompilation of the affected runtime views.
Their effective values would fall back to the next less-specific policy or
profile default.

### Future SGPURuntimePolicy Fanout

`SGPURuntimePolicy` may be applied to a set of inventory resources or the whole inventory.
When it comes to a simulation of big clusters, we can easily have tens of thousands of specific sGPU runtime views to update.
So the question is how to do that efficiently on that scale and above?

Option 1. Shard runtime views to recompute by logical rack and Node coordinates.

Option 2. Recompute runtime views lazily when the node agent requests them.

## CRD Packaging

Separate CRD packaging is implemented by the `mokka-crds` Helm chart in
[`deployments/mokka-crds/helm/mokka-crds`](../../../deployments/mokka-crds/helm/mokka-crds).
The chart is intended to be installed by a privileged admin user before the main chart.
This approach is also used by [Envoy Gateway](https://github.com/envoyproxy/gateway/tree/main/charts), for example.

Separate packaging prevents a cyclic dependency between the presence of CRDs in the cluster
and the content of the main Mokka Helm chart.

## Future Redis Evaluation

Redis is not used or deployed by the current controller stage. If a future
runtime-state design selects Redis, its packaging, persistence, supported Redis
topologies, and failure semantics must be specified separately. It must not
replace Kubernetes as the authoritative source for inventories, profiles,
materialized racks, or exact Node bindings.

## sGPU to Node Placement

In terms of sGPU placement on Kubernetes CPU nodes, we have two aspects:
- we need to place the Mokka node agent on all nodes that need to have sGPUs
- we need to allow cluster admins to specify what type of sGPU they want to see on the CPU node

In order to do that, we should allow:
- specifying a nodeSelector for sGPU nodes e.g. `mokka.nvidia.com/sgpu-node: "true"` (the Mokka node agent Daemonset will use it)
- specifying `SGPUInventory.rackGroups[].placement` and an additional node label like `mokka.nvidia.com/sgpu: "training"` to match a specific rackGroup

### Topology

For network topology, the current controller generates rack-local fabric and
clique identity and projects the clique label consistently to assigned Nodes.
Cross-rack topology generation remains future work and could use a three-level
core-spine-leaf switch topology.

The core-spine-leaf switch topology is used by major clouds like AWS, GCP, OCI, etc. 
By using it by default, we can simplify the cluster administrator's life.

![Mokka Topology Generation](./img/mokka-topograph-integration.png)

The implementation details are outside the scope of this MEP and likely need a dedicated MEP.

### Cluster Admin Experience

The main high-level goal of this proposal is to simplify and reduce the number of things
the cluster admins who deploy Mokka and set up sGPU clusters should be responsible for.

After this proposal is implemented, the following steps would be needed to set up a new sGPU cluster.

#### Scenario 1. Simple sGPU Setup 

- Deploy a Mokka CRD helm chart.
- Deploy a single instance of the Mokka main helm charts.
- Configure sGPU inventory via K8s CRs. For example:
```yaml
apiVersion: mokka.nvidia.com/v1alpha1
kind: SGPUInventory
metadata:
  name: sgpu-inventory
spec:
  rackGroups:
    - id: training
      count: 2
      profileRef:
        name: gb300-nvl72
```
- Label the CPU nodes that are supposed to have sGPU with `mokka.nvidia.com/sgpu-node: "true"`.

#### Scenario 2. Selective sGPU Placement

- Deploy a Mokka CRD helm chart.
- Deploy a single instance of the Mokka main helm charts.
- Configure sGPU inventory via K8s CRs. For example:

```yaml
apiVersion: mokka.nvidia.com/v1alpha1
kind: SGPUInventory
metadata:
  name: sgpu-inventory
spec:
  rackGroups:
    - id: training
      count: 4
      profileRef:
        name: gb300-nvl72
      placement:
        nodeSelector:
          matchLabels:
            mokka.nvidia.com/sgpu-group: training

    - id: inference
      count: 2
      profileRef:
        name: gb300-nvl72
      placement:
        nodeSelector:
          matchLabels:
            mokka.nvidia.com/sgpu-group: inference
```
- Cluster administrator creates two node groups with the following labels:
  - Training group: 
    - `mokka.nvidia.com/sgpu-node: "true"`
    - `mokka.nvidia.com/sgpu-group: "training"`
  - Inference group:
    - `mokka.nvidia.com/sgpu-node: "true"`
    - `mokka.nvidia.com/sgpu-group: "inference"`

#### Scenario 3. Half of sGPUs Failed

**Future runtime-policy scenario.** Policy evaluation is not implemented by the
current controller stage.

- Deploy a Mokka CRD helm chart.
- Deploy a single instance of the Mokka main helm charts.
- Configure sGPU inventory via K8s CRs. For example:

```yaml
apiVersion: mokka.nvidia.com/v1alpha1
kind: SGPUInventory
metadata:
  name: sgpu-inventory
spec:
  rackGroups:
    - id: training
      count: 4
      profileRef:
        name: gb300-nvl72
    - id: inference
      count: 2
      profileRef:
        name: gb300-nvl72
```
- Label the CPU nodes that are supposed to have sGPU with `mokka.nvidia.com/sgpu-node: "true"`.
- Create a runtime policy to fail the training rack group:

```yaml
apiVersion: mokka.nvidia.com/v1alpha1
kind: SGPURuntimePolicy
metadata:
  name: sgpu-inventory-training-failed
spec:
  targetRef:
    group: mokka.nvidia.com
    kind: SGPUInventory 
    name: sgpu-inventory
    rackGroups: [training]

  runtime:
    deviceState: Failed
```

## Drawbacks

The main drawback is that we push the system to be more complicated. 
We add one more component, introduce network communication between the control and data planes, and have to manage Control Plane state.

The current controller stores materialized racks and Node bindings in Kubernetes
and derives Node metadata from them. This increases the number and size of
Kubernetes objects, but avoids an additional stateful dependency and makes
controller state reconstructable after restart. Future agent and runtime-policy
work must evaluate its own storage and scaling trade-offs.

## Alternatives

<!--
What other approaches did you consider, and why did you rule them out? These do
not need to be as detailed as the proposal, but should include enough
information to express the idea and why it was not acceptable.
-->
