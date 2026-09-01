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
- Mokka Node Agent should act as a supervisor that gives a shared lifecycle to all e.g. `Stage()`, `Apply()`, `Run()` (for long-lived operations like IB sim servers), `Revoke()`, `Discard()`. It will act as a reconciler.

Additionally, we want to run as much of the simulation logic in parallel as possible, leveraging their independence,
so they apply as fast as available CPU/IOPS allow.

### Non-Goals

- This proposal wants to keep the current simulation logic identical. Any missing coverage or improvements should be done outside the MEP. 
- The allocation watcher stays the separate sidecar it is today (`nvml-mock-ctl watch-allocations`). It simulates no surface, and it runs with `drop: ["ALL"]` and two mounts — folding it into the agent would hand the pod-resources socket to a process that needs `mknod` and write access across `/host`.

## Proposal

<!--
This is where we get down to the specifics of what the proposal actually is.
This should have enough detail that reviewers can understand exactly what
you're proposing, but should not include things like API designs or
implementation. What is the desired outcome and how do we measure success?
The "Design Details" section below is for the real nitty-gritty.
-->

Instead of having many tiny CLIs and a big setup.sh script that starts them and do a portion of simuation,
we will have one mokka-node-agent CLI that acts as reconciler and superviser of multiple `Simulator` structs, 
each knows how to simulate/unsimulate a specific subsystem like devicedriver, imex, etc.

The mokka-node-agent CLI will load configuration from available sources (filesystem & Control Plane REST API) 
and pass it to each `Simulator` for reconciliation. 

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

#### Simulator

One owner per simulated component:

```go
type Simulator interface {
    Name() string
    Stage(ctx context.Context, host Host, state *State) error // materialize artifacts; nothing outside can see them yet
    Discard(ctx context.Context, host Host) error             // inverse of Stage
    Ready() bool
}

// Applier is implemented by simulators whose staged artifacts something outside
// the node acts on — containerd, NFD, the GPU Operator validator.
type Applier interface {
    Apply(ctx context.Context, host Host, state *State) error
    Revoke(ctx context.Context, host Host) error
}

// Daemon is implemented by simulators that supervise a long-running background
// process (fabricmanager marker loop, mock-ib server). Only fabricmanager and
// infiniband implement this. The agent launches Run once, on the Stage barrier
// of the first successful reconcile; Reload delivers later States to it.
type Daemon interface {
    Run(ctx context.Context) error
    Reload(ctx context.Context, state *State) error
}
```

- `Stage` and `Apply` together are the level-triggered reconcile: both re-run on every state change and must be idempotent. `Revoke` and `Discard` run only on shutdown.
- They converge rather than only materialize: a simulator removes surfaces it previously created that the current `State` no longer calls for, so shrinking a node from 8 GPUs to 4 removes `/dev/nvidia4..7` and their PCI entries.
- Internally, `Stage` parallelizes independent surfaces via a local `errgroup`. The GPU-driver-footprint simulator runs 7 things (chardevs + NVML shim + CUDA shim + nvidia-smi + procfs version + procfs params + engine config) concurrently.
- Only three simulators implement `Applier`: `cdi` (both CDI YAMLs), `gpudriver` (`/run/nvidia/driver` symlink), `pcibus` (NFD feature file). The other four have no surface a third party acts on.
- `Stage`↔`Discard` and `Apply`↔`Revoke` are exact inverses, so the shutdown order is written once in the agent and cannot delete an artifact before the spec that references it.
- Only `fabricmanager` and `ib` implement `Daemon`. The agent type-asserts each `Simulator` to `Daemon` and launches `Run` for those that match, on the Stage barrier rather than at startup — `mock-ib` scans its rendered sysfs tree when the server is constructed, so it cannot start before `Stage`. A simulator that owns a file something else can delete re-asserts it from `Run`, like the fabric-manager marker every 2 s.
- `Discard` removes only files the simulator created; the top-level `/host/var/lib/nvml-mock` root is left to the pod's hostPath lifecycle.
- `Ready()` powers `/readyz` and attributes failures per surface.

