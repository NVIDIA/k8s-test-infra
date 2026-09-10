---
# The page opens with the hero <div>, so MkDocs cannot infer a title from the
# H1 below it and falls back to the filename. Set it explicitly.
title: Overview
---

<div class="mokka-hero" markdown>
![Mokka](img/logo.png)

<p class="mokka-tagline">Simulate your GPU infrastructure on CPU nodes.</p>
</div>

# Mokka

Mokka simulates the software contracts around NVIDIA devices rather than the
devices themselves. The rule of thumb: reach for Mokka when your system **reads**
hardware state and reacts to it, and for real hardware when it **executes** work,
moves data, or measures performance.

That makes it a good fit for:

- Kubernetes discovery and allocation through the device plugin or DRA —
  scheduling, ResourceClaims, CDI visibility, topology attributes.
- Software that consumes NVML, `nvidia-smi`, DCGM or DCGM Exporter.
- Monitoring dashboards, parsers, alerting, and remediation logic that cordons,
  drains, reschedules and recovers.
- Repeatable fault injection — device loss, Xid errors, ECC errors, temperature,
  power, utilisation, clocks, and GPU-side NVLink errors.
- Code that interprets declared PCI, NUMA, NVLink, fabric UUID or clique
  topology.
- IMEX peer readiness and liveness. The peer protocol is real and runs over the
  pod network; no GPU or NVLink traffic is involved.

## Try it

```bash
kind create cluster --name mokka

helm install nvml-mock oci://ghcr.io/nvidia/k8s-test-infra/chart/nvml-mock \
    --namespace mokka --create-namespace
```

Every node now reports mock GPUs. The [Quick Start](quickstart.md) takes it from
here.

## Simulation depth by area

Kubernetes control flows and client binaries are real. Hardware identity,
topology, counters and failures are synthesised.

