# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- The node agent gains `pcibus`, `cdi` and `imex` simulators, each an
  `agent.Simulator` with the same stage/apply/discard lifecycle as the existing
  `gpudriver`. Together they subsume the device-surface construction that
  `deployments/nvml-mock/scripts/setup.sh` performed at pod start, which loses
  369 lines (`cleanup.sh` loses 18). Staging a surface can now fail without
  leaving a half-built node, because the agent discards what it staged instead
  of exiting partway through a shell script. (#TBD)
- The `imex` simulator renders the capability surface the NVIDIA DRA driver's
  compute-domain kubelet plugin needs on a node with no NVIDIA kernel driver:
  the `nvidia-caps-imex-channels` channel device nodes, the `/proc/devices`
  substitute the plugin reads through `ALT_PROC_DEVICES_PATH`, and the
  `fabric-imex-mgmt` capability file. Without the `/proc/devices` entry the
  plugin aborts at startup, and without the channel nodes containerd refuses to
  admit a pod carrying a compute-domain CDI spec. Gated on
  `imex.mockChannels.enabled`. (#TBD)
- `host.Host.Mknod` centralises privileged character-device creation, so
  `gpudriver` and `imex` share one primitive rather than each carrying its own
  copy of the `mknod`-then-`chmod` sequence. (#TBD)
- `make build` now also builds the Go shims under `shims/`, and
  `make test-nvidia-imex-shim` runs the shim's integration tests against that
  built binary instead of compiling one during the test run. (#TBD)
- mocknvml: the `Conf Compute Protected Memory Usage` block of `nvidia-smi -q`
  now reports `0 MiB` for Total, Used and Free instead of `N/A`. Both NVML
  getters behind it were generated stubs that `nvidia-smi` calls once per GPU on
  every `-q` run, so the mock said the driver could not tell whether any memory
  is protected, where every real board answers that none is. The values are not
  configurable and are not gated on the part: all seven real-hardware captures
  in-tree report `0 MiB` here on drivers 570 and later, including the A100, L40S
  and T4 boards that cannot do Confidential Compute at all. Nothing partitions
  protected memory until CC mode is on, which stays unmodelled (#377). The two
  exports are registered as arriving in driver `525`, so a profile pinned below
  that reports `N/A` as real hardware of that vintage does. The unread
  `features.confidential_compute` key is removed from the `h100`, `b200`,
  `gb200` and `gb300` profiles: gating this surface on it would have made a T4
  report `N/A` where a real one reports `0 MiB`. (#711)
- A `--observability` Tilt consumer that runs kube-prometheus-stack and the GPU
  Operator's `dcgm-exporter` over mock GPUs and ships a Grafana dashboard
  in-tree, so the real exporter is scraped and rendered on a cluster with no
  GPUs. Adds two manual triggers that inject a temperature or Xid fault through
  `nvml-mock-ctl` and fail if it never reaches Prometheus, making the scrape
  path asserted rather than eyeballed. (#597)
- mocknvml: the `Clocks Event Reasons Counters` block of `nvidia-smi -q` now
  reports microsecond totals instead of `N/A` for all five causes. The counters
  are how throttling is diagnosed after the fact — a workload that ran slow is
  investigated by reading accumulated `SW Power Capping` or `HW Thermal
  Slowdown` time, not by sampling the instantaneous flag and hoping to catch it
  — and the block previously contradicted the flags directly above it, which
  stated confidently that no reason was active while the counters could not say
  whether one ever had been. A 580-era `nvidia-smi` reads them through
  `nvmlDeviceGetFieldValues`, which had no case for the field ids behind them, so
  each came back per-field `NOT_SUPPORTED` while the overall call succeeded and
  no unimplemented-symbol stub was reached. A new
  `clocks_throttle_reasons.counters` block seeds the accrued time per cause per
  device, defaulting to `0 us` — a real answer, where `N/A` was not — and a
  device accrues further time on top of that baseline for as long as the
  matching flag is set, within one process. A throttle state entered at runtime
  therefore carries no history of its own, so seed the counter alongside the
  flag when a GPU is meant to have been throttling for a while.
  `nvmlDeviceGetViolationStatus` now honours its `perfPolicyType` and
  timestamps the reading, where it previously returned a zero
  `nvmlViolationTime_t` for every policy, leaving the five causes
  indistinguishable. The limiters the mock does not model (board limit, low
  utilization, reliability, total base clocks) keep reporting `0 ns`, since they
  are real policies that simply never fired; `NVML_PERF_POLICY_TOTAL_APP_CLOCKS`
  now reports `NOT_SUPPORTED`, which is the one policy `nvml.h` marks
  "DEPRECATED, Do not use", and a value outside the enum reports
  `INVALID_ARGUMENT` rather than a zero violation time. A counter can also be
  seeded while a workload runs, with
  `nvml-mock-ctl set --gpu <idx>
  clocks_throttle_reasons.counters.sw_power_cap_us=<us>`. (#678)
- mocknvml: `nvidia-smi -q` now reports the `Platform Info` block on the
  `gb200`/`gb300` profiles — chassis serial number, slot number, tray index,
  host ID, peer type and module ID, where every row previously read `N/A`. These
  are the fields rack-scale fault correlation reads to turn a GPU fault into a
  physical location, so a Grace-Blackwell rack was indistinguishable from a PCIe
  workstation card by location. `nvmlDeviceGetPlatformInfo` (both struct
  versions) and `nvmlDeviceGetModuleId` are implemented and no longer generated
  stubs, and a new `device_defaults.platform` block configures the identity —
  per device, though only `module_id` varies between the GPUs of a node, since
  NVML scopes the rest to the node, which sits in exactly one tray of one
  chassis. Profiles that declare no block, and any profile on a driver older
  than 560, keep reporting `N/A`. The `GPU Fabric GUID` row of the same block is
  not modelled and now renders `0x0000000000000000` where it used to read `N/A`.
  (#642)
- mocknvml: SRAM ECC errors and row-remap availability are now modelled, so SRAM
  fault handling can be tested. Every SRAM row of `nvidia-smi -q` read `N/A`
  while `nvmlDeviceGetSramEccErrorStatus` and
  `nvmlDeviceGetRowRemapperHistogram` were generated stubs, which told a consumer
  the feature was unsupported rather than that the GPU was healthy. A new
  `ecc.sram` config block carries the correctable, uncorrectable-parity and
  uncorrectable-SEC-DED counters for both scopes, the aggregate per-unit source
  breakdown (L2, SM, microcontroller, PCIe, other) and the
  `SRAM Threshold Exceeded` flag; `remapped_rows.availability_histogram` carries
  the bank remap availability the Ampere-and-later profiles now ship (`t4` leaves
  it out and keeps reporting the histogram unsupported, as Turing does). Errors
  can be injected into a running workload with
  `nvml-mock-ctl sram-ecc --gpu <idx> --type secded --source sm
  --threshold-exceeded <count>`, and a count of `0` heals. The same values are
  visible through the per-location ECC field values DCGM reads, and
  `nvmlDeviceGetMemoryErrorCounter` now honours its `locationType` and
  `counterType` arguments instead of returning the DRAM counter for every
  location. (#641)
- mocknvml: GPU fabric health is now decoded and configurable, so
  `nvidia-smi -q` reports `Summary : Healthy` / `Bandwidth : Full` on the
  shipped Grace-Blackwell profiles instead of `N/A` for every row of the
  `Fabric` → `Health` block. A `fabric:` block with no health keys means a
  healthy fabric; individual conditions (`degraded_bandwidth`,
  `route_recovery`, `route_unhealthy`, `access_timeout_recovery`,
  `incorrect_configuration`) can be faulted under `fabric.health`, and the
  `Summary` row is derived from them (degraded bandwidth alone reports
  `Limited Capacity`) unless pinned with `fabric.health_summary`. The raw
  `fabric.health_mask` stays available as an escape hatch and is no longer
  silently dropped — it previously had no effect on the rendered output
  because the health summary was always zero. Fabric health can also be
  degraded and restored while a workload runs, with
  `nvml-mock-ctl fabric-health --gpu <idx> route_unhealthy` and
  `... fabric-health --gpu <idx> healthy`. `Partition Assigned` is reported as
  NOT_SUPPORTED, matching hardware, which newer `nvidia-smi` builds render as
  `N/A`. v1/v2 `nvmlDeviceGetGpuFabricInfo` callers are unaffected by the
  now non-zero summary: the field lives past the end of the v2 struct, pinned
  by a unit test on the struct-tail boundary. (#677)
- mocknvml: configured `processes:` now surface in nvidia-smi — the default
  table's Processes box, `-q`, and `--query-compute-apps` all report the
  configured PIDs, names and GPU memory instead of always reporting none.
  Processes can also be driven at runtime, per device, with
  `nvml-mock-ctl set --gpu <idx> 'processes=[{pid: 100, type: C, name: train.py,
  used_memory_mib: 8192}]'`. nvidia-smi enumerates processes through the
  internal export table rather than the public
  `nvmlDeviceGet*RunningProcesses` APIs, and its entry layout carries an
  inline 4096-byte name buffer (4128-byte stride), so the name must be written
  into the entry itself — without it nvidia-smi drops the rows from the default
  table. `nvmlSystemGetProcessName` is also implemented (was a stub); it is
  what `--query-compute-apps=...,process_name` resolves each pid through.
  Remaining gaps: the Type column always reads `M+C+G` because the entry
  carries no process-type field, and `nvidia-smi pmon` still does not work — it
  reads processes through a separate internal entry point that is not mapped.
  `pmon` now reports "Not supported on the device(s)" and exits non-zero where
  it previously printed nothing and exited 0; it has never listed processes on
  the mock. DCGM is unaffected; it reads per-process utilization through the
  public NVML APIs.

### Changed
- The mock renderers move out of `pkg/` now that their CLI entry points are
  gone: `pkg/system/mockpcisysfs/render` becomes `internal/pcisysfs` and
  `pkg/system/mockimex/render` becomes `internal/imex`. Each had one in-repo
  consumer, so `pkg/` was advertising an importable contract that nothing
  outside the repo used. (#TBD)
- The LD_PRELOAD and `execve` shims are collected under a single `shims/`
  directory: `pkg/system/mockpcisysfs/c` becomes `shims/libpcisysfs`, and
  `cmd/imex-nogpu-shim` becomes `shims/nvidia-imex-shim` — named after the
  binary it wraps rather than the flag it appends, and moved out of `cmd/`
  because it is not a CLI anyone invokes. (#TBD)
- The chart's IMEX environment moves from the `nvml-mock` container to
  `node-agent`, which is the process that now materialises the surface those
  variables describe. (#TBD)
- Tilt's `docker_build(only=…)` allowlists now cover `shims/`, `internal/` and
  `Makefile`. They had drifted from the Dockerfiles they feed, and because
  `only=` is a context allowlist rather than a cache hint, `COPY shims/ shims/`
  failed with `"/shims": not found` — the path was absent from the build
  context entirely. (#TBD)
- The local-dev and CI Kind clusters no longer run nvml-mock on the control-plane
  node. The chart tolerates every taint, so the DaemonSet landed there too, but
  nothing follows it: GPU Operator operands, FGO and NFD workers all stop at the
  `NoSchedule` taint. That left a node advertising a GPU driver footprint no
  consumer could use, and scenarios that pick a mock pod by list position
  intermittently targeted it. Every Kind worker now carries
  `mokka.nvidia.com/type=sgpu` and `local/nvml-mock.values.yaml` selects on it;
  the compute-domain overlay repeats the selector because its Tiltfile installs
  the chart without that baseline, and its `topology.yaml` gives the control
  plane no clique to report. Pinning stays additive — Helm deep-merges maps, so
  the FGO pool selector and `--multi-gpu-profile`'s hostname pin compose with
  it. (#724)
- The nvml-mock chart now defaults `gpu.profile` to `gb300` instead of `a100`, so
  a bare `helm install` simulates current-generation hardware. Ampere is the
  architecture least likely to expose a gap in software being tested against a
  simulated fleet; GB300 exercises the newer surfaces (Grace-Blackwell C2C, FP4/FP6,
  NVLink v5, the 570 driver line) that consumers are actually being ported to.
  Pass `--set gpu.profile=a100` to keep the previous behaviour.
- CI no longer depends on the third-party `ttl.sh` registry to share e2e images
  between jobs. The nvml-mock and kind-node images are exported as tarballs and
  handed to the legs that need them as run-scoped GitHub Actions artifacts, which need no
  credentials (fork PRs keep working) and remove a single-attempt external
  dependency that could fail the whole matrix. Each leg stages its tarball with
  a new `make image-load TARBALL=<tar> IMAGE=<repo:tag>` target, so the step is
  reproducible outside CI. Digest pinning went with it:
  artifacts are immutable and scoped to the run, so there is no tag-overwrite
  surface to defend against. `deployments/kind-nvidia-cdi/Makefile` drops its
  `ttl.sh` default too: `make build` now tags `kind-nvidia-cdi:local` in the
  local docker daemon, which is all `kind create cluster --image` reads, and
  `make push` requires a registry-qualified `IMAGE`. (#566)
- The ComputeDomain demo now runs real IMEX as a separate, ordinary workload;
  NRI supplies its mock NVML overlay, per-node topology, and annotated channel
  devices. Reruns deterministically reuse only compatible NRI-enabled Kind
  clusters, while incompatible clusters require explicit recreation with
  `FORCE_RECREATE=true`.
- The `e2e-nri` CI leg drops from ~12min to ~6min per GPU profile. `sleep` as
  PID 1 ignores SIGTERM, so all 13 pod deletions waited out the 30s grace period
  (6.7min of the leg). The keepalive now traps SIGTERM and exits in ~0.1s, with
  `terminationGracePeriodSeconds` capped at 1 as a backstop. `e2e-gpu-operator`
  is now the pipeline's critical path.
- Test pod manifests moved into a generic `pod.tpl.yaml` under
  `tests/e2e/go/framework/pod`, rendered from a `Spec` carrying only what varies.
- The `gb200` and `gb300` profiles now model 4 GPUs per node over 2 PCI root
  complexes, not 8 over 4. Both were written to the 8-GPU baseboard shape the
  other profiles use, but an NVL72 rack reaches 72 GPUs as 18 compute trays of
  4, and `nvidia-smi` runs per node: the real captures in
  `tests/e2e/go/assertions/nvidiasmi/testdata/hardware/` report 4 attached GPUs
  on one tray, in one slot, of one chassis. A node twice its real size inflates
  every count a consumer derives from it — allocatable GPUs, GFD's
  `nvidia.com/gpu.count`, ResourceSlice sizes — and invents a second pair of
  Grace CPUs and NUMA nodes that no such node has. Two details the captures
  report are now reproduced with it: the two GPUs of a superchip share one board
  serial, and `module_id` does not follow the device index (2, 1, 4, 3 across
  devices 0..3), so a consumer that assumes either breaks here rather than on
  real hardware. `gpu.count` left empty still derives from the profile, so the
  chart needs no change, but `gb300` is the chart default: a default install now
  exposes 4 GPUs per node rather than 8. Set `gpu.profile` to one of the 8-GPU
  baseboards, or `gpu.count` explicitly, for a larger node. The standalone and
  node-wide demos now default the count from the selected profile's device list
  instead of a hardcoded 8.

### Removed
- Chart value `nodeLabels.pciVendorPresent`. The NFD feature file behind
  `feature.node.kubernetes.io/pci-10de.present` is now always written. A
  leftover `--set nodeLabels.pciVendorPresent=false` is silently ignored, not
  rejected. (#719)

### Removed
- `cmd/fake-imex` (both the daemon and the ctl). The real `nvidia-imex` in NO
  GPU mode, reached through `shims/nvidia-imex-shim`, supersedes the
  marker-file simulation, completing the deprecation announced in 0.3.0. (#304)
- `cmd/render-imex-procdevices` and `cmd/render-pci-sysfs`. Both were one-shot
  renderers that `setup.sh` invoked; the `imex` and `pcibus` simulators call
  the same rendering code in-process. (#TBD)
- `pkg/imexcoord` and the chart's `imex.enabled` / `imex.stateDir` values,
  along with the `host-imex-state` hostPath they mounted. This was the
  marker-file protocol the removed fakes coordinated through; real daemons
  coordinate over the pod network and need no hostPath. `imex.mockChannels` is
  unaffected. The chart schema permits unknown keys, so a values file still
  setting `imex.enabled: true` installs silently with no state directory
  mounted rather than failing. (#304)

### Fixed
- mocknvml: `nvmlPciInfo_t.busId` now reports the 8-digit PCI domain real NVML
  uses (`00000000:07:00.0`, `NVML_DEVICE_PCI_BUS_ID_FMT`) while `busIdLegacy`
  keeps the 4-digit one (`0000:07:00.0`). Both were filled with the profile's
  4-digit `bus_id` verbatim, and consumers recover a sysfs BDF by stripping a
  leading `0000` from `busId` — so go-nvlib derived the malformed `:07:00.0` and
  every `/sys/bus/pci` lookup built from it failed. GPU Operator's
  gpu-feature-discovery logged `unable to read PCI device vendor id for
  :07:00.0` and labelled `nvidia.com/gpu.mode=unknown`. NVLink remote PCI info
  follows the same split, `nvidia-smi` reports the address hardware shows, and
  bus-ID handle lookups now accept either domain width and either case, so a
  consumer that hands back the `busId` it just read (DCGM) still resolves the
  device. (#671)
- mocknvml: `nvidia-smi` no longer reports impossible per-GPU PCIe identity
  values. `Board ID` is derived from the device's PCI address the way NVML does
  — `(domain << 16) | (bus << 8) | (device << 3)`, so a GPU at `0000:07:00.0`
  reports `0x700` — instead of `0x0` for every GPU, which left an eight-GPU node
  indistinguishable by board ID. `nvmlDeviceGetGpuMaxPcieLinkGeneration` is now
  hand-written in the bridge, so the `Device Max` PCIe generation reads Gen3 on
  `t4` through Gen6 on Blackwell instead of `N/A`. `Host Max` now matches the
  device maximum instead of an impossible Gen0: no public NVML API exposes a
  host-side maximum, and nvidia-smi reads it through a slot of the internal
  export table whose catch-all stub wrote a zero count over the caller's
  reading — the same class of bug as the phantom processes above. All three
  maxima now agree in `nvidia-smi -q`, `-q -x` (`<max_host_link_gen>`) and
  `--query-gpu=pcie.link.gen.hostmax`. (#638)
- mocknvml: `nvidia-smi -q` reports `Virtualization Mode : None` instead of
  `N/A`. `nvmlDeviceGetVirtualizationMode` was a generated stub — the most
  frequently called one in a single `-q` run — so the mock claimed it could not
  tell whether it was virtualized, where bare-metal hardware always answers
  `None`. The export is now hand-written and resolves the `virtualization.mode`
  the profiles already carried (`none`, `passthrough`, `vgpu`; anything
  unrecognised reads as bare metal). vGPU stays out of scope: `Host VGPU Mode`
  and `vGPU Heterogeneous Mode` still read `N/A`, matching bare metal. An
  earlier attempt at this fix was reverted during PR #630 because it moved
  `nvidia-smi pmon` onto a different code path and segfaulted it, so an e2e spec
  now pins `pmon -c 1` to a graceful refusal or success and requires `topo -m`
  to succeed, guarding the neighbouring internal-export-table path. (#640)
- Wire eight NVML device exports that already had engine implementations but
  were still generated stubs (`GetEncoderStats`, `GetFBCStats`,
  `GetAccountingBufferSize`, `GetEncoderCapacity`, `GetEncoderSessions`,
  `GetFBCSessions`, `GetRetiredPages_v2`, `GetViolationStatus`). Profile
  `encoder_stats` / `fbc_stats` and the accounting buffer size now reach
  `nvidia-smi -q` instead of silently reporting `N/A`. A regression guard
  fails when an engine method is left behind a `stubReturn` export. (#636)
- mocknvml: the internal export-table shim no longer writes a process list into
  memory the caller never handed it. Every table slot pointed at one stub, so
  the process-list call was recognised from the shape of its arguments; slots
  that take fewer arguments left a stale register in the position the stub read
  as the array pointer, and any configured `processes:` entry was then written
  through it. On arm64 that faulted, so `nvidia-smi -q` (and `-q -x`) died with
  a SIGSEGV mid-output as soon as a process was configured; elsewhere it wrote
  into whatever the stale value addressed. Each slot now has its own trampoline
  and only the three slots that carry a process array are filled.
- mocknvml: `nvidia-smi -q -d UTILIZATION` reports the configured
  `utilization.jpeg` and `utilization.ofa` percentages instead of `N/A`. Both
  keys were parsed into the device config and then dropped: there was no engine
  getter for either, and `nvmlDeviceGetJpgUtilization` /
  `nvmlDeviceGetOfaUtilization` were generated stubs returning
  `NVML_ERROR_NOT_SUPPORTED`. Both are now hand-written in the bridge and read
  their config field, matching the existing encoder and decoder getters. The
  values are also settable at runtime with
  `nvml-mock-ctl set --gpu <idx> utilization.jpeg=35 utilization.ofa=12`. (#637)
- Report `GPU C2C Mode` in `nvidia-smi -q` from `nvlink.c2c_enabled`.
  `nvmlDeviceGetC2cModeInfoV` was a generated stub, so the Grace-Blackwell
  profiles (`gb200`, `gb300`) reported `N/A` for the NVLink-C2C link to the
  Grace CPU that defines them, silently dropping a configured value. Boards
  with no such link keep reporting `N/A`, which is correct for them: `a100`,
  `h100` and `b200` set `c2c_enabled: false`, `l40s` and `t4` omit the key, and
  both cases answer `NVML_ERROR_NOT_SUPPORTED`. (#639)

- Gate the T.Limit temperature surfaces on Ada and later: the field IDs
  193–196 (`NVML_FI_DEV_TEMPERATURE_*_TLIMIT`) and
  `nvmlDeviceGetMarginTemperature`. Pre-Ada profiles (`t4`, `a100`) report
  `NVML_ERROR_NOT_SUPPORTED` for both, so `nvidia-smi -q` renders the absolute
  `GPU Shutdown/Slowdown/Max Operating Temp` rows from
  `nvmlDeviceGetTemperatureThreshold` instead of signed T.Limit margins shown
  as impossible (negative / inverted) absolute temperatures. Ada and later,
  including the NVSentinel thermal-margin demo on `h100`, are unchanged. (#635)

- mocknvml: `nvidia-smi -q` and `nvidia-smi --query-compute-apps` no longer
  report hundreds of phantom processes (PID 0, empty name, 0 MiB) per GPU when
  no processes are configured. nvidia-smi enumerates processes through the
  internal export table, whose catch-all C stub returned `NVML_SUCCESS` without
  writing back the caller's count, so nvidia-smi rendered its uninitialized
  buffer. The stub now writes back a real count for the per-device process-list
  call. Unrecognized internal calls still return `NVML_SUCCESS`: every slot of
  the export table points at the same stub, and `nvidia-smi topo -m` aborts with
  "Failed to run topology matrix" on any error from it. nvidia-smi does not fall
  back to the public process APIs for these views. E2E `NvidiaSMI` gained a
  regression guard.

### Security
- Go pins bumped 1.26.5 -> 1.26.6 across the build (deployment and test
  Dockerfiles, `devel` image, mocknvml/mockcuda Makefiles, helper scripts).
  1.26.5 is affected by seven standard-library advisories that `govulncheck`
  reports as reachable from this code — GO-2026-6218 (`net/url`), GO-2026-6091
  (`html/template`), GO-2026-6090 (`crypto/tls`), GO-2026-6089 and GO-2026-5026
  (`net/http`), GO-2026-6088 (`encoding/xml`) and GO-2026-5972
  (`encoding/asn1`) — all fixed in 1.26.6. CI derives its toolchain from
  `deployments/devel/Dockerfile`, so the stale pin failed `make lint` on every
  branch once the advisories were published.

## [0.3.0] - 2026-08-01

### Added
- Allocation-aware GPU memory, opt-in via `allocationWatcher.enabled`. A sidecar
  in the nvml-mock DaemonSet polls the kubelet pod-resources API and mirrors each
  pod's `nvidia.com/gpu` claim into `memory.used_bytes` / `memory.free_bytes`, so
  scheduling a workload moves the number and deleting it returns the number.
  Reads per device — a claim on GPU 3 moves GPU 3 only — and time-sliced GPUs
  accumulate one claim per holding container, clamped at the aperture. The bytes
  are **synthetic**: the mock runs no kernel, so the value says a claim exists,
  not what a workload touched. Level-triggered, so a missed event self-heals on
  the next poll and a failed poll keeps the previous reading rather than
  reporting the node idle. While enabled the watcher owns those two fields and
  overwrites an `nvml-mock-ctl set` of them; every other overridden field is
  preserved, since both writers take the same lock. Off by default because it
  mounts the kubelet pod-resources socket (read-only, node-local, no RBAC).
  New subcommand: `nvml-mock-ctl watch-allocations`. (#506)

- Mock IMEX channel injection through the NRI plugin. A pod annotated
  `nvml-mock.nvidia.com/imex-channels: "true"` receives the mock
  `/dev/nvidia-caps-imex-channels/channelN` nodes, so a ComputeDomain-style
  workload can see an IMEX fabric surface on a node with no NVIDIA kernel
  module. The channels come from the existing `imex.mockChannels` surface —
  enable it too, or the annotation is a no-op that logs a warning and starts
  the pod anyway. Injection is independent of the MEP-0002 device-plugin
  suppression, because the device plugin never allocates an IMEX channel.
  Configured by `nri.imexChannelAnnotation`. (#437)

- DCGM / dcgm-exporter support for the mock GPU stack. `nvmlDeviceGetFieldValues`
  now backs the `DCGM_FI_DEV_*` field surface (ECC, remapped rows, memory
  temperature, and the NVLink field set), and a mock GPM implementation serves
  `DCGM_FI_PROF_*` profiling metrics on Hopper+ profiles — architecture-gated
  like real NVML, with `gpm.supported` / PCIe-rate config overrides; pre-Hopper
  reports GPM unsupported. E2E coverage runs real dcgm-exporter under the GPU
  Operator via the Go harness (`gpu-operator` scenario, `dcgm`/`xid` labels):
  it asserts DEV + PROF and time-varying telemetry, plus `DCGM_FI_DEV_XID_ERRORS`
  under failure injection. `spike-dcgm.sh` provides a container-level recipe. (#370)
- `docs/configuration.md` gains a **Metric Fidelity** section stating, per metric,
  which reported values are simulated (clock-driven via `dynamic_metrics`), which
  are static until the config changes, and which are fixed by design — including
  the exact fractions of `utilization.gpu` behind each `DCGM_FI_PROF_*` activity
  metric, and why tying them to real work is out of scope for a library that
  never runs a kernel. It also records that no reported value is workload-aware:
  a pod holding an `nvidia.com/gpu` claim does not move `memory.used_bytes`,
  `processes`, or `utilization.gpu`. (#506)
- NRI foundation for container-level device injection: a node-wide NRI plugin
  injects mock NVIDIA device nodes into annotated containers, shipped as an
  opt-in DaemonSet with its own labels and selector so the main nvml-mock
  DaemonSet cannot adopt its pods. (#433) The plugin now also renders the mock
  IMEX surface (`/dev/nvidia-caps-imex-channels`) that the DRA compute-domain
  plugin expects. (#541)
- `nvml-mock-ctl`, a runtime control plane for the mock: mutate device state in
  a live pod (memory, utilization, failure modes) through override entries with
  a TTL, without restarting the DaemonSet. (#476) Adds an `nvlink-error`
  subcommand that injects NVLink DL replay / recovery / CRC errors. (#494)
- NVSwitch topology and `nvidia-fabricmanager` simulation, so fabric-aware
  consumers see a coherent NVSwitch-attached node. (#387)
- mockib subnet manager: `sminfo` and `ibnetdiscover` resolve against the mock
  InfiniBand fabric. (#401)
- Per-process `nvmlDeviceGetProcessUtilization` with a per-device `processes`
  config override, including the two-call probe/fill contract real NVML uses.
  (#381)
- Local development standardised on Tilt: one `tilt up` creates the Kind
  cluster and deploys the chart with live reload. (#470)
- A dedicated Kind node image carrying `nvidia-container-toolkit` and CDI, so
  e2e legs exercise a realistic container-runtime path instead of patching the
  runtime at test time. (#464)
- The chart autodiscovers the GPU profiles bundled in the image instead of
  carrying a hand-maintained profile list. (#463)
- The e2e suite reports a Ginkgo skipped-spec summary, so silently skipped
  coverage is visible in CI output. (#469)
- A Mock Enhancement Proposal (MEP) process and template under
  `docs/enhancements/`. (#521)
- A documented NVSentinel demo that drives thermal-margin detect / remediate /
  recover against mock GPUs. (#493)

### Changed
- Every shipped GPU profile now reports a moving, non-zero `utilization.gpu`.
  Each profile carries its own live `device_defaults.dynamic_metrics.utilization`
  block (`pattern: steady`, GPU 10-45%, memory 5-25%), so a default install no
  longer reads a constant 0 through `DCGM_FI_DEV_GPU_UTIL` — nor through the
  `DCGM_FI_PROF_*` metrics, which the mock derives as fixed fractions of it.
  The floor sits above 0 deliberately, so "utilization is reported" is a
  deterministic assertion rather than a flaky one. The value still tracks
  **elapsed time, not workload**: it moves on a fully idle node. Temperature and
  power simulation stays opt-in behind `gpu.dynamicMetrics.enabled`; note that
  enabling that overlay replaces the profile block with the chart baseline,
  whose `burst` 0-100 band does dip to near 0. (#506)

- ComputeDomain simulation now runs the REAL `nvidia-imex` daemon in NO
  GPU mode (`--nogpu`) instead of the fake marker-file binaries: the new
  `imex-nogpu-shim` injects the flag around upstream's hard-coded argv,
  `Dockerfile.compute-domain-daemon` installs the 595-branch package
  from Ubuntu jammy multiverse at LOCAL build time (never published),
  and the compute-domain demo asserts real gRPC domain readiness over
  the pod network, including peer-death detection. (#304) The chart's
  ibping NetworkPolicy now also admits the IMEX peer and command ports
  between nvml-mock pods (kind's kindnetd enforces NetworkPolicy on current
  releases, so the allow-list is load-bearing on Kind).
- **Breaking (profile config):** NVLink bandwidth is expressed as a single
  `bandwidth_per_link_mbps` field. Profiles carrying the previous aggregate and
  per-link pair must be updated. (#405)
- The nvml-mock E2E suite is a Go/Ginkgo harness under `tests/e2e` instead of
  the previous shell scripts. The harness owns the whole lifecycle (Kind
  create/teardown, image build/load, Helm install, validation, diagnostics)
  behind one `make e2e` entrypoint that runs identically locally and in CI.
  (#442) Cluster setup inside the harness now goes through Tilt, and the mock
  deploys into the `mokka` namespace. (#492)
- CI builds the nvml-mock image once and shares it with every e2e leg through
  the ephemeral ttl.sh registry, pulled by digest. This removes a per-leg
  rebuild and works on fork PRs, which have no registry credentials. (#465)
- `golangci-lint` now covers `e2e`- and `integration`-tagged sources, which
  were previously invisible to lint. (#516)
- CI closes the tier-1 gaps found by the AICR pre-silicon preflight review.
  (#512)

### Deprecated
- The fake `nvidia-imex` / `nvidia-imex-ctl` binaries, `pkg/imexcoord`,
  and the chart's `imex.enabled` hostPath coordination — superseded by
  the real daemon's NO GPU mode; removal in a follow-up release. Both
  fakes print a deprecation notice (the ctl only on non-READY paths, to
  preserve the upstream `CombinedOutput == "READY\n"` probe contract). (#304)

### Fixed
- `nvmlEventSetWait_v1`/`_v2` now block for the caller's timeout (re-checking
  every 100 ms) instead of returning `NVML_ERROR_TIMEOUT` immediately. Clients
  loop on the wait with no sleep of their own, so the immediate return turned
  `nvidia-device-plugin`'s health monitor into a busy spin that burned a full
  CPU core per pod. A pending Xid is still delivered on the first poll;
  `timeoutms=0` remains a non-blocking poll.
- `pkg/gpu/mocknvml` no longer drops `BUILD_TAGS` in the default (two-pass,
  padded) build path — `make BUILD_TAGS=foo` compiled without `foo` unless the
  tag set also disabled padding.
- `cleanup.sh` now removes `nvml-mock-nri.yaml`, the NRI CDI spec `setup.sh`
  stages, alongside the `nvidia.yaml` it already removed. It had been left
  behind while the same hook deleted the device nodes the spec names, so the
  runtime kept resolving a CDI reference to hostPaths that no longer existed:
  every annotated pod on the node landed in `CreateContainerError` ("failed to
  stat CDI host device"), retried by the kubelet indefinitely, while the NRI
  DaemonSet stayed Ready because `/readyz` reports registration. Nothing
  surfaced it. Introduced in this release by (#550).
- The NRI plugin now logs when device injection is requested and the device
  tree is present but holds no device nodes. A missing tree already warned; an
  empty one returned success with an empty set, so the container started with
  the overlay, no `/dev/nvidia*`, and no diagnostic — and the engine then
  derived a zero-GPU visible set as though that were configured.
- Documentation corrected against measured behaviour: the quick start's
  `kind load docker-image` step (which cannot load the multi-arch published
  image from Docker Desktop's containerd store), the seven profile files
  claiming the NVIDIA DRA driver resolves `dra.k8s.io/pcieRoot` (it does not —
  it is a Go binary and cannot see the `LD_PRELOAD`-shimmed sysfs tree, #265),
  the fake-gpu-operator guide's `gb200` product label and its
  library-path troubleshooting command, `docs/cuda-mock.md`'s claim that the
  mock is sufficient for `cuda-sample:vectoradd`, and `VERSION-MATRIX.md`,
  which listed floating chart versions as pinned and three never-run scenarios
  as tested in CI.
- The NRI device opt-in no longer offers the `nvidia-caps-imex-channels`
  DIRECTORY to the runtime as a device node. It sits inside the device root the
  plugin scans and matched the `nvidia` prefix filter, so any annotated pod on a
  node with `imex.mockChannels` enabled was affected. (#437)
- `make e2e-nfd` pins `E2E_PROFILES=a100` to match the profile the scenario
  actually instantiates. It had inherited `gb200` from the default set, so the
  run announced a profile it never exercised — indistinguishable in a log from
  one that did.
- `nvml-mock-ctl set --gpu <n> memory.total_bytes|free_bytes|used_bytes=...` now
  takes effect within one override TTL instead of silently doing nothing until
  the pod restarts. `nvmlDeviceGetMemoryInfo` and `nvmlDeviceGetMemoryInfo_v2`
  read device memory from the effective (override-merged) config rather than a
  struct baked at device construction. Values are reported verbatim: overriding
  `used_bytes` alone does not recompute `free_bytes`. The BAR1 aperture
  (`bar1_memory`) is still baked at construction. (#506)
- The NRI plugin no longer wedges silently. It detects a stalled or
  unregistered plugin and recovers, and the failure modes are covered by
  dedicated probes rather than inferred from a healthy-path test. (#434, #540)
- The NRI device helper decodes the full Linux `dev_t` (32-bit major, 32-bit
  minor across the split encoding) instead of truncating it, so injected
  device nodes carry the right major/minor. The helper set gained unit
  coverage. (#439, #539)
- `lspci` enumerates the mock GPUs: `mockpcisysfs` renders the sysfs
  attributes the tool reads, not only the ones NVML needs. (#477)
- The `pci-10de` node label is derived by NFD from the rendered PCI sysfs tree
  instead of being hand-written by the mock, which was masking whether the
  sysfs rendering was correct. (#534)
- The GFD e2e scenario asserts the expected `nvidia.com/gpu.*` labels instead
  of logging a warning when they are absent, so a regression fails the leg.
  (#542)
- The CGo bridge stubs are regenerated for `go-nvml` 0.13.2 and the e2e device
  plugin is scoped correctly. (#413)
- IB validation scripts no longer report a false negative from `SIGPIPE` when
  a downstream reader closes early. (#409)

### Security
- Go pins bumped 1.26.4 -> 1.26.5 across the build (deployment and test
  Dockerfiles, mocknvml/mockcuda Makefiles, e2e dispatch default, helper
  scripts) to resolve `GO-2026-5856` (Encrypted Client Hello privacy leak
  in `crypto/tls`), which was failing the `govulncheck` CI check.
- Dependency refreshes across the release, most consequentially `go-nvml`
  0.13.0-1 -> 0.13.3-1 and NFD 0.19.0. The `go-nvml` bump added three NVML
  entry points, so the CGo bridge stubs were regenerated; without that the
  built `libnvidia-ml.so` fails an `RTLD_NOW` `dlopen` on the first
  unresolved symbol. (#455, #520, #444, #410, #481)

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

[Unreleased]: https://github.com/NVIDIA/k8s-test-infra/compare/v0.3.0...HEAD
[0.3.0]: https://github.com/NVIDIA/k8s-test-infra/compare/v0.2.1...v0.3.0
[0.2.1]: https://github.com/NVIDIA/k8s-test-infra/compare/v0.2.0...v0.2.1
[0.2.0]: https://github.com/NVIDIA/k8s-test-infra/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/NVIDIA/k8s-test-infra/releases/tag/v0.1.0