`Host` abstracts the on-host root (`/host/var/lib/nvml-mock`, `/host/dev`, `/host/proc`, `/host/sys`, `/host/etc`). Tests retarget to `t.TempDir()`.

#### State

This is where the desired state comes from:

```go
type StateSource interface {
    Watch(ctx context.Context) <-chan Update // sends current on subscribe; pushes on change
    Close() error
}

// Update is one observation. Exactly one of State or Err is set.
// Err means the source could not refresh — the last good State stays in force.
type Update struct {
    State *State
    Err   error
    At    time.Time // last successful contact with the backing store
}
```

How a source detects change — inotify, polling, long-poll — is its own business.
What it cannot hide is failure: without `Err`, silence means both "nothing changed" and "the Control Plane is down".
A closed channel means the source is terminally done and the agent stops; a source never sends a zero `State` to signal anything.

The compiled state the reconciler acts on maps 1:1 to what [MEP-0001]'s Control Plane will emit:

```go
type State struct {
    Generation int64              // MEP-0001 allocation generation; reported back as observed
    Node       NodeMeta           // hostRoot, nodeName, hostname
    Software   SoftwareVersions   // driver / NVML / CUDA (MEP-0001 §SGPURackProfile.spec.software)
    NodeShape  NodeShape          // GPU count, host CPU, PCIe/NUMA topology, GPU fabric, network
    Devices    []DeviceSpec       // per-GPU: identity + hardware + runtime
    Fabric     FabricState        // clique ID + fabric domain
}
```

Two implementations from day one:

- `FileSource` — watches the mounted ConfigMap profile YAML plus `overrides.yaml`; merges into `State`. ConfigMap updates arrive as an atomic `..data` symlink swap, so the watch is on the directory — watching the file pins the replaced inode.
- `ControlPlaneSource` — [MEP-0001]'s polling client.

`FileSource` reads today's `configs/mock-nvml-config-*.yaml` and compiles it into `State`.
`ControlPlaneSource` will emit an already-compiled `State`. Components only see `State`.

`overrides.yaml` is a source input, never an agent-materialized surface, so `nvml-mock-ctl` and `allocwatch` keep writing it under their shared lock (`pkg/gpu/mockctl`).
`ControlPlaneSource` merges Control Plane state underneath it: node-local overrides win, which is what keeps runtime failure injection working without a pod restart.

#### Agent

This is the reconciler & supervisor.

`Agent.Run(ctx)`:

