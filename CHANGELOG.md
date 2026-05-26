# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- New GPU profile `gb300` modeling the NVIDIA GB300 NVL (Grace-Blackwell
  Ultra Superchip): 8 GPUs/node arranged as 4 Grace+2×B300 trays, 288 GiB
  HBM3e per GPU, 1.4 kW default TDP / 1.6 kW boost, PCIe Gen6, NVLink v5
  with NVLink-C2C to Grace, FP4/FP6/FP8 + Transformer Engine, MIG-capable,
  and the Blackwell Ultra driver line (`570.124.06`). Ships with a
  canonical 4-digit-BDF layout and a `pcie_topology:` block describing
  4 PCI root complexes (one per Grace pair, 2 B300 GPUs each), so the
  `render-pci-sysfs` step lights up an NVL72-shaped `/sys/bus/pci/devices`
  tree out of the box. Driver-version derivation, NOTES.txt profile list,
  fake-gpu-operator ConfigMap fanout, the e2e workflow profile matrix,
  and Helm unittests / Go profile-consistency tests were all extended to
  cover `gb300` in lockstep with the existing profiles.
- nvml-mock library-size padding: the built `libnvidia-ml.so` is now padded
  in a dedicated `.data.nvml_mock_padding` section to land within ~10% of
  the real driver-shipped library (≈14 MiB for driver 550.x), so detection
  / security tools that sanity-check the NVML file size accept the mock.
  Configurable via `TARGET_LIB_SIZE` (auto two-pass build, default), an
  explicit `PADDING_BYTES`, or fully disabled with `NO_PADDING=1` /
  `BUILD_TAGS=nopadding` for minimal images. No functional impact on the
  NVML API surface. (#247)
- nvml-mock PCIe topology: profiles now carry a `pcie_topology:` block
  describing PCI root complexes, NUMA nodes, and device-to-root mapping.
  A new `render-pci-sysfs` binary (built from `cmd/render-pci-sysfs/`,
  schema and renderer in `pkg/system/mockpcisysfs/`) materializes a fake
  `/sys/bus/pci/devices` + `/sys/devices/pciDDDD:BB` tree under
  `/var/lib/nvml-mock/sys/` from the init container, so topology-aware
  consumers (NVIDIA DRA driver's `dra.k8s.io/pcieRoot`, device-plugin
  NUMA hints) can resolve PCIe root complex via `readlink()` + path
  parse. Defaults populated for every profile: `a100`/`b200`/`h100`/`l40s`
  -> 2 root complexes (dual-socket), `gb200` -> 4 root complexes (one per
  Grace pair), `t4` -> 1 root complex. (#263)

### Changed
- nvml-mock profile `bus_id` fields now use the canonical Linux sysfs
  4-digit-domain form (`0000:07:00.0`) instead of the NVML 8-digit
  `busIdLegacy` form (`00000000:07:00.0`). The bridge already returned
  the same string verbatim in `nvmlPciInfo.busId`; the new format aligns
  the mock with what real Linux PCI sysfs exposes and is a hard
  prerequisite for the PCIe sysfs renderer above. (#263)
- InfiniBand mock: real `ibstat`, `ibstatus`, `iblinkinfo`, and other
  `infiniband-diags` / `rdma-core` tools now work inside the nvml-mock
  DaemonSet without IB hardware. Implementation: `LD_PRELOAD` shim
  (`libibmocksys.so`) redirects libc filesystem accesses against
  `/sys/class/infiniband*` and `/dev/infiniband` to a fake tree rendered
  at startup by `mock-ib` from each profile's new `infiniband:`
  block. Defaults: `a100` -> ConnectX-6 HDR; `h100` / `b200` / `gb200`
  -> ConnectX-7 NDR; `l40s` / `t4` -> disabled.
- Optional cross-node `ibping` via `mock-ib` and `libibmockumad.so`
  (default off). Enable with Helm `infiniband.ping.enabled=true` to preload
  both shims, start the in-pod daemon, and relay LID-based ping traffic
  between nvml-mock pods over the Kubernetes pod network. E2E:
  `tests/e2e/validate-ibping.sh`.

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
