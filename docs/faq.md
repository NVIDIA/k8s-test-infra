# FAQ

## What is not simulated yet?

Reading a surface that Mokka does not stage gives the same answer as a machine
with no GPU, so a consumer gated on one of these will refuse to start rather
than fail in an interesting way:

| Surface | Consumer that reads it |
|---|---|
| PCIe config space — `/sys/bus/pci/devices/<BDF>/config`, `/proc/bus/pci/devices` | `lspci -vv`, low-level probes |
| fabricmanager telemetry socket, `nvswitch-audit` | DCGM-Exporter fabric-manager collector, operator diagnostics |
| NVLink link-state change events, RDMA netlink events | tools reacting to link degradation |

## Can I run CUDA workloads against Mokka?

No. CUDA simulation on CPU is not supported as of now.

## Do I need a real GPU, or a GPU driver?

No. Mokka runs on ordinary CPU nodes. There is no kernel module and no data
path — that is the point.

## Is this safe for production?

No, and it is not meant to be. Mokka is a test double for CI and test clusters.
It reports GPUs that do not exist, which is useful for testing scheduling and
failure handling and actively harmful anywhere real work is expected to land.

## Which GPU models can I simulate?

Seven profiles ship with the chart: `a100`, `b200`, `gb200`, `gb300`, `h100`,
`l40s` and `t4`. `gb300` is the default. Switch with `--set gpu.profile=<name>`.

See [Configuration](configuration.md) for what each profile defines.

## Can one cluster have different GPU models on different nodes?

Yes. Each release targets a node set, so a cluster can present A100 and T4
workers at the same time. This is exercised in CI as a heterogeneous fleet. See
[different GPU models on different nodes](guides/device-plugin.md#different-gpu-models-on-different-nodes).

## Does `nvidia-smi` actually work?

Yes — the real binary, unmodified. It loads Mokka's `libnvidia-ml.so` instead of
the vendor one and reports whatever the profile describes.

## Why does `nvidia-smi` always report 0 MiB used?

Because the profile says so, and nothing moves it. Mokka runs no kernels, so no
workload consumes device memory, and the profile's `memory.used_bytes` — `0` in
every shipped profile — is what every consumer reads no matter what is
scheduled.

Set `allocationWatcher.enabled=true` to make used and free memory track
Kubernetes GPU allocation instead: a sidecar polls the kubelet pod-resources API
and moves the numbers as claims come and go. The values are still synthetic —
they report that a claim *exists*, not what a workload touched — which is enough
to exercise a consumer that reads memory pressure. See
[allocation-aware memory](configuration.md#allocation-aware--opt-in).

## Which kernel modules does `lsmod` show?

`nvidia`, `nvidia_uvm`, `nvidia_modeset`, `gdrdrv` and `nvidia_fs`, plus
`nvidia_peermem` and `mlx5_core` where InfiniBand is enabled.
`/sys/module/nvidia/refcnt` exists too, and the node's own modules stay visible
beside the simulated ones.

That covers the modules the GPU Operator validator greps for. The mirror is
refreshed by a state reconcile, so it can lag a module load or unload on the
node. See [the Helm chart reference](helm-chart.md) for how the surface reaches
a container.

## My pod requests a GPU but has no `nvidia-smi`. Why?

Requesting `nvidia.com/gpu` gets your pod the `/dev/nvidiaN` device node and
nothing else — no `nvidia-smi`, and no `libnvidia-ml.so` on its filesystem.
Putting the libraries inside a container is the [NRI plugin](components/nri-plugin.md)'s
job, and that needs the containerd NRI socket, which a stock KIND cluster does
not expose.

Either enable NRI, or mount the driver root yourself the way
[`local/gpu-validator.k8s.yaml`](https://github.com/NVIDIA/k8s-test-infra/blob/main/local/gpu-validator.k8s.yaml)
does: a hostPath at `/run/nvidia/driver` plus a matching `LD_LIBRARY_PATH`.
This is a boundary of the plain install, not a bug.

## Can I use MIG?

Yes, on the MIG-capable profiles — `a100`, `h100`, `b200`, `gb200` and `gb300`.
`nvidia-smi -mig` and `nvidia-smi mig -cgi/-cci/-dgi/-dci` work against the
mock as they do against a driver, and a node can boot already partitioned, in
which case the device plugin advertises one resource per slice under
`migStrategy=single`.

One boundary: a repartition made at runtime moves the NVML view only. What a
node can *allocate* is the layout it booted with, because the driver capability
surface is staged when the pod starts.

See the [MIG partitioning guide](guides/mig/README.md).

## Can I simulate a broken GPU?

Yes. Failure is a configuration state rather than a special code path, so every
surface reports it consistently: `nvidia-smi`, DCGM and the device plugin agree,
as they would on real hardware. ECC errors, a lost GPU and a fallen-off-the-bus
GPU are all injectable.

See the [failure injection guide](guides/failure-injection/README.md).
