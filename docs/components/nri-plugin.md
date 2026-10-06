# NRI Plugin

The component that puts mock GPUs inside a container the pod author never
changed.

The [node daemon](node-daemon.md) stages a GPU tree on the host, but a container
sees only what its runtime gives it. Without help, a workload would need
Mokka-specific pod spec changes — a `hostPath` mount, some `MOCK_*`
environment. The NRI plugin removes that step: it registers with containerd's
Node Resource Interface and edits containers as they are created, so a workload
that was allocated GPUs the usual way comes up with the mock driver, unchanged.

!!! note "What NRI is"
    The **Node Resource Interface** is a framework for plugging extensions into
    OCI-compatible container runtimes. A plugin registers with the runtime, is
    notified as containers are created, and may make limited adjustments to a
    container's OCI spec — mounts, devices, environment — before it starts.

    It sits *below* the Container Runtime Interface, so it is a runtime feature
    rather than a Kubernetes API: there is no NRI object in the Kubernetes API
    and nothing on kubernetes.io describing it. containerd 1.7 or later is
    required. See the
    [NRI project](https://github.com/containerd/nri) and
    [containerd's NRI documentation](https://github.com/containerd/containerd/blob/main/docs/NRI.md).

For where it sits in the wider system, see the
[architecture overview](../architecture.md).

## Which containers are injected

The plugin injects only containers that were given GPUs or explicitly asked for
the mock. Everything else on the node is left exactly as authored, just as a
real GPU node gives nothing to a pod that requested no GPU.

| Container | Receives |
|---|---|
| Holds a GPU allocation from the device plugin or the NVIDIA DRA driver | The overlay. The allocated GPUs stay exactly as the scheduler assigned them |
| Pod annotated `nvml-mock.nvidia.com/devices: "true"`, no allocation | The overlay and every mock GPU on the node |
| Pod annotated `nvml-mock.nvidia.com/imex-channels: "true"` | The overlay and the mock IMEX channels, without GPUs |
| The DRA ComputeDomain daemon: container `compute-domain-daemon` in a pod with the `resource.nvidia.com/computeDomain` label | Only the real IMEX binary and the node topology; see [ComputeDomain CDI composition](#computedomain-cdi-composition) |
| Anything else | Nothing |

The **overlay** is the mock driver tree plus the environment that points at it
(`LD_LIBRARY_PATH`, the `LD_PRELOAD` shims and the `MOCK_*` variables) —
enough for `nvidia-smi` and NVML clients to run.

The `devices` annotation is the management path: it gives a pod the whole node,
such as a monitoring agent, without scheduler accounting. It is a pod
annotation, so it applies to every container in that pod.

## What happens to a container

Adjustment runs as a fixed sequence. Only the ComputeDomain daemon's step can fail; see [Startup ordering and recovery](#startup-ordering-and-recovery):

```mermaid
flowchart TB
    create[containerd: CreateContainer] --> skip{Opt-out annotation or<br/>excluded namespace?}
    skip -->|yes| asis[Leave exactly as authored]
    skip -->|no| domain{DRA ComputeDomain<br/>daemon?}
    domain -->|yes| missing[Real IMEX binary<br/>and mock topology only]
    domain -->|no| alloc{Holds a GPU<br/>allocation?}
    alloc -->|yes| keep[Overlay; keep the<br/>allocated GPUs]
    alloc -->|no| mgmt{devices annotation?}
    mgmt -->|yes| all[Overlay + every mock GPU<br/>raw nodes or CDI ref]
    mgmt -->|no| imexq{imex-channels<br/>annotation?}
    imexq -->|yes| imexonly[Overlay]
    imexq -->|no| asis
    keep --> imex[Attach IMEX channels<br/>if annotated]
    all --> imex
    imexonly --> imex
```

### When a container is left alone

Any of these leaves the container exactly as authored:

- the pod carries `nvml-mock.nvidia.com/inject: "false"`;
- its namespace is in the excluded list;
- it holds no GPU allocation and carries neither the `devices` nor the
  `imex-channels` annotation, and is not the DRA ComputeDomain daemon;
- it already mounts the overlay at the destination path.

That last check is what makes re-adjustment safe. A container that already has
the overlay has been through here before, so re-running would double-apply.

### ComputeDomain CDI composition

The upstream DRA driver's CDI edits deliver the mock driver, IMEX shim and CLI,
generated domain config, and device nodes. NRI receives `CreateContainer`
before containerd resolves CDI devices sourced from the pod's resource claim,
so that CDI reference is not available as a selector. Mokka instead requires
both the DRA-owned `resource.nvidia.com/computeDomain` pod label and the
`compute-domain-daemon` container name. It then adds only the node-staged
`nvidia-imex.real` executable and mock topology document, plus the environment
pointer to that topology. It does not mount the ambient overlay, rewrite
`PATH`, or attach devices again.

The two-part match keeps the exception limited to the intended container;
matching only the `nvidia` namespace would also affect unrelated containers.

## Recognising a GPU allocation

The kubelet applies a device plugin's or DRA driver's allocation before the
runtime asks this plugin to adjust the container, so the allocation is visible
in the incoming container spec. Only these count as evidence:

| Evidence | Produced by |
|---|---|
| A numbered `/dev/nvidiaN` character device **and** a cgroup rule allowing exactly that device | device plugin with `--pass-device-specs=true` |
| A CDI device `k8s.device-plugin.nvidia.com/gpu=<id>` | device plugin with the `cdi-cri` device list strategy; `<id>` is the GPU UUID, or its index with `--device-id-strategy=index` |
| A CDI device `k8s.gpu.nvidia.com/claim=<id>` | NVIDIA DRA driver |

The rules are strict so that a container is never mistaken for an allocated
one:

- A privileged container inherits every host device under a wildcard cgroup
  rule. Its `/dev/nvidiaN` nodes were not allocated, so they do not count.
- Control devices such as `/dev/nvidiactl` or `/dev/nvidia-uvm` accompany an
  allocation but do not identify a GPU.
- Other CDI kinds do not count. That includes the device plugin's
  `k8s.device-plugin.nvidia.com/gdrcopy=all` and `…/mofed=all`, which come with
  every allocation, the container toolkit's `nvidia.com/gpu`, and Mokka's own
  `nvml-mock.nvidia.com/gpu=all`.

An allocation always wins over the `devices` annotation: the container keeps
exactly the GPUs it was allocated, and `nvidia-smi` reports that subset, not
the whole node. This is the rule from
[MEP-0002](https://github.com/NVIDIA/k8s-test-infra/tree/main/enhancements/meps/0002-device-plugin-nri-composition):
whatever the scheduler allocated is never widened.

!!! warning "The device plugin must leave evidence of the allocation"
    A device plugin that only sets `NVIDIA_VISIBLE_DEVICES` (the default
    `envvar` strategy) or delivers CDI devices only through pod annotations
    (`cdi-annotations`) leaves no evidence in the container spec. Its pods are
    treated as unallocated and receive nothing. Run the device plugin with
    `--pass-device-specs=true`, as the
    [device plugin guide](../guides/device-plugin.md) does, or with the
    `cdi-cri` strategy, as the GPU Operator does when CDI is enabled.

!!! note "IMEX sits outside this rule"
    IMEX channels are requested by their own annotation and never suppressed.
    Neither the device plugin nor the DRA driver delivers mock IMEX channels, so
    there is nothing to defer to.

## Device injection modes

When the plugin delivers GPUs to a `devices`-annotated pod with no allocation,
`deviceInjectionMode` picks the mechanism. It changes *how*, never *whether*,
and never touches an allocated container.

| Mode | Delivers | Use when |
|---|---|---|
| `raw` (default) | The mock `/dev/nvidiaN` nodes, staged directly into the adjustment | Always works. Required by MEP-0002 to stay reachable, and the only mode that works where CDI is off or absent |
| `cdi` | A CDI device reference the runtime resolves from the spec the `cdi` simulator wrote | containerd 2.x, which enables CDI by default — no container toolkit needed on the node |

An unknown value is rejected rather than coerced. A typo that silently fell back
to `raw` would look identical to a working CDI deployment, and the difference is
only visible in the OCI spec of an already-running pod.

## Annotations

| Annotation | Effect |
|---|---|
| `nvml-mock.nvidia.com/inject: "false"` | Opt out of adjustment entirely |
| `nvml-mock.nvidia.com/devices: "true"` | Without an allocation: receive the overlay and every mock GPU. With an allocation: no effect |
| `nvml-mock.nvidia.com/imex-channels: "true"` | Receive the overlay and the mock IMEX channels |

## Startup ordering and recovery

Every step of the generic overlay degrades rather than blocks.

Before each adjustment, the plugin checks the node agent in its pod. It takes
the shared side of a staging lock, which the agent holds exclusively while it
stages or tears down the driver tree, and asks the agent's `/stagedz` endpoint
whether the tree is staged. `/stagedz` follows the agent's Stage wave only, so
a failed Apply, such as a failed write of the NFD feature file, does not close
it. In `cdi` mode, Stage also withdraws the plugin's CDI spec until Apply
republishes it, so a container created in between gets raw device nodes from
the new tree rather than a spec naming nodes a smaller profile removed. While
the agent is restarting, restaging or not yet staged, the plugin
leaves the container unmodified, logs a warning naming its namespace, pod and
container, and fails `/readyz` with the reason. Containers the plugin skips
anyway, such as those in an excluded namespace, are not reported.

The lock file lives in a memory-backed `emptyDir` mounted only into the node
agent and the plugin, so no workload can hold it and stall staging. It covers
the plugin's decision, not the runtime applying it, so a teardown that starts
in between can still race one container. During pod termination the agent
waits at most half of its shutdown timeout for the lock, then tears down
without it.

Once the agent is serving, individual missing device surfaces can still reduce
injection to overlay-only instead of blocking container creation.

The DRA ComputeDomain daemon is the exception. Its Mokka-specific adjustment is
useful only when both the real IMEX executable and the node topology are staged.
When the node agent stages them (`imex.nodeSoftware.enabled` and
`topology.enabled`), the plugin rejects that container's creation while either
is missing, so kubelet retries until the agent has staged them. Otherwise they
would never appear, so the plugin leaves the daemon unmodified and logs a
warning. The same applies while the node agent is staging or unavailable: with
staging expected, the plugin fails the daemon's creation instead of leaving it
unmodified. This gate applies only to a container named `compute-domain-daemon`
in a pod carrying the DRA ComputeDomain label; it cannot block the node agent or
unrelated workloads.

That choice has a consequence worth knowing about. **A plugin containerd has
unregistered stays alive and silently stops injecting** — nothing crashes, pods
just quietly come up without GPUs. The plugin therefore reports whether
injection is actually happening, rather than merely whether the process is
running.

It also watches for wedged requests. If an in-flight adjustment exceeds the
timeout containerd told the plugin it is applying, the runtime has already
abandoned that request and the container was created without injection. The
threshold is a multiple of the runtime's own timeout, so a single slow request
cannot trigger a restart, but a genuinely stuck plugin gets replaced.

## Mount propagation

The overlay arrives as two bind mounts: the driver tree read-only, and its
`config` directory writable on top of it, because `nvidia-smi --gpu-reset`
writes there. Both are `rprivate`, the propagation a `hostPath` volume without
`mountPropagation` gets.

A container with a `Bidirectional` volume, such as a DRA kubelet plugin, has a
shared root. Without the option, the writable bind was copied onto the node at
`/var/lib/nvml-mock/driver/config`, doubling there with every such container
and staying after the pod was gone. `rslave` would also stop that, but
containerd accepts it only when the node mount holding the overlay is shared or
slave, and otherwise fails container creation. For nodes that already have the
copies, see
[troubleshooting](../troubleshooting.md#mounts-pile-up-on-a-node-with-nri-enabled).

## How the code is split

| Package | Role |
|---|---|
| `internal/nri` | The runtime-coupled half: registers with containerd, translates its container types, reports health |
| `internal/nri/inject` | The decision: what to add to a container. No containerd types cross this boundary, so every rule above is exercisable as a plain table test |

## Related

| To read about | See |
|---|---|
| How the whole system fits together | [Architecture](../architecture.md) |
| What stages the tree this plugin mounts | [Node Daemon](node-daemon.md) |
| Enabling NRI and its chart values | [Installation](../helm-chart.md) |
| A runnable walkthrough | [Node-Wide Injection](../guides/node-wide-injection/README.md) |
