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
6. **Redirects NFD's `host-sys` mount** at the mock PCI tree
   (`/var/lib/nvml-mock/sys`) so NFD self-derives the `pci-15b3` label (see
   [The NFD sysfs redirect](#the-nfd-sysfs-redirect-demo-only)).
7. **Observes** the operator + NFD pod phases and the per-node `pci-15b3` label.
8. **Pushes**: applies a `NicClusterPolicy` (RDMA shared device plugin), then
   reports what progresses versus what remains blocked.

NFD derives `feature.node.kubernetes.io/pci-15b3.present=true` on the workers
**itself** — the exact label the operator's default `NodeFeatureRule`
(`nvidia-nics-rules`) derives for Mellanox NICs. It can do so because run.sh
repoints NFD's `host-sys` volume at the mock PCI tree (`/var/lib/nvml-mock/sys`),
whose synthesized `15b3` NIC entries (with `vendor`/`class` files) NFD's
`pci.device` source reads via `/host-sys`. No `kind.yaml` stamp or manual
`kubectl label` is used.

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
| pci-15b3.present node label | true on workers (NFD-derived via redirected sysfs mount) | NFD's pci.device source reads the mock PCI tree we mount at /host-sys; the real kernel /sys still can't be faked |
| `rdma-shared-device-plugin` (after push) | **Crash-loops** at startup; advertises **no** `rdma/*` | Exits with `can not get RDMA subsystem network namespace mode` — it needs a real RDMA kernel subsystem (rdma netlink) that Kind's kernel does not expose, so it never reaches device enumeration (and it runs in the NRI-excluded operator namespace and is a static Go binary, so it would miss the pod-only mock anyway) |
| OFED/DOCA driver | Not enabled | Builds kernel modules against the host kernel — unsupported on Kind |

The takeaway: NRI node-wide injection makes the mock devices real **to
libc-based userspace tools in injected pods**. NFD's node-level PCI detection is
handled separately, by redirecting its `host-sys` mount at the mock PCI tree so
it self-derives `pci-15b3.present` (see below). But the operator's device
plugins/drivers (which need real kernel-level RDMA/driver support, run in the
NRI-excluded operator namespace, and are static Go binaries) operate outside
both layers, so they never see the mocks.

## The NFD sysfs redirect (demo-only)

NFD's `pci.device` source enumerates PCI devices by reading
`/host-sys/bus/pci/devices/*`; that `host-sys` mount is `hostPath: /sys` by
default. On Kind, the real kernel `/sys/bus/pci` has no Mellanox `15b3` devices
— the mock NICs live only inside the overlay staged at `/var/lib/nvml-mock/sys`,
and the kernel's sysfs cannot be faked. So after installing the operator, run.sh
patches the NFD worker DaemonSet
(`network-operator-node-feature-discovery-worker`) to repoint its `host-sys`
volume at `/var/lib/nvml-mock/sys`, which carries the synthesized `15b3` entries
with `vendor`/`class` files. NFD then discovers the NICs on its own and labels
the workers `feature.node.kubernetes.io/pci-15b3.present=true`.

The patch is a strategic merge that names only the `host-sys` volume; because
`volumes` uses `name` as its merge key, every other NFD mount (`/boot`,
`/etc/os-release`, `/usr/lib`, `/lib`, `/proc/swaps`, `features.d`, and the
worker ConfigMap) is left untouched.

This redirect is **demo-only**. It exists solely to let NFD "see" the mock NICs
on a Kind node where the real kernel `/sys` cannot be populated; a real cluster
with physical Mellanox NICs needs no such redirect (NFD reads the genuine
`/sys/bus/pci`).

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
