
Author: [Roman Hlushko](https://github.com/roma-glushko)

<!-- toc -->
- [Summary](#summary)
- [Motivation](#motivation)
  - [Background](#background)
    - [The Run:ai Fake GPU Operator](#the-runai-fake-gpu-operator)
    - [What Mokka does today](#what-mokka-does-today)
    - [Limits of the current integration](#limits-of-the-current-integration)
  - [Goals](#goals)
  - [Non-Goals](#non-goals)
- [Proposal](#proposal)
  - [User Stories (Optional)](#user-stories-optional)
    - [Story 1 (Optional)](#story-1-optional)
    - [Story 2 (Optional)](#story-2-optional)
  - [Notes/Constraints/Caveats (Optional)](#notesconstraintscaveats-optional)
  - [Risks and Mitigations](#risks-and-mitigations)
- [Design Details](#design-details)
- [Drawbacks](#drawbacks)
- [Alternatives](#alternatives)
<!-- /toc -->

## Summary

The proposal explores how to incorporate the relevant portion of
[Run:AI Fake GPU Operator (FGO)](https://github.com/run-ai/fake-gpu-operator) functionality directly into Mokka.

## Motivation

The integration will enable Mokka to:
- consolidate Mokka and Run:AI team's effort in one single project. Save time and effort on FGO maintenance
- replace FGO in Run:AI and KAI Scheduler CI/CD pipelines.
- extend Mokka's functionality with optional KWOK (no kubelet, virtual nodes) support for cheap and massive load testing where accessing `nvidia-smi` and other on-host tools and libs are not needed.

### Background

[Fake GPU Operator (FGO)](https://github.com/run-ai/fake-gpu-operator) is a Kubernetes operator for testing Run:AI's GPU scheduling capabilities
without having real, expensive GPU wasted on that activity.

FGO supports two backends:

- `fake` - the primary FGO mode. It uses a custom, fake instances of nvidia-smi, device driver, DRA plugin, etc. in order to simulate presence of GPU devices on the node.
- `mock` - the prior integration with Mokka during the early days of the Mokka project.

#### Fake Backend

The idea behind the `fake` backend is to fake the NVIDIA GPU infrastructure in order to propagate simulated GPU to Kubernetes:

- FGO is deployed to `gpu-operator` namespace to mimic presence of real GPU operator
- FGO comes with a custom device plugin that propagates GPU capacity of the node
- FGO comes with the `status-exporter` deamonset that mimics DGCM metrics
- FGO installs a custom `nvidia-smi` binary that supports a subset of commands and flags of the original `nvidia-smi`.

This gives the Kubernetes API level GPU simulation.

##### Node Lifecycle

![FGO SGPU Propagation on Node Startup](./img/fgo-sgpu-on-node-startup.png)

### Metrics

FGO supports utilization simulation via custom annotations:

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: fixed-30
  namespace: fgo-lab-04
  annotations:
    run.ai/simulated-gpu-utilization: "30"
spec:
  terminationGracePeriodSeconds: 0
  containers:
    - name: main
      image: "ubuntu:22.04"
      command: ["sleep", "infinity"]
      resources: 
        limits: 
          nvidia.com/gpu: 1
```

The `run.ai/simulated-gpu-utilization` annotation can be:
- a fixed value like `30` which means 30% utilization
- a range like `30-60` so the actual value is sampled from the range

Apart from that there are few utilization pattern kinds:

- `workloadKind: train` - jitter in the 80-100 GPU utilization range
- `workloadKind: interactive` - 0 GPU utilization
- `workloadKind: inference` - 0 GPU utilization

Memory utilization is reported based on `GPU memory x GPU fraction` where [GPU fraction](#resource-reservation--gpu-fractions) is another custom annotation:

```yaml
annotations:
  gpu-fraction: "0.25"
  run.ai/simulated-gpu-utilization: "50"
```

The `status-exporter` runs on the node and responsible for simulation and submitting utilization metrics via a well-known DCGM port `9400`:

```mermaid
flowchart TB
    pod["Pod training (API object)<br/>label workloadKind:<br/>TrainingWorkload"]
    su["status-updater (Deployment)<br/>calculateUsage:<br/>label → range 80–100"]
    cm["ConfigMap topology-n1<br/>(node GPU state)<br/>GPU0, pod UID →<br/>utilization min 80, max 100"]

    subgraph n1["Node n1"]
        subgraph se["status-exporter (DaemonSet pod)"]
            watch["watcher: reads the<br/>ConfigMap on change<br/>(or after 10 s quiet)"]
            pick["every 10 s: pick a<br/>random number in<br/>the range → 87"]
            dcgm["value kept in memory,<br/>served on :9400/metrics<br/>DCGM_FI_DEV_GPU_UTIL<br/>{pod=training} = 87"]
        end
    end

    prom["Prometheus"]

    pod ~~~ su
    su -->|"watches pods"| pod
    su -->|"reads, sets GPU status,<br/>writes back"| cm
    cm ~~~ watch
    watch -->|"watches"| cm
    watch -->|"sends node GPU state"| pick
    pick -->|"stores current value"| dcgm
    dcgm ~~~ prom
    prom -->|"scrapes"| dcgm

    classDef state fill:#fff3bf,stroke:#e0a800,color:#000
    classDef seen fill:#d3f9d8,stroke:#2b8a3e,color:#000
    class cm state
    class dcgm seen
    style n1 stroke-dasharray: 6 4
```

The metric looks like this in raw Prometheus (less labels than real DCGM sends):

```prometheus
DCGM_FI_DEV_GPU_UTIL{Hostname="nvidia-dcgm-exporter-ce3313",UUID="GPU-299e8440-...",container="main",
  device="nvidia2",gpu="2",modelName="NVIDIA A100-SXM4-40GB",namespace="fgo-lab-04",pod="ranged-20-60"} 59
```

### Resource Reservation & GPU Fractions

Run:ai and KAI share one physical GPU between several workloads without any real GPU partitioning. This is an optimization to bind one GPU to multiple low GPU consumption pods that do need real GPU resource without underutilizating the whole GPU device.

They park a reservation pod that holds the whole GPU device through the ordinary `nvidia.com/gpu` resource,
then place fractional pods that request no GPU resource at all and just point at the reservation. The kubelet only knows about the reservation pod.

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: reservation-g1
  namespace: runai-reservation # a namespace for placing reservation pods
  labels:
    runai-gpu-group: g1 # the pod group that is then being turned into GPU assignment annotation by FGO e.g. run.ai/reserve_for_gpu_index: GPU-775135a6-f582-540e-84df-8f4e9a049910
spec:
  terminationGracePeriodSeconds: 0
  containers:
    - name: reserve
      image: ubuntu:22.04
      command: ["sleep", "infinity"]
      resources:
        limits:
          nvidia.com/gpu: 1 # requests GPU device
```

Then, the fractional pod looks like this:

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: frac-a
  namespace: fgo-lab-05
  labels:
    runai-gpu-group: g1 # the same gpu group reference
  annotations:
    gpu-fraction: "0.5" # the gpu fraction is set
    run.ai/simulated-gpu-utilization: "30-40"
spec:
  terminationGracePeriodSeconds: 0
  affinity:
    podAffinity: # podAffinity places the fractional pod on the same node as the reservation pod
      requiredDuringSchedulingIgnoredDuringExecution:
        - topologyKey: kubernetes.io/hostname
          namespaces: ["runai-reservation"]
          labelSelector:
            matchLabels:
              runai-gpu-group: g1
  containers:
    - name: main
      image: ubuntu:22.04
      command: ["sleep", "infinity"]
      env:
        - name: NODE_NAME
          valueFrom: {fieldRef: {fieldPath: spec.nodeName}}
        - name: POD_UUID
          valueFrom: {fieldRef: {fieldPath: metadata.uid}}
        - name: RUNAI_NUM_OF_GPUS
          value: "0.5"
      volumeMounts:
        - name: smi
          mountPath: /bin/nvidia-smi # nvidia-smi is how Run:AI bridge no GPU pod to a GPU
  volumes:
    - name: smi
      hostPath:
        path: /var/lib/runai/bin/nvidia-smi
        type: File

  # Note that it requests no GPU by itself
```

The fractional pods get access to GPU device via shared `nvidia-smi` CLI mounted from a GPU-enabled node.

The `status-exporter` also leaves a few files intended for Run:AI container toolkit on the node filesystem:

- `/var/lib/runai/proc/pod/<pod UID>/metrics/gpu/<GPU index>/utilization.sm` - e.g. "34" percent
- `/var/lib/runai/proc/pod/<pod UID>/metrics/gpu/<GPU index>/memory.allocated` - e.g. "20480000000" bytes
- `/var/lib/runai/proc/events/` - created, always empty

#### NUMA-aware Scheduling

NUMA-aware schedulers (KAI, the Kubernetes scheduler-plugins) place GPU pods by reading one `NodeResourceTopology` (NRT) object per node:

```
Name:         fgo-lab-worker
Namespace:    
Labels:       <none>
Annotations:  <none>
API Version:  topology.node.k8s.io/v1alpha2
Attributes:
  Name:   topologyManagerPolicy
  Value:  single-numa-node
  Name:   topologyManagerScope
  Value:  container
Kind:     NodeResourceTopology
Metadata:
  Creation Timestamp:  2026-10-28T17:05:25Z
  Generation:          1
  Owner References:
    API Version:     v1
    Kind:            Node
    Name:            fgo-lab-worker
    UID:             0ec5ff95-72a6-4db8-a9ff-d41c3fffcb38
  Resource Version:  922551
  UID:               01ea8497-64d0-4cd6-9fb0-6a91591e38f5
Topology Policies:
  SingleNUMANodeContainerLevel
Zones:
  Costs:
    Name:   node-0
    Value:  10
    Name:   node-1
    Value:  21
  Name:     node-0
  Resources:
    Allocatable:  4
    Available:    4
    Capacity:     4
    Name:         nvidia.com/gpu
    Allocatable:  9
    Available:    9
    Capacity:     9
    Name:         cpu
    Allocatable:  8211190Ki
    Available:    8211190Ki
    Capacity:     8211190Ki
    Name:         memory
  Type:           Node
  Costs:
    Name:   node-0
    Value:  21
    Name:   node-1
    Value:  10
  Name:     node-1
  Resources:
    Allocatable:  4
    Available:    4
    Capacity:     4
    Name:         nvidia.com/gpu
    Allocatable:  9
    Available:    9
    Capacity:     9
    Name:         cpu
    Allocatable:  8211190Ki
    Available:    8211190Ki
    Capacity:     8211190Ki
    Name:         memory
  Type:           Node
Events:           <none>
```

FGO can fabricate those from a `numa` block in a pool.

On the real node though, NFD should create NRT resources. This is roughly the information considered by NFD:

| Path on the node                                                           | Used for                                                                                                                                                               |
|----------------------------------------------------------------------------|------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `/sys/devices/system/node/node<N>/` (the directory list)                   | **Which zones exist.** Each `node<N>` becomes zone `node-<N>`                                                                                                          |
| `/sys/devices/system/node/node<N>/cpu<M>/` and `…/cpu<M>/topology/core_id` | **Which CPUs belong to which zone.** Maps CPU IDs to zones. CPUs the kubelet doesn't list as allocatable count as reserved and are added to that zone's CPU *capacity* |
| `/sys/devices/system/node/node<N>/distance`                                | Zone **costs** (e.g. `10 21`)                                                                                                                                          |
| `/sys/bus/node/devices/node<N>/meminfo` (`MemTotal` line)                  | **Memory capacity** per zone                                                                                                                                           |
| `/sys/bus/node/devices/node<N>/hugepages/hugepages-<size>kB/nr_hugepages`  | **Hugepages capacity** per zone (`hugepages-2Mi`, `hugepages-1Gi`)                                                                                                     |
| `/sys/devices/cpu_*/cpus`                                                  | `cpu_core` / `cpu_atom` attributes, only on hybrid CPUs                                                                                                                |

#### KWOK

FGO reaches fleet scale with [KWOK](https://kwok.sigs.k8s.io/) (Kubernetes
WithOut Kubelet), which creates Node objects and drives the lifecycle of the
pods bound to them, with no kubelet and no containers behind either. 

KWOK resources are just `Node` resources:

```yaml
apiVersion: v1
kind: Node
metadata:
  name: knok-node-1
  annotations:
    kwok.x-k8s.io/node: fake
    node.alpha.kubernetes.io/ttl: "0"
  labels:
    fgo-lab/managed: "true"
    kubernetes.io/role: worker
    node.kubernetes.io/instance-type: gpu-node
    run.ai/simulated-gpu-node-pool: g1
    type: kwok # KWOK label
spec:
  taints: 
    - effect: NoSchedule
      key: kwok.x-k8s.io/node # regular pods won't be scheduled on kwok nodes
      value: fake
status:
  allocatable:
    cpu: "32"
    memory: 128Gi
    pods: "110"
  capacity:
    cpu: "32"
    memory: 128Gi
    pods: "110"
  nodeInfo:
    architecture: amd64
    containerRuntimeVersion: fake
    kernelVersion: fake
    kubeProxyVersion: fake
    kubeletVersion: fake
    operatingSystem: linux
    osImage: fake
```

Then, a test workload looks like this:

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: kwok-pod
  namespace: fgo-lab-07
  annotations:
    run.ai/simulated-gpu-utilization: "40"
spec:
  nodeSelector:
    type: kwok
  tolerations:
    - key: kwok.x-k8s.io/node
      operator: Equal
      value: fake
      effect: NoSchedule
  containers:
    - name: main
      image: ubuntu:22.04
      command: ["sleep", "infinity"]
      resources:
        limits:
          nvidia.com/gpu: 2
```

FGO comes with a few KWOK plugins that are deployed as a Deployment per cluster:

| Deployment                                           | On by default                                 | Watches                                                                       | Writes                                                                                                            | Replaces                                              | Lab |
|------------------------------------------------------|-----------------------------------------------|-------------------------------------------------------------------------------|-------------------------------------------------------------------------------------------------------------------|-------------------------------------------------------|-----|
| `kwok-gpu-device-plugin`                             | yes                                           | Nodes' GPU state ConfigMaps (`topology-<node>`), **only when one is created** | Merge-patch of `Node.status` capacity and allocatable: `nvidia.com/gpu` = number of GPUs, plus any `otherDevices` | device-plugin DaemonSet, which reports to the kubelet | 07  |
| `nvidia-dcgm-exporter-kwok` (`status-exporter-kwok`) | yes, whenever `statusExporter.enabled` is set | Nodes' GPU state ConfigMaps: add, update, delete                              | One `:9400` for every KWOK node, `nvidia.com/gpu.*` node labels, and an NRT if enabled                            | per-node exporter DaemonSet                           | 07  |
| `kwok-dra-plugin`                                    | no                                            | Nodes' GPU state ConfigMaps: add, update, delete                              | ResourceSlice `kwok-<node>-gpu`, driver `gpu.nvidia.com`, one device per GPU (UUID, model, memory)                | `dra-plugin-gpu` DaemonSet                            | 08  |
| `kwok-compute-domain-dra-plugin`                     | no                                            | **Nodes**, not ConfigMaps                                                     | ResourceSlice `kwok-<node>-compute-domain-channel` with one device, `channel-0`                                   | compute-domain kubelet plugin                         | 09  |

KWOK's node topology `ConfigMaps` are no different to the normal CPU nodes.

The KWOK plugins are nothing else but servers that does some manipulations with Kubernetes API based on FGO's node topology `ConfigMaps`.

FGO doesn't use any experimental KWOK CRDs lik `ClusterExec`, `ClusterAttach`, `ClusterLogs`, etc.

#### Run:AI x Mokka Integration (Mock Backend)

FGO creates Mokka's profiles as `ConfigMaps` and use some information from there in the `fake` backend mode like:

- Product name,
- memory,
- GPU count,
- driver and CUDA versions

When the node pool is defined to be `mock`, FGO actually deploys a Mokka instance on the nodes it manages:

```yaml
cluster:
  nodePoolLabelKey: run.ai/simulated-gpu-node-pool
  migStrategy: none
  nodePools:
    a100-pool:
      gpu:
        backend: mock
        profile: a100
        overrides:
          device_count: 2
```

On the Mokka end, with [integrations.fakeGpuOperator.enabled](../../../deployments/nvml-mock/helm/nvml-mock/templates/integrations-fgo.yaml),
the Mokka chart creates publishes one ConfigMap per Mokka GPU profile like `gb300`, `h100`, `a100`, etc. in a way that FGO expects
(e.g. named `gpu-profile-<profile>`, with the profile under the `profile.yaml` key, in `integrations.fakeGpuOperator.targetNamespace`).
That's the only place Mokka knows about FGO.

### NVIDIA Ecosystem Integrations

FGO mocks or uses the following NVIDIA components:

- device-plugin
- DRA
- compute domains
- nvidia-smi
- DCGM
- MIG Manager

### Goals

- Propose an actionable plan for FGO incorporation into Mokka
- Reach the feature parity required to migrate Run:AI and KAI Scheduler off FGO

### Non-Goals

- Mokka remains to be a generic project, so all functionality very specific to Run:ai and KAI are not going to be directly included.
- MIG support is out of scope for this MEP.
- Fault injection in KWOK mode is out of scope for this MEP.
- Making virtual pod (e.g. a pod scheduled to a KWOK virtual node) SSH work is out of scope of this MEP.

## Proposal

Looking through the list of FGO features, there are three that Mokka should support:

- [KWOK Integration](#kwok-integration). Completely new functionality and the biggest piece of work.
- [Workload-aware dynamic metrics](#workload-aware-dynamic-metrics). Mokka supports dynamic metrics today with a couple of profile patterns (e.g. range, bust, idle, busy). That setting is on GPU level though, it's not workload-aware.
- [Numa Setup](#numa-setup). Mokka should simulate NUMA information according to the GPU rack profile that NFD uses for NRT resource creation.

The [NVIDIA component mocks](#nvidia-ecosystem-integrations) should be replaced by 
Mokka and the real NVIDIA components on top of it.

### User Stories (Optional)

<!--
Detail the things that people will be able to do if this MEP is implemented.
Include as much detail as possible so that people can understand the "how" of
the system. The goal here is to make this feel real for users without getting
bogged down.
-->

#### Story 1 (Optional)

#### Story 2 (Optional)

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

### KWOK Integration

Mokka can frame KWOK support as an alternative provisioning path for sGPU nodes, so we will end up having two modes:

- `CPU` (default), the current deployment mode that expects real CPU nodes. The control plane binds racks to existing CPU nodes, and the node daemon simulates the GPUs on them.
- `KWOK`, the new deployment mode based on KWOK virtual nodes. The KWOK provisioner creates a virtual Node for each logical rack Node, the KWOK data plane simulates what runs on it, and the GPUs exist only in the Kubernetes API.

The provisioner is set per rack group in `SGPUInventory`, so one inventory can mix CPU and KWOK rack groups:

```yaml
apiVersion: mokka.nvidia.com/v1alpha1
kind: SGPUInventory
metadata:
  name: dev
spec:
  rackGroups:
    - id: training
      provisioner: CPU # the default
      count: 10
      profileRef:
        name: gb300-nvl72

      placement: # CPU only: the existing Nodes that may host the racks
        nodeSelector:
          matchLabels:
            mokka.nvidia.com/sgpu: training

    - id: ci
      provisioner: KWOK
      count: 1000 # a thousand of gb300 racks
      profileRef:
        name: gb300-nvl72

      kwok: # KWOK only: settings for the Nodes the KWOK provisioner creates
        kubelet: # the configuration of the kubelet the Nodes simulate
          maxPods: 250
        nodeTemplate:
          labels:
            run.ai/simulated-gpu-node-pool: ci
          taints:
            - key: kwok.x-k8s.io/node # keeps regular pods off the virtual Nodes
              value: fake
              effect: NoSchedule
```

The KWOK's `ci` will create `rackGroup.count` × `profile.nodesPerRack` (18000 virtual nodes in this example).

There's no kubelet on a KWOK Node, so the KWOK provisioner reports what one would. `kwok.kubelet` holds the configuration it simulates, starting with `maxPods`, which sets each Node's pod capacity. These settings stay out of the profile because the same profile also backs CPU groups, where the real kubelet decides them.

The CRD validation enforces these rules on a rack group:

- `provisioner` is `CPU` or `KWOK`, defaults to `CPU`, and can't change, so a rack is handled by the same service for its whole life. To change a group's provisioner, replace the group with one under a new `id`.
- `placement` is allowed only on `CPU` groups, and `kwok` only on `KWOK` groups.
- `kwok.nodeTemplate.labels` can't set `mokka.nvidia.com/sgpu-node`, which would make the virtual Nodes eligible for CPU racks, or `mokka.nvidia.com/provisioner`, which Mokka sets itself.
- `kwok.kubelet.maxPods` is at least 1 and defaults to 110, the kubelet's default.

The Mokka Control Plane ([MEP0001](../0001-mokka-control-plane/README.md)) watches `SGPUInventory` and creates `SGPURacks` as usual. It copies `provisioner` and the `kwok` block from the rack group into each rack, and mirrors `provisioner` into a label (see [Separating CPU and KWOK Racks](#separating-cpu-and-kwok-racks)). 

For KWOK provisioning, `SGPURack` would look like this:

```yaml
apiVersion: mokka.nvidia.com/v1alpha1
kind: SGPURack
metadata:
  name: dev-ci-0-3f9c2a17b04e
  labels:
    mokka.nvidia.com/inventory: dev
    mokka.nvidia.com/rack-group: ci
    mokka.nvidia.com/rack-index: "0"
    mokka.nvidia.com/provisioner: KWOK # new: mirrors spec.provisioner
  annotations:
    mokka.nvidia.com/inventory-uid: <inventory UID>
  finalizers:
    - mokka.nvidia.com/rack-cleanup
  ownerReferences:
    - apiVersion: mokka.nvidia.com/v1alpha1
      kind: SGPUInventory
      name: dev
      uid: <inventory UID>
      controller: true
      blockOwnerDeletion: false
spec:
  provisioner: KWOK # new: copied from the rack group
  kwok: # new: copied from the rack group
    kubelet:
      maxPods: 250
    nodeTemplate:
      labels:
        run.ai/simulated-gpu-node-pool: ci
      taints:
        - key: kwok.x-k8s.io/node
          value: fake
          effect: NoSchedule
  inventoryRef:
    name: dev
    uid: <inventory UID>
  profileRef:
    name: gb300-nvl72
    uid: <profile UID>
    generation: 1
    revision: <profile revision>
  identity:
    rackGroup: ci
    rackIndex: 0
    fabricUUID: <fabric UUID>
    cliqueID: 0
  nodes:
    - index: 0 # no nodeRef: KWOK racks are never bound to existing Nodes
      gpus:
        - index: 0
          uuid: GPU-<UUID>
          serial: "<serial>"
          minorNumber: 0
          pciAddress: "0008:01:00.0"
          rootComplex: pci0008:00
          numaNode: 0
          hostProcessorIndex: 0
        # GPUs 1-3
    # Nodes 1-17
```

KWOK racks are rendered exactly like CPU racks, so their simulated GPUs keep stable UUIDs, serials, PCI addresses, and NUMA placement. 
Because the `kwok` block is in the rack, [the KWOK provisioner](#kwok-provisioner) doesn't need to watch inventories: a change to it re-renders the racks, and the provisioner sees the rack updates.

#### KWOK Components

KWOK mode adds two components, split by whether what they simulate is fixed for a Node's life or changes while the Node runs:

![Mokka x KWOK Integration](./img/mokka-kwok-integration.png)

|               | [KWOK provisioner](#kwok-provisioner)                     | [KWOK data plane](#kwok-data-plane)                                                         |
|---------------|-----------------------------------------------------------|---------------------------------------------------------------------------------------------|
| Owns          | what's fixed for a Node's life, derived from its rack     | what changes while the Node runs                                                            |
| Writes        | Nodes (labels, taints, capacity) and their ResourceSlices | GPU allocation on Pods, NodeResourceTopology                                                |
| Serves        | nothing                                                   | DCGM metrics                                                                                |
| Load follows  | rack and profile changes                                  | Pods, scrapes and runtime views                                                             |
| Runs as       | `Deployment` with leader election                         | `StatefulSet` sharded by rack                                                               |
| Stands in for | Node registration                                         | the per-Node agents: the kubelet's device manager, NFD's topology updater and dcgm-exporter |

The split follows load and ownership:

- **Load.** The provisioner's work follows racks and profiles, which change rarely, so one replica under leader election is enough. The data plane's work grows with the test: an allocation and a NodeResourceTopology update for every GPU Pod, and 216,000 metric series for the example inventory's 72,000 GPUs. It scales by sharding racks across replicas.
- **Ownership.** Per-Node state needs one writer per Node, not one for the whole cluster. Sharding gives each Node exactly one owning replica, as a real cluster runs one kubelet and one topology updater per Node, so only cluster-wide work needs a leader.
- **Failures.** A Pod burst or a slow scrape can't stall Node creation, and a provisioner failover doesn't interrupt allocation or metrics.
- **Future surfaces.** GPU health, MIG reconfiguration, ComputeDomain readiness and InfiniBand counters all change while a Node runs, so they'll join the data plane instead of adding components.

#### KWOK Provisioner

We will introduce a new component called the KWOK provisioner as an optional `Deployment` named `mokka-kwok-provisioner`.


The responsibility will be:

- watching `SGPURack` resources that belong to KWOK provisioning
- creating KWOK `Node` resources like:

```yaml
apiVersion: v1
kind: Node
metadata:
  name: dev-ci-0-0 # format: {inventory_name}-{rack_group_id}-{rack_index}-{node_index}
  annotations:
    kwok.x-k8s.io/node: fake # matched by KWOK's default annotation selector
  labels:
    kubernetes.io/hostname: dev-ci-0-0
    kubernetes.io/os: linux
    kubernetes.io/arch: arm64 # from profile node.host.cpu.architecture
    mokka.nvidia.com/provisioner: KWOK
    mokka.nvidia.com/inventory: dev
    mokka.nvidia.com/rack-group: ci
    mokka.nvidia.com/rack-index: "0"
    mokka.nvidia.com/node-index: "0"
    nvidia.com/gpu.clique: <fabric UUID> # the value the control plane distributes
    run.ai/simulated-gpu-node-pool: ci # from spec.kwok.nodeTemplate
    nvidia.com/gpu.deploy.operands: false # prevents GPU Operator to try to schedule daemonsets on virtual node
    # Plus, NFD/GFD labels
    # ...
  ownerReferences:
    - apiVersion: mokka.nvidia.com/v1alpha1
      kind: SGPURack
      name: dev-ci-0-3f9c2a17b04e
      uid: <rack UID>
      controller: true
      blockOwnerDeletion: false
spec:
  taints:
    - key: kwok.x-k8s.io/node
      value: fake
      effect: NoSchedule
status:
  capacity:
    cpu: "144" # from profile node.host.cpu.cores
    memory: 960Gi # from profile node.host.memory.capacity
    pods: "250" # from spec.kwok.kubelet.maxPods
    nvidia.com/gpu: "4" # from profile node.gpus.count
  allocatable:
    cpu: "144"
    memory: 960Gi
    pods: "250"
    nvidia.com/gpu: "4"
  nodeInfo:
    architecture: arm64
    operatingSystem: linux
    kubeletVersion: fake
```

- creating each Node's DRA `ResourceSlice`, owned by the Node, with one device per GPU in the rack
- leaving Node deletion to the Kubernetes garbage collector: each Node is owned by its `SGPURack`, so the Nodes go away with the rack.

Node names are the rack coordinates joined by hyphens, `<inventory>-<rack group>-<rack index>-<node index>`, as in `dev-ci-0-0`:

```
dev-ci-0-0
│   │  │ └─ node index within the rack (0–17 for an 18-Node rack)
│   │  └─── rack index within the group (0–999 in this example)
│   └────── rack group ID
└────────── inventory name
```

Unlike rack names, Node names carry no hash suffix: users see them in `kubectl get nodes` and scheduler logs, and tests can pin a pod with `kubernetes.io/hostname: dev-ci-0-0` without looking anything up. Three rules keep the names valid and unique:

- **Length.** The name is also the `kubernetes.io/hostname` label, which the fractional-GPU `podAffinity` in [Resource Reservation & GPU Fractions](#resource-reservation--gpu-fractions) uses as its topology key, so it must fit in 63 characters. Rack indexes have at most 5 digits and node indexes at most 4, so the CRD rejects a KWOK group whose inventory name and group ID together exceed 51 characters.
- **Uniqueness.** The control plane rejects a KWOK group whose `<inventory>-<rack group>` matches that of another KWOK group in any inventory, as `dev` with group `ci-x` and `dev-ci` with group `x` would. The last two parts of every name are numbers, so different prefixes always give different names.
- **Replacement.** If a Node with the name already exists and isn't owned by the rack, the KWOK provisioner waits for it to be deleted. That happens when an inventory is recreated under the same name before its old Nodes are gone. The provisioner never takes over a Node it didn't create, such as a real Node with the same name, and emits an Event on the rack instead.

Because inventory names and group IDs can contain hyphens, tools identify a Node by its `mokka.nvidia.com/*` labels or its owner reference rather than by parsing the name.

Capacity comes from SGPURackProfile's `node.host`, so the profile of a KWOK group must define the host CPU cores and memory.
`allocatable` stays at the total as pods come and go: the scheduler subtracts pod requests itself, so lowering it would count them twice.

#### KWOK Data Plane

We will introduce a second component, the KWOK data plane, as an optional `StatefulSet` named `mokka-kwok-data-plane`. It simulates what runs on a KWOK Node for as long as the Node exists: it allocates GPUs to the Pods bound to the Node, keeps the Node's NodeResourceTopology current, and serves DCGM metrics for its GPUs. It takes over FGO's `nvidia-dcgm-exporter-kwok`, which serves these metrics and writes NodeResourceTopology; the `nvidia.com/gpu.*` labels that exporter also writes come from the [KWOK provisioner](#kwok-provisioner). Each replica owns a share of the racks, as [Sharding](#sharding) describes.

```mermaid
flowchart LR
    cp["Control plane"]

    subgraph api["Kubernetes API"]
        racks["KWOK SGPURacks<br/>and their profiles"]
        pods["Pods on KWOK Nodes"]
        nrt["NodeResourceTopology"]
    end

    dp["mokka-kwok-data-plane<br/>replicas 0 to N-1,<br/>each owning its own racks"]
    svc["Service mokka-kwok-dcgm-exporter<br/>port 9400"]
    prom["Prometheus"]
    runai["Run:ai cluster operator"]

    dp -->|"watches"| racks
    dp -->|"allocates GPUs,<br/>records them on the Pod"| pods
    dp -->|"keeps availability current"| nrt
    dp -->|"fetches runtime views<br/>on generation bump"| cp
    svc -->|"routes to"| dp
    prom -->|"scrapes each replica"| dp
    runai -->|"checks /metrics"| svc
```

##### GPU Allocation

There's no kubelet on a KWOK Node, so the data plane allocates GPUs to the Pods bound to the Nodes of its racks, as the kubelet's device manager would, and records the allocation on the Pod:

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: train-0
  namespace: team-a
  annotations:
    mokka.nvidia.com/gpu-allocation: '{"main":["GPU-<UUID>","GPU-<UUID>"]}' # GPU UUIDs per container
spec:
  nodeName: dev-ci-0-0
```

`NodeResourceTopology` availability and the `pod` labels on DCGM metrics both come from this record. It covers `nvidia.com/gpu` requests and DRA claims alike, as the kubelet's PodResources API does: for a DRA claim, the data plane copies the devices the scheduler allocated. The record lives on the Pod rather than in memory, so a replica that restarts or takes over a rack reads the allocations back instead of recomputing them, which could move a running Pod to another GPU once earlier Pods are gone.

##### NodeResourceTopology

The data plane creates a `NodeResourceTopology` for each Node of its racks and keeps each zone's available GPUs current from the allocations, as NFD's topology updater does on a real Node. Zones and their capacity come from the rack's NUMA placement and the profile, so they match what NFD reports for a CPU Node with the same profile ([NUMA Setup](#numa-setup)).

##### Runtime State

The data plane applies the effective runtime view, as the node daemon does on CPU racks, but it doesn't compute it. The effective runtime view is a GPU's profile defaults with the accepted `SGPURuntimePolicy` overrides applied. [MEP0001](../0001-mokka-control-plane/README.md#runtime-view-computation-and-delivery) gives policy acceptance and this computation to the control plane, which computes the view on request and never stores it. The data plane reads the view for telemetry settings now, and for GPU health once fault injection is in scope. The provisioner never needs it: nothing it writes depends on runtime state.

The data plane takes each input from where it's published:

- **Stored state comes from Kubernetes.** Racks, including their delivery status, and the profiles they use arrive through the watches listed in [Separating CPU and KWOK Racks](#separating-cpu-and-kwok-racks). Profiles contribute static facts such as GPU model and memory, never `defaults.runtime`.
- **The effective runtime view comes from the control plane API**, one rack at a time.
- **Policy objects aren't watched.** No KWOK component watches `SGPUInventory` or `SGPURuntimePolicy`. The data plane could compile policies with `internal/sgpu/policy`, which makes no API calls, but it would then be a second component deciding which policies apply. While the two caches disagree, it could apply a policy the control plane reports as rejected, or one already deleted.

The control plane API serves the view per rack:

```http
GET /v1alpha1/racks/{rackName}/runtime?rackUID=<rack UID>&minRackDeliveryGeneration=13
```

```json
{
  "apiVersion": "v1alpha1",
  "rack": {"name": "dev-ci-0-3f9c2a17b04e", "uid": "<rack UID>"},
  "rackDeliveryGeneration": 13,
  "nodes": [
    {
      "index": 0,
      "gpus": [
        {
          "index": 0,
          "runtime": {
            "deviceState": "Healthy",
            "telemetry": {
              "utilization": {
                "mode": "Pattern",
                "pattern": {"type": "Bursty", "gpuPercent": {"minimum": 20, "maximum": 90}}
              }
            }
          }
        }
      ]
    }
  ]
}
```

The response holds the effective `RuntimeState` of every GPU in the rack, grouped by logical Node (one of each is shown), and the delivery generation it was computed from. The data plane passes the generation it last saw on the rack as the minimum. A replica whose cache is behind returns not-ready (`503`), and the data plane retries with backoff while it keeps the values it applied last. This is MEP0001's publication-floor check, applied per rack. KWOK racks are never bound to a Node, so the request carries no assignment revision. For the same reason, the per-Node endpoint the node daemon polls doesn't fit: it serves Nodes the control plane has assigned, and a KWOK Node never has an assignment.

The rack tells the data plane when to fetch. The control plane leader bumps a rack's delivery generation whenever anything feeding its effective view changes: a policy that selects the rack is added, edited or deleted, the set of accepted policies changes, or the profile's runtime defaults change. For each rack UID, the data plane keeps the generation it last applied and fetches the rack whenever the generation in Kubernetes is ahead. The check compares two numbers, so a missed watch event only delays a fetch, and after a restart a replica fetches each of its racks once. A rack recreated under the same name has a new UID and starts a new generation sequence, so it counts as a new rack.

```mermaid
sequenceDiagram
    actor Admin
    participant API as Kubernetes API
    participant Leader as Control plane leader
    participant Replica as Control plane replica
    participant DP as KWOK data plane

    Admin->>API: apply SGPURuntimePolicy
    API-->>Leader: policy event
    Leader->>API: accept policy, bump status.delivery of affected racks to 13
    API-->>DP: rack event, generation 13
    DP->>Replica: GET /v1alpha1/racks/{rack}/runtime?minRackDeliveryGeneration=13
    alt replica is at generation 13 or later
        Replica-->>DP: effective RuntimeState per GPU
        DP->>DP: apply it, remember generation 13
    else replica is behind
        Replica-->>DP: 503 not ready
        DP->>DP: retry with backoff, keep serving generation 12
    end
```

Values that change on their own never trigger a fetch. The view carries settings, such as a `Bursty` utilization pattern and its range, and [DCGM Metrics](#dcgm-metrics) describes how the data plane turns them into values.

This puts two requirements on the control plane:

- **Delivery status covers KWOK racks.** The control plane drops KWOK groups before allocation, but it still maintains `status.delivery` on their racks. Without the generation bump, the data plane has no trigger and would have to poll.
- **A per-rack endpoint** next to the per-Node one. Both serve views from the same computation.

Policy-driven telemetry depends on that endpoint. Node creation, labels, ResourceSlices, GPU allocation and NodeResourceTopology need no runtime state and don't wait for it.

##### DCGM Metrics

The data plane serves DCGM metrics for the GPUs of its racks through a `Service` named `mokka-kwok-dcgm-exporter` on port `9400`. The Service keeps a DCGM exporter's name because consumers look for one, as the [Run:ai cluster operator](#kai-use-cases) does. The metrics are the three fields that check needs:

| Metric                 | Value                                                                   |
|------------------------|-------------------------------------------------------------------------|
| `DCGM_FI_DEV_GPU_UTIL` | GPU utilization in percent, generated from the GPU's telemetry settings |
| `DCGM_FI_DEV_FB_USED`  | Frame buffer memory in use, in MiB                                      |
| `DCGM_FI_DEV_FB_FREE`  | Frame buffer memory free, in MiB                                        |

Each series carries the labels dcgm-exporter uses. `gpu`, `UUID`, `device`, `pci_bus_id` and `modelName` come from the rack and its profile, `Hostname` from the KWOK Node, and `namespace`, `pod` and `container` from the [GPU allocation](#gpu-allocation) while a Pod holds the GPU:

```prometheus
DCGM_FI_DEV_GPU_UTIL{gpu="0",UUID="GPU-<UUID>",device="nvidia0",pci_bus_id="00000008:01:00.0",modelName="NVIDIA GB300 NVL",Hostname="dev-ci-0-0",namespace="team-a",pod="train-0",container="main"} 87
```

`Hostname` is the GPU's KWOK Node name. On a GPU Node, all series from one dcgm-exporter instance share a `Hostname`, because the instance serves only that Node. The data plane serves many Nodes, so it keeps one value per Node by using the Node name. The chart's optional `ServiceMonitor` also sets `instance` from `Hostname`, which gives each KWOK Node its own `instance`, as with per-Node exporters.

`DCGM_FI_DEV_GPU_UTIL` follows the GPU's telemetry settings from the [runtime view](#runtime-state). The settings say how the value moves, such as a `Bursty` pattern between 20 and 90 percent, and the data plane's metrics generator turns them into values, as the NVML engine does on CPU Nodes. Both use one generator package, so a policy produces the same series on both kinds of Node. Today the generator is internal to `pkg/gpu/mocknvml/engine`, which uses cgo, and it implements `steady`, `idle`, `busy` and `burst` rather than the `Steady`, `Bursty` and `Wave` patterns the CRD defines. Moving it into its own package, with the CRD's patterns, is part of this work.

TODO: Is the current way we simulate dynamic metrics flexible/extensible enough to build upon it?

`DCGM_FI_DEV_FB_USED` and `DCGM_FI_DEV_FB_FREE` split the GPU memory in the profile and stay constant, as on CPU Nodes, where the NVML engine reports the profile's memory figures unchanged. The data plane generates values for every GPU, with or without a Pod, as the NVML engine does. [Workload-aware Dynamic Metrics](#workload-aware-dynamic-metrics) covers values that follow the Pod.

**Open question: one DCGM exporter Service.** The Run:ai cluster operator expects exactly one DCGM exporter Service. An inventory that mixes CPU and KWOK racks has two: the GPU Operator's for CPU Nodes and this one.

##### Sharding

Each rack has exactly one owning replica, so the data plane needs no leader election even though it writes. Each replica works out its own share of racks, as kube-state-metrics does for its automated sharding:

- A replica takes its ordinal from its Pod name and the replica count from its `StatefulSet`'s `spec.replicas`, which it watches, so `kubectl scale` reshards without a restart.
- For each rack, a replica computes `hash(rack UID, ordinal)` for every ordinal and owns the rack if its own ordinal scores highest. Adding a replica moves only the racks the new one wins, about 1/N of them, where `hash mod N` would move most, and each moved rack costs a runtime fetch.
- It drops other replicas' racks, Pods and claims in its list-watch, before they reach its cache, and fetches runtime views only for its own racks.
- `podManagementPolicy: Parallel` starts all replicas together.

The rack is the shard unit because it's also the unit of the runtime fetch and the delivery generation, so everything about a rack stays in one replica. With eight replicas, each owns 2,250 Nodes and 9,000 GPUs of the example inventory and serves 27,000 series.

The scheme accepts three limits:

- **Overlap while scaling.** Replicas see a new count at slightly different moments, so a few racks are briefly owned twice or not at all. Two owners could both allocate GPUs to a new Pod, so the data plane writes an allocation only if the Pod has none, with the Pod's `resourceVersion` as a precondition: the first write wins. `NodeResourceTopology` is recomputed from the allocations, so it converges.
- **Gaps on restart.** While a replica restarts, new Pods on its racks wait for an allocation, their NodeResourceTopology goes stale, and their GPUs have no metrics, as on a real Node whose kubelet, topology updater or dcgm-exporter restarts.
- **No empty replicas.** A replica without racks serves no DCGM series, and a Run:ai check that reaches it through the `Service` fails. The chart defaults to one replica, and the count should stay well below the number of racks.

#### Separating CPU and KWOK Racks

Each service lists and watches only the objects it needs, and the API server applies the selectors, so little else reaches the service's cache. The KWOK data plane's Pod watch is the exception: a field selector matches one Node name, not a set. The control plane's filters exist today; those of the KWOK components are new:

| Service          | Resource                                       | Server-side selector                  | Result                                                    |
|------------------|------------------------------------------------|---------------------------------------|-----------------------------------------------------------|
| Control plane    | `SGPUInventory`, `SGPURackProfile`, `SGPURack` | none                                  | creates and retires every rack                            |
| Control plane    | `Node`                                         | `mokka.nvidia.com/sgpu-node=true`     | never receives KWOK Nodes, which never carry the label    |
| KWOK provisioner | `SGPURack`                                     | `mokka.nvidia.com/provisioner=KWOK`   | never receives CPU racks                                  |
| KWOK provisioner | `Node`                                         | `mokka.nvidia.com/provisioner=KWOK`   | receives only the Nodes it created                        |
| KWOK provisioner | `SGPURackProfile`                              | none                                  | a handful of small objects                                |
| KWOK data plane  | `SGPURack`                                     | `mokka.nvidia.com/provisioner=KWOK`   | keeps the racks it owns                                   |
| KWOK data plane  | `SGPURackProfile`                              | none                                  | a handful of small objects                                |
| KWOK data plane  | `Pod`                                          | `spec.nodeName!=`                     | receives every bound Pod, keeps those on its racks' Nodes |
| KWOK data plane  | `ResourceClaim`                                | none                                  | keeps the claims allocated on its racks' Nodes            |
| KWOK data plane  | `StatefulSet`                                  | `metadata.name=mokka-kwok-data-plane` | reads its own replica count                               |

- **The label mirrors the spec.** `spec.provisioner` is the source of truth. The control plane copies it to the `mokka.nvidia.com/provisioner` label on every rack and restores the label if someone edits it. The KWOK provisioner skips a rack whose spec says otherwise and ignores racks that leave its watch, since only the garbage collector deletes Nodes.
- **Each object has one writer.** The control plane writes `SGPURack`, including its status. The KWOK provisioner writes the Nodes and ResourceSlices it creates. The KWOK data plane writes the GPU allocation on Pods and the NodeResourceTopology of the Nodes in its racks.
- **KWOK groups skip placement.** A group without `placement` matches every eligible Node, so the control plane drops KWOK groups before allocation, Node event routing, and usage reporting. They still count towards capacity and the rack group limit.
- **The node daemon stays off KWOK Nodes.** Its DaemonSet tolerates every taint, so the chart adds the node affinity `mokka.nvidia.com/provisioner NotIn [KWOK]`, which unlabeled CPU nodes still match.

A CRD field selector on `spec.provisioner` (`selectableFields`) would make the label unnecessary, but field selectors on custom resources are on by default only from Kubernetes 1.31, while the Mokka CRDs support 1.28. Once Mokka's minimum reaches 1.32, where the feature is GA, the label can go.

#### GPU Footprint in KWOK Virtual Nodes

KWOK nodes are completely virtual entities. The real NVIDIA components can run or interact with in most cases.
Still we would like to simulate a few surfaces based on `SGPURackProfile` information to make them look like a real GPU nodes from outside:

- Add NFD/GFD labels e.g. `nvidia.com/gpu.*`, `nvidia.com/mig.strategy`, `feature.node.kubernetes.io/pci-10de.present`
- DRA ResourceSlices resources
- NodeResourceTopology resources for NUMA-aware workload placing
- DCGM metrics

The KWOK provisioner creates the labels and ResourceSlices with each Node, since they're fixed for its life. The [KWOK data plane](#kwok-data-plane) creates the NodeResourceTopology resources and serves the DCGM metrics, since both change while the Node runs.

We should put generation of CRs under feature gates, 
so cluster admins don't need to install all CRDs just to try out KWOK integration.

#### KWOK Installation

The KWOK functionality in Mokka should be disabled by default.
Mokka helm chart will get a new flag `kwok.enabled`, based on which we will deploy or not the KWOK provisioner and the [KWOK data plane](#kwok-data-plane). `kwok.dataPlane.enabled: false` leaves the data plane out, so KWOK Nodes carry only their fixed footprint. `kwok.dataPlane.replicas` sets its number of [shards](#sharding), and `kwok.dataPlane.serviceMonitor.enabled` adds a Prometheus Operator `ServiceMonitor` for its DCGM metrics.

The KWOK is assumed to be installed by cluster admins prior to enabling Mokka's KWOK functionality. That's gonna be documented.
KWOK chart is not gonna be explicitly included into the Mokka helm chart.

#### KAI Use Cases

| Consumer                | Required resources or behavior                                                                                                                                                                                                                       | How Mokka Can Help?                                                                                                                       |
|-------------------------|------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|-------------------------------------------------------------------------------------------------------------------------------------------|
| Run:ai installer        | The `clusterpolicies.nvidia.com` CRD and a `gpu.*operator` Deployment in `gpu-operator`. No `ClusterPolicy` instance is necessary.                                                                                                                   | ✅KAI Test Setup should install GPU Operator CRD                                                                                           |
| Run:ai cluster operator | Exactly one DCGM exporter Service. Its `/metrics` must contain `DCGM_FI_DEV_GPU_UTIL`, `DCGM_FI_DEV_FB_USED`, and `DCGM_FI_DEV_FB_FREE`. A failed check sets `DependenciesFulfilled=False`. This does not apply when dependency checks are disabled. | In KWOK mode, the [KWOK data plane](#kwok-data-plane)'s DCGM exporter Service. A mixed CPU and KWOK inventory has two (open question)     |
| KAI whole-GPU           | `nvidia.com/gpu` and the node labels `nvidia.com/gpu.count` and `nvidia.com/gpu.memory`                                                                                                                                                              | Mokka + real device plugin and GFD. In KWOK mode, the KWOK provisioner sets the `nvidia.com/gpu` capacity and the labels from the profile |
| KAI fractions           | A GPU UUID in `run.ai/reserve_for_gpu_index` on each reservation pod. `NvFractions` mode also requires a ready `GpuFractioningConfig` named `default` (fractional GPU documentation).                                                                | ✅Should work with Mokka out of the box                                                                                                    |
| KAI NUMA                | A current `NodeResourceTopology` for each node. It contains policy, scope, zones, and per-zone availability. Availability changes with allocation and release.                                                                                       | Mokka node daemon does [NUMA setup](#numa-setup) + real NFD deployment. In KWOK mode, the KWOK data plane creates NRT resources           |
| DRA                     | Claims with the same behavior on KWOK nodes and nodes that use the GPU Operator's DRA driver.                                                                                                                                                        | Mokka + real DRA deployment. In KWOK mode, the KWOK provisioner creates `ResourceSlice` resources                                         |

### Workload-aware Dynamic Metrics

TBU

### NUMA Setup

On a CPU Node, Mokka doesn't create the `NodeResourceTopology`. 
The node daemon gives the Node the NUMA layout of its GPU profile, and the real kubelet and NFD topology updater produce the NRT from it. 

```mermaid
flowchart LR
    subgraph kubelet["<b>kubelet</b> admission, per container"]
        direction LR
        cpu["CPU Manager<br/>hints only when static"]
        mem["Memory Manager<br/>hints only when Static"]
        dev["Device Manager<br/>NUMA node from<br/>device plugin"]
        tm["<b>Topology Manager</b><br/><code>single-numa-node</code>"]
        admit(["admit"])
        reject(["reject"])

        cpu --> tm
        mem -->|"hints"| tm
        dev --> tm
        tm --> admit
        tm --> reject
    end

    subgraph scheduler["<b>kube-scheduler</b>"]
        filter["<code>NodeResourceTopologyMatch</code><br/>filters nodes by zone"]
    end

    nfd["<b><code>nfd-topology-updater</code></b><br/>reads PodResources,<br/>the NUMA tree and<br/>kubelet's config file"]
    nrt["<b>NodeResourceTopology</b><br/>one zone per NUMA node,<br/>each with capacity and<br/>free cpu, memory, GPUs"]

    scheduler ~~~ nrt
    nrt ~~~ nfd
    scheduler -->|"binds pod"| kubelet
    kubelet -->|"PodResources API"| nfd
    nfd -->|"publishes"| nrt
    nrt -->|"read by the filter"| scheduler

    classDef focus fill:#e0e7ff,stroke:#6366f1,stroke-width:2px,color:#000
    class tm,nrt focus
```

Jonathan Rosenboim has prototyped this idea in ([#1147](https://github.com/NVIDIA/k8s-test-infra/pull/1147), [#1148](https://github.com/NVIDIA/k8s-test-infra/pull/1148)).

Each part of the NRT traces back to something Mokka has to put in place on the Node:

| In the NRT | NFD reads it from | Mokka provides |
|---|---|---|
| Zones and their costs | The NUMA tree, `/sys/devices/system/node` | A zone for each NUMA node of the profile's PCIe root complexes, numbered from 0. Distance 10 within a zone and 21 between zones |
| `nvidia.com/gpu` per zone | PodResources. The kubelet takes each GPU's zone from the device plugin, which reads the GPU's PCI `numa_node` | Each GPU's `numa_node` set to its root complex's zone. Its `local_cpulist` and NVML CPU affinity name that zone's CPUs |
| `cpu` per zone | PodResources, only under the static CPU Manager. The NUMA tree maps CPU IDs to zones | The Node's real CPU IDs, split across zones without separating a core's hyperthreads, or the profile's per-zone lists. The kubelet's static CPU Manager |
| `memory` per zone | PodResources, only under the static Memory Manager. Capacity comes from `/sys/bus/node` | Host memory, split evenly across zones. The kubelet's static Memory Manager, and the memory-binding rewrite below |
| `topologyManagerPolicy` and `topologyManagerScope` attributes | The kubelet's config file | The policy, e.g. `single-numa-node`, and the scope, set on the kubelet |
| Availability | PodResources, rescanned on pod changes | Nothing. It's the kubelet's real allocations |

The NUMA trees look like this for a two-zone profile with GPU 0 in zone 0, on a host with 8 cores and 2 hyperthreads per core (CPUs *N* and *N*+8 are siblings). `[Mokka]` marks what the node daemon adds, `[existing]` what Mokka already serves, and `[kernel]` what stays as the kernel has it:

```text
/sys
├── devices/system/
│   ├── node/                                   [Mokka] replaces the kernel's directory (1)
│   │   ├── online, possible, has_cpu, …        0-1 (2)
│   │   ├── node0/                              zone 0
│   │   │   ├── cpu0 → ../../cpu/cpu0           CPUs 0-3 and their hyperthreads 8-11 (3)
│   │   │   ├── …
│   │   │   ├── cpulist                         0-3,8-11, read by KAI
│   │   │   ├── cpumap                          the same CPUs as a hex mask
│   │   │   ├── distance                        10 21, the zone's NRT costs (4)
│   │   │   ├── meminfo                         Node 0 MemTotal: host's ÷ 2 (4)
│   │   │   ├── memory0 → ../../memory/memory0  half the real blocks (5)
│   │   │   ├── …
│   │   │   └── hugepages/                      empty (4)
│   │   └── node1/                              zone 1: CPUs 4-7,12-15, distance 21 10
│   ├── cpu/                                    [kernel] what the cpu<K> links point at (6)
│   └── memory/                                 [kernel] what the memory<M> links point at
└── bus/
    ├── node/devices/                           [Mokka] (7)
    │   ├── node0 → ../../../devices/system/node/node0
    │   └── node1 → ../../../devices/system/node/node1
    └── pci/devices/0000:18:00.0/               [existing] GPU 0 (8)
        ├── numa_node                           0
        └── local_cpulist                       0-3,8-11

/proc
├── meminfo                                     [kernel] MemTotal, split across the zones
└── <pid>/status                                [kernel] Mems_allowed_list (9)
```

1. Mounted read-only in the Node's mount namespace. The kubelet reads it directly, and NFD reads it through its hostPath mount of `/sys`. A pod sees it only if it mounts the Node's `/sys`.
2. They list the zone IDs. Real kernels have these files, but neither the kubelet nor NFD reads them.
3. The links point at the real CPU directories, so `online`, `core_id`, `physical_package_id` and the caches stay the kernel's. The kubelet and NFD build cores from `core_id`, so a core's hyperthreads have to stay in one zone. Otherwise the kubelet counts that core as two.
4. Every zone needs `distance`, `meminfo` with a `MemTotal` line, and a `hugepages/` directory, even an empty one. If a zone lacks one, NFD stops reading zones there and publishes only the ones before it, without an error. The kubelet tolerates a missing file, and that zone just loses the data. But if it can't parse a file, it can't detect the Node's CPUs and doesn't start.
5. Nothing in the kubelet or the NRT depends on these. They're there because a real kernel has them.
6. The kernel's own `cpu<K>/node<N>` links stay and point at the real zones. A tool that looks up a CPU's zone from the CPU side gets the kernel's answer, not Mokka's. The kubelet and NFD only look from the zone side.
7. These links point into the tree above, as on a real kernel, so NFD reads memory capacity from the same `meminfo` and `hugepages/`. Without them, NFD sees only the kernel's own NUMA nodes, usually just `node0`. Those resolve into Mokka's tree, so only they get the simulated capacity, and the other zones fall back to their allocatable. On kernels without NUMA support this directory doesn't exist, and NFD's topology updater exits at startup.
8. Mokka already serves these in containers that get its CDI spec, the device plugin included.
9. It still lists the kernel's memory nodes, which is why memory binding falls back to them. See the constraints below.

The kubelet and NFD each read the layout only once, when they start, so the order matters:

```mermaid
sequenceDiagram
    participant D as Mokka node daemon
    participant S as NUMA trees
    participant K as kubelet
    participant P as device plugin
    participant N as nfd-topology-updater
    participant R as NodeResourceTopology

    D->>S: publish the profile's zones
    D->>K: configure NUMA-aware managers and restart
    K->>S: read the zones once, at startup
    P->>K: register the GPUs with their NUMA nodes
    N->>S: read the zones and memory once, at startup
    N->>K: read allocatable resources once, at startup
    N->>R: create
    loop on every pod change
        N->>K: list pod resources
        N->>R: update zone availability
    end
```

So a layout change means restarting the kubelet, and any layout or GPU change means restarting NFD.

Three constraints shape how Mokka does this:

- **The trees cover the whole Node, and only that Node.** They replace the kernel's view for every process on the Node, not only inside NFD's container. Otherwise the kubelet and the NRT disagree. They must not leak to other Kind Nodes on the same kernel. On kernels without NUMA support, such as Docker Desktop's on Apple Silicon, the trees have to be added rather than replaced.
- **Memory binding falls back to the kernel's real memory nodes.** The static Memory Manager binds containers to the memory of zones the kernel doesn't have, and such containers fail to start. Mokka's NRI plugin rewrites the binding to the memory nodes the kernel allows. The kubelet's memory placement is simulated; physical memory locality isn't.
- **Policy changes need a drained Node.** Changing a manager policy invalidates the kubelet's allocation checkpoints. Mokka can configure and restart the kubelet itself, or leave that to whoever provisions the Node, e.g. through the Kind config.

These steps change the Node itself: its sysfs trees and the kubelet configuration. So NUMA setup is opt-in and meant for disposable test Nodes. It survives Mokka pod restarts and is undone only when it's explicitly disabled. While it's active on a Node, it has to be undone before a different layout can be applied.

KWOK Nodes have no kubelet or NFD. The [KWOK data plane](#noderesourcetopology) writes their NRT from the same layout, taking each GPU's NUMA node from the rack.

## Drawbacks

<!--
Why should this MEP _not_ be implemented?
-->

## Alternatives

<!--
What other approaches did you consider, and why did you rule them out? These do
not need to be as detailed as the proposal, but should include enough
information to express the idea and why it was not acceptable.
-->
