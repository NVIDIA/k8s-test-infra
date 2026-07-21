# kind-rdma — Kind node image with the RDMA userspace stack

A drop-in [`kindest/node`](https://kind.sigs.k8s.io/) image with the RDMA
userspace stack baked in, used by the
[network-operator demo](../../docs/demo/network-operator/) for its Soft-RoCE
(`rdma_rxe`) Tier 3 path.

Baking the stack into the node image removes the per-run package installs the
demo used to do inside an `alpine` DaemonSet, so `rdma` (iproute2), `modprobe`
(kmod), the ibverbs **rxe** provider, and `ib_write_bw` (perftest) are already
present on every node.

## What's inside

`iproute2`, `rdma-core`, `ibverbs-providers`, `ibverbs-utils`, `libibverbs1`, `infiniband-diags`,
`perftest`, `kmod` — installed on top of the pinned `kindest/node` base.

## Kernel module caveat (Linux only)

Kind nodes run on the **host kernel**, so this image cannot ship an
`rdma_rxe.ko` that matches an arbitrary host. The module must exist on the host
(`sudo apt-get install linux-modules-extra-$(uname -r)`); Kind bind-mounts the
host `/lib/modules` read-only into each node, so `modprobe rdma_rxe` from inside
a node loads it. On macOS/Docker Desktop the module is unavailable and the
demo's Soft-RoCE phase self-skips.

## Build

```bash
docker build -t nvml-mock/kind-rdma:soft-roce deployments/kind-rdma
# Override the base (defaults to kindest/node:v1.32.2):
docker build -t nvml-mock/kind-rdma:soft-roce \
  --build-arg BASE_IMAGE=kindest/node:v1.31.4 deployments/kind-rdma
```

The demo's `run.sh` builds this automatically and creates the cluster with
`kind create cluster --image nvml-mock/kind-rdma:soft-roce` (both the tag and
base are overridable via `KIND_NODE_IMAGE` / `KIND_NODE_BASE`).
