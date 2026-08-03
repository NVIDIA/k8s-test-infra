# How the preflight works

The mechanism, in the order things come up. Every command here is one this sweep actually ran; the
harness that ran them is [`tests/aicr-sweep/`](../../tests/aicr-sweep/).

## Where the mock intercepts

Mokka replaces `libnvidia-ml.so.1`, the NVML library, on the node. Everything above that line is the
real software:

```text
real:   GPU Operator · device plugin · GFD · dcgm-exporter · DRA driver · AICR validators
        ---------------------------------------------------------------- NVML / driver line
mock:   libnvidia-ml.so.1 · nvidia-smi · fabricmanager · ibstat/ibnetdiscover · IMEX channels
        ---------------------------------------------------------------- 
real:   kubelet · containerd · CPU node
```

A consumer calls `nvmlDeviceGetCount`, `nvmlDeviceGetPciInfo`, `nvmlDeviceGetFieldValues` and so on,
and the mock answers from a YAML profile (`gb200`, `gb300`, `h100`, `b200`, `a100`, `l40s`, `t4`). The
consumer cannot tell, because the C ABI is the same: v0.3.0 exports 417 NVML entry points, 135
hand-written and engine-backed, 282 stubs returning `NOT_SUPPORTED` so an `RTLD_NOW` `dlopen`
resolves.

## Bring-up order

1. **Cluster.** kind with containerd NRI enabled. On kind 0.32.0's node image (containerd 2.2.0+) NRI
   is on by default; the config patch is kept because it is load-bearing on containerd 1.7.x.
2. **Mokka.** The chart installs a DaemonSet that stages the mock driver root under
   `/var/lib/nvml-mock`, plus an NRI plugin DaemonSet that registers with containerd.
3. **Injection.** The NRI plugin subscribes to `CreateContainer`. For every container not excluded, it
   bind-mounts the overlay at `/opt/nvml-mock`, prepends `PATH` and `LD_LIBRARY_PATH`, and sets the
   `MOCK_*` environment. Device nodes are added only for containers that opt in.
4. **GPU stack.** The real GPU Operator installs with `driver.enabled=false` and `toolkit.enabled=false`,
   pointed at `NVIDIA_DRIVER_ROOT=/var/lib/nvml-mock/driver`. The device plugin, GFD and dcgm-exporter
   load the mock NVML like any other consumer.
5. **DRA.** The NVIDIA DRA driver's kubelet plugin enumerates through the mock and publishes
   ResourceSlices.
6. **AICR.** `aicr recipe` resolves criteria to a component list; `aicr validate` launches one
   containerized Job per selected check and merges the results into a CTRF report.

## What node-wide injection means in practice

This is the part that surprises people. A pod with no GPU request, no volumes and no runtime class:

```yaml
apiVersion: v1
kind: Pod
metadata: {name: probe}
spec:
  containers:
    - name: probe
      image: debian:bookworm-slim
      command: ["sh","-c","sleep 300"]
```

```console
$ kubectl exec probe -- nvidia-smi -L
GPU 0: NVIDIA GB200 (UUID: GPU-b200b200-0000-0000-0000-000000000000)
...
GPU 7: NVIDIA GB200 (UUID: GPU-b200b200-0000-0000-0000-000000000007)
$ kubectl get pod probe -o jsonpath='{.spec.containers[0].resources}'
{}
```

No pod-spec change. That is what makes AICR run *unmodified*: nothing in the recipe or the checks has
to know it is on a simulated fleet.

When the device plugin also serves a node, MEP-0002 governs the composition: the resource request
wins, and the NRI plugin suppresses its own device injection for any container that already carries
`/dev/nvidia*` nodes or an `nvidia.com/` CDI device. Exactly one component serves a given container.

## How AICR executes against it

```bash
aicr recipe --service kind --accelerator gb200 --intent inference -o recipe.yaml
```

resolves to a 14-component recipe over 5 overlays. Then:

```bash
aicr validate --recipe recipe.yaml --phase deployment --phase conformance --phase performance --fail-on-error=false --output ctrf/validate.json
```

Two things matter here. `--fail-on-error=false`, because the point is to record every outcome rather
than stop at the first failure. And **read the CTRF report, never the exit code**: the exit code
collapses 21 checks into one number.

AICR selects which checks apply. In this sweep it dispatched 9 of 21: the performance phase selected
0, and several conformance checks skipped because the recipe components they target were not
deployed. A check that is never dispatched is recorded `not dispatched`, never as a pass.

## How to read A/B/C/G, and K/U/X

Two independent axes. Keeping them apart is what makes the result trustworthy.

**Bucket** says what a check would prove. It is judged from AICR source before any run, and never
edited to match a result.

| | |
|---|---|
| **A** | Exercises real integration. Could plausibly fail for a real reason. This is the movable-left set. |
| **B** | Green only because the mock answers the API. Would pass regardless of correctness. Not a win. |
| **C** | Hardware-dependent. Cannot be judged pre-silicon. These are why the final gate stays on silicon. |
| **G** | Would be meaningful pre-silicon, but Mokka lacks the capability. Closable, so it is roadmap. |

**Cause** says what stopped a particular run.

| | |
|---|---|
| **K** | kind or single-host artifact. Would not happen on a real cluster. **Not** a Mokka finding. |
| **U** | An upstream dependency has not released what is needed. **Not** a Mokka gap. |
| **X** | AICR has no recipe for the combination. **Not** a Mokka gap. |
| **BUDGET** | The host ran out of memory; the cell was not attempted. |

The rule for telling **G** from **K**, fixed before any cell ran so it cannot be bent to fit a number:

> **Did Mokka promise to render this path or speak this protocol?**
> If the failing read targets something Mokka stages (`/var/lib/nvml-mock/**`, `MOCK_IB_ROOT`, an
> injected `/dev/nvidia*`, the topology ConfigMap) or a protocol Mokka speaks for real (IMEX gRPC, the
> mock-ib TCP relay, an NVML or DCGM call), it is **G**.
> If it targets real host kernel state Mokka never claimed (NUMA nodes, the real PCI tree, the NVLink
> data plane, RDMA verbs), it is **K**.

Worked examples from the sweep:

- The DRA controller crashing on `/proc/devices` is **U**: Mokka renders the substitute, but the DRA
  driver needs an `altProcDevices` flag that is on main and in no release.
- `gb300` is **X**: AICR's catalog has no such accelerator, so there is nothing to run.
- GFD crashing is **K**, provisionally: the repo's own CDI-path e2e asserts GFD labels and passes, so
  the difference is this sweep's no-toolkit configuration. Marked "not distinguished" rather than
  guessed, because the controlled cell did not fit the memory budget.
- The PCI bus ID arriving without its domain prefix is **G**: Mokka renders the tree correctly, so the
  malformed string is Mokka's to fix.
