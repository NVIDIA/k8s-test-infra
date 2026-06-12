## MAGI Report: BALTHASAR / claude-opus-4-8-thinking-high

PR #387 — `feat/nvswitch-fabricmanager` (simulate NVSwitch topology + fake
nvidia-fabricmanager in the mock NVML engine).

Scope reviewed: `pkg/gpu/mocknvml/engine/{topology,fabric,fabric_readiness,nvlink_fields,nvlink_counters,device,engine}.go`,
`pkg/fmcoord/coord.go`, shipped profiles, `deployments/nvml-mock/scripts/setup.sh`,
and the new test suites. `go test ./pkg/gpu/mocknvml/... ./pkg/fmcoord/...`
passes (engine/bridge/fmcoord all `ok`).

Bottom line: the architecture is sound (single immutable `NodeFabric`, pure
time-function counters, injectable clock/stat) and there are no must-fix
correctness defects that affect the *shipped* profiles. However there are
several real latent correctness bugs that surface the moment a config deviates
from the homogeneous, contiguous, all-switch shipped layout — exactly the
"hidden edge case" class. Findings below, highest-impact first.

### Findings

- [Medium] Should-fix: `computeNVCounts` fans switch links to *every* GPU, including non-switch GPUs (asymmetric + over-reporting)
  - Evidence: `topology.go` `computeNVCounts()` second loop:
    ```401:431:pkg/gpu/mocknvml/engine/topology.go
    for i := 0; i < f.numDevices; i++ {
        if switchLinks[i] == 0 {
            continue
        }
        for j := 0; j < f.numDevices; j++ {
            if j != i {
                f.nvCount[i][j] += switchLinks[i]
            }
        }
    }
    ```
    The inner loop adds `switchLinks[i]` to `nvCount[i][j]` for *all* `j != i`,
    never checking whether `j` is itself attached to the switch fabric. For a
    mixed node (GPU `i` on an NVSwitch, GPU `j` direct-PCIe only):
    `nvCount[i][j] = switchLinks[i] > 0` but `nvCount[j][i] = 0`. The matrix is
    both asymmetric and physically wrong — a non-switch GPU is reported as
    switch-reachable from the switch side.
  - Impact: `nvidia-smi topo -m` would draw `NV#` from switch GPUs to GPUs that
    are not on the fabric, and the matrix would not be transpose-symmetric.
    `GetP2PStatus` (which keys off `NVLinkCount`) would then return `P2P_OK` in
    one direction and `NOT_SUPPORTED` in the other for the same pair. No impact
    on shipped profiles (all GPUs are homogeneously switch-attached, so all
    `switchLinks[i]` are equal → symmetric by accident), but any partial-fabric
    or heterogeneous profile trips it.
  - Suggested fix: only fan between two switch-attached endpoints, which also
    restores symmetry:
    ```go
    if switchLinks[i] == 0 { continue }
    for j := 0; j < f.numDevices; j++ {
        if j != i && switchLinks[j] > 0 {
            f.nvCount[i][j] += switchLinks[i]
        }
    }
    ```
    (Consider using `min(switchLinks[i], switchLinks[j])` if you want the pair
    count to reflect the bottleneck endpoint rather than the source endpoint.)
  - Confidence: High (code path is unambiguous; verified the shipped profiles
    are homogeneous via `TestNodeFabric_BuiltinProfiles`).

