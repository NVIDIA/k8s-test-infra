# k8s-test-infra

Kubernetes test infrastructure for NVIDIA GPU software — mock GPU environments,
CI tooling, and testing utilities.

## GPU Mock

Turn any Kubernetes cluster into a multi-GPU environment for testing.
No physical NVIDIA hardware required.

```bash
# 1. Build the image (no published image yet)
docker build -t gpu-mock:local -f deployments/gpu-mock/Dockerfile .

# 2. Load into KIND
kind create cluster --name gpu-test
kind load docker-image gpu-mock:local --name gpu-test

# 3. Install
helm install gpu-mock deployments/gpu-mock/helm/gpu-mock \
  --set image.repository=gpu-mock \
  --set image.tag=local
```

After install, deploy a consumer to test:

| Consumer | Guide |
|----------|-------|
| **NVIDIA Device Plugin** | [Quick Start](deployments/gpu-mock/helm/gpu-mock/README.md#quick-start-device-plugin-on-kind) |
| **NVIDIA DRA Driver** | [Quick Start](deployments/gpu-mock/helm/gpu-mock/README.md#quick-start-dra-driver-on-kind) |
| **NVIDIA GPU Operator** | [Quick Start](deployments/gpu-mock/helm/gpu-mock/README.md#quick-start-gpu-operator-on-kind) |

**Full documentation:** [gpu-mock Helm chart README](deployments/gpu-mock/helm/gpu-mock/README.md)

## E2E Testing

The gpu-mock E2E workflow tests all GPU consumers across multiple profiles
and node topologies. Run manually via `workflow_dispatch` or automatically
on PRs.

| Test Suite | What It Validates | Profiles |
|------------|-------------------|----------|
| **Device Plugin** | `nvidia.com/gpu` allocatable resources | A100, H100, T4 |
| **DRA Driver** | ResourceSlices via Dynamic Resource Allocation | A100, H100, T4 |
| **GPU Operator** | Operator components: device plugin + GFD + validator (CDI injection) | A100, H100, T4 |
| **Multi-Node Fleet** | Cross-node scheduling with heterogeneous GPUs | A100 + T4 |

Manual dispatch supports all 6 profiles: `a100`, `h100`, `b200`, `gb200`, `l40s`, `t4`.

See [`.github/workflows/gpu-mock-e2e.yaml`](.github/workflows/gpu-mock-e2e.yaml) for details.

## Mock NVML Library

The underlying CGo-based mock `libnvidia-ml.so` that powers gpu-mock.
Use standalone for local development and CI pipelines.

| Document | Description |
|----------|-------------|
| [Overview](docs/mocknvml/README.md) | What Mock NVML is and how to use it |
| [Quick Start](docs/mocknvml/quickstart.md) | Build and run in 5 minutes |
| [Configuration](docs/mocknvml/configuration.md) | YAML configuration reference |
| [Architecture](docs/mocknvml/architecture.md) | System design and components |
| [Development](docs/mocknvml/development.md) | Contributing and extending the library |
| [Troubleshooting](docs/mocknvml/troubleshooting.md) | Common issues and solutions |

## License

Apache License 2.0 — see [LICENSE](LICENSE).
