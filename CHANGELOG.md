# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- InfiniBand mock: real `ibstat`, `ibstatus`, `iblinkinfo`, and other
  `infiniband-diags` / `rdma-core` tools now work inside the nvml-mock
  DaemonSet without IB hardware. Implementation: `LD_PRELOAD` shim
  (`libibmocksys.so`) redirects libc filesystem accesses against
  `/sys/class/infiniband*` and `/dev/infiniband` to a fake tree rendered
  at startup by `render-ib-sysfs` from each profile's new `infiniband:`
  block. Defaults: `a100` -> ConnectX-6 HDR; `h100` / `b200` / `gb200`
  -> ConnectX-7 NDR; `l40s` / `t4` -> disabled.

## [0.1.0] - 2026-04-07

### Added
- Mock NVML library (`libnvidia-ml.so`) with 400 C API exports (111 hand-written,
  289 auto-generated stubs)
- Mock CUDA library (`libcuda.so.1`) with 15 functions
- Real `nvidia-smi` binary with RPATH patch backed by mock NVML
- YAML-configurable GPU profiles: A100, H100, B200, GB200, L40S, T4
- Helm chart for DaemonSet deployment on Kubernetes
- CDI (Container Device Interface) spec generation for GPU Operator
- E2E test suites: Device Plugin, DRA Driver, GPU Operator, Multi-Node Fleet
- fake-gpu-operator integration (FGO-style labels and ConfigMaps)
- Standalone and with-fgo demo scripts
- Comprehensive documentation: quickstart, architecture, configuration,
  development guide, troubleshooting

### Changed
- Rebranded from gpu-mock to nvml-mock (PRs #273, #274, #275, #281, #282)

[Unreleased]: https://github.com/NVIDIA/k8s-test-infra/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/NVIDIA/k8s-test-infra/releases/tag/v0.1.0