- [Medium] Should-fix: NVLink enumeration conflates "active link count" with "max link index" — non-contiguous or gapped active links are mis-enumerated
  - Evidence: `fiNvlinkLinkCount` is backed by `ActiveLinkCount` (a *cardinality*):
    ```181:183:pkg/gpu/mocknvml/engine/nvlink_fields.go
    case fiNvlinkLinkCount:
        return NVLinkFieldUint, uint64(f.ActiveLinkCount(d.index)), nvml.SUCCESS
    ```
    but every per-link getter is keyed by the link *identifier* via
    `f.Link(dev, link)` which linear-searches `l.Link == link` (`topology.go`
    L469-479), and nvidia-smi iterates `scopeId = 0 .. LINK_COUNT-1`
    (`bridge/fieldvalues.go` passes `scopeId` straight through). If a device's
    active links are not the contiguous block `0..count-1` — e.g. link 0
    inactive and links 1..N active, or configured links `{0,2,4}` — then
    `LINK_COUNT = N` but iteration over scopeIds `0..N-1` queries non-existent
    indices (returns `NOT_SUPPORTED`/disabled) and never reaches the
    highest-numbered active link.
  - Impact: dropped/mislabeled links in `nvlink -s/-c/-e` whenever active links
    aren't a dense `0..N-1` range. Real NVML reports `LINK_COUNT` as the number
    of link *slots* (max index + 1), not the active count, and enumerates the
    full index range. Shipped profiles are dense and all-active (synthesized
    `Link: k` for `k in 0..want-1`), so no current impact — but partial-link or
    sparse configs (a natural failure-injection scenario for this mock) break.
  - Suggested fix: back `fiNvlinkLinkCount` with the max link slot count
    (`NumLinks`/highest `Link`+1) rather than `ActiveLinkCount`, and have the
    inactive-link getters return a defined "disabled" rather than NOT_SUPPORTED
    so iteration stays aligned; or document/enforce that link indices must be
    dense `0..N-1` (validate in `Validate()`).
  - Confidence: High on the mechanism; Medium on user impact (no shipped
    profile hits it today).

- [Medium] Should-fix: topology enumeration getters ignore the CDI visible-device subset
  - Evidence: `engine.go` `TopologyNearestGpus` iterates the *full* fabric and
    returns peers regardless of visibility:
    ```388:406:pkg/gpu/mocknvml/engine/engine.go
    for j := 0; j < cd.fabric.NumDevices(); j++ {
        if j == cd.index { continue }
        if cd.fabric.TopoLevel(cd.index, j) > level { continue }
        peer := e.server.configurableDevices[j]
        ...
        out = append(out, h)
    }
    ```
    `TopologyGpuSet` (L424-444) likewise loops over all `configurableDevices`
    with no `isDeviceVisible` check. The per-handle filter only lives in
    `DeviceGetHandleBy*` / `DeviceGetCount`.
  - Impact: in a container where CDI injected only a subset of `/dev/nvidia*`
    (so `visibleDevices` is set), `nvmlDeviceGetTopologyNearestGpus` and
    `nvmlSystemGetTopologyGpuSet` return — and auto-register handles for —
    GPUs that are not allocated to the container, leaking hidden devices into
    the container's NVML view and contradicting the visibility illusion the rest
    of the engine maintains. `nvidia-smi topo -m` could then list non-allocated
    GPUs.
  - Suggested fix: gate both loops with `s.isDeviceVisible(j)` (and remap to
    visible indices for any index-based output), mirroring `DeviceGetHandleByIndex`.
  - Confidence: High.

- [Medium] Should-fix: NUMA node defaults to 0 for unmatched devices (zero-value ambiguity → misreport)
  - Evidence: `numaOf` is allocated zero-filled and only written for devices
    whose BDF matched a root complex:
    ```144:158:pkg/gpu/mocknvml/engine/topology.go
    numaOf:     make([]int, n),
    ```
    `resolveAffinity` only sets `f.numaOf[idx]` inside the `bdfToIndex` match
    (L211-214). `NumaNode` then returns the raw value whenever `hasPCIe`:
    ```505:511:pkg/gpu/mocknvml/engine/topology.go
    func (f *NodeFabric) NumaNode(dev int) int {
        if dev < 0 || dev >= f.numDevices || !f.hasPCIe {
            return -1
        }
        return f.numaOf[dev]
    }
    ```
    A device that exists but is absent from `pcie_topology.root_complexes`
    (typo, partial config, or `num_devices` > listed devices) reports NUMA
    node 0 — indistinguishable from a legitimately node-0 device.
  - Impact: `GetNumaNodeId` returns `(0, SUCCESS)` and `MemoryAffinityMask`
    sets the node-0 bit for an unaffined device, instead of `NOT_SUPPORTED`.
    Wrong NUMA/affinity is silently fed to schedulers/topology managers under test.
  - Suggested fix: initialize `numaOf` to `-1` (e.g. loop after `make`, or set
    during `BuildNodeFabric`), so `NumaNode` returns `-1` (→ `NOT_SUPPORTED`)
    for unmatched devices. Same defensiveness for `rootOf`/`cpusOf` is already
    safe (empty string / nil).
  - Confidence: High.