1. Subscribe to `StateSource.Watch(ctx)`; cache the last `State` in memory ([MEP-0001]'s crash-tolerance requirement). An `Update` carrying `Err` leaves that cache in force — the agent keeps serving it and does not reconcile.
2. **Stage wave (parallel)** — on every `State` (initial + updates), call `Stage(ctx, host, state)` on every simulator concurrently via a `sync.WaitGroup`. Failures are fully isolated — a failing simulator never cancels its siblings. All errors are collected; if any Stage failed, the Apply wave is skipped and the errors are returned to the reconcile loop. Apply requires a clean Stage because appliers depend on Stage artifacts being present (CDI spec → chardevs, NFD file → PCI sysfs tree).
3. **Barrier, then apply wave (parallel)** — once every `Stage` has returned, call `Apply(ctx, host, state)` on every `Applier`. The barrier is what stops containerd admitting a container against a CDI spec whose chardevs do not exist yet; reconciling again cannot undo that admission.
4. **Supervisor wave (parallel, launched once)** — each simulator that implements `Daemon` has its `Run(ctx)` launched under a supervisor `errgroup` at startup (type assertion, not a required method). Daemons continue across state changes; only a canceled `ctx` stops them.
5. Expose `/healthz` + `/readyz` HTTP endpoints, aggregated + per-simulator (shape from `cmd/nvml-mock-nri/main.go:107` `serveHealth`). `/healthz` is liveness only and never depends on `StateSource` reachability: otherwise one Control Plane outage restarts — and per step 6, tears down — the whole fleet at once. `/readyz` means the simulators reconciled the last accepted `State`, and is red only until the first one arrives; later staleness is a metric over `Update.At` and `State.Generation`, not a probe.
6. On `ctx.Done()`: cancel `Run` goroutines, `Revoke` on every `Applier` in parallel, then — after that wave completes — `Discard` on every simulator in parallel. Both teardown waves use `errgroup.Group` rather than `errgroup.WithContext`: staging wants fail-fast, teardown wants best-effort so one stuck simulator does not strand the rest of the host. Because `ctx` is already done at this point, both waves must use a fresh context: `context.WithTimeout(context.WithoutCancel(ctx), timeout)`, budgeted under `terminationGracePeriodSeconds` (same pattern as `internal/controlplane/server.go:84`). Without this, any context-aware work in `Revoke` or `Discard` returns immediately and the node keeps advertising surfaces the agent believes it retracted.

Graceful shutdown via `signal.NotifyContext(ctx, SIGINT, SIGTERM)`.

### Simulators

One per simulated component. Each is a package under `internal/agent/`:

| Package                 | Simulates                                                       | Stage does (in parallel)                                                                   | Apply does                            | Daemon.Run does                                                      |
|-------------------------|-----------------------------------------------------------------|--------------------------------------------------------------------------------------------|-----------------------------------------|----------------------------------------------------------------------|
| `gpudriver`             | Component 1 — GPU driver footprint                              | chardevs, NVML shim, CUDA shim, nvidia-smi, procfs version+params, mock-NVML engine config | `/run/nvidia/driver` symlink            | —                                                                    |
| `pcibus`                | Component 2 — GPU on PCI bus                                    | PCI sysfs tree + libpcisysfs.so staging                                                  | NFD feature file                        | —                                                                    |
| `fabricmanager`         | Component 3 — NVSwitch fabric manager                           | initial marker write                                                                       | —                                       | re-assert marker every 2 s                                           |
| `imex`                  | Component 4 — NVIDIA IMEX                                       | IMEX channel chardevs + `/proc/devices` overlay                                            | —                                       | —                                                                    |
| `ib`                 | Component 5 — InfiniBand HCA                                    | IB sysfs tree + libibmock*.so staging + IB CLI tool staging                                | —                                       | `internal/ib/daemon.Server`; optional fabric relay            |
| `nvlink`                | Component 6 — NVLink fabric / compute domain                    | topology YAML overlay                                                                      | —                                       | —                                                                    |
| `cdi`                   | Component 7 — CDI surface                                       | —                                                                                          | `nvidia.yaml` + `nvml-mock-nri.yaml`    | —                                                                    |

#### Example: `devicedriver.Stage`

```go
func (c *DeviceDriver) Stage(ctx context.Context, host host.Host, state *node.State) error {
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

The reconciler is a fan-out with one barrier:

```
                     ┌── gpudriver      (chardevs + libs + smi + procfs + engine config, all parallel)
                     ├── pcibus         (sysfs tree)
                     ├── fabricmanager  (marker)                             ┌── cdi        (2 YAMLs)
   State snapshot ───┼── imex           (chardevs + /proc/devices overlay) ──┼── gpudriver  (/run/nvidia/driver symlink)
                     ├── ib             (sysfs tree)                         └── pcibus     (NFD feature file)
                     └── nvlink         (topology overlay)
                          Stage wave              barrier +                      Apply wave
                                              supervisor wave
                                        (Run: fabricmanager marker
                                         loop, mock-ib daemon)
```

Startup time ≈ `max(t_stage) + max(t_apply)` instead of `sum(t_phase)`. Dominant cost is IB sysfs render + `mock-ib` daemon warm-up; the apply wave is four small file writes, so the barrier costs almost nothing.

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
| Mock-NVML engine on-disk config (compiled `state` → engine config file the shim reads at dlopen)              | mock-NVML shim itself at container-run time                                  | ✓ `gpudriver` — atomic write. The runtime override file beside it is owned by `nvml-mock-ctl` and `allocwatch` under their shared `pkg/gpu/mockctl` lock, not by the agent |
| `/run/nvidia/driver` symlink → `/var/lib/nvml-mock/driver`                                                     | GPU Operator validator (probes for driver root on the host)                  | ✓ `gpudriver.Apply` — atomic `ln -sfn`, after the barrier so the driver root exists                                                                                        |

**Delivery**: files materialized under `/host/var/lib/nvml-mock/driver/`, mounted into workload containers via CDI (`nvidia.yaml`) or NRI overlay. LD_PRELOAD is not used here (Go consumers bypass it).

**Restage trigger**: `state.Software` or `state.Devices` fields change.

#### GPU Device on the PCI bus

**What we pretend exists**: NVIDIA GPU hardware attached to the PCIe root complex, discoverable by any tool that enumerates PCI devices.

| Surface                                                                                        | Consumers                                                                                                      | Coverage                                                                   |
|------------------------------------------------------------------------------------------------|----------------------------------------------------------------------------------------------------------------|----------------------------------------------------------------------------|
| `/sys/bus/pci/devices/<BDF>/{vendor,device,class,numa_node,subsystem_vendor,subsystem_device}` | NFD (`feature.node.kubernetes.io/pci-10de.present`), GPU Operator validator, `nvidia-smi topo`, K8s DRA driver | ✓ `pcibus` — full materialization from `state.NodeShape.Topology.GpuSlots` |
| `/sys/devices/pci<domain>:<bus>/...` topology tree with intermediate bridges                   | Topograph, DCGM topology discovery                                                                             | ✓ `pcibus`                                                                 |
| `/sys/bus/pci/devices/<BDF>/driver → /sys/bus/pci/drivers/nvidia` symlink                      | GPU Operator's "is nvidia driver bound" probes                                                                 | ? verify; add explicit test to MEP-0003 test plan                          |
| `/sys/bus/pci/devices/<BDF>/config` (PCIe config space, 4 KB)                                  | `lspci -vv`, low-level probes                                                                                  | ✗ **gap** — not materialized                                               |
| `/proc/bus/pci/devices`                                                                        | legacy `lspci` fallback                                                                                        | ✗ **gap** — not materialized                                               |
| `libpci`-based tools via `libpcisysfs.so` LD_PRELOAD                                         | `lspci`, C-based sysfs walkers                                                                                 | ✓ `pcibus` stages the shim + owns the LD_PRELOAD entry                     |
| NFD feature file `/etc/kubernetes/node-feature-discovery/features.d/nvml-mock.features` → `feature.node.kubernetes.io/pci-10de.present=true` | NFD-based operators, DRA driver | ✓ `pcibus.Apply` — bridge because NFD reads real host `/sys/bus/pci/`, which cannot see the mock's isolated PCI tree |

**Delivery**: files under `/host/var/lib/nvml-mock/sys/` mounted at `/sys` in containers via CDI. LD_PRELOAD of `libpcisysfs.so` covers C `libpci` consumers.

**Restage trigger**: `state.NodeShape.Topology` changes.

#### NVSwitch fabric manager

**What we pretend exists**: the `nv-fabricmanager` daemon running on a NVSwitch-equipped platform (HGX, GB200/GB300), signaling fabric readiness.

| Surface                                                                                       | Consumers                                                                                  | Coverage                                                                                                                                                             |
|-----------------------------------------------------------------------------------------------|--------------------------------------------------------------------------------------------|----------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| Readiness marker `/var/lib/nvml-mock/fabric-state/fabricmanager.ready`, re-asserted every 2 s | GPU Operator (waits for fabric ready before workloads), nvidia-smi via NVML fabric queries | ✓ `fabricmanager` via `internal/fabricmanager`; `Run()` supervises the re-assertion loop; optional init delay simulates real startup latency                                    |
| Process presence in `ps` / `systemctl status nvidia-fabricmanager`                            | operator diagnostic tooling                                                                | ~ marker only; process name is `mokka-node-agent`, not `nv-fabricmanager`. Acceptable divergence unless a consumer greps for the process name — call out in MEP-0003 |
| `nv-fabricmanager` telemetry Unix socket `/run/nvidia-fabricmanager/socket`                   | DCGM-Exporter `fabric_manager_status` collector                                            | ✗ **gap** — DCGM-Exporter's fabric metrics report unknown                                                                                                            |
| `nvswitch-audit` CLI                                                                          | operator diagnostics                                                                       | ✗ **gap** — CLI not shipped                                                                                                                                          |

**Delivery**: file materialization only; no IPC endpoint simulated.

**Restage trigger**: `state.Fabric` changes (enable/disable, init delay).

#### NVIDIA IMEX subsystem

**What we pretend exists**: NVIDIA IMEX (Inter-node Memory Exchange) capability + daemon for multi-node compute-domain coordination.

| Surface                                                                          | Consumers                                             | Coverage                                                                                                         |
|----------------------------------------------------------------------------------|-------------------------------------------------------|------------------------------------------------------------------------------------------------------------------|
| `/dev/nvidia-caps-imex-channels/channel<N>` chardevs (major 235, 2048 minors)    | DRA compute-domain kubelet plugin                     | ✓ `imex` — channels + majors materialized                                                                        |
| `/proc/devices` entry for `nvidia-caps-imex-channels`                            | DRA driver's `ALT_PROC_DEVICES_PATH` consumer         | ✓ `imex` via `pkg/system/mockimex`                                                                               |
| Real `nvidia-imex --nogpu` daemon process (compute-domain-daemon image only)     | ComputeDomain workload's cross-node peer coordination | ✓ `imex-nogpu-shim` binary wraps upstream `nvidia-imex`; installed at Docker build time, out of node-agent scope |
| Legacy IMEX peer marker files `/var/lib/nvml-mock/imex-state/*` (fake-imex path) | none currently                                        | ✗ **deprecated** — `cmd/fake-imex` will retire; superseded by real `--nogpu` daemon                              |

**Delivery**: chardevs + `/proc/devices` overlay materialized on host; real IMEX daemon delivered via image layer + `imex-nogpu-shim` execve wrapper (not managed by node-agent).

**Restage trigger**: `state.Subsystems.imexChannels` toggle or device count change.

#### InfiniBand HCA

**What we pretend exists**: a Mellanox ConnectX-class InfiniBand adapter per simulated node, with a fabric spanning multiple nodes.

| Surface                                                                                                    | Consumers                                                                  | Coverage                                                                                                                                                                         |
|------------------------------------------------------------------------------------------------------------|----------------------------------------------------------------------------|----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `/sys/class/infiniband/mlx5_<N>/*`                                                                         | Network Operator, `ibstat`, `ibv_devinfo`, DCGM fabric metrics, Topograph  | ✓ `ib` via `internal/ib/sysfs` — full HCA surface (18 file attributes + `gids/`, `pkeys/`, `counters/`, `gid_attrs/` subdirs); `gids/0` derived as `fe80::+port_guid` |
| `libibverbs` C ABI (`ibv_get_device_list`, `ibv_open_device`, `ibv_query_port`, ...)                       | RDMA-aware apps, MPI, DCGM                                                 | ✓ `ib` stages `libibmockverbs.so` for LD_PRELOAD                                                                                                                              |
| `libibumad` UMAD socket protocol                                                                           | admin/diagnostic tools (`ibping`, `iblinkinfo`, `ibnetdiscover`, `sminfo`) | ✓ `libibmockumad.so` LD_PRELOAD → Unix socket to in-process `mock-ib` daemon via `internal/ib/daemon.Server`; `Run()` supervises the daemon                               |
| Cross-node fabric relay (TCP, `MOCK_IB=full`)                                                              | multi-node `iblinkinfo`, subnet discovery                                  | ✓ `ib` fabric mode via `internal/ib/fabric`                                                                                                                            |
| IB CLI tools (`ibstat`, `iblinkinfo`, `ibnetdiscover`, `ibping`, `ibv_devinfo`) — real ELFs, RPATH-patched | operator introspection, GPU Operator IB validator                          | ✓ `ib` stages tools RPATH-patched at Docker build time by `bundle-ib-tools.sh`                                                                                                 |
| RDMA netlink (`RDMA_NL_LS`) events                                                                         | newer RDMA management tools                                                | ✗ **gap** — not simulated                                                                                                                                                        |
| CM (Communication Manager) socket for the RDMA data path                                                   | MPI, workload data-plane                                                   | N/A — data-plane simulation is not the design goal                                                                                                                               |

**Delivery**: sysfs tree mounted at `/sys/class/infiniband` in containers; C-shim libs via LD_PRELOAD; Unix socket for UMAD wire protocol; optional TCP fabric relay for cross-node.

**Restage trigger**: `state.NodeShape.Topology.Network` changes; `state.Subsystems.ib.mode` toggle. `Stage` finishes rendering sysfs before `Run` starts the daemon — the natural within-component ordering.

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

**Restage trigger**: `state.Fabric` or `state.NodeShape.Topology.GpuFabric` changes.

#### CDI (Container Device Interface) surface

**What we pretend exists**: a CDI-aware container runtime finding NVIDIA device references in `/var/run/cdi/*.yaml`.

| Surface                                                                     | Consumers                                                                              | Coverage                                              |
|-----------------------------------------------------------------------------|----------------------------------------------------------------------------------------|-------------------------------------------------------|
| `/var/run/cdi/nvidia.yaml` — vendor `nvidia.com`, class `gpu`, device `all` | K8s device-plugin CDI strategies, GPU Operator cdi-mode, workload pods via CDI request | ✓ `cdi.Apply` — regenerated from `state.Devices`            |
| `/var/run/cdi/nvml-mock-nri.yaml` — devices referenced by NRI plugin        | `nvml-mock-nri` when injecting via CDI (MEP-0002 direction)                            | ✓ `cdi.Apply`                                               |
| MIG partition CDI specs (`nvidia.com/mig-1g.5gb=...`)                       | MIG-aware workloads                                                                    | ✗ **gap** — MIG partitioning not simulated end-to-end |
| CDI hooks (`createContainer`, `startContainer`) referencing helper scripts  | GPU Operator's toolkit hooks                                                           | ✗ **gap** — hook scripts not shipped                  |

**Delivery**: YAML files materialized at `/host/var/run/cdi/`, discovered by containerd 2.x (`enable_cdi = true` default).

**Restage trigger**: `state.Devices` or `state.Software` changes.

### Tech Debt

####  K8s-visible node identity

Node identity is **not a subsystem** of the Mokka Node Agent. Node identity is K8s API state we mutate, not host state we render. 

This appendix documents the surfaces the current `setup.sh` publishes to node-adjacent K8s state, only so implementers refactoring `setup.sh` into the node agent know where each surface goes.

**Folded into subsystems**:

- NFD feature file `/etc/kubernetes/node-feature-discovery/features.d/nvml-mock.features` → belongs to [`pcibus`](#gpu-device-on-the-pci-bus). PCI presence is what NFD labels from; the file exists because NFD reads real host `/sys/bus/pci/` and cannot see the mock's isolated PCI tree.
- `/run/nvidia/driver` symlink → belongs to [`gpudriver`](#nvidia-gpu-driver). The symlink points at the driver root `gpudriver` materializes.

**Remaining surfaces (delegated, gap, or N/A)**:

| Surface                                                     | Destination                                                                                                                                          |
|-------------------------------------------------------------|------------------------------------------------------------------------------------------------------------------------------------------------------|
| Node label `nvidia.com/gpu.present=true`                    | **Delegated out of the agent.** NFD applies from `pcibus`'s feature-file bridge; GFD applies from mock NVML; Mokka Control Plane can apply directly. |
| Richer NFD features: GPU count, driver version, MIG support | ✗ **gap** — not covered by the immediate refactor. Extend `pcibus` (PCI-derived features) or defer to GFD (NVML-derived features).                   |
| Container-runtime CDI configuration via `nvidia-ctk config` | N/A — bypassed; we rely on containerd's built-in CDI discovery.                                                                                      |

**Refactor path**:

NFD feature-file rendering folds into `pcibus.Apply`; `/run/nvidia/driver` symlink folds into `gpudriver.Apply`. Both are `Applier` surfaces, so the barrier keeps them after the artifacts they advertise.

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
