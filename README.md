<div align="center">
    <img src="./docs/img/logo.png" width="180px" alt="Mokka" />
    <h1>Mokka</h1>
    <p>Simulate your GPU infrastructure on CPU nodes.</p>
    <a href="https://github.com/NVIDIA/k8s-test-infra/actions/workflows/ci.yaml">
        <img src="https://github.com/NVIDIA/k8s-test-infra/actions/workflows/ci.yaml/badge.svg" alt="CI pipelines" />
    </a>
    <a href="https://nvidia.github.io/k8s-test-infra/">
        <img src="https://github.com/NVIDIA/k8s-test-infra/actions/workflows/deploy-pages.yaml/badge.svg" alt="Documentation" />
    </a>
    <a href="https://scorecard.dev/viewer/?uri=github.com/NVIDIA/k8s-test-infra">
        <img src="https://api.scorecard.dev/projects/github.com/NVIDIA/k8s-test-infra/badge" alt="OpenSSF Scorecard" />
    </a>
    <a href="https://www.bestpractices.dev/projects/14445">
        <img src="https://www.bestpractices.dev/projects/14445/badge" alt="OpenSSF Best Practices" />
    </a>
    <a href="LICENSE">
        <img src="https://img.shields.io/badge/License-Apache_2.0-blue.svg" alt="License" />
    </a>
</div>

---

Mokka turns any Kubernetes cluster into a multi-GPU environment for testing. It
implements the NVIDIA driver interfaces that GPU software talks to, so the
device plugin, the DRA driver, the GPU Operator and `nvidia-smi` all behave as
though real hardware were present.

Use it to test scheduling, node labelling, telemetry and failure handling where
real GPUs are not available. It is a test double for CI and test clusters, not
something to run in production.

## Quick start

```bash
kind create cluster --name mokka

helm install nvml-mock oci://ghcr.io/nvidia/k8s-test-infra/chart/nvml-mock \
    --namespace mokka --create-namespace
```

Every node now reports four mock GB300 GPUs. Swap in `a100`, `b200`, `gb200`,
`h100`, `l40s` or `t4` with `--set gpu.profile=<name>`.

## Tested consumers

| Consumer | What works |
|---|---|
| Node Feature Discovery | PCI vendor labels derived from the feature file Mokka writes |
| GPU Feature Discovery | Node labels derived from NVML |
| NVIDIA Device Plugin | Allocatable `nvidia.com/gpu` matches the profile, and workloads schedule against it |
| NVIDIA DRA Driver | ResourceSlices report the right GPUs, and a `ResourceClaimTemplate` pod reaches `Running` |
| NVIDIA GPU Operator | The full operand stack installs and its validator starts |
| DCGM / dcgm-exporter | Telemetry, time-varying power, and injected Xid errors |
| Run:ai fake-gpu-operator | Profile ConfigMaps published in the shape its discovery expects |

## Documentation

**[nvidia.github.io/k8s-test-infra](https://nvidia.github.io/k8s-test-infra/)**

|                                                                         |                                                                          |
|-------------------------------------------------------------------------|--------------------------------------------------------------------------|
| [Quick Start](https://nvidia.github.io/k8s-test-infra/quickstart/)      | Install and see simulated GPUs                                           |
| [Architecture](https://nvidia.github.io/k8s-test-infra/architecture/)   | The moving parts and how the system behaves                              |
| [Guides](https://nvidia.github.io/k8s-test-infra/guides/)                 | Device plugin, DRA, GPU Operator, failure injection, node-wide injection |
| [Configuration](https://nvidia.github.io/k8s-test-infra/configuration/) | Every profile knob                                                       |
| [FAQ](https://nvidia.github.io/k8s-test-infra/faq/)                     | What Mokka simulates, and what it does not                               |

## Contributing

See the [contributing guide](https://nvidia.github.io/k8s-test-infra/contributing/)
for local development with Tilt, the test suites, and how to submit a change.
Substantial changes start with a [Mokka Enhancement Proposal](enhancements/).

Report vulnerabilities privately — see [SECURITY.md](SECURITY.md), not the issue
tracker.

## Credits

- Logo designed by [Roman Hlushko](https://github.com/roma-glushko) with the assistance of OpenAI's ChatGPT.

## License

Apache License 2.0 — see [LICENSE](LICENSE).
