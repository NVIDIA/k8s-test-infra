# FAQ

## What is not simulated yet?

Reading a surface that Mokka does not stage gives the same answer as a machine
with no GPU, so a consumer gated on one of these will refuse to start rather
than fail in an interesting way:

| Surface | Consumer that reads it |
|---|---|
| Kernel module presence — `/proc/modules`, `/sys/module/nvidia/` | `lsmod`, the GPU Operator driver-container gate, DCGM startup checks |
| PCIe config space — `/sys/bus/pci/devices/<BDF>/config`, `/proc/bus/pci/devices` | `lspci -vv`, low-level probes |
| fabricmanager telemetry socket, `nvswitch-audit` | DCGM-Exporter fabric-manager collector, operator diagnostics |
| MIG partition CDI specs and CDI hooks | MIG-aware workloads, GPU Operator toolkit hooks |
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
workers at the same time. This is exercised in CI as a heterogeneous fleet.

## Does `nvidia-smi` actually work?

Yes — the real binary, unmodified. It loads Mokka's `libnvidia-ml.so` instead of
the vendor one and reports whatever the profile describes.

## Why does `lsmod` show no `nvidia` module?

Because kernel module presence is not simulated. Consumers that gate on
`/proc/modules` or `/sys/module/nvidia/` will conclude no driver is installed.
This is one of a handful of known gaps — see
[what is not simulated yet](#what-is-not-simulated-yet).

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

## Can I simulate a broken GPU?

Yes. Failure is a configuration state rather than a special code path, so every
surface reports it consistently: `nvidia-smi`, DCGM and the device plugin agree,
as they would on real hardware. ECC errors, a lost GPU and a fallen-off-the-bus
GPU are all injectable.

See the [failure injection guide](guides/failure-injection/README.md).