| Area | What Mokka simulates | Good for | What it does not prove |
|---|---|---|---|
| **GPU and NVML** | A configurable NVML-visible GPU. Profiles and per-device overrides define identity, model, memory size, PCI and NUMA identity, clocks, temperature, power, utilisation, ECC state, process records and failure responses. [`nvml-mock-ctl`](nvml-mock-ctl.md) changes most of them at runtime. | Inventory, monitoring, parsers, DCGM integration, alert handling, failure recovery | No kernel driver, firmware, CUDA kernels, contexts, streams or data path. Unimplemented NVML functions return `NVML_ERROR_NOT_SUPPORTED`, so a new consumer should confirm the calls it makes. MIG instances and partition lifecycle are absent. |
| **Kubernetes allocation** | Real allocation workflows over synthetic devices. The device plugin and DRA driver advertise and allocate GPUs; Kubernetes schedules pods and creates ResourceClaims, ResourceSlices, CDI assignments and topology attributes. | Scheduler, operator, admission, claim lifecycle and controller-recovery tests | A claim proves allocation, not that a workload used GPU memory or did work. Process records do not follow allocations. Driver, firmware and toolkit install or upgrade paths are not exercised. |
| **Metrics** | Utilisation, temperature, power, profiling activity and NVLink counters, either configured or generated from elapsed time. See [Metric Fidelity](configuration.md#metric-fidelity) for which is which. | Dashboards, thresholds, alerts, autoscaling inputs, deterministic failure scenarios | No metric is workload-correlated, so none can establish throughput, efficiency, thermal behaviour, timing or performance. |
| **PCI and NUMA** | A synthetic discovery tree: PCI BDFs, root-complex paths, NUMA node values, selected device nodes, and sysfs content for served containers. | Discovery, placement, topology parsing, and behaviour driven by declared locality | No PCIe transactions, DMA, IOMMU or ACS behaviour, bandwidth, latency or hardware errors, and no real CPU or memory locality. A consumer that does not receive the tree through CDI or NRI reads the host's own sysfs instead. |
| **NVLink** | Link generation, count, state, remote GPU or NVSwitch endpoint, capabilities and line rate. `nvidia-smi topo -m` renders the declared local matrix, counters accrue synthetically, and GPU-side link errors reach DCGM health. | Topology parsing, policy decisions, local link health, fault handling, clique logic | No CUDA peer access, GPU traffic, cross-node NVLink path, collectives, NCCL, congestion, bandwidth or latency. Counters do not reflect workload traffic. |
| **NVSwitch and Fabric Manager** | GPU-visible fabric state: NVSwitch endpoints in the declared topology, plus fabric UUID, clique, registration and [health](configuration.md#fabric-health). The Fabric Manager stand-in publishes a node-local readiness marker and can delay registration. | Software that waits for fabric registration, reads fabric identity, or reacts to GPU-visible fabric health | No NVSwitch ASIC, forwarding, NSCQ, SXID model, switch control API or CLI, firmware, partitions or routing. |
| **InfiniBand HCA** | Synthetic HCA sysfs, UMAD and verbs surfaces with configurable model, firmware, GUID, LID, GID, link layer, state and rate. `ibstat`, `ibstatus`, `ibv_devices` and much of `ibv_devinfo` work against them. | Discovery, parsers, inventory, link-state handling, selected management-plane workflows | No kernel provider, queue pairs, completion queues, memory registration, verbs execution, RDMA traffic or performance. |
| **InfiniBand fabric** | Peer HCAs registered across pods, SA and SMP replies, a deterministic synthetic subnet-manager identity, `iblinkinfo`, `ibnetdiscover`, and cross-node `ibping` over the pod network. | Tool compatibility, parser behaviour, peer discovery, simple control-plane failure handling | No IB switch, subnet-manager election, switch NOS, firmware, routing, adaptive routing, congestion, QoS, PKeys, cable behaviour or IB data path. The fabric view is synthesised from a full-mesh HCA model. |

## Use real hardware for

- CUDA application correctness, compatibility, kernels, memory access and CUDA
  peer-to-peer.
- NCCL collectives, GPUDirect, RDMA, and NVLink, NVSwitch or InfiniBand data
  paths.
- Throughput, latency, oversubscription, congestion, scaling efficiency and
  thermal behaviour.
- GPU, HCA, NVSwitch or IB switch driver and firmware installation, upgrade,
  reset and recovery.
- Operating a switch through its management plane, CLI, telemetry, routing or
  firmware interfaces.
- Reproducing hardware faults, race conditions and timing with physical
  fidelity.
- MIG partition creation and lifecycle, and Confidential Computing.

## Tested consumers

| Consumer                 | What works                                                                                |
|--------------------------|-------------------------------------------------------------------------------------------|
| Node Feature Discovery   | PCI vendor labels derived from the feature file Mokka writes                              |
| GPU Feature Discovery    | Node labels derived from NVML                                                             |
| NVIDIA Device Plugin     | Allocatable `nvidia.com/gpu` matches the profile, and workloads schedule against it       |
| NVIDIA DRA Driver        | ResourceSlices report the right GPUs, and a `ResourceClaimTemplate` pod reaches `Running` |
| NVIDIA GPU Operator      | The full operand stack installs and its validator starts                                  |
| DCGM / dcgm-exporter     | Telemetry, time-varying power, and injected Xid errors                                    |
| Run:ai fake-gpu-operator | Profile ConfigMaps published in the shape its discovery expects                           |

## Where to go next

<div class="grid cards" markdown>

-   **Get it running**

    Install into a KIND cluster and see simulated GPUs in five minutes.

    [Quick Start](quickstart.md)

-   **Understand how it works**

    The moving parts, how they connect, and how the system behaves.

    [Architecture](architecture.md)

-   **Do something specific**

    Task-oriented walkthroughs: the device plugin, DRA, the GPU Operator,
    failure injection, node-wide injection.

    [Guides](guides/README.md)

-   **Change Mokka**

    Local development with Tilt, the test suites, and how to submit a change.

    [Contributing](contributing/index.md)

</div>

The [FAQ](faq.md) answers what usually comes up next: which GPU models ship,
which surfaces are not staged at all, and why `nvidia-smi` reports the numbers
it does.
