## MAGI Report: BALTHASAR / claude-opus-4-8-thinking-high

### Plan

Scope: correctness + architecture review of PR #387 (`feat/nvswitch-fabricmanager`).
The design centers on an immutable, node-level `NodeFabric` (built once at config
load, shared read-only by every device) from which every NVLink / topology /
affinity surface is *derived*. The architecture is sound — single source of truth,
pure functions, injectable clock/stat for tests — so my review concentrates on
where the derivations, unit math, and the marker-coupling contract can drift from
real NVML semantics or break the immutability/monotonicity guarantees the code
advertises.

- Risk areas:

  - **Counter accrual overflow / float precision (correctness, high priority).**
    `accrue()` (topology.go) computes `seed + uint64(math.Floor(dt*rate))` where
    `rate = bw * dutyCycle`, `bw = Mbps*1e6` (≈5.3e13 for NVLink5) and the epoch is
    `/proc/stat btime` (system boot, potentially far in the past). `dt*rate` can
    exceed both `float64`'s 2^53 exact-integer range and `uint64` max, where a
    Go `float64 -> uint64` conversion of an out-of-range value is
    implementation-defined and can wrap. This would violate the monotonicity
    guarantee the unit tests claim (they only inject tiny synthetic epochs, so they
    never exercise the btime-anchored large-`dt` regime). Verify behavior at
    realistic uptimes and rates, and whether FEC-bin divisions (`v/600_000`,
    `v/2_500_000`) and `v*107` / `v*avgBytesPerNvlinkPacket` can themselves overflow.

  - **Unit semantics of `bwBytesSec` (correctness).** Field is named "BytesSec" but
    set to `Mbps*1e6` (megabits domain) or `GBPS*1e9`. `NvLinkSpeedMbps` divides by
    1e6 and round-trips the *speed* display correctly, but the same `bw` feeds the
    throughput-counter `rate`, so counter magnitudes are in a megabit-scaled unit
    that does not match the "bytes" name or `nvlink -gt d` semantics. Confirm intent
    and naming; this affects what `nvlink -e`/`-gt` counters represent.

  - **NV# matrix asymmetry for heterogeneous switch counts (correctness).**
    `computeNVCounts` broadcasts each GPU's own `switchLinks[i]` to *every* peer in
    row `i`, so `nvCount[a][b]` = a's switch-link count, `nvCount[b][a]` = b's. For
    uniform shipped profiles this is symmetric (NV18/NV12), but a config where GPUs
    have differing switch-link counts yields an asymmetric NV# matrix and asymmetric
    `GetP2PStatus`. Validate whether that is intended, and whether nvidia-smi's
    per-GPU field-147 model (which is inherently per-GPU, not pairwise) actually
    matches this.

  - **Link-index range vs enumeration mismatch (edge case).** `resolveLink` stores
    any `Link` value, and `ActiveLinkCount` / `NVSwitchConnectedLinkCount` count
    links with index ≥ `nvLinkMaxLinks` (18), but every per-link getter rejects
    `link >= 18` via `nvlinkLinkInRange` with `ERROR_INVALID_ARGUMENT`. A config with
    a link index ≥18 would make LINK_COUNT disagree with what nvidia-smi can
    enumerate. Check whether link indices are clamped/validated at build time.

  - **Per-field consistency for inactive/missing links (correctness).** Per-link
    speed fields L0–L11 return `(Uint, 0, SUCCESS)` for inactive links, while
    `fiNvlinkGetSpeed` returns `NOT_SUPPORTED`, and several count fields key off
    `Link(...)` existence vs `Active`. Audit each field branch in
    `GetNvLinkFieldValue` against real NVML's SUCCESS-vs-NOT_SUPPORTED behavior so
    nvidia-smi's enumeration loop terminates/continues as on hardware.

  - **Device-visibility vs topology leakage (architecture/correctness).**
    `createDevicesFromYAML` builds `configurableDevices` for all `NumDevices`, but
    `detectVisibleDevices` filters only `DeviceGetCount`. `TopologyNearestGpus` and
    `TopologyGpuSet` iterate the full `configurableDevices` array and register
    handles for peers, so a container with a CDI-injected subset (only
    `/dev/nvidia0`) could surface handles/peers for GPUs it should not see. Confirm
    this is acceptable or a leak relative to the visibility model.

  - **NUMA default-zero misreport (edge case).** With `hasPCIe == true` but a device
    BDF absent from every root complex, `numaOf` defaults to 0 and `rootOf` to "".
    `GetNumaNodeId`/`NumaNode` then report NUMA 0 (not `NOT_SUPPORTED`), and CPU/mem
    affinity masks default to node 0. Verify shipped profiles cover every device BDF,
    and whether unmatched devices should report unknown instead of 0.

  - **fabricmanager marker coupling semantics (architecture).**
    `fabricReadiness` is a process-global singleton with a 1s TTL that
    `ResetForTesting` does not reset (cross-test contamination risk). Coupling is OFF
    unless `MOCK_FABRICMANAGER_STATE_DIR` is set, so "auto" silently resolves to
    COMPLETED whenever the env is unset — confirm the Helm/DaemonSet wiring sets it
    exactly when fabricmanager is enabled, and that disabling fabricmanager cannot
    strand "auto" devices. The marker is liveness-free: a SIGKILL'd daemon leaves a
    stale READY marker forever (no heartbeat/TTL on the file). Daemon re-assert tick
    (2s) vs engine TTL (1s) leaves a small stale-READY window after external marker
    deletion. Assess acceptability for a mock and the contract-drift guard
    (`fmcoord` vs `engine` duplicated constants — currently guarded by
    `TestFabricCoordContract`).

  - **C-bridge marshalling safety (correctness).** `GetNvLinkRemotePciInfo` writes
    raw ASCII BDF bytes into `pci.BusId[i]` (loop bound 32) and the 0xFF sentinel
    into `Domain/Bus/Device`. Verify `nvml.PciInfo.BusId` length/signedness, NUL
    termination, and that the `unsafe.Slice` / `uintptr->unsafe.Pointer` handle
    conversions in topology.go/affinity.go/fieldvalues.go are correct under the
    two-phase NVML size-query convention (nil array returns size; INSUFFICIENT_SIZE
    path).

  - **Parsing robustness (edge case, low priority).** `parseCPURange` silently
    accepts very large ranges (e.g. "0-1000000") and allocates the full slice at
    build time (memory blowup); `parseClusterUUID` drops non-hex and zero-pads
    (collision/silent-misconfig risk). Both are warn-and-continue by design; confirm
    bounds are acceptable.

