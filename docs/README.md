# nvml-mock

Mock NVIDIA driver infrastructure for Kubernetes testing. Simulate GPUs on any
Linux system -- no hardware required.

## Components

| Component | Description | Status |
|-----------|-------------|--------|
| Mock NVML (`libnvidia-ml.so`) | 400 NVML C API exports (111 with configurable behavior, 289 stubs), YAML-configurable GPU profiles | Production |
| Mock CUDA (`libcuda.so`) | 15 CUDA functions -- init, device, memory management | Early |
| nvidia-smi | Real binary with RPATH patch, backed by mock NVML | Production |
| Helm Chart | DaemonSet deployment with 6 GPU profiles | Production |
| CDI Injection | Container Device Interface specs for GPU Operator | Production |

## GPU Profiles

| Profile | GPU Name | VRAM | Architecture |
|---------|----------|------|--------------|
| `a100` | A100-SXM4-40GB | 40 GiB | Ampere |
| `h100` | H100 80GB HBM3 | 80 GiB | Hopper |
| `b200` | B200 | 192 GiB | Blackwell |
| `gb200` | GB200 | 192 GiB | Blackwell |
| `l40s` | L40S | 48 GiB | Ada Lovelace |
| `t4` | Tesla T4 | 16 GiB | Turing |

## Quick Start

```bash
kind create cluster --name test
docker pull ghcr.io/nvidia/nvml-mock:latest
kind load docker-image ghcr.io/nvidia/nvml-mock:latest --name test
helm install nvml-mock deployments/nvml-mock/helm/nvml-mock
```

See [Helm Chart README](../deployments/nvml-mock/helm/nvml-mock/README.md) for
full walkthrough.

## Documentation

| Document | Description |
|----------|-------------|
| [Quick Start](quickstart.md) | Get up and running in 5 minutes |
| [Architecture](architecture.md) | System design and component overview |
| [Configuration Reference](configuration.md) | Complete YAML configuration guide |
| [CUDA Mock](cuda-mock.md) | Mock CUDA driver library details |
| [Development Guide](development.md) | Contributing and extending the project |
| [Examples](examples.md) | Common usage patterns and scenarios |
| [Troubleshooting](troubleshooting.md) | Common issues and solutions |

## Integrations

| Integration | Description |
|-------------|-------------|
| [fake-gpu-operator](integrations/fake-gpu-operator.md) | Run:ai's K8s-level GPU simulation + nvml-mock driver fidelity |

## Tested Consumers

| Consumer | Role | Status |
|----------|------|--------|
| NVIDIA Device Plugin | `nvidia.com/gpu` extended resources | Tested |
| NVIDIA DRA Driver | Dynamic Resource Allocation | Tested |
| NVIDIA GPU Operator | Full stack device plugin + GFD + validator | Tested |
| GPU Feature Discovery | Node labeling from NVML | Tested |
