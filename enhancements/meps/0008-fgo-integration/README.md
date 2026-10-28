# MEP-0008: Run:ai Fake GPU Operator incorporation

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
[Run:ai Fake GPU Operator (FGO)](https://github.com/run-ai/fake-gpu-operator) functionality directly into Mokka.

## Motivation

The integration will enable Mokka to:
- consolidate Mokka and the Run:ai team's efforts in a single project
- replace FGO in the Run:ai and KAI Scheduler CI/CD pipelines
- extend Mokka with optional [KWOK](https://kwok.sigs.k8s.io/) (Kubernetes WithOut Kubelet, virtual nodes) support for massive, cheap load testing where `nvidia-smi` and other on-host tools and libraries are not needed

### Background

[Fake GPU Operator (FGO)](https://github.com/run-ai/fake-gpu-operator) is a Kubernetes operator for testing Run:ai's GPU scheduling capabilities
without spending real, expensive GPUs on it.

FGO supports two backends:

- `fake` - the primary FGO mode. It uses custom, fake instances of `nvidia-smi`, the device driver, the Dynamic Resource Allocation (DRA) plugin, etc. to simulate the presence of GPU devices on the node.
- `mock` - the earlier integration with Mokka, built in the early days of the Mokka project.

Here is a list of the FGO main features:

- TBU

#### Fake Backend

The idea behind the `fake` backend is to fake the NVIDIA GPU infrastructure in order to propagate simulated GPU to Kubernetes:

- FGO is deployed to the `gpu-operator` namespace to mimic the presence of the real GPU Operator
- FGO comes with a custom device plugin that propagates the GPU capacity of the node
- FGO comes with the `status-exporter` DaemonSet that mimics NVIDIA Data Center GPU Manager (DCGM) metrics
- FGO installs a custom `nvidia-smi` binary that supports a subset of commands and flags of the original `nvidia-smi`

This gives a Kubernetes API-level GPU simulation.

TBU

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

Without the annotation, utilization follows the pod's `workloadKind` label, or, when the label is missing, the priority class of the pod's PodGroup:

- `TrainingWorkload` - a random value from the 80-100% range
- `InteractiveWorkload` - 0% utilization
- `InferenceWorkload` and `DistributedWorkload` - 0% utilization

A pod of any other kind reports 100%.

Memory utilization is reported based on `GPU memory x GPU fraction` where [GPU fraction](#resource-reservation--gpu-fractions) is another custom annotation:

```yaml
annotations:
  gpu-fraction: "0.25"
  run.ai/simulated-gpu-utilization: "50"
```

The `status-exporter` runs on the node and is responsible for simulating utilization and publishing the metrics on the well-known DCGM exporter port `9400`:

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

The metric looks like this in raw Prometheus (fewer labels than real DCGM sends):

```prometheus
DCGM_FI_DEV_GPU_UTIL{Hostname="nvidia-dcgm-exporter-ce3313",UUID="GPU-299e8440-...",container="main",
  device="nvidia2",gpu="2",modelName="NVIDIA A100-SXM4-40GB",namespace="fgo-lab-04",pod="ranged-20-60"} 59
```

### Resource Reservation & GPU Fractions

Run:ai and KAI Scheduler share one physical GPU between several workloads without any real GPU partitioning. This is an optimization for pods that consume little GPU but still need a real GPU resource, so the whole device is not left underutilized.

They park a reservation pod that holds the whole GPU device through the ordinary `nvidia.com/gpu` resource,
then place fractional pods that request no GPU resource at all and just point at the reservation. The kubelet only knows about the reservation pod.

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: reservation-g1
  namespace: runai-reservation # a namespace for placing reservation pods
  labels:
    runai-gpu-group: g1 # the pod group that FGO turns into a GPU assignment annotation, e.g. run.ai/reserve_for_gpu_index: GPU-775135a6-f582-540e-84df-8f4e9a049910
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
          mountPath: /bin/nvidia-smi # nvidia-smi is how Run:ai bridges a pod with no GPU to a GPU
  volumes:
    - name: smi
      hostPath:
        path: /var/lib/runai/bin/nvidia-smi
        type: File

  # Note that it requests no GPU by itself
```

The fractional pods get access to the GPU device via the shared `nvidia-smi` CLI mounted from a GPU-enabled node.

The `status-exporter` also leaves a few files intended for the Run:ai container toolkit on the node filesystem:

- `/var/lib/runai/proc/pod/<pod UID>/metrics/gpu/<GPU index>/utilization.sm` - e.g. "34" percent
- `/var/lib/runai/proc/pod/<pod UID>/metrics/gpu/<GPU index>/memory.allocated` - e.g. "20480000000" bytes
- `/var/lib/runai/proc/events/` - created, always empty

#### KWOK

FGO reaches fleet scale with [KWOK](https://kwok.sigs.k8s.io/) (Kubernetes
WithOut Kubelet), which creates Node objects and drives the lifecycle of the
pods bound to them, with no kubelet and no containers behind either. On a KWOK
node, FGO's central `kwok-gpu-device-plugin` publishes the `nvidia.com/gpu`
capacity and its `kwok-dra-plugin` creates the ResourceSlice, so a scheduler
sees GPU nodes that have no machine behind them.

#### Run:ai x Mokka Integration (Mock Backend)

FGO creates Mokka's profiles as ConfigMaps and uses some information from them in the `fake` backend mode, like:

- product name
- memory
- GPU count
- driver and CUDA versions

When the node pool is defined to be `mock`:

```yaml
topology:
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

FGO actually deploys a Mokka instance on the nodes it manages.

TBU

On the Mokka end, with [integrations.fakeGpuOperator.enabled](../../../deployments/nvml-mock/helm/nvml-mock/templates/integrations-fgo.yaml),
the Mokka chart publishes one ConfigMap per Mokka GPU profile, such as `gb300`, `h100` and `a100`, in the shape FGO expects
(named `gpu-profile-<profile>`, with the profile under the `profile.yaml` key, in `integrations.fakeGpuOperator.targetNamespace`).
That template is the only Mokka code that knows about FGO.

TBU

### KAI Test Suite

TBU

### Goals

- Propose an actionable plan for FGO incorporation into Mokka
- Reach the feature parity required to migrate Run:ai and KAI Scheduler off FGO

### Non-Goals

<!--
What is out of scope for this MEP? Listing non-goals helps to focus discussion
and make progress.
-->

## Proposal

<!--
This is where we get down to the specifics of what the proposal actually is.
This should have enough detail that reviewers can understand exactly what
you're proposing, but should not include things like API designs or
implementation. What is the desired outcome and how do we measure success?
The "Design Details" section below is for the real nitty-gritty.
-->

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

<!--
This section should contain enough information that the specifics of your
change are understandable. This may include API specs (though not always
required) or even code snippets. If there's any ambiguity about HOW your
proposal will be implemented, this is the place to discuss them.
-->

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
