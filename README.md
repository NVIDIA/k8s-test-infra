<div align="center">
    <img src="./docs/img/logo.png" width="350px" alt="Mokka" />
    <h1>Mokka</h1>
    <p>Simulate your GPU infrastructure on CPU nodes.</p>
    <a href="https://github.com/NVIDIA/k8s-test-infra/actions/workflows/ci.yaml">
        <img src="https://github.com/NVIDIA/k8s-test-infra/actions/workflows/ci.yaml/badge.svg" alt="CI pipelines" />
    </a>
    <a href="https://scorecard.dev/viewer/?uri=github.com/NVIDIA/k8s-test-infra">
        <img src="https://api.scorecard.dev/projects/github.com/NVIDIA/k8s-test-infra/badge" alt="OpenSSF Scorecard" />
    </a>
    <a href="LICENSE">
        <img src="https://img.shields.io/badge/License-Apache_2.0-blue.svg" alt="License" />
    </a>
</div>

---

Kubernetes test infrastructure for NVIDIA GPU software — mock GPU environments,
CI tooling, and testing utilities.

## nvml-mock

Turn any Kubernetes cluster into a multi-GPU environment for testing.
No physical NVIDIA hardware required.

```bash
kind create cluster --name mokka

helm install mokka oci://ghcr.io/nvidia/k8s-test-infra/chart/nvml-mock \
    --namespace mokka --create-namespace \
    --set gpu.profile=gb300
```

Every node now reports 8 mock GB300 GPUs. `gb300` is the chart default; swap in
`a100`, `h100`, `b200`, `gb200`, `l40s`, or `t4` for other hardware.

After install, deploy a consumer to test:

| Consumer                 | Guide                                                                                           |
|--------------------------|-------------------------------------------------------------------------------------------------|
| **NVIDIA Device Plugin** | [Quick Start](https://nvidia.github.io/k8s-test-infra/helm-chart/#quick-start-device-plugin-on-kind) |
| **NVIDIA DRA Driver**    | [Quick Start](https://nvidia.github.io/k8s-test-infra/helm-chart/#quick-start-dra-driver-on-kind)    |
| **NVIDIA GPU Operator**  | [Quick Start](https://nvidia.github.io/k8s-test-infra/helm-chart/#quick-start-gpu-operator-on-kind)  |

**Full documentation:** [Mokka documentation site](https://nvidia.github.io/k8s-test-infra/)

## E2E Testing

The nvml-mock Go E2E workflow gates standalone, DRA, GPU Operator, multi-node,
node-wide NRI, and NFD label-provenance coverage. Run manually via
`workflow_dispatch` or automatically on PRs.

| Test Suite | What It Validates | Profiles |
|------------|-------------------|----------|
| **Standalone Demo** | nvml-mock chart install, `nvidia-smi`, NVLink/fabricmanager, InfiniBand, PCI sysfs, and cross-node checks | Workflow-selected profiles |
| **Failure Injection** | Healthy, ECC, lost, and fallen-off-bus modes | Workflow-selected profiles |
| **DRA Driver** | Mock driver files, `nvidia-smi`, ResourceSlices, and DRA ResourceClaim scheduling | Workflow-selected profiles |
| **GPU Operator** | GPU Operator install, validator pod startup, GFD labels, and allocatable GPUs | Workflow-selected profiles |
| **Multi-Node Fleet** | Heterogeneous A100/T4 workers, mock files, InfiniBand behavior, device plugin resources, and GPU workload scheduling | Fixed multi-node topology |
| **Node-Wide NRI Injection** | Ambient mock GPU injection into ordinary pods without GPU requests or hostPath mounts | Workflow-selected profiles |
| **NFD Label Provenance** | That NFD creates `feature.node.kubernetes.io/pci-10de.present` from the feature file nvml-mock writes, and that nvml-mock does not write the label itself | Pinned to `a100` — the label is vendor-only and byte-identical across profiles |

## Mock NVML Library

The underlying CGo-based mock `libnvidia-ml.so` that powers nvml-mock.
Use standalone for local development and CI pipelines.

| Document | Description |
|----------|-------------|
| [Overview](docs/index.md) | Project overview, components, GPU profiles |
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

See [docs/demo/](docs/demo/) for the full list.

## Credits

- Logo designed by [Roman Hlushko](https://github.com/roma-glushko) with the assistance of OpenAI's ChatGPT.

## License

Apache License 2.0 — see [LICENSE](LICENSE).
