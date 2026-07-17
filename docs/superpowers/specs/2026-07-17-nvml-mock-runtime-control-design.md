# nvml-mock Runtime Control (`nvml-mock-ctl`) — Design

Date: 2026-07-17
Status: Approved (design); pending implementation plan

## Problem

Today the nvml-mock GPU simulation is driven entirely by a static `config.yaml`
delivered through a Helm-managed ConfigMap. Changing any simulated GPU property
(for example, forcing a GPU into `ecc_uncorrectable` or `lost`) requires a Helm
upgrade plus restarting the consumer pods so the `libnvidia-ml.so` reloads.

We want to mutate simulated GPU state **at runtime**, without a Helm upgrade, and
have both already-running and newly-started consumers observe the change. On a
DaemonSet pod restart the state must fall back to the pristine YAML.

## Goals

1. A dedicated CLI, `nvml-mock-ctl`, that changes simulated GPU parameters at
   runtime (broad: any field in the device config; motivating cases are failure
   modes such as `ecc_uncorrectable` and `lost`).
2. Changes are observed by **both** already-running and newly-started consumer
   processes (bounded staleness, not necessarily instantaneous).
3. Restarting the nvml-mock DaemonSet pod resets state back to the original YAML.
4. The static `config.yaml` is never mutated; overrides are a separate layer.

## Non-goals

