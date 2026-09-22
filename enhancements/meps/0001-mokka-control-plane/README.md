# MEP-0001: Mokka Control Plane

Author: [Roman Hlushko](https://github.com/roma-glushko)

<!-- toc -->
- [Summary](#summary)
- [Motivation](#motivation)
  - [Goals](#goals)
  - [Non-Goals](#non-goals)
- [Proposal](#proposal)
  - [Context](#context)
  - [Mental Model](#mental-model)
  - [Architecture](#architecture)
    - [Runtime State](#runtime-state)
  - [User Stories](#user-stories)
    - [S1: Precise Multi-GPU Distribution Simulation](#s1-precise-multi-gpu-distribution-simulation)
    - [S2: Dynamic Failure Injection](#s2-dynamic-failure-injection)
  - [Notes/Constraints/Caveats (Optional)](#notesconstraintscaveats-optional)
  - [Risks and Mitigations](#risks-and-mitigations)
- [Design Details](#design-details)
  - [sGPU Inventory Distribution](#sgpu-inventory-distribution)
  - [Node Agent](#node-agent)
  - [Control Plane State](#control-plane-state)
  - [CRD Design](#crd-design)
    - [SGPURackProfile](#sgpurackprofile)
    - [SGPUInventory](#sgpuinventory)
    - [SGPURuntimePolicy](#sgpuruntimepolicy)
    - [SGPURack](#sgpurack)
  - [SGPURuntimePolicy Apply Strategy](#sgpuruntimepolicy-apply-strategy)
  - [Runtime View Computation and Delivery](#runtime-view-computation-and-delivery)
- [CRD Packaging](#crd-packaging)
- [sGPU to Node Placement](#sgpu-to-node-placement)
  - [Topology](#topology)
  - [Cluster Admin Experience](#cluster-admin-experience)
    - [Scenario 1. Simple sGPU Setup](#scenario-1-simple-sgpu-setup)
    - [Scenario 2. Selective sGPU Placement](#scenario-2-selective-sgpu-placement)
    - [Scenario 3. Half of sGPUs Failed](#scenario-3-half-of-sgpus-failed)
- [Drawbacks](#drawbacks)
- [Alternatives and Design History](#alternatives-and-design-history)
  - [State Categories](#state-categories)
  - [Per-Node SGPUNodeAllocation Resources](#per-node-sgpunodeallocation-resources)
  - [Redis as a Runtime Backend](#redis-as-a-runtime-backend)
  - [Heartbeat-Based Assignment Reclamation](#heartbeat-based-assignment-reclamation)
  - [Eager Runtime-View Fanout](#eager-runtime-view-fanout)
<!-- /toc -->

## Summary

Mokka Control Plane centralizes:

- distribution of virtual GPU inventory across a Kubernetes cluster;
- management of simulated GPU runtime state, including chaos testing and fault
  injection.

## Motivation

Without the control plane, the architecture has these limitations:

- a central NVML mock component acts as a node agent that applies configuration
  and topology from ConfigMaps;
- each NVML instance is independent and knows nothing about other NVML mock
  instances;
- different GPU profiles on different Nodes require labels and multiple Mokka
  Helm releases with different Node selectors.

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

Mokka uses a control-plane/data-plane architecture:

- The control plane owns simulated GPU (sGPU) inventory, assignment, topology,
  and runtime-policy decisions.
- A node-level data-plane agent applies the effective sGPU and networking state
  returned for its Kubernetes Node.

![proposed-architecture.png](./img/proposed-architecture.png)

The Kubernetes API is the durable authority. `SGPURackProfile` and
`SGPUInventory` declare capacity, controller-owned `SGPURack` objects store
materialized racks and exact Node assignments, and `SGPURuntimePolicy` objects
declare runtime overrides. Redis is not part of the architecture.

Every control-plane replica maintains a rebuildable, informer-backed read view
of these resources. A replica becomes ready to serve reads only after its
caches synchronize. All ready replicas may answer node-agent REST requests, but
only the leader elected through Kubernetes may reconcile or mutate Kubernetes
resources. A replica that cannot establish a sufficiently current view for an
agent request must retry the read rather than serve older state.

Each rack records the exact inventory name and UID, plus the exact profile name,
UID, generation, and canonical content revision used to render it. Rack group,
rack index, logical Node index, and GPU index form stable coordinates. Fabric
UUIDs, GPU UUIDs, and serials are generated deterministically from the inventory
UID and those coordinates; minor numbers and topology placement come from the
profile's indexed GPU slots. They remain stable across reconciliation and
Kubernetes Node rebinding. Recreating an inventory with the same name produces
a new identity because its UID changes.

Logical Nodes may be unbound. A bound slot records the exact Kubernetes Node
name and UID plus an assignment revision, so a same-name Node replacement is a
different assignment. The revision is monotonic only within that exact Node
UID. The controller preserves valid bindings and allocates free slots
deterministically. It projects successful assignments onto the bound Node
without forcing field ownership:

- `mokka.nvidia.com/sgpu-assigned: "true"`
- `nvidia.com/gpu.clique: <fabric UUID>.<clique ID>`
- `mokka.nvidia.com/sgpu-assignment`: a versioned compact annotation containing
  the Node UID, assignment revision, exact rack UID and logical coordinates,
  and a rack delivery generation floor

`SGPURack` remains the assignment authority. The assignment annotation is the
publication and fencing record that makes an authoritative binding safe to
serve; it is not a second assignment authority. Its rack generation is the
minimum generation settled before that assignment was published, not a
permanently exact generation. The leader increments the persisted revision with
a Kubernetes `resourceVersion`-guarded Node update for every assignment
transition. On release it removes the assignment labels and binding details but
retains a minimal unassigned annotation containing the exact Node UID and
advanced revision. Thus a move, release, or later rebind cannot reuse an earlier
fence.

The controller does not overwrite incompatible values or fields owned by
another manager. Capacity shrink, group removal, selector mismatch, Node
ineligibility, inventory deletion, and rack deletion first remove compatible
controller-owned Node metadata for the exact binding, while retaining the fence
when the exact Node still exists. Finalizers keep the binding or rack until that
cleanup is acknowledged. Missing Nodes and same-name replacements are treated
as the exact old UID being absent; a replacement UID starts a new revision
domain. Controller-owned racks use an inventory controller owner reference with
`blockOwnerDeletion: false`; finalizers, rather than a blocking owner reference,
coordinate projection cleanup. Racks owned by another object are not adopted.

This MEP defines the complete architecture rather than the delivery status of
an implementation stage. See the [Mokka controller operational
documentation](https://github.com/NVIDIA/k8s-test-infra/blob/main/docs/mokka-controller.md#stage-1-exclusions) for
currently available behavior and its Stage 1 exclusions.

#### Runtime State

Runtime state is split by durability and ownership:

| State | Authority and lifetime |
| --- | --- |
| Inventory, profiles, and runtime-policy declarations | Durable Kubernetes resources managed declaratively by administrators or GitOps. |
| Exact Node assignments, assignment fences, and materialized identities | Durable controller-owned `SGPURack` state is authoritative for bindings; controller-owned Kubernetes Node projection metadata publishes a monotonic fence scoped to the exact Node UID. |
| Informer indexes and effective per-node runtime views | Rebuildable control-plane memory. Effective views are computed lazily for agent requests and are not persisted. |
| Last successfully applied node state | A node-agent cache used while the control plane is temporarily unavailable. |

An effective runtime view combines the assigned rack slot and profile defaults
with all accepted `SGPURuntimePolicy` overrides that target its coordinates.
The control plane computes this view when the assigned node agent requests it;
it does not fan policies out into one durable object per Node or GPU.

Polling is a state-delivery mechanism, not an independent liveness authority.
The control plane does not persist agent last-seen records and does not reclaim
an assignment because a polling interval elapsed. Kubernetes Node identity and
lifecycle, including deletion, replacement with a new UID, or loss of
eligibility, control assignment release.

### User Stories

#### S1: Precise Multi-GPU Distribution Simulation

As a cluster administrator, I don't need to worry about GPU rack architecture details
in order to simulate the GPU capacity my team expects.

I just specify the GPU racks we expect to have and rough network topology between them, and my cluster topology will be representative.

#### S2: Dynamic Failure Injection

As a cluster administrator, I can manage runtime state of my simulated GPU
inventory in one place through Kubernetes custom resources. I can write
an automation that modifies the Kubernetes state and expect node agents to
observe it without being concerned with cluster topology.

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

The node agent applies, but does not author, expected NVML and networking state.
It periodically polls the control-plane REST API using its exact Kubernetes
Node name and UID. Any cache-synchronized ready control-plane replica may serve
the request.

Each response identifies the exact Kubernetes Node UID and carries its
assignment revision plus the assigned rack's durable delivery generation. The
elected leader advances the rack generation in `SGPURack.status.delivery`
whenever an assignment or an accepted input to any effective runtime view in
the rack changes. The status records the exact rack, inventory, profile, and
accepted-policy revisions for that generation; it does not contain the
effective runtime payload.

The agent sends its last successfully applied Node UID, assignment revision,
and rack delivery generation when polling. For the same Node UID it compares
`(assignment revision, rack delivery generation)` lexicographically and accepts
only a non-regressing pair. A higher assignment revision may therefore select a
new rack whose delivery generation is numerically lower, while an equal
assignment revision requires an equal or higher rack generation. Equal pairs
are idempotent. An unassigned tombstone uses rack generation zero. A different
Kubernetes Node UID starts a fresh ordering domain, but a response must still
match the exact UID requested by that agent.

A read replica serves assigned state only when its informer caches contain the
exact Node UID, assignment revision, rack UID, logical coordinates, and
authoritative binding. Its current cached rack delivery generation must be at
least the floor in the assignment annotation, and its caches must contain the
exact rack spec and complete dependency manifest for that current generation.
The replica offers its current generation and requires the resulting pair not
to precede the pair sent by the agent. Otherwise it returns retry/not-ready
without replacement state. A stale replica may therefore serve an older runtime
generation only until the agent reports that it has applied a newer one. These
rules prevent a lagging replica from combining revisions or replaying an old
assignment after a newer one.

The agent uses the [node-agent state
cache](../0003-node-agent/README.md#state) to retain the last successfully
received and applied state. A temporary API or control-plane failure therefore
does not erase working node state. Poll failure or silence does not release
capacity; Kubernetes Node identity and lifecycle drive release.

### Control Plane State

Kubernetes resources are authoritative:

- `SGPURackProfile` stores reusable static rack and GPU shape, including default
  runtime values.
- `SGPUInventory` declares rack groups, counts, profiles, and placement.
- Controller-owned `SGPURack` stores rendered identity, topology placement, and
  exact optional Node bindings.
- `SGPURuntimePolicy` declares sparse runtime overrides.
- Kubernetes Node labels and the assignment annotation are a derived projection
  of rack bindings, not another source of truth. The annotation is the durable
  publication fence for the exact Node UID.

Every replica rebuilds read indexes from synchronized informers. The indexes
support assignment lookup and lazy policy evaluation, but loss of a replica
does not lose authoritative state. Only the elected replica attaches mutation
handlers, runs reconciliation work queues, advances the generation in each
rack's status, or writes Kubernetes resources. A new leader reads both the
persisted rack generation and the exact Node's persisted assignment annotation
before advancing either value. Kubernetes optimistic concurrency rejects a
rack or Node update based on an older `resourceVersion`; the leader rereads and
retries instead of publishing a locally chosen counter. Policy deletion and
leadership changes therefore cannot reset or reuse a generation within a rack
UID, and assignment transitions cannot reset or reuse a revision within a Node
UID. All cache-synchronized ready replicas may serve read-only agent requests
from their local view while enforcing the publication and dependency checks
described below.

No Redis service, persisted per-node effective runtime view, agent last-seen
record, or heartbeat-expiry controller participates in this state model.

### CRD Design

The architecture uses these cluster-scoped CRDs:

- [Admin/GitOps] sGPU Rack Profile: specifies reusable static rack and GPU
  shape, including default runtime values.
- [Admin/GitOps] sGPU Inventory: specifies rack groups, counts, profiles, and
  Node placement constraints.
- [Admin/GitOps] sGPU Runtime Policy: declares sparse runtime overrides for an
  inventory or selected coordinates within it.
- [Control Plane] sGPU Rack: materializes one inventory rack, including stable
  logical identities and optional exact Node bindings.

There is no `SGPUNodeAllocation` CRD. Durable assignment is represented by
`SGPURack.spec.nodes[].nodeRef`; effective per-node runtime views are computed
on demand rather than stored as Kubernetes resources.

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
simulated GPU state follows the [runtime-state model](#runtime-state).

#### SGPURuntimePolicy

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

The runtime-policy controller must perform reference-dependent validation of `targetRef`:

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

The following illustrative one-Node, one-GPU rack uses the `v1alpha1` fields:

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
        assignmentRevision: 7
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
  delivery:
    generation: 12
    rackSpecGeneration: 2
    inventoryRef:
      uid: 2d50c972-39d4-4f63-ae42-ea2d639a17a1
      generation: 3
    profileRef:
      uid: 26fc3c9b-a857-4320-863d-334af5a5d768
      generation: 3
      revision: 5a683f0d9ad8d10e5a683f0d9ad8d10e5a683f0d9ad8d10e5a683f0d9ad8d10e
    acceptedPolicies:
      - uid: 6da9d1bf-3d0f-4416-95ce-220f42eb51c2
        generation: 4
  conditions:
    - type: Ready
      status: "True"
      reason: Ready
      message: Rack bindings are valid.
      observedGeneration: 2
      lastTransitionTime: "2026-08-05T10:40:00Z"
```

The corresponding controller-owned Node projection is compact. It pins the
assignment identity and publishes the rack generation floor established for
that assignment:

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
      {"v":1,"nodeUID":"33427206-1021-4bd9-a6fb-c23737696e98","assignmentRevision":7,"assigned":true,"rack":{"name":"dev-training-0-cf4957078095","uid":"7f6db22d-f91b-4f98-a0d2-a408bc3b78a2"},"rackGroup":"training","rackIndex":0,"nodeIndex":0,"rackDeliveryGenerationFloor":12}
```

`assignmentRevision` is allocated from this annotation and is monotonic only
for its exact `nodeUID`. The matching `SGPURack.spec.nodes[].nodeRef` records
the same revision so readers can verify that the publication describes the
authoritative binding. `rackDeliveryGenerationFloor` is the generation whose
binding and dependency manifest were settled before publication. Later profile
or policy revisions advance `SGPURack.status.delivery` without rewriting this
annotation; those mutable revisions come from the current delivery manifest.
After release, the controller removes the assignment labels and retains only
the fence:

```yaml
metadata:
  annotations:
    mokka.nvidia.com/sgpu-assignment: >-
      {"v":1,"nodeUID":"33427206-1021-4bd9-a6fb-c23737696e98","assignmentRevision":8,"assigned":false}
```

The absent rack generation in a tombstone is represented as zero on the wire.
The tombstone prevents a cached response at revision 7 from restoring the old
binding. Deleting this Kubernetes Node ends the ordering domain; a Node later
created with the same name has a different UID and starts its own revision
sequence.

### SGPURuntimePolicy Apply Strategy

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

In this case, the oldest policy by Kubernetes `creationTimestamp` remains in
force and all challenger policies are rejected as conflicting. Policy UID
breaks a tie so the leader selects a deterministic winner and publishes the
accepted set in each affected rack's delivery status.

After a policy is deleted, the leader first recomputes conflict acceptance and
winner selection at the same specificity. The oldest remaining policy at that
level becomes accepted and effective, including a former challenger that is no
longer conflicting. Only when no same-specificity policy controls a field does
that field fall back to the next less-specific policy or profile default.

### Runtime View Computation and Delivery

A policy can target a broad inventory or selected rack groups, racks, logical
Nodes, and GPUs. The control plane does not eagerly materialize every affected
per-node or per-GPU view. On each node-agent request, a ready replica looks up
the exact Node UID's durable rack binding, selects the applicable accepted
policies from its informer indexes, and computes the effective view using the
precedence rules above.

This lazy path scales with agent polling and the policies relevant to the
requested coordinates instead of creating or rewriting an object for every
possible target. Policy changes and deletions invalidate rebuildable indexes.
On deletion, conflict acceptance and winner selection are recomputed at the
same specificity first: the oldest remaining policy at that level becomes
effective, and only the absence of a same-level winner causes a field to fall
back to a less-specific policy or profile default.

`SGPURack.status.delivery` is the Kubernetes-authoritative ordering record for
this lazy computation. It contains a monotonically increasing generation, the
observed rack spec generation, the inventory UID and generation, the profile
UID, generation, and canonical revision, and the exact set of accepted policy
UIDs and Kubernetes metadata generations that can affect the rack. Within an
object UID, `metadata.generation` identifies the exact inventory or policy spec
revision; the profile's existing canonical revision additionally identifies
the rendered defaults. The policy set is also the selection manifest for that
generation: a reader uses only those policies, so a stale policy-cache entry
cannot reintroduce a deleted policy. A policy revision becomes effective for a
rack only when the leader publishes it in this manifest.

The leader updates the delivery record whenever the rack assignment, rendered
profile/default inputs, or accepted-policy set or content changes. A deletion
that promotes a same-level challenger or exposes a less-specific value
therefore advances the generation just like an addition or update. A profile
or policy change requires one status update per affected rack; it does not
rewrite assignment annotations or create a Kubernetes object or persisted
payload per Node.

The leader obtains the current rack and increments its persisted generation in
a status update guarded by the rack's Kubernetes `resourceVersion`. After a
leader change, the new leader continues from that stored value rather than
from replica-local state. A conflicting write is reread and retried, never
replaced with a locally chosen counter. A recreated rack has a new rack UID and
may restart its rack generation, because assignment ordering across racks is
provided by the Node-scoped fence.

Assignment changes use two-phase publication because a rack binding and its
Node projection cannot be changed in one Kubernetes transaction:

1. The leader reads the exact Node UID and its persisted assignment annotation,
   chooses the next assignment revision, and first changes the authoritative
   `SGPURack` binding. A direct move removes the old binding before installing
   the new one. An assigned `nodeRef` records the chosen revision. The leader
   also advances each affected rack's delivery status with
   `resourceVersion`-guarded updates until the new binding and its dependency
   manifest are complete. A release ends this phase with no rack binding.
2. Only after the authoritative binding, rack spec, and dependency manifest are
   settled, the leader publishes that completed state by updating the same
   exact Node with a fresh `resourceVersion`. An assignment annotation contains
   the new revision, exact rack UID and coordinates, and the settled rack
   delivery generation as its publication floor. A release publishes the new
   revision as the minimal unassigned tombstone. Assignment labels are added or
   removed with this projection update.

The first assignment for a Node UID uses revision 1. Every subsequent move,
release, and rebind advances it; a release followed later by a rebind therefore
uses two distinct revisions. A failed Node update is reread and retried, so an
old writer cannot overwrite a newer fence. Reconciliation does not erase the
annotation or reset the counter. After failover, a new leader reads the
persisted rack and Node values: it completes an authoritative binding whose
chosen next revision has not yet been published, recognizes an already matching
publication as complete, or advances from the Node's greater persisted value
when reconciling a superseding transition. An extra increment after ambiguous
failure is harmless, but reuse or regression is not allowed.

A read replica may serve assigned state only when its informer caches contain
all of the following:

- the requested Kubernetes Node UID and its assignment annotation exactly;
- an authoritative `SGPURack.spec.nodes[].nodeRef` with the same Node UID and
  assignment revision at exactly the annotation's rack UID and logical
  coordinates;
- the replica's current `SGPURack.status.delivery`, with a generation greater
  than or equal to the annotation's `rackDeliveryGenerationFloor`, and the
  exact current rack spec whose `metadata.generation` equals its
  `rackSpecGeneration`; and
- every inventory, profile, and accepted-policy identity and revision in that
  current delivery generation's complete dependency manifest.

The replica computes the payload from exactly that current manifest and offers
`(assignmentRevision, currentRackDeliveryGeneration)`. Mutable profile and
policy revisions always come from the manifest, not from assignment-time Node
metadata. For an unassigned response, the cached tombstone must match the
requested Node UID and revision and the synchronized binding index must contain
no rack binding for that UID. Any missing or unequal exact value, generation
below the publication floor, partial publication phase, incomplete manifest, or
offered pair older than the agent's last applied `(assignment revision, rack
delivery generation)` yields retry/not-ready instead of replacement state. The
effective per-node runtime payload itself remains lazy and is never written to
Redis or Kubernetes.

For example, an assignment published at `(7, 12)` remains valid after a profile
or policy change advances that rack's delivery record to generation 13. A
replica whose current complete view is generation 13 offers `(7, 13)`. A stale
replica may still offer `(7, 12)` to an agent at `(7, 12)`, but after the agent
applies `(7, 13)`, its last-applied pair makes that stale response regress and
the replica returns retry/not-ready.

If Node UID `N` moves from assignment `A` at revision 7 to `B` at revision 8,
the new annotation's floor is published only after `B` and its current delivery
state are settled. A replica older than that transition cannot satisfy the
floor and binding checks. Every cached response for `A` also sorts before the
agent's applied `(8, G-B)` pair even when `A` and `B` use different rack UIDs and
unrelated rack generation ranges. A lagging standby therefore cannot replay `A`
after `B`. Deleting and recreating the Kubernetes Node ends `N`'s domain: the
replacement UID may start again at revision 1, while responses for `N` fail the
exact-UID check.

## CRD Packaging

The `mokka-crds` Helm chart packages the CRDs in
[`deployments/mokka-crds/helm/mokka-crds`](https://github.com/NVIDIA/k8s-test-infra/tree/main/deployments/mokka-crds/helm/mokka-crds).
The chart is intended to be installed by a privileged admin user before the main chart.
This approach is also used by [Envoy Gateway](https://github.com/envoyproxy/gateway/tree/main/charts), for example.

Separate packaging prevents a cyclic dependency between the presence of CRDs in the cluster
and the content of the main Mokka Helm chart.

## sGPU to Node Placement

In terms of sGPU placement on Kubernetes CPU nodes, we have two aspects:
- we need to place the Mokka node agent on all nodes that need to have sGPUs
- we need to allow cluster admins to specify what type of sGPU they want to see on the CPU node

In order to do that, we should allow:
- specifying a nodeSelector for sGPU nodes e.g. `mokka.nvidia.com/sgpu-node: "true"` (the Mokka node agent DaemonSet uses it)
- specifying `SGPUInventory.rackGroups[].placement` and an additional node label like `mokka.nvidia.com/sgpu: "training"` to match a specific rackGroup

### Topology

For network topology, the control plane generates rack-local fabric and clique
identity and projects the clique label consistently to assigned Nodes.
Cross-rack topology generation is outside this MEP's scope; a separate proposal
may define a three-level core-spine-leaf switch topology.

The core-spine-leaf switch topology is used by major clouds like AWS, GCP, OCI, etc. 
By using it by default, we can simplify the cluster administrator's life.

![Mokka Topology Generation](./img/mokka-topograph-integration.png)

The implementation details are outside the scope of this MEP and likely need a dedicated MEP.

### Cluster Admin Experience

The main high-level goal of this proposal is to simplify and reduce the number of things
the cluster admins who deploy Mokka and set up sGPU clusters should be responsible for.

The complete architecture supports the following workflows for setting up an sGPU cluster.

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

The control plane adds a networked component and requires node agents to poll
it. Kubernetes stores materialized racks and exact Node bindings in addition to
the administrator-authored declarations. At very large scale this increases
Kubernetes object size and reconciliation work, although rack-granular objects
avoid one object per assigned Node and lazy runtime evaluation avoids one
object per effective Node or GPU view.

Keeping the effective views only in memory makes them rebuildable rather than
durable, so a restarted replica must synchronize its informers before becoming
ready. Agents must also retain the last successfully applied state to tolerate
a temporary period with no ready control-plane endpoint.

## Alternatives and Design History

The selected design keeps Kubernetes as the durable authority, keeps exact
assignments in rack-granular resources, computes effective runtime views lazily,
and uses Kubernetes Node lifecycle for reclamation. The alternatives below
record why earlier proposals differed and which parts remain.

### State Categories

An earlier model divided control-plane data into mostly static inventory,
recreatable Node assignments, and disposable sGPU runtime state. Treating an
assignment as disposable simplifies recovery only while Node identity and
projection cleanup are ignored. It can otherwise reassign capacity after a
restart and leave a Node with stale metadata.

The selected model retains the useful distinction between durable declarations
and high-churn derived state, but moves the boundary:

- inventory, profiles, policy declarations, rack identities, exact Node
  assignments, and Node-scoped assignment fences are durable Kubernetes state;
- informer indexes and effective per-node/per-GPU views are rebuildable;
- the node agent retains only its last successfully applied view for temporary
  control-plane outages.

This preserves declarative recovery without persisting high-cardinality
computed views.

### Per-Node SGPUNodeAllocation Resources

The original Kubernetes-only alternative proposed one `SGPUNodeAllocation` per
assigned Node. Each object could hold the sGPU-to-Kubernetes-Node assignment,
last agent fetch time, and fully or partially computed runtime state. Its main
advantages were a single Kubernetes dependency, declarative inspection, and
standard Kubernetes persistence and correctness mechanisms.

At large scale, however, it creates roughly one object per simulated GPU Node
and turns agent polling or runtime changes into frequent API writes. A policy
covering thousands of Nodes could update thousands of objects, adding
high-cardinality and high-churn load to the API server and etcd.

The selected design preserves the Kubernetes-only dependency and durable exact
assignment, but stores bindings in `SGPURack.spec.nodes[].nodeRef`. One rack
object amortizes many logical Node bindings and also contains their stable
rendered identities. The existing Kubernetes Node carries only its compact
projection and monotonic assignment fence. The design rejects per-Node custom
resources, persisted last-fetch timestamps, and persisted effective runtime
views.

### Redis as a Runtime Backend

An earlier proposal selected Redis for Node assignment, last-seen, and runtime
state. Redis offers convenient atomic and concurrent operations, efficient
lookup and search, horizontal scaling options, and no hot-state write pressure
on Kubernetes etcd. Those properties were attractive for heartbeat updates and
eagerly materialized runtime views.

Redis also introduces another stateful production dependency. Durable use
requires persistence and persistent volumes, and deployments must define
standalone, Sentinel, or clustered topology, backup and recovery, TLS and
custom certificate authorities, upgrades, and failure semantics. Splitting
assignment authority between Kubernetes and Redis also complicates recovery
and exact cleanup when a Kubernetes Node is replaced with the same name.

The selected architecture rejects Redis. Kubernetes resources are the complete
durable authority, and replicas derive their read views from informer caches.
It retains Redis's intended avoidance of hot per-agent writes by not recording
polls and by computing effective runtime state in memory rather than persisting
it.

### Heartbeat-Based Assignment Reclamation

The earlier node-agent poll was both state fetch and heartbeat. The control
plane would persist a last-seen time and release capacity after missed polls.
This promised automatic recovery when a host or agent disappeared without a
clean shutdown.

A timeout cannot reliably distinguish a dead Node from a partitioned agent or
control plane. Releasing its assignment while the old agent continues using a
cached configuration can assign the same simulated capacity twice. Persisting
every poll also creates write load proportional to Node count and polling
frequency.

The selected design retains periodic polling and the agent's last-known-good
cache for delivery and outage tolerance, but rejects heartbeat expiry and
last-seen state. Assignment reclamation follows the exact Kubernetes Node UID
and lifecycle. Deletion, replacement, or loss of eligibility releases the
binding through normal reconciliation and finalizer cleanup; a missed poll does
not.

### Eager Runtime-View Fanout

Earlier fanout designs considered sharding affected allocation IDs or logical
rack/Node coordinates across control-plane replicas, then eagerly recomputing
and storing each view after a broad policy change. This can make reads cheap and
allows parallel recomputation, but one inventory-wide change still creates
work and storage proportional to all affected Nodes or GPUs. Multi-writer
recomputation also conflicts with the simpler single-writer Kubernetes model.

The selected design rejects eager fanout. All ready replicas index policy
declarations and lazily compute only the exact Node view requested by an agent.
Only the elected replica mutates Kubernetes resources; read replicas do not
materialize views. Assignment-publication floors, current rack-scoped delivery
generations, and the agent's last-applied pair prevent a lagging read replica
from rolling an agent back across either runtime changes or assignment
transitions without per-Node policy fanout.
