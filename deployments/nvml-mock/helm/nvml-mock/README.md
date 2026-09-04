# nvml-mock Helm Chart

Mock NVIDIA driver infrastructure for Kubernetes testing. Installs a DaemonSet
that presents simulated GPUs to the NVIDIA device plugin, the DRA driver, the
GPU Operator and `nvidia-smi`, so GPU software can be exercised on nodes with
no physical NVIDIA hardware.

**Full documentation: <https://nvidia.github.io/k8s-test-infra/helm-chart/>**

That guide covers the per-consumer walkthroughs (device plugin, DRA driver, GPU
Operator, multi-node heterogeneous fleet), the complete values reference,
topology and IMEX simulation, failure injection, and troubleshooting. This file
is a summary so that `helm show readme` stays useful.

## Install

```bash
helm install nvml-mock oci://ghcr.io/nvidia/k8s-test-infra/chart/nvml-mock
```

## GPU profiles

Set `gpu.profile` to one of the profiles shipped in `profiles/`:

| Profile | GPU Name | VRAM | Architecture |
|---------|----------|------|--------------|
| `a100` | A100-SXM4-40GB | 40 GiB | Ampere |
| `h100` | H100 80GB HBM3 | 80 GiB | Hopper |
| `b200` | B200 | 192 GiB | Blackwell |
| `gb200` | GB200 | 192 GiB | Blackwell |
| `gb300` (default) | GB300 NVL | 288 GiB | Blackwell Ultra |
| `l40s` | L40S | 48 GiB | Ada Lovelace |
| `t4` | Tesla T4 | 16 GiB | Turing |

```bash
helm install nvml-mock oci://ghcr.io/nvidia/k8s-test-infra/chart/nvml-mock \
  --set gpu.profile=h100 \
  --set gpu.count=8
```

## Values

`values.yaml` is annotated and is the authoritative list. The top-level groups:

| Key | Purpose |
|-----|---------|
| `gpu` | Profile, device count, per-device overrides |
| `image` | Repository, tag, pull policy |
| `nodeSelector`, `tolerations` | Standard scheduling; `nodeAgent.resources` sets limits |
| `nodeAgent.kernelLog` | Announce injected Xids on the node's `/dev/kmsg` (privileged; on) |
| `nodeLabels` | Labels applied to nodes running the mock |
| `allocationWatcher` | Tracks device-plugin allocations for utilization simulation |
| `nri` | Node-wide NRI injection (see MEP-0002) |
| `topology` | Multi-node and multi-GPU interconnect layout |
| `infiniband` | Mock IB devices and counters |
| `imex`, `fabricmanager` | IMEX and fabric-manager simulation |
| `integrations` | fake-gpu-operator interoperation |
| `controlPlane` | Mokka control-plane service |

Values are validated against `values.schema.json` at install and upgrade time,
so a mistyped key fails fast rather than deploying a broken DaemonSet.

## Support matrix

- Kubernetes >= 1.28.0
- Chart version: see `Chart.yaml`

## Links

- [Documentation site](https://nvidia.github.io/k8s-test-infra/)
- [Quick start](https://nvidia.github.io/k8s-test-infra/quickstart/)
- [Configuration reference](https://nvidia.github.io/k8s-test-infra/configuration/)
- [Troubleshooting](https://nvidia.github.io/k8s-test-infra/troubleshooting/)
- [Source](https://github.com/NVIDIA/k8s-test-infra)
