<div class="mokka-hero" markdown>
![Mokka](img/logo.png)

<p class="mokka-tagline">Simulate your GPU infrastructure on CPU nodes.</p>
</div>

# Mokka

Mokka turns any Kubernetes cluster into a multi-GPU environment for testing.
It implements the NVIDIA driver interfaces that GPU software talks to, so the
device plugin, the DRA driver, the GPU Operator and `nvidia-smi` all behave as
though real hardware were present. No physical NVIDIA GPU is required.

<div class="grid cards" markdown>

-   **Quick Start**

    Install into a KIND cluster and see simulated GPUs in five minutes.

    [Get started](quickstart.md)

-   **Architecture**

    How the mock NVML library, the CGo bridge and the GPU profiles fit together.

    [Read the design](architecture.md)

-   **Demos**

    Six runnable walkthroughs, from a standalone install to NVSentinel health
    monitoring.

    [Browse demos](demo/README.md)

-   **Configuration**

    Every YAML knob: profiles, topology, failure injection and dynamic metrics.

    [See the reference](configuration.md)

</div>

## Try it

There are two ways in, and they are not interchangeable. Pick by what you have
on disk.

| | Path 1 | Path 2 |
|---|---|---|
| You need | `kind`, `helm`, `docker` | a repo clone, plus `tilt` |
| Cluster | `kind create cluster` | `make cluster-create` |
| Covers | the mock driver, `nvidia-smi`, the device plugin | everything, including the GPU Operator, DRA, NRI and multi-node |
| Gated by CI | no | yes |

### Path 1: you have kind and Helm

Nothing is cloned. Both the image and the chart come from `ghcr.io`, so you
need network access to it plus `docker`, `kind`, `helm` and `kubectl`.

```bash
# 1. Create a cluster
kind create cluster --name mokka

# 2. Load the published image. It is multi-arch, and `kind load docker-image`
# cannot load a multi-arch image from Docker Desktop's containerd store, so
# save a single platform first.
ARCH=$(uname -m | sed 's/x86_64/amd64/; s/aarch64/arm64/')
docker pull ghcr.io/nvidia/nvml-mock:latest
docker save --platform "linux/${ARCH}" ghcr.io/nvidia/nvml-mock:latest -o nvml-mock.tar
kind load image-archive nvml-mock.tar --name mokka

# 3. Install
helm install nvml-mock oci://ghcr.io/nvidia/k8s-test-infra/chart/nvml-mock
```

The chart tolerates every taint, so it schedules on the single control-plane
node that a bare `kind create cluster` gives you. Check the result:

```bash
kubectl rollout status daemonset/nvml-mock --timeout=60s
kubectl exec daemonset/nvml-mock -- nvidia-smi -L
```

```
GPU 0: NVIDIA A100-SXM4-40GB (UUID: GPU-12345678-1234-1234-1234-123456780000)
GPU 1: NVIDIA A100-SXM4-40GB (UUID: GPU-12345678-1234-1234-1234-123456780001)
...
```

**What Path 1 gives you**

- The mock driver root staged on the node under `/var/lib/nvml-mock/driver`:
  `libnvidia-ml.so`, `libcuda.so`, a patched `nvidia-smi`, `/dev/nvidia*`
  device nodes, the PCI sysfs tree and the InfiniBand mock.
- The node label `nvidia.com/gpu.present=true`.
- Anything you can observe from inside the nvml-mock pod: `nvidia-smi`,
  `nvidia-smi -q`, `nvidia-smi topo -m`, `ibv_devinfo`, and
  [runtime fault injection](nvml-mock-ctl.md).
