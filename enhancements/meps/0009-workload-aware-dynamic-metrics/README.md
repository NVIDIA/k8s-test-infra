# MEP-0009: Workload-aware Dynamic Metrics

Author: [Roman Hlushko](https://github.com/roma-glushko)

<!-- toc -->
- [Summary](#summary)
- [Motivation](#motivation)
  - [Background](#background)
    - [Metrics](#metrics)
    - [Resource Reservation & GPU Fractions](#resource-reservation--gpu-fractions)
  - [Goals](#goals)
  - [Non-Goals](#non-goals)
- [Proposal](#proposal)
  - [User Stories](#user-stories)
  - [Notes/Constraints/Caveats (Optional)](#notesconstraintscaveats-optional)
  - [Risks and Mitigations](#risks-and-mitigations)
- [Design Details](#design-details)
  - [Workload-aware Dynamic Metrics](#workload-aware-dynamic-metrics)
    - [CPU Nodes: Workload Tracker](#cpu-nodes-workload-tracker)
    - [Pod-to-GPU Binding](#pod-to-gpu-binding)
    - [KWOK Nodes: Data Plane](#kwok-nodes-data-plane)
- [Drawbacks](#drawbacks)
- [Alternatives](#alternatives)
<!-- /toc -->

## Summary

This MEP makes simulated GPU metrics follow the Pods that use each GPU. A Pod sets its GPUs' utilization and memory use through annotations.

- **CPU Nodes:** the workload tracker, today's allocation watcher, matches Pods to GPUs and turns their annotations into settings for the NVML engine, which dcgm-exporter, `nvidia-smi` and other NVML clients read.
- **KWOK Nodes:** the [KWOK data plane](../0008-fgo-integration/README.md#kwok-data-plane) of MEP0008 does the same in memory and serves the values as DCGM metrics.

This proposal is a part of [Run:ai Fake GPU Operator (FGO) incorporation](../0008-fgo-integration/README.md).

## Motivation

We would like to be able to specify metric simulation information on the level of individual Pods in order to make it more realistic to simulate utilization and other metrics.
This is also needed to close a feature gap with [Run:ai Fake GPU Operator (FGO)](../0008-fgo-integration/README.md#background), which we plan to incorporate into Mokka.

### Background

#### Metrics

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

Apart from that, there are a few utilization patterns, chosen by the `workloadKind` label:

- `workloadKind: TrainingWorkload` - jitter in the 80-100 GPU utilization range
- `workloadKind: InteractiveWorkload` - 0 GPU utilization
- `workloadKind: InferenceWorkload` - 0 GPU utilization

Memory utilization is reported based on `GPU memory x GPU fraction`, where [GPU fraction](#resource-reservation--gpu-fractions) is another custom annotation:

```yaml
annotations:
  gpu-fraction: "0.25"
  run.ai/simulated-gpu-utilization: "50"
```

The `status-exporter` runs on each node and is responsible for simulating utilization and serving it as metrics on the well-known DCGM port `9400`:

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

The metric looks like this in raw Prometheus, with fewer labels than the real dcgm-exporter adds:

```prometheus
DCGM_FI_DEV_GPU_UTIL{Hostname="nvidia-dcgm-exporter-ce3313",UUID="GPU-299e8440-...",container="main",
  device="nvidia2",gpu="2",modelName="NVIDIA A100-SXM4-40GB",namespace="fgo-lab-04",pod="ranged-20-60"} 59
```

#### Resource Reservation & GPU Fractions

Run:ai and KAI share one physical GPU between several workloads without any real GPU partitioning. This is an optimization that binds one GPU to multiple pods with low GPU consumption that do need a real GPU, without underutilizing the whole GPU device.

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
          mountPath: /bin/nvidia-smi # nvidia-smi is how Run:ai bridges a pod without a GPU request to a GPU
  volumes:
    - name: smi
      hostPath:
        path: /var/lib/runai/bin/nvidia-smi
        type: File

  # Note that it requests no GPU by itself
```

The fractional pods get access to the GPU device through a shared `nvidia-smi` CLI mounted from the GPU-enabled node.

The `status-exporter` also leaves a few files intended for the Run:ai container toolkit on the node filesystem:

- `/var/lib/runai/proc/pod/<pod UID>/metrics/gpu/<GPU index>/utilization.sm` - e.g. "34" percent
- `/var/lib/runai/proc/pod/<pod UID>/metrics/gpu/<GPU index>/memory.allocated` - e.g. "20480000000" bytes
- `/var/lib/runai/proc/events/` - created, always empty

### Goals

- Let a Pod set the utilization and memory use of the GPUs it holds through annotations, whether the device plugin or DRA allocated them.
- Give an annotation the same meaning on CPU Nodes and KWOK Nodes.
- Keep GPUs without Pods on the values their profile and runtime policies give them.
- Support the fixed values and ranges that tests set with FGO's `run.ai/simulated-gpu-utilization` annotation.

### Non-Goals

- Metrics that reflect real work. Values follow what a Pod declares, not what it runs.
- Run:ai-specific behavior: FGO's `run.ai/*` annotations, the `workloadKind` patterns and the Run:ai container toolkit files. Tests use Mokka's annotations instead.
- Metrics for Pods that hold no GPU themselves, such as [fractional Pods](#resource-reservation--gpu-fractions). A GPU's metrics follow the Pods it's allocated to.
- Showing a Pod's containers as GPU processes, such as in `nvidia-smi`'s process list.

## Proposal

Mokka supports dynamic metrics today with a few profile patterns (`idle`, `busy`, `burst` and `steady`). That setting is on the GPU level, though; it's not workload-aware.

### User Stories

1. **A busy and an idle workload on one Node.** A test runs a training Pod annotated with `mokka.nvidia.com/gpu-utilization: "80-100"` and a notebook Pod with `"0"` on the same Node. dcgm-exporter reports 80-100% for the training Pod's GPUs and 0% for the notebook's, each series labeled with its Pod, and `nvidia-smi` reports the same values.
2. **A workload goes idle.** A test checks an idle-GPU alert. It annotates a running Pod with `mokka.nvidia.com/gpu-utilization: "0"`, the Pod's GPUs drop to 0%, and the alert fires.
3. **The same test on KWOK Nodes.** Scheduled to KWOK Nodes, the Pods from the first story get the same values from the KWOK data plane's DCGM metrics, so the test runs unchanged on thousands of virtual Nodes.

### Notes/Constraints/Caveats (Optional)

<!--
What are the caveats to the proposal?
What are some important details that didn't come across above?
Go in to as much detail as necessary here.
This might be a good place to talk about core concepts and how they relate.
-->

### Risks and Mitigations

- **The removal of `nvml-mock-ctl watch-allocations`.** The `nvml-mock-ctl watch-allocations` command gets removed. The allocation watcher's container and Helm values get new names. The old Helm values and command stay as deprecated aliases for a release, and the changelog marks the command removal and container rename as breaking.
- **Every CPU Node adds a Pod watch.** At the 100k Nodes Mokka targets, that's 100k more watches on the API server, one next to each kubelet's. The tracker stays opt-in, as the allocation watcher is today, and watches only its Node's Pods and only their metadata, which holds the telemetry annotations.

## Design Details

### Workload-aware Dynamic Metrics

As of today, Mokka's allocation watcher just sets 50% memory usage on each GPU allocated to a Pod.
We should extend that to make it much more flexible, and FGO's approach is a good example to follow.

> [!NOTE]
> We plan to rename the allocation watcher to the **workload tracker** and split it out of `nvml-mock-ctl` into a separate CLI application, deployed with the Mokka node daemon as it is today. In the target shape it tracks which workloads hold each GPU and what they declare, and keeps each GPU's workload telemetry current for the NVML engine, so a name about watching allocations no longer fits. The rename covers the `allocationWatcher` Helm values, the `allocation-watcher` container and the `nvml-mock-ctl watch-allocations` command. The rest of this document uses the new name, except where it describes how the allocation watcher works today.

The general idea is to use:
- On CPU nodes, the workload tracker, which extends the existing allocation watcher to fetch `Pod` metadata and `PodResources` from the kubelet (what we are doing today) and translate them into workload telemetry for the NVML engine.
- On KWOK nodes, the KWOK data plane, which already allocates GPUs to Pods and watches them, turns the same Pod metadata into each GPU's workload telemetry in memory and generates the metrics on each scrape.

The values of metrics like utilization, memory, etc. should be taken from the Pod annotations:

| Annotation                              | Value                                                                                                             | Without it                                 |
|-----------------------------------------|-------------------------------------------------------------------------------------------------------------------|--------------------------------------------|
| `mokka.nvidia.com/gpu-utilization`      | GPU utilization, in percent: a fixed value, such as `30`, or a range, such as `80-100`                            | the runtime view's utilization             |
| `mokka.nvidia.com/gpu-utilization-type` | how utilization moves within the range: `Steady`, `Bursty` or `Wave`, as in the runtime view's telemetry settings | `Steady`                                   |
| `mokka.nvidia.com/gpu-memory-used`      | memory in use on each GPU, in percent of its usable memory: a fixed value, such as `25`                           | `50`, as the allocation watcher sets today |

- The rest of this section calls them telemetry annotations. Each applies to every GPU the Pod holds.
- They're annotations, not labels, because they configure the workload rather than identify it.
- When several Pods share a GPU, their percentages add up, to at most 100.
- A value Mokka can't parse counts as absent and is logged.

#### CPU Nodes: Workload Tracker

Today the allocation watcher turns the kubelet's `PodResources` claims into memory use, half of a GPU's usable memory per claim, and rewrites it into `overrides.yaml` every 2 seconds.
`nvml-mock-ctl` and NVML set calls write the same file, so the last writer wins.
The NVML engine has no layer for workload telemetry, and each NVML process runs the generator on its own clock.
Red marks the parts the target shape changes:

```mermaid
flowchart LR
    kubelet["<b>kubelet</b><br/>PodResources API"]

    ctl["<b><code>nvml-mock-ctl set</code></b><br/>NVML set calls"]
    watcher["<b>allocation watcher</b><br/>sidecar, unprivileged<br/><code>memory.used</code> = claims × 50 % usable"]
    daemon["<b>node daemon</b><br/>applies the runtime view"]

    overrides["<b><code>overrides.yaml</code></b><br/>3 writers, last one wins"]
    config["<b><code>config.yaml</code></b><br/>defaults + policies"]

    subgraph engine["<b>NVML engine</b>, in every NVML process"]
        direction TB
        l1["1 · fault state"]
        l2["2 · overrides"]
        l3["no workload telemetry"]
        l4["4 · runtime view"]
        rule["top layer with a value wins"]
        l1 ~~~ l2 ~~~ l3 ~~~ l4 ~~~ rule
    end

    dcgm["<b>dcgm-exporter</b><br/>:9400, adds pod labels"]
    smi["<b><code>nvidia-smi</code></b>"]
    dp["<b>device plugin</b>"]

    kubelet ~~~ ctl
    kubelet ~~~ daemon
    kubelet -->|"claims, every 2 s"| watcher
    ctl -->|"sets"| overrides
    watcher -->|"rewrites every 2 s"| overrides
    daemon -->|"writes"| config
    overrides -->|"layer 2"| engine
    config -->|"layer 4"| engine
    engine -->|"values, on every call"| dcgm & smi & dp
    kubelet -.->|"pod labels, from the same PodResources API"| dcgm

    classDef todayOnly fill:#f7e4e0,stroke:#ae3d2a,stroke-width:2px,color:#000
    class overrides todayOnly
    style l3 stroke-dasharray: 5 5
    style rule fill:none,stroke:none
    linkStyle 8 stroke:#ae3d2a,stroke-width:2px
```

In the target shape, the workload tracker also reads this Node's Pods from the Kubernetes API. It turns binding and telemetry annotations into each GPU's workload telemetry and writes it to a file that only it writes. Like the runtime view's telemetry settings, it says how values move, such as utilization between 80 and 100 percent with half the memory used, not the values themselves. The engine reads that file as layer 3, between manual overrides and the runtime view. The generator counts time from a shared epoch, so every NVML process reports the same series. Teal marks what's new or changed:

```mermaid
flowchart LR
    kubelet["<b>kubelet</b><br/>PodResources API"]
    api["<b>Kubernetes API</b><br/>this Node's Pods"]

    ctl["<b><code>nvml-mock-ctl set</code></b><br/>NVML set calls"]
    tracker["<b>workload tracker</b><br/>sidecar, unprivileged<br/>binding +<br/>telemetry annotations<br/>→ workload telemetry"]
    daemon["<b>node daemon</b><br/>applies the runtime view"]

    overrides["<b><code>overrides.yaml</code></b><br/>kept until reset"]
    telemetry["<b>workload telemetry file</b><br/>only the tracker writes"]
    config["<b><code>config.yaml</code></b><br/>defaults + policies"]

    subgraph engine["<b>NVML engine</b>, in every NVML process"]
        direction TB
        l1["1 · fault state"]
        l2["2 · overrides"]
        l3["3 · workload telemetry"]
        l4["4 · runtime view"]
        rule["top layer with a value wins"]
        l1 ~~~ l2 ~~~ l3 ~~~ l4 ~~~ rule
    end

    dcgm["<b>dcgm-exporter</b><br/>:9400, adds pod labels"]
    smi["<b><code>nvidia-smi</code></b>"]
    dp["<b>device plugin</b>"]

    kubelet ~~~ ctl
    kubelet ~~~ daemon
    kubelet -->|"claims, every 2 s"| tracker
    api -->|"telemetry annotations"| tracker
    ctl -->|"sets"| overrides
    tracker -->|"writes"| telemetry
    daemon -->|"writes"| config
    overrides -->|"layer 2"| engine
    telemetry -->|"layer 3"| engine
    config -->|"layer 4"| engine
    engine -->|"values, on every call"| dcgm & smi & dp
    kubelet -.->|"pod labels, from the same PodResources API"| dcgm

    classDef new fill:#ddf0f2,stroke:#0a7782,stroke-width:2px,color:#000
    class api,telemetry,l3 new
    style rule fill:none,stroke:none
    linkStyle 7,9,12 stroke:#0a7782,stroke-width:2px
```

#### Pod-to-GPU Binding

On a CPU Node, the workload tracker never decides which GPU a pod gets. It reads the decision back from the kubelet's PodResources API every 2 seconds. On a KWOK Node, the data plane's [allocation record](../0008-fgo-integration/README.md#gpu-allocation) gives the same binding. In the diagrams below, the shaded steps are the workload tracker's.

With the device plugin, the scheduler only picks the Node, and the kubelet's device manager picks the device. Under the device plugin's default `deviceIDStrategy: uuid`, each device ID is a GPU UUID, which the plugin read from Mokka's NVML and Mokka took from the profile. So the tracker only has to match IDs to UUIDs:

```mermaid
sequenceDiagram
    participant S as kube-scheduler<br/>picks the Node
    participant K as kubelet<br/>device manager
    participant P as NVIDIA device plugin<br/>real, on Mokka's NVML
    participant N as NVML engine<br/>GPUs from the profile
    participant T as workload tracker<br/>sidecar

    P->>N: lists the GPUs through NVML
    N-->>P: UUIDs from the profile
    P->>K: advertises GPUs, ID = UUID
    S->>K: binds the pod to this Node<br/>a GPU count, no device
    Note over K: picks free device IDs,<br/>with Topology Manager hints,<br/>and checkpoints them
    K->>P: Allocate(those IDs)
    rect rgba(10, 119, 130, 0.12)
        T->>K: PodResources List, every 2 s
        K-->>T: pod, container → device IDs
        Note over T: matches each ID to a UUID
        T->>N: marks that GPU busy
    end
```

With DRA, the scheduler picks the device as well as the Node and records it in the ResourceClaim. PodResources reports the device by driver, pool and name, by default from Kubernetes 1.34. The NVIDIA DRA driver names a GPU `gpu-<minor>`, using the minor number Mokka's NVML reports for it, so the tracker matches the number in the name to the GPU with that minor number. It needs no access to the DRA API. Today the allocation watcher reads only device-plugin devices, so reading DRA claims is new:

```mermaid
sequenceDiagram
    participant S as kube-scheduler<br/>picks Node and device
    participant A as kube-apiserver<br/>slices and claims
    participant K as kubelet<br/>DRA manager
    participant D as NVIDIA DRA driver<br/>real, on Mokka's NVML
    participant N as NVML engine<br/>GPUs from the profile
    participant T as workload tracker<br/>sidecar

    D->>N: lists the GPUs through NVML
    N-->>D: UUIDs and minor numbers
    D->>A: publishes this Node's ResourceSlice:<br/>gpu-0 … gpu-3, named by minor number
    A-->>S: the slices, the pod's claim
    Note over S: picks a free device<br/>that fits the claim,<br/>such as gpu-2
    S->>A: allocates gpu-2 in the claim
    S->>A: binds the pod to this Node
    A-->>K: the pod and its claim
    K->>D: NodePrepareResources
    D-->>K: a CDI device for the claim
    rect rgba(10, 119, 130, 0.12)
        T->>K: PodResources List, every 2 s
        K-->>T: pod, container → driver, pool, gpu-2
        Note over T: matches gpu-2 to the GPU<br/>with minor number 2
        T->>N: marks that GPU busy
    end
```

#### KWOK Nodes: Data Plane

A KWOK Node has no kubelet, NVML engine or processes, so the [KWOK data plane](../0008-fgo-integration/README.md#kwok-data-plane) replica that owns the rack does every step in memory. It runs the workload tracker in process instead of as a sidecar, with the same code as on CPU Nodes, so a Pod's telemetry annotations mean the same on both kinds of Node. Teal marks what workload awareness adds to the data plane:

```mermaid
flowchart LR
    api["<b>Kubernetes API</b><br/>Pods on the replica's racks"]
    cp["<b>control plane API</b><br/>runtime view per rack"]

    subgraph dp["<b>KWOK data plane replica</b>, in memory"]
        tracker["<b>workload tracker</b><br/>in process<br/>binding +<br/>telemetry annotations<br/>→ workload telemetry"]
        view["<b>runtime view</b><br/>telemetry settings"]
        subgraph metrics["<b>DCGM metrics</b>, on each scrape"]
            direction TB
            l3["3 · workload telemetry"]
            l4["4 · runtime view"]
            rule["top layer with a value wins"]
            l3 ~~~ l4 ~~~ rule
        end
    end

    prom["<b>Prometheus</b><br/>scrapes each replica on :9400"]

    api -->|"allocation record,<br/>telemetry annotations"| tracker
    cp -->|"fetched on a delivery<br/>generation bump"| view
    tracker -->|"layer 3"| metrics
    view -->|"layer 4"| metrics
    metrics -->|"values, with pod labels"| prom

    classDef new fill:#ddf0f2,stroke:#0a7782,stroke-width:2px,color:#000
    class tracker,l3 new
    style rule fill:none,stroke:none
    linkStyle 2,4 stroke:#0a7782,stroke-width:2px
```

- **Binding** comes from the [GPU allocation](../0008-fgo-integration/README.md#gpu-allocation) record the data plane writes on each Pod, for `nvidia.com/gpu` requests and DRA claims alike. The data plane knows a binding as soon as it writes the record, so nothing is polled.
- **Telemetry annotations** come from the [Pod watch](../0008-fgo-integration/README.md#separating-cpu-and-kwok-racks) the data plane already has, so it needs no new API access.
- **Workload telemetry** stays in the replica's memory, since nothing outside the replica reads it. The replica recomputes a GPU's workload telemetry when a Pod on it is allocated, changes or goes away.
- **Values** are computed on scrape, at most once per 10-second step, as [DCGM Metrics](../0008-fgo-integration/README.md#dcgm-metrics) in MEP0008 describes. When a GPU has workload telemetry, `DCGM_FI_DEV_GPU_UTIL` follows it instead of the runtime view, `DCGM_FI_DEV_FB_USED` follows its memory share, and `DCGM_FI_DEV_FB_FREE` is the rest. A GPU without Pods keeps following the runtime view.
- **Only layers 3 and 4 exist.** There's no NVML for `nvml-mock-ctl` to override, and fault injection in KWOK mode is a [non-goal of MEP0008](../0008-fgo-integration/README.md#non-goals).
- **Workload telemetry and values aren't persisted.** Values depend only on telemetry settings and time, counted from an epoch every replica shares, such as the Unix epoch. A replica that restarts, or takes over a rack after a [reshard](../0008-fgo-integration/README.md#sharding), rebuilds the workload telemetry from the allocation records and continues the same series without a jump.

## Drawbacks

- **The workload tracker needs wider permissions.** Today the allocation watcher is an unprivileged container that reads only the kubelet's PodResources socket. The [workload tracker](#cpu-nodes-workload-tracker) also reads Pods from the Kubernetes API. RBAC can't limit a DaemonSet to the Pods of its own Node, so every Node's tracker can read every Pod in the cluster, plain-text environment variables included.

## Alternatives

**Look up the GPU's UUID in the Node's ResourceSlice.** With DRA, PodResources tells the workload tracker which device a Pod got by its name, such as `gpu-2`, not by its UUID, and [Pod-to-GPU Binding](#pod-to-gpu-binding) reads the minor number from that name. Instead, the tracker could find the device in the ResourceSlice that the NVIDIA DRA driver publishes for the Node and take its `uuid` attribute, as dcgm-exporter does with `--kubernetes-enable-dra`. That would keep working if a driver release stopped naming GPUs after their minor numbers, a change that would make the minor-number match pick the wrong GPU or none. The MEP doesn't use it because the tracker would then depend on the DRA API: it would watch ResourceSlices and need permission to read them, while the minor-number match needs only PodResources and Pods.
