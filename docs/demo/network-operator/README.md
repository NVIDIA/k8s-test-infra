# NVIDIA Network Operator over NRI node-wide injection (experiment)

This demo deploys the **real** NVIDIA Network Operator on a Kind cluster where
the mock GPU / InfiniBand stack is delivered ambiently via containerd **NRI**
(the [node-wide-injection](../node-wide-injection) method), and reports **how
far the operator gets** against devices that exist only inside pods.

It is an **exploratory / diagnostic** demo, not a green-path one. Its value is a
reproducible experiment that shows exactly where the real operator does and
does not interact with the mocks.

## Prerequisites

- `docker`, `kind`, `kubectl`, `helm`, `python3`
- Network egress to `helm.ngc.nvidia.com` (chart) and `nvcr.io` /
  `registry.k8s.io` (operator + NFD images), plus `deb.debian.org` from pods
  (the `ib-agent` installs `infiniband-diags` at start).

## What it does

1. Creates a Kind cluster (`nvml-mock-network-operator-demo`, 1 control-plane +
   2 workers) with containerd NRI enabled.
2. Builds and loads the local `nvml-mock` image.
3. Installs `nvml-mock` with the `h100` profile and `nri.enabled=true`, so the
   mock GPU/InfiniBand stack is injected into ordinary pods cluster-wide. The
   operator namespaces are added to `nri.excludedNamespaces` (see
   [NRI boundary](#nri-boundary-why-the-operator-is-excluded)).
4. Deploys a plain `ib-agent` DaemonSet — no `nvidia.com/gpu` request, no
   volumes, no `MOCK_*`/`LD_PRELOAD` env — and runs `ibstat -l` /
   `ibv_devinfo -l` to prove the mock RDMA fabric is present purely from NRI.
5. Installs the real **NVIDIA Network Operator** (bundled NFD).
6. **Observes** the natural blockers (no `pci-15b3` node label; component pod
   phases).
7. **Pushes**: manually labels the workers
   `feature.node.kubernetes.io/pci-15b3.present=true` and applies a
   `NicClusterPolicy` (RDMA shared device plugin), then reports what progresses
   versus what remains blocked.

## Quick start

```bash
./run.sh
```

Re-runnable: it reuses the cluster unless `FORCE_RECREATE=true`, rebuilds and
reloads the image, and redeploys.

Overrides:

```bash
GPU_PROFILE=gb200 ./run.sh
NET_OPERATOR_VERSION=v26.4.0 ./run.sh
SKIP_PUSH=true ./run.sh          # stop after the observation phase
FORCE_RECREATE=true ./run.sh
```

## Expected outcome (and why)

| Component | Outcome | Why |
|-----------|---------|-----|
| `ib-agent` (plain pod) | Sees mock ConnectX-7 HCAs via `ibstat`/`ibv_devinfo` | NRI injects the mock IB sysfs + `LD_PRELOAD` shims into the pod |
| Operator controller + NFD | Running | Standard controllers; no device dependency |
| NFD node labels | No `pci-15b3.present` | NFD scans the node's real `/sys/bus/pci`; the mock devices exist only inside pods |
| `rdma-shared-device-plugin` (after push) | Runs, advertises **no** `rdma/*` | The operator schedules it in its own namespace, which the demo excludes from NRI injection, so it reads the node's real (empty) host sysfs (and even if injected, a static Go binary's raw syscalls would bypass the `LD_PRELOAD`-simulated fabric) |
| OFED/DOCA driver | Not enabled | Builds kernel modules against the host kernel — unsupported on Kind |

The takeaway: NRI node-wide injection makes the mock devices real **to
libc-based userspace tools in injected pods**, but the Network Operator's
node-level detection (NFD, which scans host PCI) and its own components (which
run in the NRI-excluded operator namespace — and are static Go binaries that
would bypass the glibc-level shims even if injected) operate outside that
layer, so they do not see the mocks.

## NRI boundary (why the operator is excluded)

The `nvml-mock-nri` plugin injects `LD_PRELOAD` of glibc shims into every
non-excluded pod. Forcing that into the operator's own (often distroless / Go)
containers risks destabilizing them and muddies the experiment, so the operator
+ NFD namespaces are excluded from injection. The NRI method is demonstrated in
the workload namespace (`ib-agent`) instead.

## Trust boundary

`nvml-mock` with NRI enabled injects into workloads cluster-wide, and the
`mock-ib` fabric listener/socket are test-only (no auth). Run this demo only on
a throwaway Kind cluster, never a shared or production cluster.

## Clean up

```bash
kind delete cluster --name nvml-mock-network-operator-demo
```