- The NVIDIA device plugin, if you also apply
  [`tests/e2e/device-plugin-mock.yaml`](https://github.com/NVIDIA/k8s-test-infra/blob/main/tests/e2e/device-plugin-mock.yaml),
  the one file you need from the repository. It reads the mock library through
  a hostPath and passes device specs to kubelet itself, so it needs nothing
  from the container runtime. `nvidia.com/gpu` then becomes allocatable and
  pods that request it schedule. Walkthrough:
  [Device Plugin on KIND](helm-chart.md#quick-start-device-plugin-on-kind).

**What Path 1 does not give you**

- **The GPU Operator.** It needs `nvidia-container-toolkit` on the node and an
  `nvidia` containerd runtime handler, neither of which a stock kind node has.
- **The DRA driver.** It needs the `DynamicResourceAllocation` feature gate and
  `resource.k8s.io/v1beta1` in the API server runtime config, so it publishes
  no ResourceSlices here.
- **NRI device injection.** It needs the containerd NRI socket.
- **Anything multi-node**: a heterogeneous fleet, fake-gpu-operator node pools,
  or NVLink clique topology. A bare `kind create cluster` is one node.
- **Automatic library injection into your own pods.** A pod that requests
  `nvidia.com/gpu` gets its `/dev/nvidiaN` device node and nothing else, with
  no `nvidia-smi` and no `libnvidia-ml.so` on its filesystem. Mount the driver
  root and set `LD_LIBRARY_PATH` yourself, the way
  [`local/gpu-validator.k8s.yaml`](https://github.com/NVIDIA/k8s-test-infra/blob/main/local/gpu-validator.k8s.yaml)
  does. This is a boundary of the path, not a bug.

### Path 2: you cloned the repository

Every scenario Path 1 cannot reach needs a cluster built for it, and that is
what `make cluster-create` is. It builds a Kind node image carrying
`nvidia-container-toolkit` with CDI enabled in containerd, then creates a
cluster (1 control-plane and 2 workers) with the DRA feature gate, the NRI
socket, and the node names and labels the multi-node scenarios select on.
[Tilt](https://tilt.dev/) is the installer on top: it builds nvml-mock from
source and applies the Helm value overlays each consumer needs.

Add `tilt` to the Path 1 tool list. Then:

```bash
make cluster-create   # build the node image, create the cluster
tilt up               # install nvml-mock with the a100 profile on every node
```

`tilt up` takes scenario flags after `--`:

| Command | Scenario |
|---|---|
| `tilt up -- --gpu-profile h100` | a different GPU profile |
| `tilt up -- --multi-gpu-profile` | heterogeneous fleet: a100 on `worker-0`, t4 on `worker-1` |
| `tilt up -- --gpu-operator` | NVIDIA GPU Operator in CDI mode |
| `tilt up -- --dra` | NVIDIA DRA driver publishing ResourceSlices |
| `tilt up -- --fgo` | Run:ai fake-gpu-operator sharing the fleet with nvml-mock |
| `tilt up -- --observability` | Prometheus, Grafana and a real `dcgm-exporter` |
| `make cluster-create PROFILE=compute-domain` then `tilt up -- --compute-domain` | 4 workers with NVLink cliques |

This is the environment CI exercises. Every job in the Go E2E workflow runs
`make cluster-create` and then `tilt ci` with these same flags, so a scenario
that works here is the one the tests gate.

Tear down with `make cluster-delete`, passing the same `PROFILE=` you created
with. The full flag reference, including which combinations are mutually
exclusive, is in
[`local/README.md`](https://github.com/NVIDIA/k8s-test-infra/blob/main/local/README.md).

Either way, the [Helm chart guide](helm-chart.md) has the per-consumer
walkthrough for the device plugin, the DRA driver, the GPU Operator and a
multi-node heterogeneous fleet.

## How it fits together

<div class="mokka-architecture" markdown>
![Mokka architecture](img/mokka-general-architecture.png)
</div>

## Components

| Component | Description | Status |
|-----------|-------------|--------|
| Mock NVML (`libnvidia-ml.so`) | 400 NVML C API exports (111 with configurable behavior, 289 stubs), YAML-configurable GPU profiles | Production |
| Mock CUDA (`libcuda.so`) | 15 CUDA functions: init, device, memory management | Early |
| nvidia-smi | Real binary with RPATH patch, backed by mock NVML | Production |
| Helm Chart | DaemonSet deployment with 7 GPU profiles | Production |
| CDI Injection | Container Device Interface specs for GPU Operator | Production |

## GPU profiles

| Profile | GPU Name | VRAM | Architecture |
|---------|----------|------|--------------|
| `a100` | A100-SXM4-40GB | 40 GiB | Ampere |
| `h100` | H100 80GB HBM3 | 80 GiB | Hopper |
| `b200` | B200 | 192 GiB | Blackwell |
| `gb200` | GB200 | 192 GiB | Blackwell |
| `gb300` | GB300 NVL | 288 GiB | Blackwell Ultra |
| `l40s` | L40S | 48 GiB | Ada Lovelace |
| `t4` | Tesla T4 | 16 GiB | Turing |

## Tested consumers

Software that runs unmodified against the mock driver. Most of it is NVIDIA's
own GPU stack. fake-gpu-operator is a third-party project Mokka composes with
rather than replaces, which is the only reason it is called out separately in
the Type column.

| Consumer | Type | Role | Coverage |
|----------|------|------|----------|
| NVIDIA Device Plugin | NVIDIA component | `nvidia.com/gpu` extended resources | CI |
| NVIDIA DRA Driver | NVIDIA component | Dynamic Resource Allocation | CI |
| NVIDIA GPU Operator | NVIDIA component | Full stack device plugin, GFD and validator | CI |
| GPU Feature Discovery | NVIDIA component | Node labeling from NVML | CI |
| [fake-gpu-operator](integrations/fake-gpu-operator.md) | Third party (Run:ai) | Kubernetes-level GPU simulation for the nodes nvml-mock does not cover | `tilt up -- --fgo` and the [with-fgo demo](demo/with-fgo/README.md) |

"CI" means a job in the Go E2E workflow asserts against it on every run.
fake-gpu-operator has a Tilt path and a runnable demo but no CI job.