- [Low] Should-fix: deterministic NVLink counters can overflow uint64 on long-uptime nodes (btime epoch × per-field multipliers)
  - Evidence: counters anchor to `/proc/stat` btime in production (no
    `MOCK_NVML_EPOCH` is exported — see `setup.sh` L316-318), so `dt` is the
    full node uptime. `accrue` (`topology.go` L615-624) has no upper bound, and
    callers multiply the accrued value again:
    ```169:178:pkg/gpu/mocknvml/engine/nvlink_fields.go
    case 0:
        return NVLinkFieldUint64, v * 107, nvml.SUCCESS
    ```
    ```285:290:pkg/gpu/mocknvml/engine/nvlink_fields.go
    case fiNvlinkCountXmitBytes, fiNvlinkCountRcvBytes:
        ...
        return NVLinkFieldUint64, v * avgBytesPerNvlinkPacket, nvml.SUCCESS
    ```
    For GB200 (`rate = 53125e6 * 0.05 ≈ 2.66e9`/s), `v ≈ 1.6e17` at ~2 years
    uptime; `v*107 ≈ 1.7e19` is at the edge of `uint64` (`1.8e19`) and wraps
    beyond that, breaking monotonicity (counter appears to jump backwards).
    Separately, `uint64(math.Floor(dt*rate))` is undefined for out-of-range
    floats in Go (only reachable at absurd uptimes, but the conversion has no
    guard), and values past 2^53 lose integer precision in the float multiply.
  - Impact: low likelihood (requires very long-lived nodes), but a wrap
    silently violates the monotonicity contract the new tests assert
    (`TestNvLinkCounter_Monotonic`), and would only manifest in long-running
    soak environments — hard to reproduce.
  - Suggested fix: saturate in `accrue` (cap product at `math.MaxUint64`,
    guard the float→uint conversion) and apply the `*107` / `*86` multipliers
    with saturation (or pre-divide so the post-multiply stays bounded).
  - Confidence: Medium (math verified; depends on deployment uptime).

