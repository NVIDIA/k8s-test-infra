# MEP-0003: Mokka Node Agent

Author: [Roman Hlushko](https://github.com/roma-glushko)

<!-- toc -->
- [Summary](#summary)
- [Motivation](#motivation)
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

<!--
A good summary is probably at least a paragraph in length. Aim for a
tone and level of detail that make sense to someone unfamiliar with the
proposal — reviewers, future contributors, and downstream consumers.
-->

This proposal refactors the current state and shape of the NVML mock CLIs in order to
turn them into a proper node agent.

## Motivation

In scope of [MEP-0001 (Mokka Control Plane)],
the parent proposal, we plan to control the simulation process on each individual node
by sending the expected sGPU node state from the Mokka Control Plane service via REST API.

However, given the current organization of NVML mock, it's not straightforward to do so.

At this moment, NVML mock is essentially:

- A set of ~a dozen independent CLIs — long-running daemons (`mock-ib`, `fake-fabricmanager`, `fake-imex`), one-shot renderers (`render-pci-sysfs`, `render-imex-procdevices`), one transparent `execve` wrapper (`imex-nogpu-shim`), etc.
- Some CLIs are overly fine-grained, doing one very specific thing. On top of that, we don't use a consistent logging setup or a proper CLI framework to structure them well.
- The CLIs don't have any shared lifecycle. Nothing knows about anything else, either.
- `deployments/nvml-mock/scripts/setup.sh` (690 lines of bash driving 11 numbered phases) is not just an entrypoint — it contains a sprawl of legitimate simulation logic. This belongs in Go code.
- Finally, because of this logic sprawl, it's harder to understand:
  - Which components do we simulate already and which don't we?
  - What does it take to simulate/reverse a specific component? What might we have missed in each component's surface area?

On top of that, `deployments/nvml-mock/scripts/cleanup.sh` doesn't try to perfectly reverse NVML mock effects.
Also, many things are applied sequentially even though they work with disjoint filesystems and other objects.

### Goals

This MEP sets a few goals:

- Merge all simulation logic into one CLI aka Mokka Node Agent CLI with a shared lifecycle and configuration reading logic. This agent will load configuration (first, from the YAML file like today, then from Mokka Control Plane).
- Structure the simulation logic around the components/subsystems it belongs to, so it's easy to see what it takes to simulate a specific aspect of the infrastructure footprint as well as to identify what we are missing.
- Mokka Node Agent should act as a supervisor that gives a shared lifecycle to all e.g. `Setup()`, `Run()` (for long-lived operations like IB sim servers), `Cleanup()`. It will act as a reconciler.

Additionally, we want to run as much of the simulation logic in parallel as possible, leveraging their independence,
so they apply as fast as available CPU/IOPS allow.

### Non-Goals

- This proposal wants to keep the current simulation logic identical. Any missing coverage or improvements should be done outside the MEP. 

## Proposal

<!--
This is where we get down to the specifics of what the proposal actually is.
This should have enough detail that reviewers can understand exactly what
you're proposing, but should not include things like API designs or
implementation. What is the desired outcome and how do we measure success?
The "Design Details" section below is for the real nitty-gritty.
-->

Instead of having many tiny CLIs and a big setup.sh script that starts them and do a portion of simuation,
we will have one mokka-node-agent CLI that acts as reconciler and superviser of multiple `Components` structs, 
each knows how to simulate/unsimulate a specific subsystem like devicedriver, imex, etc.

The mokka-node-agent CLI will load configuration from available sources (filesystem & Control Plane REST API) 
and pass it to each `Component` for reconciliation. 

The mokka-node-agent CLI will also keep any simulation servers that need to be active all the time. 
The servers shutdown will be aligned with the signals that mokka-node-agent receives.

### Notes/Constraints/Caveats (Optional)

<!--
What are the caveats to the proposal?
What are some important details that didn't come across above?
Go in to as much detail as necessary here.
This might be a good place to talk about core concepts and how they relate.
-->

### Risks and Mitigations

- There are risks to break some unclearly articulated assumptions in the existing codebase during refactoring.

## Design Details

Introduce **`internal/agent`** (a level-triggered reconciler + supervisor) and **`cmd/mokka-node-agent`** (a single binary).

### Interfaces

There are three main interfaces.

#### Component

One owner per simulated component:

```go
type Component interface {
    Name() string
    Reconcile(ctx context.Context, host Host, state *State) error // level-triggered; idempotent
    Run(ctx context.Context) error                                // long-running; nil-op for pure renderers
    Cleanup(ctx context.Context, host Host) error                 // reverses effects (per-component)
    Ready() bool
}
```

- `Reconcile` is called on every state change with the current desired state. Must be idempotent — if nothing changed, it's a no-op.
- Internally, `Reconcile` parallelizes independent surfaces via a local `errgroup`. The GPU-driver-footprint component runs 7 things (chardevs + NVML shim + CUDA shim + nvidia-smi + procfs version + procfs params + engine config) concurrently.
- `Run` supervises background daemons (mock-ib server, fabric-manager marker loop, allocwatch loop). Returning nil means "one-shot done".
- `Cleanup` reverses effects on graceful shutdown, per-component. Each component removes only files it created; the top-level `/host/var/lib/nvml-mock` root is left to the pod's hostPath lifecycle.
- `Ready()` powers `/readyz` and attributes failures per surface.

`Host` abstracts the on-host root (`/host/var/lib/nvml-mock`, `/host/dev`, `/host/proc`, `/host/sys`, `/host/etc`). Tests retarget to `t.TempDir()`.

#### State

This is where the desired state comes from:

```go
type StateSource interface {
    Watch(ctx context.Context) (<-chan State, error) // sends current on first call; pushes on change
    Close() error
}
```

The compiled state the reconciler acts on maps 1:1 to what [MEP-0001]'s Control Plane will emit:

```go
type State struct {
    Node       NodeMeta           // hostRoot, nodeName, hostname
    Software   SoftwareVersions   // driver / NVML / CUDA (MEP-0001 §SGPUProfile.spec.software)
    NodeShape  NodeShape          // GPU count, host CPU, PCIe/NUMA topology, GPU fabric, network
    Devices    []DeviceState      // per-GPU: identity + hardware + runtime
    Fabric     FabricState        // clique ID + fabric domain
}
```

Two implementations from day one:

- `FileSource` — watches the mounted ConfigMap profile YAML plus `overrides.yaml`; merges into `State`.
- `ControlPlaneSource` — [MEP-0001]'s polling client.

`FileSource` reads today's `configs/mock-nvml-config-*.yaml` and compiles it into `State`.
`ControlPlaneSource` will emit an already-compiled `State`. Components only see `State`.

#### Agent

This is the reconciler & supervisor.

`Agent.Run(ctx)`:

1. Subscribe to `StateSource.Watch(ctx)`; cache the last `State` in memory ([MEP-0001]'s crash-tolerance requirement).
2. **Wave 1 (parallel)** — on every `State` (initial + updates), call `Reconcile(ctx, host, state)` on every component concurrently via `errgroup`. Component failures are isolated: an optional component's failure marks it unready and continues; a required component's failure returns.
3. **Wave 2 (parallel, launched once)** — each component's `Run(ctx)` is launched under a supervisor `errgroup` at startup. Runs continue across state changes; only a canceled `ctx` stops them.
4. Expose `/healthz` + `/readyz` HTTP endpoints, aggregated + per-component (shape from `cmd/nvml-mock-nri/main.go:107` `serveHealth`).
5. On `ctx.Done()`: cancel `Run` goroutines, then call `Cleanup(ctx, host)` on every component in parallel.

Graceful shutdown via `signal.NotifyContext(ctx, SIGINT, SIGTERM)`.

### Components

These are the simulated components. Each is a package under `internal/agent/`:

| Package                 | Simulates                                                                                                              | Reconcile does (in parallel)                                                               | Run does                                                             |
|-------------------------|------------------------------------------------------------------------------------------------------------------------|--------------------------------------------------------------------------------------------|----------------------------------------------------------------------|
| `gpudriver`       | Component 1 — GPU driver footprint                                                                                     | chardevs, NVML shim, CUDA shim, nvidia-smi, procfs version+params, mock-NVML engine config | —                                                                    |
| `pcibus`                | Component 2 — GPU on PCI bus                                                                                           | PCI sysfs tree + libpcimocksys.so staging                                                  | —                                                                    |
| `fabricmanager`         | Component 3 — NVSwitch fabric manager                                                                                  | initial marker write                                                                       | re-assert marker every 2 s                                           |
| `imex`                  | Component 4 — NVIDIA IMEX                                                                                              | IMEX channel chardevs + `/proc/devices` overlay                                            | —                                                                    |
| `ibhca`                 | Component 5 — InfiniBand HCA                                                                                           | IB sysfs tree + libibmock*.so staging + IB CLI tool staging                                | `pkg/network/mockib/daemon.Server`; optional fabric relay            |
| `nvlink`                | Component 6 — NVLink fabric / compute domain                                                                           | topology YAML overlay                                                                      | —                                                                    |
| `cdi`                   | Component 7 — CDI surface                                                                                              | `nvidia.yaml` + `nvml-mock-nri.yaml`                                                       | —                                                                    |
| `allocwatch` (optional) | (not a simulated surface — mirrors pod GPU claims into `state`)                                                        | —                                                                                          | kubelet PodResources → override memory.used via `pkg/gpu/allocwatch` |

#### Example: `devicedriver.Reconcile`

```go
func (c *DeviceDriver) Reconcile(ctx context.Context, host host.Host, state *node.State) error {
    g, gctx := errgroup.WithContext(ctx)
    g.Go(func() error { return c.materializeCharDevs(gctx, host, state.Devices) })
    g.Go(func() error { return c.stageNVMLShim(gctx, host, state.Software) })
    g.Go(func() error { return c.stageCUDAShim(gctx, host, state.Software) })
    g.Go(func() error { return c.stageNvidiaSMI(gctx, host, state.Software) })
    g.Go(func() error { return c.writeProcVersion(gctx, host, state.Software) })
    g.Go(func() error { return c.writeProcParams(gctx, host, state.Software) })
    g.Go(func() error { return c.writeEngineConfig(gctx, host, state) })
    return g.Wait()
}
```

Each op is a short function (~30 lines) that (a) computes the desired state for its surface from `state`, (b) writes idempotently via `internal/agent/host/` helpers, (c) returns an error tagged with the surface name for `/readyz` attribution. All components follow the same shape.

### The parallel-reconcile shape

The reconciler is a fan-out:

```
                     ┌── gpudriver      (chardevs + libs + smi + procfs + engine config + /run/nvidia/driver symlink, all parallel)
                     ├── pcibus         (sysfs tree + NFD feature file)
                     ├── fabricmanager  (marker; Run→ re-assertion loop)
   State snapshot ───┼── imex           (chardevs + /proc/devices overlay, parallel)
                     ├── ibhca          (sysfs tree; Run→ mock-ib daemon + fabric relay)
                     ├── nvlink         (topology overlay)
                     ├── cdi            (2 YAMLs, parallel)
                     └── allocwatch     (optional; Run→ PodResources loop)
```

Startup time ≈ `max(t_component)` instead of `sum(t_phase)`. Dominant cost is IB sysfs render + `mock-ib` daemon warm-up.

### Simulated Surface

The unit of analysis is a *simulated component* — a real-world thing we pretend exists on the host.
For each, we list its contract surfaces, which upstream consumers touch them, and how well the node-agent covers each surface.
Legend: **✓** covered, **~** partial, **✗** gap, **N/A** intentionally out of scope.

#### NVIDIA GPU Driver

**What we pretend exists**: a working NVIDIA GPU driver on the host, without the kernel module or GPU data path.

| Surface                                                                                                       | Consumers                                                                    | Coverage                                                                                                                                                             |
|---------------------------------------------------------------------------------------------------------------|------------------------------------------------------------------------------|----------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `/dev/nvidia0..N`, `/dev/nvidiactl`, `/dev/nvidia-uvm`, `/dev/nvidia-uvm-tools` char devices (majors 195/510) | K8s device-plugin NVML discovery, NRI plugin device injection, workload libs | ✓ `gpudriver` — `mknod` with correct major/minor                                                                                                               |
| `libnvidia-ml.so.<version>` C ABI (~400 symbols)                                                              | `nvidia-smi`, DCGM, go-nvml consumers, K8s device-plugin                     | ✓ `gpudriver` stages the shim; 89 real implementations in `pkg/gpu/mocknvml/engine` + auto-generated stubs. Coverage measurable via `generate-bridge --stats`. |
| `libcuda.so.<version>` C ABI (driver API)                                                                     | CUDA driver-API-only apps                                                    | ~ `gpudriver` stages the shim; only 15 functions in `pkg/gpu/mockcuda`. Full CUDA runtime not covered.                                                         |
| `/proc/driver/nvidia/version`                                                                                 | `nvidia-smi` startup banner, GPU Operator validator                          | ✓ `gpudriver`                                                                                                                                                  |
| `/proc/driver/nvidia/params`                                                                                  | GPU Operator driver-status probes                                            | ✓ `gpudriver`                                                                                                                                                  |
| Kernel module presence: `/proc/modules`, `/sys/module/nvidia/version`, `/sys/module/nvidia_uvm/`              | `lsmod`, GPU Operator `driver-container` gate, DCGM startup checks           | ✗ **gap** — not simulated; unmodified consumers checking module state see nothing                                                                                    |
| `nvidia-smi` ELF binary + shell fallback                                                                      | shell scripts, operator tooling that invokes `nvidia-smi`                    | ✓ `gpudriver` stages real RPATH-patched ELF; shell fallback covers minimal cases                                                                               |
| Mock-NVML engine on-disk config (compiled `state` → engine config file the shim reads at dlopen)              | mock-NVML shim itself at container-run time                                  | ✓ `gpudriver` — atomic write with `unix.Flock`; co-writer `nvml-mock-ctl` shares the lock                                                                      |
| `/run/nvidia/driver` symlink → `/var/lib/nvml-mock/driver`                                                     | GPU Operator validator (probes for driver root on the host)                  | ✓ `gpudriver` — atomic `ln -sfn`, materialized after driver root is populated                                                                                        |

**Delivery**: files materialized under `/host/var/lib/nvml-mock/driver/`, mounted into workload containers via CDI (`nvidia.yaml`) or NRI overlay. LD_PRELOAD is not used here (Go consumers bypass it).

**Reconcile trigger**: `state.Software` or `state.Devices` fields change.

#### GPU Device on the PCI bus

**What we pretend exists**: NVIDIA GPU hardware attached to the PCIe root complex, discoverable by any tool that enumerates PCI devices.

| Surface                                                                                        | Consumers                                                                                                      | Coverage                                                                   |
|------------------------------------------------------------------------------------------------|----------------------------------------------------------------------------------------------------------------|----------------------------------------------------------------------------|
| `/sys/bus/pci/devices/<BDF>/{vendor,device,class,numa_node,subsystem_vendor,subsystem_device}` | NFD (`feature.node.kubernetes.io/pci-10de.present`), GPU Operator validator, `nvidia-smi topo`, K8s DRA driver | ✓ `pcibus` — full materialization from `state.NodeShape.Topology.GpuSlots` |
| `/sys/devices/pci<domain>:<bus>/...` topology tree with intermediate bridges                   | Topograph, DCGM topology discovery                                                                             | ✓ `pcibus`                                                                 |
| `/sys/bus/pci/devices/<BDF>/driver → /sys/bus/pci/drivers/nvidia` symlink                      | GPU Operator's "is nvidia driver bound" probes                                                                 | ? verify; add explicit test to MEP-0003 test plan                          |
| `/sys/bus/pci/devices/<BDF>/config` (PCIe config space, 4 KB)                                  | `lspci -vv`, low-level probes                                                                                  | ✗ **gap** — not materialized                                               |
| `/proc/bus/pci/devices`                                                                        | legacy `lspci` fallback                                                                                        | ✗ **gap** — not materialized                                               |
| `libpci`-based tools via `libpcimocksys.so` LD_PRELOAD                                         | `lspci`, C-based sysfs walkers                                                                                 | ✓ `pcibus` stages the shim + owns the LD_PRELOAD entry                     |
| NFD feature file `/etc/kubernetes/node-feature-discovery/features.d/nvml-mock.features` → `feature.node.kubernetes.io/pci-10de.present=true` | NFD-based operators, DRA driver | ✓ `pcibus` — bridge because NFD reads real host `/sys/bus/pci/`, which cannot see the mock's isolated PCI tree |

**Delivery**: files under `/host/var/lib/nvml-mock/sys/` mounted at `/sys` in containers via CDI. LD_PRELOAD of `libpcimocksys.so` covers C `libpci` consumers.

**Reconcile trigger**: `state.NodeShape.Topology` changes.

#### NVSwitch fabric manager

**What we pretend exists**: the `nv-fabricmanager` daemon running on a NVSwitch-equipped platform (HGX, GB200/GB300), signaling fabric readiness.

| Surface                                                                                       | Consumers                                                                                  | Coverage                                                                                                                                                             |
|-----------------------------------------------------------------------------------------------|--------------------------------------------------------------------------------------------|----------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| Readiness marker `/var/lib/nvml-mock/fabric-state/fabricmanager.ready`, re-asserted every 2 s | GPU Operator (waits for fabric ready before workloads), nvidia-smi via NVML fabric queries | ✓ `fabricmanager` via `pkg/fmcoord`; `Run()` supervises the re-assertion loop; optional init delay simulates real startup latency                                    |
| Process presence in `ps` / `systemctl status nvidia-fabricmanager`                            | operator diagnostic tooling                                                                | ~ marker only; process name is `mokka-node-agent`, not `nv-fabricmanager`. Acceptable divergence unless a consumer greps for the process name — call out in MEP-0003 |
| `nv-fabricmanager` telemetry Unix socket `/run/nvidia-fabricmanager/socket`                   | DCGM-Exporter `fabric_manager_status` collector                                            | ✗ **gap** — DCGM-Exporter's fabric metrics report unknown                                                                                                            |
| `nvswitch-audit` CLI                                                                          | operator diagnostics                                                                       | ✗ **gap** — CLI not shipped                                                                                                                                          |

**Delivery**: file materialization only; no IPC endpoint simulated.

**Reconcile trigger**: `state.Fabric` changes (enable/disable, init delay).

#### NVIDIA IMEX subsystem

**What we pretend exists**: NVIDIA IMEX (Inter-node Memory Exchange) capability + daemon for multi-node compute-domain coordination.

| Surface                                                                          | Consumers                                             | Coverage                                                                                                         |
|----------------------------------------------------------------------------------|-------------------------------------------------------|------------------------------------------------------------------------------------------------------------------|
| `/dev/nvidia-caps-imex-channels/channel<N>` chardevs (major 235, 2048 minors)    | DRA compute-domain kubelet plugin                     | ✓ `imex` — channels + majors materialized                                                                        |
| `/proc/devices` entry for `nvidia-caps-imex-channels`                            | DRA driver's `ALT_PROC_DEVICES_PATH` consumer         | ✓ `imex` via `pkg/system/mockimex`                                                                               |
| Real `nvidia-imex --nogpu` daemon process (compute-domain-daemon image only)     | ComputeDomain workload's cross-node peer coordination | ✓ `imex-nogpu-shim` binary wraps upstream `nvidia-imex`; installed at Docker build time, out of node-agent scope |
| Legacy IMEX peer marker files `/var/lib/nvml-mock/imex-state/*` (fake-imex path) | none currently                                        | ✗ **deprecated** — `cmd/fake-imex` will retire; superseded by real `--nogpu` daemon                              |

**Delivery**: chardevs + `/proc/devices` overlay materialized on host; real IMEX daemon delivered via image layer + `imex-nogpu-shim` execve wrapper (not managed by node-agent).

**Reconcile trigger**: `state.Subsystems.imexChannels` toggle or device count change.

#### InfiniBand HCA

**What we pretend exists**: a Mellanox ConnectX-class InfiniBand adapter per simulated node, with a fabric spanning multiple nodes.

| Surface                                                                                                                                                                                                                                           | Consumers                                                                  | Coverage                                                                                                                                                                         |
|---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|----------------------------------------------------------------------------|----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `/sys/class/infiniband/mlx5_<N>/{board_id,fw_ver,hca_type,hw_rev,node_desc,node_guid,node_type,sys_image_guid,ports/<P>/{state,phys_state,lid,lid_mask_count,rate,sm_lid,sm_sl,cap_mask,link_layer,port_guid,gids/,pkeys/,counters/,gid_attrs/}}` | Network Operator, `ibstat`, `ibv_devinfo`, DCGM fabric metrics, Topograph  | ✓ `ibhca` via `pkg/network/mockib/render` — full HCA surface (18 file attributes + `gids/`, `pkeys/`, `counters/`, `gid_attrs/` subdirs); `gids/0` derived as `fe80::+port_guid` |
| `libibverbs` C ABI (`ibv_get_device_list`, `ibv_open_device`, `ibv_query_port`, ...)                                                                                                                                                              | RDMA-aware apps, MPI, DCGM                                                 | ✓ `ibhca` stages `libibmockverbs.so` for LD_PRELOAD                                                                                                                              |
| `libibumad` UMAD socket protocol                                                                                                                                                                                                                  | admin/diagnostic tools (`ibping`, `iblinkinfo`, `ibnetdiscover`, `sminfo`) | ✓ `libibmockumad.so` LD_PRELOAD → Unix socket to in-process `mock-ib` daemon via `pkg/network/mockib/daemon.Server`; `Run()` supervises the daemon                               |
| Cross-node fabric relay (TCP, `MOCK_IB=full`)                                                                                                                                                                                                     | multi-node `iblinkinfo`, subnet discovery                                  | ✓ `ibhca` fabric mode via `pkg/network/mockib/fabric`                                                                                                                            |
| IB CLI tools (`ibstat`, `iblinkinfo`, `ibnetdiscover`, `ibping`, `ibv_devinfo`) — real ELFs, RPATH-patched                                                                                                                                        | operator introspection, GPU Operator IB validator                          | ✓ `ibhca` stages tools RPATH-patched at Docker build time by `stage-ib-tools.sh`                                                                                                 |
| RDMA netlink (`RDMA_NL_LS`) events                                                                                                                                                                                                                | newer RDMA management tools                                                | ✗ **gap** — not simulated                                                                                                                                                        |
| CM (Communication Manager) socket for the RDMA data path                                                                                                                                                                                          | MPI, workload data-plane                                                   | N/A — data-plane simulation is not the design goal                                                                                                                               |

**Delivery**: sysfs tree mounted at `/sys/class/infiniband` in containers; C-shim libs via LD_PRELOAD; Unix socket for UMAD wire protocol; optional TCP fabric relay for cross-node.

**Reconcile trigger**: `state.NodeShape.Topology.Network` changes; `state.Subsystems.ib.mode` toggle. `Reconcile` finishes rendering sysfs before `Run` starts the daemon — the natural within-component ordering.

#### NVLink fabric / compute domain

**What we pretend exists**: an NVLink fabric domain (rack or larger) with clique membership assignments per GPU node, exposed through NVML.

| Surface                                                               | Consumers                                                 | Coverage                                                                                                                     |
|-----------------------------------------------------------------------|-----------------------------------------------------------|------------------------------------------------------------------------------------------------------------------------------|
| `nvmlSystemGetConfComputeState`, `nvmlDeviceGetFabricInfo` NVML calls | DRA compute-domain kubelet plugin, ComputeDomain workload | ✓ NVML impl reads `state.Fabric`; `nvlink` writes the compute-domain overlay the shim consumes                               |
| Cluster UUID, clique ID, fabric-domain scope                          | ML frameworks doing multi-node collective ops             | ✓ generated upstream (Control Plane per MEP-0001, or `FileSource`-provided); agent applies verbatim — does not mint identity |
| `nvidia-smi topo -m` output (NVLink adjacency matrix)                 | operator introspection, ML framework auto-tuning          | ✓ derived from `state.NodeShape.Topology.GpuFabric`                                                                          |
| Per-link bandwidth counters via NVML                                  | DCGM-Exporter NVLink metrics                              | ~ static values from state; no live variation                                                                                |
| NVLink LP link-state change events                                    | operator tools reacting to link degradation               | ✗ **gap** — no runtime injection path for link-down events                                                                   |

**Delivery**: `topology.yaml` overlay materialized on host; consumed by mock NVML at workload dlopen time.

**Reconcile trigger**: `state.Fabric` or `state.NodeShape.Topology.GpuFabric` changes.

#### CDI (Container Device Interface) surface

**What we pretend exists**: a CDI-aware container runtime finding NVIDIA device references in `/var/run/cdi/*.yaml`.

| Surface                                                                     | Consumers                                                                              | Coverage                                              |
|-----------------------------------------------------------------------------|----------------------------------------------------------------------------------------|-------------------------------------------------------|
| `/var/run/cdi/nvidia.yaml` — vendor `nvidia.com`, class `gpu`, device `all` | K8s device-plugin CDI strategies, GPU Operator cdi-mode, workload pods via CDI request | ✓ `cdi` — regenerated from `state.Devices`            |
| `/var/run/cdi/nvml-mock-nri.yaml` — devices referenced by NRI plugin        | `nvml-mock-nri` when injecting via CDI (MEP-0002 direction)                            | ✓ `cdi`                                               |
| MIG partition CDI specs (`nvidia.com/mig-1g.5gb=...`)                       | MIG-aware workloads                                                                    | ✗ **gap** — MIG partitioning not simulated end-to-end |
| CDI hooks (`createContainer`, `startContainer`) referencing helper scripts  | GPU Operator's toolkit hooks                                                           | ✗ **gap** — hook scripts not shipped                  |

**Delivery**: YAML files materialized at `/host/var/run/cdi/`, discovered by containerd 2.x (`enable_cdi = true` default).

**Reconcile trigger**: `state.Devices` or `state.Software` changes.

### Compatibility carry-over: K8s-visible node identity (not a subsystem)

Node identity is **not a subsystem** of the Mokka Node Agent — it fails the definition of a simulated component (*"a real-world thing we pretend exists on the host"*, per [§Simulated Surface](#simulated-surface)). Node identity is K8s API state we mutate, not host state we render. It shares no ownership model with `gpudriver`, `pcibus`, `ibhca`, `imex`, `nvlink`, `fabricmanager`, or `cdi`.

This appendix documents the surfaces the current `setup.sh` publishes to node-adjacent K8s state, only so implementers refactoring `setup.sh` into the node agent know where each surface goes.

**Folded into subsystems**:

- NFD feature file `/etc/kubernetes/node-feature-discovery/features.d/nvml-mock.features` → belongs to [`pcibus`](#gpu-device-on-the-pci-bus). PCI presence is what NFD labels from; the file exists because NFD reads real host `/sys/bus/pci/` and cannot see the mock's isolated PCI tree.
- `/run/nvidia/driver` symlink → belongs to [`gpudriver`](#nvidia-gpu-driver). The symlink points at the driver root `gpudriver` materializes.

**Remaining surfaces (delegated, gap, or N/A)**:

| Surface                                                                     | Destination                                                                                                                                                                                                                            |
|-----------------------------------------------------------------------------|----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| Node label `nvidia.com/gpu.present=true`                                    | **Delegated out of the agent.** NFD applies from `pcibus`'s feature-file bridge; GFD applies from mock NVML; MCP can apply directly. Optional `--legacy-node-label` flag retains `setup.sh` parity with a deprecation warning.          |
| Richer NFD features: GPU count, driver version, MIG support                 | ✗ **gap** — not covered by the immediate refactor. Extend `pcibus` (PCI-derived features) or defer to GFD (NVML-derived features).                                                                                                     |
| Container-runtime CDI configuration via `nvidia-ctk config`                 | N/A — bypassed; we rely on containerd's built-in CDI discovery.                                                                                                                                                                        |

**Refactor path**:

- **Immediate (this MEP)**: NFD feature-file rendering folds into `pcibus.Reconcile`; `/run/nvidia/driver` symlink folds into `gpudriver.Reconcile`. **No `nodeidentity` package is created under `internal/agent/`.**
- **Compat mode**: if `setup.sh` parity is required in a test env, an opt-in `--legacy-node-label` flag applies the K8s node label at agent startup and prints a deprecation warning. Expected to be removed once MCP / GFD / NFD label the node from real substrate in the target environment.
- **Long-term**: `/run/nvidia/driver` symlink is a GPU Operator quirk — track upstream removal, then drop from `gpudriver.Reconcile`.

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

[MEP0001]: ../0001-mokka-control-plane/README.md
[MEP-0001 (Mokka Control Plane)]: ../0001-mokka-control-plane/README.md
