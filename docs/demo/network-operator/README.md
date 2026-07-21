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
6. **Lets NFD publish the `pci-15b3` label** from the feature file the
   nvml-mock chart already wrote to each node's `features.d` directory (see
   [How NFD sees the mock NIC](#how-nfd-sees-the-mock-nic-demo-only)).
7. **Observes** the operator + NFD pod phases and the per-node `pci-15b3` label.
8. **Pushes**: applies a `NicClusterPolicy` (RDMA shared device plugin), then
   reports what progresses versus what remains blocked.

NFD publishes `feature.node.kubernetes.io/pci-15b3.present=true` on every node
running the mock stack — the exact label the operator's default `NodeFeatureRule`
(`nvidia-nics-rules`) derives for Mellanox NICs. It can do so because the
nvml-mock chart (installed with `infiniband.nfd.publishNicLabel=true`) writes an
NFD **local** source feature file into each node's `features.d` directory, which
NFD's bundled worker reads on its next scan. No `kind.yaml` stamp, no
`kubectl patch`, and no manual `kubectl label` is used.

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
| pci-15b3.present node label | true on every node running the mock stack (NFD publishes it from the nvml-mock features.d file) | The kernel's `/sys` can't be faked, so the nvml-mock chart writes an NFD `local` source feature file to `features.d`; NFD's worker reads it and labels the node. The label takes a scan interval or two to appear (NFD rescan + master apply) |
| `rdma-shared-device-plugin` (after push) | **Linux + Soft-RoCE:** advertises `rdma/rdma_shared_device_a`; the `rdma-test` pod schedules. **Otherwise:** crash-loops (`can not get RDMA subsystem network namespace mode`), no `rdma/*` | On Linux, `run.sh` uses a kind-rdma node image and `docker exec` per node to load `rdma_rxe` and create a real software RDMA device (`rxe0` over `eth0`), so the plugin enumerates it. On macOS/Kind's kernel there is no RDMA netlink subsystem and `rdma_rxe` is unavailable, so it stays blocked |
| OFED/DOCA driver | Not enabled | Builds kernel modules against the host kernel — unsupported on Kind |

The takeaway: NRI node-wide injection makes the mock devices real **to
libc-based userspace tools in injected pods**. NFD's node-level PCI detection is
handled separately, by advertising the mock NIC to NFD's `local` source so it
publishes `pci-15b3.present` (see below). But the operator's device
plugins/drivers (which need real kernel-level RDMA/driver support, run in the
NRI-excluded operator namespace, and are static Go binaries) operate outside
both layers, so they never see the mocks.

## How NFD sees the mock NIC (demo-only)

NFD's `pci.device` source enumerates PCI devices by reading
`/host-sys/bus/pci/devices/*`; that `host-sys` mount is `hostPath: /sys`. On
Kind the real kernel `/sys/bus/pci` has no Mellanox `15b3` devices — the mock
NICs live only inside the overlay staged at `/var/lib/nvml-mock/sys`, and the
kernel's sysfs cannot be faked. NFD also exposes **no values-based way** to
redirect `host-sys` (the hostPath is hardcoded in the chart), and patching the
worker DaemonSet directly is non-durable: the Network Operator reconciles that
DaemonSet and reverts the remount, dropping the derived label.

So instead of touching NFD, the **nvml-mock chart advertises the NIC to NFD's
`local` source**. With `infiniband.nfd.publishNicLabel=true`, the node agent
(`setup.sh`) writes a feature file to the node's NFD `features.d` directory
(`/etc/kubernetes/node-feature-discovery/features.d/nvml-mock-ib.features`)
containing:

```
pci-15b3.present=true
```

NFD's bundled worker mounts that same directory, reads it on its next scan, and
publishes `feature.node.kubernetes.io/pci-15b3.present=true` — the same label
`nvidia-nics-rules` derives for a real Mellanox NIC. Because the file lives on
the node (not in NFD's pod spec), it survives operator reconciles of the NFD
DaemonSet; `cleanup.sh` removes it on pod teardown.

This is **demo-only** scaffolding. It exists solely to let NFD publish the mock
NIC label on a Kind node where the real kernel `/sys` cannot be populated; a
real cluster with physical Mellanox NICs needs none of it (NFD reads the genuine
`/sys/bus/pci` and derives the label via `pci.device`).

## Tier 3: Soft-RoCE — real RDMA so the plugin advances (Linux only)

The `rdma-shared-device-plugin` is a static Go binary that opens a
`NETLINK_RDMA` socket. On Kind's kernel that socket cannot even be created
(`rdma system` → `Failed to open NETLINK_RDMA socket`): there is no RDMA
subsystem, and because the binary makes raw syscalls, `LD_PRELOAD` cannot fake
it. There is no userspace shortcut.

The tractable fix is to give the kernel a **real software RDMA device**:
Soft-RoCE (`rdma_rxe`), an in-tree module that layers RDMA over an ordinary
netdev. With it loaded, `NETLINK_RDMA` registers for real and the plugin
enumerates a device.

`run.sh` gates Tier 3 on `ENABLE_SOFT_ROCE` (default `auto`: enabled on Linux
hosts, disabled elsewhere such as macOS/Docker Desktop). Set `true` or `false`
explicitly to force either behavior. The phase only runs where `rdma_rxe` is
available on a real Linux host; on macOS/Docker Desktop's linuxkit kernel it
is skipped:

Soft-RoCE setup runs directly on each Kind node. The demo builds a dedicated
node image (`deployments/kind-rdma`, overridable via `KIND_NODE_IMAGE` /
`KIND_NODE_BASE`) with the RDMA userspace stack baked in — `rdma` (iproute2),
`modprobe`, the ibverbs **rxe** provider, and `ib_write_bw` — and creates the
cluster from it. `run.sh` then, for each node, runs `docker exec <node> …` to
`modprobe rdma_rxe`, set `rdma system` netns mode to `exclusive`, and
`rdma link add rxe0 type rxe netdev eth0`. This creates a real software RDMA
device on the node's kube netdev, so the operator's `rdma-shared-device-plugin`
enumerates a device and advertises `rdma/rdma_shared_device_a`.

The `rdma_rxe` kernel module still comes from the host (Kind bind-mounts
`/lib/modules` read-only); it is available only on Linux hosts, so the phase
self-skips on macOS/Docker Desktop.

1. The `NicClusterPolicy` selector is `ifNames: ["eth0"]` (matching the rxe
   netdev — rxe is generic software RDMA, not a `15b3` Mellanox NIC).
2. The plugin advertises `rdma/rdma_shared_device_a`; the `rdma-test` pod
   requesting it schedules and runs. NRI still injects the mock ConnectX HCAs,
   so the pod sees both the real rxe-backed resource (kernel) and the mock IB
   fabric (userspace).

**Requirements & limits:**
- Linux host with `rdma_rxe` available. **Not** possible on macOS Docker
  Desktop (the linuxkit kernel ships no `rdma_rxe`); Tier 3 self-skips there.
- Loading `rdma_rxe` mutates the shared host kernel. Cleanup:
  `rdma link del rxe0` per node netns (removed automatically when the Kind node
  containers are deleted) and optionally `modprobe -r rdma_rxe` on the host.
- This provides *generic* software RDMA to unblock the plugin; it does not make
  the mock ConnectX HCAs themselves kernel-visible.

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