- [Low] Should-fix: fabric readiness is presence-only — a SIGKILL'd fabricmanager stays "COMPLETED" for the life of the pod
  - Evidence: `isReady` only stats the marker file (`fabric_readiness.go`
    L77-88); `fmcoord` removes it only on SIGTERM/SIGINT (`coord.go` L24-25,
    `RemoveReady`). A `kill -9` of the daemon (or a daemon crash) leaves the
    marker in place, and the cache will keep returning `ready=true` on every
    re-stat after the TTL. There is no liveness signal (PID, heartbeat,
    marker mtime freshness) even though the daemon "re-asserts every 2s"
    (`setup.sh` L322).
  - Impact: a dead fabricmanager continues to report fabric state COMPLETED,
    so the mock cannot represent the very "fabricmanager died" fault it exists
    to simulate. Partially mitigated across pod *restarts* because `setup.sh`
    L329 deletes a stale marker before starting the fresh daemon, but not for
    an in-pod daemon death.
  - Suggested fix: have the daemon refresh the marker mtime on each 2s tick and
    have `isReady` treat a marker older than, say, 2–3× the tick as not-ready
    (liveness via freshness), or write the daemon PID and verify it.
  - Confidence: Medium (behavior is by-design per the doc comment, but it's a
    correctness gap for the mock's stated purpose).

- [Low] Should-fix: `fabricReadiness` is a process-global cache that `ResetForTesting` does not clear (test-isolation hazard)
  - Evidence: `var fabricReadiness = &fabricReadinessCache{...}` (singleton,
    `fabric_readiness.go` L61) is consulted by the production path
    `resolveFabricState("auto")` (`fabric.go` L103-108), but `ResetForTesting`
    (`engine.go` L555-564) resets only the engine/config singletons, not this
    cache. Today no test populates it with `EnvFabricStateDir` set
    (`TestResolveFabricState_AutoDisabled` uses an empty dir, which short-circuits
    before `isReady`), so it's latent — but any future test that exercises the
    "auto + coupling on" production path through `resolveFabricState` will leak
    a cached `checked`/`ready` (1s TTL) into subsequent tests, producing
    order-dependent flakes.
  - Impact: latent flaky tests; also makes the global hard to reason about.
  - Suggested fix: reset `fabricReadiness` in `ResetForTesting` (e.g. assign a
    fresh `&fabricReadinessCache{now: time.Now, stat: os.Stat}`), and/or add a
    `resolveFabricState`-via-global test that resets it. The existing
    `newCache` helper already proves the design supports injection.
  - Confidence: High (test gap is verifiable).

- [Low] No-action (document): direct GPU↔GPU NV# is intentionally directional, so one-sided configs yield an asymmetric matrix
  - Evidence: `computeNVCounts` increments only `f.nvCount[i][l.RemotePeer]++`
    for `RemoteGPU` links (`topology.go` L409-412), and
    `TestNodeFabric_LegacyFlatLinksMapToDevice0` *enshrines* the asymmetry
    (`NVLinkCount(0,1)=1`, `NVLinkCount(1,0)=0`). This is fine only because
    real configs are expected to author reciprocal links on both peers.
  - Impact: a config author who lists a GPU↔GPU link on one side only gets a
    non-symmetric `topo -m`. Easy to do by accident; not validated.
  - Suggested fix: either symmetrize direct GPU links in `computeNVCounts`
    (mirror `nvCount[peer][i]`), or add a `Validate()` warning when
    `nvCount[i][j] != nvCount[j][i]`. At minimum, document the reciprocity
    requirement near `NVLinkLinkConfig`.
  - Confidence: High (verified against the test that codifies it).

- [Low] No-action: packet vs. byte NVLink counters are mutually inconsistent
  - Evidence: `NvLinkCounters` accrues a *byte*-rate value (`rate = bw * duty`)
    but it is returned verbatim as the packet counter
    (`fiNvlinkCountXmitPackets`/`RcvPackets`, `nvlink_fields.go` L278-283) and
    *also* multiplied by 86 to produce the byte counter (L285-290). So reported
    Xmit/Rcv "packets" already equal the throughput-byte value, and "bytes" are
    86× that — `nvlink -e` byte/packet ratio won't be self-consistent nor match
    `nvlink -s` throughput.
  - Impact: cosmetic for a mock; only matters if a consumer cross-checks
    packets×avg_size ≈ bytes.
  - Suggested fix: derive packets = `v / avgBytesPerNvlinkPacket` (or accrue a
    packet-rate base and multiply up for bytes) so the two track.
  - Confidence: Medium (intent unclear; flagging as a maintainability note).

- [Info] No-action: per-link `SPEED_MBPS_Lx` fields only cover links 0–11
  - Evidence: `speedFieldLink` maps field ids 84–89→links 0–5 and 132–137→links
    6–11 (`nvlink_fields.go` L131-140); there is no field-id range for links
    12–17. GB200 has 18 links. This is harmless because `fiNvlinkGetSpeed`
    (164, scopeId-indexed) and `NVSWITCH_CONNECTED_LINK_COUNT` cover all links,
    and the 580 nvidia-smi uses those — but the per-field speed surface is
    asymmetric across the link range. No change needed unless a tool reads the
    legacy per-field speeds for links ≥12.
  - Confidence: High.

### Architecture / maintainability notes (non-blocking)
- The immutable-`NodeFabric` + pure-time-function-counter design is the right
  call: it eliminates getter drift and is verified race-clean by
  `TestNodeFabric_ConcurrentReads`. Keep it.
- The "filename drift" contract test (`fmcoord_contract_test.go`) coupling
  `engine.FabricReadyMarker` to `fmcoord.ReadyMarker` is a good guard against
  the two halves of the marker protocol diverging.
- Several `nvLinkMaxLinks`/`Link()` paths re-scan `f.links[dev]` linearly per
  call; fine at current scale (≤18 links, ≤8 devices) but worth a map if link
  counts ever grow.