- Files to inspect:

  - `pkg/gpu/mocknvml/engine/topology.go` — `accrue`/`NvLinkCounters` overflow,
    `computeNVCounts` symmetry, `resolveLink` index handling, `bwBytesSec` units,
    `computePCIeLevels`, affinity-mask packing, `parseCPURange`.
  - `pkg/gpu/mocknvml/engine/nvlink_fields.go` — per-field SUCCESS/NOT_SUPPORTED
    consistency, FEC/byte scaling overflow, scopeId→link mapping.
  - `pkg/gpu/mocknvml/engine/nvlink_counters.go` — epoch resolution, btime parsing,
    cross-process growth claim.
  - `pkg/gpu/mocknvml/engine/fabric_readiness.go` + `fabric.go` — coupling, global
    cache lifecycle, `resolveFabricState("auto")`, `ResetForTesting` interaction.
  - `pkg/fmcoord/coord.go` + `cmd/fake-fabricmanager/{daemon,ctl}/main.go` — marker
    write/remove/idempotency, signal handling, env contract, liveness gap.
  - `pkg/gpu/mocknvml/engine/device.go` (1020–1323) — `GetP2PStatus`,
    `GetNvLinkRemotePciInfo` sentinel, affinity getters, NUMA default-zero.
  - `pkg/gpu/mocknvml/engine/engine.go` — fabric build, visibility vs
    `TopologyNearestGpus`/`TopologyGpuSet` handle registration.
  - `pkg/gpu/mocknvml/bridge/{affinity,topology,nvlink,fieldvalues}.go` +
    `nvml_types.h`, `stubs_generated.go` — C marshalling, union member tagging,
    two-phase size queries, struct field widths.
  - `pkg/gpu/mocknvml/engine/config_types.go` — new config shape, precedence rules
    (Mbps vs GBPS, defaults, legacy flat Links → device 0).
  - Built-in profiles `pkg/gpu/mocknvml/configs/mock-nvml-config-*.yaml` and Helm
    `profiles/*.yaml`, `templates/_helpers.tpl`, `templates/daemonset.yaml`,
    `values.schema.json`, `scripts/setup.sh` — env wiring (`MOCK_NVML_EPOCH`,
    `MOCK_FABRICMANAGER_STATE_DIR`), GPU-count/fabricmanager derivation, BDF coverage.
  - Tests: `topology_test.go`, `nvlink_fields_test.go`, `nvlink_counters_test.go`,
    `p2p_status_test.go`, `fabric_readiness_test.go`, `fmcoord_contract_test.go`,
    `device_test.go`, `coord_test.go`; e2e `tests/e2e/validate-nvlink.sh`,
    `validate-nvidia-smi.sh`, `.github/workflows/nvml-mock-e2e.yaml`.

