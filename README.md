# k8s-test-infra

[![CI](https://github.com/NVIDIA/k8s-test-infra/actions/workflows/ci.yaml/badge.svg)](https://github.com/NVIDIA/k8s-test-infra/actions/workflows/ci.yaml)
[![OpenSSF Scorecard](https://api.scorecard.dev/projects/github.com/NVIDIA/k8s-test-infra/badge)](https://scorecard.dev/viewer/?uri=github.com/NVIDIA/k8s-test-infra)
[![License](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](LICENSE)

Kubernetes test infrastructure for NVIDIA GPU software — mock GPU environments,
CI tooling, and testing utilities.

## nvml-mock

Turn any Kubernetes cluster into a multi-GPU environment for testing.
No physical NVIDIA hardware required.

```bash
# 1. Create cluster
kind create cluster --name gpu-test

# 2. Load the published image (or build locally with: docker build -t nvml-mock:local -f deployments/nvml-mock/Dockerfile .)
kind load docker-image ghcr.io/nvidia/nvml-mock:latest --name gpu-test

# 3. Install
helm install nvml-mock deployments/nvml-mock/helm/nvml-mock
```

After install, deploy a consumer to test:

| Consumer | Guide |
|----------|-------|
| **NVIDIA Device Plugin** | [Quick Start](deployments/nvml-mock/helm/nvml-mock/README.md#quick-start-device-plugin-on-kind) |
| **NVIDIA DRA Driver** | [Quick Start](deployments/nvml-mock/helm/nvml-mock/README.md#quick-start-dra-driver-on-kind) |
| **NVIDIA GPU Operator** | [Quick Start](deployments/nvml-mock/helm/nvml-mock/README.md#quick-start-gpu-operator-on-kind) |

**Full documentation:** [nvml-mock Helm chart README](deployments/nvml-mock/helm/nvml-mock/README.md)

## E2E Testing

The nvml-mock E2E workflow tests all GPU consumers across multiple profiles
and node topologies. Run manually via `workflow_dispatch` or automatically
on PRs.

| Test Suite | What It Validates | Profiles |
|------------|-------------------|----------|
| **Device Plugin** | `nvidia.com/gpu` allocatable resources | A100, H100, T4 |
| **DRA Driver** | ResourceSlices via Dynamic Resource Allocation | A100, H100, T4 |
| **GPU Operator** | Operator components: device plugin + GFD + validator (CDI injection) | A100, H100, T4 |
| **Multi-Node Fleet** | Cross-node scheduling with heterogeneous GPUs | A100 + T4 |

Manual dispatch supports all 6 profiles: `a100`, `h100`, `b200`, `gb200`, `l40s`, `t4`.

See [`.github/workflows/nvml-mock-e2e.yaml`](.github/workflows/nvml-mock-e2e.yaml) for details.

## Mock NVML Library

The underlying CGo-based mock `libnvidia-ml.so` that powers nvml-mock.
Use standalone for local development and CI pipelines.

| Document | Description |
|----------|-------------|
| [Overview](docs/README.md) | Project overview, components, GPU profiles |
| [Quick Start](docs/quickstart.md) | Build and run in 5 minutes |
| [Configuration](docs/configuration.md) | YAML configuration reference |
| [Architecture](docs/architecture.md) | System design and components |
| [CUDA Mock](docs/cuda-mock.md) | Mock CUDA library overview |
| [Development](docs/development.md) | Contributing and extending the library |
| [Examples](docs/examples.md) | Usage patterns and scenarios |
| [Troubleshooting](docs/troubleshooting.md) | Common issues and solutions |

## Integrations

| Integration | Description | Guide |
|-------------|-------------|-------|
| **fake-gpu-operator** | Run:ai's K8s-level GPU simulation | [Integration Guide](docs/integrations/fake-gpu-operator.md) |

## Demos

| Demo | Description |
|------|-------------|
| [Standalone](docs/demo/standalone/) | nvml-mock with FGO-style labels on Kind |
| [With fake-gpu-operator](docs/demo/with-fgo/) | Full FGO + nvml-mock integration |

## License

Apache License 2.0 — see [LICENSE](LICENSE).