- No cross-node orchestration in v1 (each DaemonSet pod controls its own node's
  GPUs; `kubectl exec` targets a specific node's pod).
- No new long-running daemon or socket IPC.
- No push/event-based propagation; polling with a short TTL is sufficient.
- No changes to the NVML C ABI surface.

## Key architectural context

nvml-mock is **not a running service**. `libnvidia-ml.so` is loaded fresh into
every consumer process (nvidia-smi, DCGM, device plugin, workloads). Each process
holds its own in-memory `Engine` singleton, and config is loaded and cached once
at init. There is no central process holding GPU state and no existing IPC for
NVML state.

There is, however, an established pattern for file-based runtime coordination:
`fabric_readiness.go` polls a marker file with a 1-second TTL cache. This design
reuses that pattern.

## Chosen approach: file-based overlay with TTL re-read

`nvml-mock-ctl` writes a single per-node **overlay file**; the engine re-reads it
on a short TTL and merges it over the base config on read. This was chosen over a
control daemon + Unix socket IPC (heavier: requires mounting a socket into every
consumer, connection lifecycle, a new daemon) and over shared memory (fragile
across containers, poor fit for CGo and the CDI bind-mount model).

### Data flow

```
kubectl exec <nvml-mock-pod> -- nvml-mock-ctl fail --gpu 0 --mode ecc_uncorrectable
        │
        └─> writes /var/lib/nvml-mock/driver/config/overrides.yaml   (atomic rename)
                │  host path, already inside every consumer's CDI-mounted driver tree
                ▼
Consumer process with libnvidia-ml.so loaded
        │  on each NVML call, engine checks overlay mtime (≤ TTL cache)
        │  if changed: deep-merge overlay over base config → new *DeviceConfig
        │  atomic-swap the live config pointer; reconcile failure injector
        ▼
NVML query returns the overridden value (both already-running and new procs)
```

### Why the overlay location works

The overlay lives at `<driver_root>/config/overrides.yaml`
(`/var/lib/nvml-mock/driver/config/overrides.yaml` on the host). This directory is
already part of the CDI-mounted driver tree, so every consumer container sees the
file without any new mount plumbing. `nvml-mock-ctl` runs inside the nvml-mock
DaemonSet pod (via `kubectl exec`) and writes to the same host path.

## Overlay file format

Uses the same schema vocabulary as `config.yaml`, so any field is controllable
without per-field plumbing.

```yaml
version: 1
# applies to every GPU unless overridden per-index
all:
  ecc:
    mode_current: disabled
# per-GPU overrides, keyed by device index
devices:
  0:
    failure:
      mode: ecc_uncorrectable
  3:
    temperature:
      gpu_temp_c: 95
```

### Merge semantics

The overlay is parsed as a generic nested map and **deep-merged** over the base
config's map representation, in precedence order:

```
base DeviceConfig  <  overlay.all  <  overlay.devices[index]
```

The merged map is then unmarshaled into the typed `DeviceConfig`. Only keys
present in the overlay change; all other fields retain their YAML values. The
generic map-merge (rather than typed partial structs) is what makes broad,
any-field control cheap and keeps the CLI and engine decoupled from the specific
field set.

## CLI surface: `nvml-mock-ctl`

Hybrid surface. Targeting by index, `all`, or UUID (UUID resolves to an index via
the loaded config).

```
nvml-mock-ctl fail   --gpu <idx|all|uuid> --mode <healthy|lost|fallen_off_bus|ecc_uncorrectable>
nvml-mock-ctl set    --gpu <idx|all|uuid> <dotted.path>=<value> [...]   # e.g. ecc.mode_current=disabled
nvml-mock-ctl apply  --gpu <idx|all|uuid> -f patch.yaml                 # arbitrary snippet
nvml-mock-ctl status [--gpu <idx>]                                      # print active overrides
nvml-mock-ctl reset  [--gpu <idx|all|uuid>]                            # clear overrides
```

- `fail` is sugar over `set failure.mode=...`.
- `set` / `apply` validate that dotted paths / snippet keys map to real
  `DeviceConfig` fields before writing (fail fast on typos).
- All mutating commands perform an atomic read-modify-write of the overlay
  (temp file + rename) under a file lock to avoid concurrent-exec races.
- `status` prints the currently active overrides (the overlay content), not the
  per-consumer effective config.

## Engine changes (`pkg/gpu/mocknvml/engine/`)

- New `runtime_overlay.go`: TTL-cached loader keyed on file mtime (mirrors
  `fabric_readiness.go`), performing the deep-merge and producing per-device
  `*DeviceConfig` snapshots.
- `ConfigurableDevice.config` becomes an `atomic.Pointer[DeviceConfig]`; a `cfg()`
  accessor replaces direct `d.config` reads throughout the device getters. This is
  a mechanical change that gives lock-free, torn-read-free swaps while a refresh
  installs a new snapshot.
- On refresh, the `failureInjector` is **reconciled** to the overlay's
  `failure.mode`, including clearing back to `healthy`. Today the injector is
  sticky (no recovery); this design adds a controlled reset path so `ctl`-driven
  recovery works.
- Overlay path resolution: next to config (`<driver_root>/config/overrides.yaml`)
  or via `MOCK_NVML_OVERRIDES` env var. TTL configurable via
  `MOCK_NVML_OVERLAY_TTL` (default 1s).

### Concurrency

Refresh happens lazily on NVML calls when the TTL has elapsed and the overlay
mtime changed. The new `*DeviceConfig` is built off to the side and installed with
a single atomic pointer swap; readers always see a consistent snapshot. The
failure injector reconcile is guarded so mode transitions are consistent with the
installed config snapshot.

## Deployment changes

- `setup.sh`: create the runtime overlay directory, ensure it is writable and
  inside the CDI-mounted tree, and **delete `overrides.yaml` on startup** so an
  nvml-mock DaemonSet pod restart resets state to pristine YAML.
- `nvml-mock-ctl` added to the Dockerfile build and baked into the image, like the
  other `cmd/` binaries.
- Helm: no required changes for v1 (works via `kubectl exec`); document the
  workflow.

## Reset semantics summary

| Event | Effect |
|-------|--------|
| `nvml-mock-ctl reset [--gpu ...]` | Clears all / per-GPU overrides live (within TTL) |
| nvml-mock DaemonSet pod restart | `setup.sh` deletes overlay → pristine YAML |
| Consumer/workload pod restart | No reset (overlay persists on host) |
| Helm upgrade | Rolls DaemonSet → overlay cleared on new pod start |

## Testing

**Unit**
- Deep-merge correctness; `all` vs per-index precedence.
- UUID → index resolution.
- Dotted-path / snippet validation against `DeviceConfig`.
- Atomic write + file lock behavior.
- Failure-injector reconcile, including healthy-reset from a tripped state.
- TTL / mtime caching (no re-parse when unchanged).
- Concurrent read during atomic config swap.

**E2E** (extend `tests/e2e/go/scenario_failure_injection.go`)
- Instead of Helm-upgrade + pod-delete, run `nvml-mock-ctl` inside the DaemonSet
  pod and assert a **running** consumer observes `ecc_uncorrectable` / `lost`
  within the TTL.
- `reset` restores healthy for the running consumer.

## Open questions / future work

- Cross-node convenience wrapper (kubectl plugin) to fan out to multiple nodes.
- Optional Helm surface to pre-seed an initial overlay.
- Persisted-override mode (survive pod restart) if a use case emerges.