- Test strategy:

  1. Build/vet/race: `go build ./...`, `go vet ./...`, then
     `go test -race ./pkg/gpu/mocknvml/... ./pkg/fmcoord/...` to validate the
     immutability/concurrency claim and catch data races in the global readiness
     cache.
  2. Targeted overflow probe: add/scratch a test for `accrue`/`NvLinkCounters` with
     a realistic btime epoch (e.g. boot 30–90 days ago) and NVLink5 rate; assert
     strict monotonicity and no wraparound at the uint64/float53 boundary.
  3. Asymmetry probe: a heterogeneous fabric (GPUs with unequal switch-link counts)
     to observe `NVLinkCount(a,b)` vs `(b,a)` and `GetP2PStatus` symmetry.
  4. Range/edge probes: link index ≥18 (LINK_COUNT vs getter mismatch); device BDF
     absent from root complexes (NUMA default-zero); both bandwidth fields zero
     (counters static); link index field-value branches for inactive links.
  5. Coupling lifecycle: drive `fabric.state: auto` through marker create/delete with
     injected `now`/`stat`, env set vs unset, and `ResetForTesting` to confirm no
     stale global state bleeds across tests; verify daemon SIGTERM removes marker and
     `ctl -q` exit codes.
  6. Confirm the deterministic engine unit oracles (`TestNodeFabric_BuiltinProfiles`,
     `TestGetNvLinkFieldValue_*`) cover the full NV# matrix and field surface; treat
     the e2e (`validate-nvlink.sh`) as the integration reveal but rely on unit tests
     as the driver-independent guard. Confirm CI matrix (the e2e workflow) exercises
     each profile and the negative controls (b200/t4/l40s = no NV#).
  7. Cross-check field IDs (84/89/90/91/132/137/146/147/161–250) against vendored
     `nvml.h` to ensure no transcription error, since these are hardcoded magic ints.

- Evidence that would change this plan:

  - The vendored `nvml.h` showing the hardcoded NVLink field IDs are wrong/right, or
    `nvml.PciInfo.BusId` width differing from the 32-byte assumption — reprioritizes
    the bridge/field audit.
  - `setup.sh` / DaemonSet proving `MOCK_NVML_EPOCH` is always set (making btime the
    fallback only on unit hosts) — downgrades the overflow risk's real-world impact
    (but the monotonicity claim is still testable and should hold for btime).
  - A build-time clamp/validation of link indices and a guarantee that every shipped
    profile's device BDFs are fully covered by root complexes — closes the
    index-mismatch and NUMA-default-zero edges.
  - Real-NVML reference output (or maintainer confirmation) for inactive-link
    SUCCESS-vs-NOT_SUPPORTED on each field, and for whether the NV# matrix is meant
    to be per-GPU (asymmetric-allowed) — settles the field-consistency and asymmetry
    questions.
  - Confirmation that CDI-subset visibility is out of scope for topology peers
    (i.e. the mock intentionally exposes the full node fabric) — closes the
    visibility-leak concern.
  - `-race` passing cleanly and the existing `TestNodeFabric_ConcurrentReads`
    covering the counter path — supports the immutability claim; a failure there
    elevates the global-cache and clock concerns.
