# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.2.1] - 2026-06-12

### Fixed
- Release machinery: chart pushes to ghcr retry through the registry's tag
  read-after-write lag (#390); image publishing now triggers on `v*` tag
  pushes (a `paths:` filter silently suppressed every tag-triggered run)
  and supports `workflow_dispatch` (#391).
- Stale Go pins bumped to 1.26.4 across the build (deployment and test
  Dockerfiles, mocknvml/mockcuda Makefiles, e2e dispatch default, helper
  scripts), unbreaking the documented `make docker-build`; bundled
  `kubectl` bumped v1.32.0 -> v1.36.1, cutting the image's CRITICAL/HIGH
  CVE findings from that binary from 17 to 8. (#394)
- mocknvml: `nvmlDeviceGetHandleByUUID` returns `NOT_FOUND` for unknown
  UUIDs; the legacy default UUID scheme no longer assigns device 0 the
  canonical nil UUID; the `tests/mocknvml` harness builds again (broken
  since #269). (#395)
- mockib hardening from review: peer-registration I/O carries deadlines
  (one wedged peer no longer halts re-registration), `recv` waits are
  bounded and honor daemon shutdown, peer discovery and registration
  sweeps respect context cancellation, missing sysfs `node_guid` no
  longer renders an all-zero NodeGUID, and steady-state re-register
  logging only fires on change. (#396)

## [0.2.0] - 2026-06-12

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
- Dynamic per-query metric sampling: utilization, temperature, power, and
  clocks vary plausibly across calls instead of returning static values.
  (#323)
- GPU failure injection: profiles can trip `ecc_uncorrectable`, `lost`,
  and `fallen_off_bus` modes at runtime, including Xid 79 propagation.
  (#328)
- ComputeDomain / NVLink fabric simulation: `nvmlDeviceGetGpuFabricInfo`
  (+`InfoV`) driven by a cluster-level topology ConfigMap, plus fake
  `nvidia-imex` / `nvidia-imex-ctl` binaries coordinating peer readiness
  through marker files on a shared volume. (#337, #342)
- Toolkit-ready marker file for GPU Operator validator compatibility.
  (#346)

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
- Cross-node `ibping`, `iblinkinfo`, and `ibv_devinfo` via `mock-ib`,
  `libibmockumad.so`, and `libibmockverbs.so`. The chart preloads the shims,
  starts the in-pod daemon, and relays MAD traffic between nvml-mock pods
  over the Kubernetes pod network; the daemon and its Service are only
  created for profiles whose `infiniband:` block is enabled. E2E:
  `tests/e2e/validate-ibping.sh` plus a multi-node ibping CI job. (#367)
- Test suite standardized on `testify/require` across all packages; soft
  `t.Errorf` checks upgraded to hard failures, expect-error assertions made
  explicit with `require.Error`. No test functions were added or removed.
  (#386)

### Fixed
- `docs/demo/standalone/demo.sh` no longer uses the bash 4 `mapfile`
  builtin and runs on macOS's stock bash 3.2. (#385)
- Helm chart OCI publishing: the cosign signing step now authenticates to
  GHCR via the Docker config and signs the chart by digest; chart signing
  had failed with UNAUTHORIZED on every publish since 2026-05-25. (#388)

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

[Unreleased]: https://github.com/NVIDIA/k8s-test-infra/compare/v0.2.1...HEAD
[0.2.1]: https://github.com/NVIDIA/k8s-test-infra/compare/v0.2.0...v0.2.1
[0.2.0]: https://github.com/NVIDIA/k8s-test-infra/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/NVIDIA/k8s-test-infra/releases/tag/v0.1.0
