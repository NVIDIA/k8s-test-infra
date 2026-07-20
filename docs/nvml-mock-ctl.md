# Runtime control with `nvml-mock-ctl`

`nvml-mock-ctl` mutates the simulated GPU state of a running nvml-mock node
**without** a Helm upgrade, image rebuild, or pod restart. Use it to inject
failures, flip ECC state, or tweak metrics (temperature, power, utilization,
clocks, fan, …) on the fly while a test is in flight.

- **Boot-time state** comes from the Helm profile / `config.yaml` (see the
  [Helm chart README](../deployments/nvml-mock/helm/nvml-mock/README.md) and
  [Configuration Reference](configuration.md)).
- **Runtime state** is layered on top by `nvml-mock-ctl`, described here.

## How it works

nvml-mock is a per-process shared library (`libnvidia-ml.so`), **not** a daemon.
There is no server to send commands to. Instead:

1. `nvml-mock-ctl` atomically writes a node-local **overlay** file,
   `overrides.yaml`, that sits next to the pristine `config.yaml`.
2. The mock engine loaded inside every consumer process re-reads that overlay on
   a short TTL (default **1s**, `MOCK_NVML_OVERLAY_TTL`) and **deep-merges** it
   over the pristine base config. The base `config.yaml` is never mutated.
3. Because the overlay is bind-mounted into consumer containers (via CDI), both
   already-running processes and freshly-started ones converge on the new state
   within one TTL.

The merge order is:

```
base config.yaml  <  overlay "all:" block  <  overlay "devices[<idx>]:" block
```

So a per-device override wins over an `all` override, which wins over the base
profile. Unknown fields and bad types are **rejected** at write time, so a typo
fails the command instead of silently doing nothing.

### v1 scope — what is and isn't hot-reloadable

The overlay drives every *config-derived getter*: failure injection, ECC
mode/counters, temperature, power, utilization, clocks, fan, performance state,
and the like. These change within one TTL.

A handful of **identity / topology** fields are baked onto the device at
construction time and are **not** hot-reloadable in v1 — changing them requires a
pod restart or a Helm change:

- device `name`
- `architecture`
- `brand`
- `compute_capability`
- `uuid`
- PCI `bus_id`
- device memory totals — `memory` / `bar1_memory` (baked at construction, so e.g.
  `set --gpu 0 memory.total_bytes=...` won't take effect until a pod restart)

If you need to change any of those, edit the profile / Helm values and restart
the DaemonSet (or the affected pod).

## Where it runs

`nvml-mock-ctl` ships inside the nvml-mock DaemonSet image and runs via
`kubectl exec` into the DaemonSet pod on the **target node**. Its scope is
**per-node**: it only affects the node whose pod you exec into. To change several
nodes, repeat the command against each node's pod.

Inside the pod the config path is wired through `MOCK_NVML_CONFIG`, and the
overlay defaults to the host-mounted driver config dir
(`/var/lib/nvml-mock/driver/config/overrides.yaml`), so you normally run the
subcommands with no path flags. (Set `MOCK_NVML_OVERRIDES` / `--file` only to
override that default.)

```bash
# Pick the nvml-mock pod on a specific node.
# Replace <node> with the node name; adjust -n if you installed elsewhere.
POD=$(kubectl -n nvml-mock get pod -l app.kubernetes.io/name=nvml-mock \
  --field-selector spec.nodeName=<node> -o jsonpath='{.items[0].metadata.name}')
```

## Command reference

```text
usage: nvml-mock-ctl <command> [flags]

commands:
  fail   --gpu <idx|all|uuid> --mode <healthy|lost|fallen_off_bus|ecc_uncorrectable> [--after-calls N] [--xid CODE]
  temp   --gpu <idx|all|uuid> <celsius>    pin reported GPU temperature
  power  --gpu <idx|all|uuid> <watts>      pin reported power draw
  fan    --gpu <idx|all|uuid> <percent>    pin reported fan speed (forces fan count >= 1)
  util   --gpu <idx|all|uuid> <percent>    pin reported GPU + memory utilization
  clocks --gpu <idx|all|uuid> <mhz>        pin reported SM + graphics clocks
  throttle --gpu <idx|all|uuid> <reason>[ reason ...]  set active throttle reasons ('none' clears)
  pstate --gpu <idx|all|uuid> <0-15>       pin reported performance state (P-state)
  set    --gpu <idx|all|uuid> key.path=value [key.path=value ...]
  status [--gpu <idx>]
  reset  [--gpu <idx|all|uuid>]

global flags:
  --file    overlay path (default $MOCK_NVML_OVERRIDES or /var/lib/nvml-mock/driver/config/overrides.yaml)
  --config  config path for UUID resolution/validation (default $MOCK_NVML_CONFIG or /var/lib/nvml-mock/driver/config/config.yaml)
```

### Targeting: `--gpu <idx|all|uuid>`

- **index** — a device index, e.g. `--gpu 0`.
- **`all`** — the shared `all` bucket; applies to every device unless a
  per-device override wins.
- **UUID** — a GPU UUID, e.g. `--gpu GPU-12345678-...`. UUID targeting resolves
  the UUID to an index using the profile config, so it **only works for devices
  that declare an explicit `uuid:` in the YAML**. Devices whose UUID is
  auto-generated (no `uuid:` in the profile) cannot be targeted by UUID — use
  the index instead.

### `fail` — inject or clear a failure

Sets the `failure` block for the target. Modes:

| mode                | effect                                                            |
| ------------------- | ---------------------------------------------------------------- |
| `healthy`           | **removes** the failure override (recovers the device)          |
| `lost`              | guarded calls and handle lookups return `ERROR_GPU_IS_LOST`     |
| `fallen_off_bus`    | same surface as `lost` (models a GPU that fell off the PCIe bus) |
| `ecc_uncorrectable` | device stays addressable; uncorrectable ECC counters climb       |

- `--after-calls N` — trip deterministically after `N` guarded NVML calls
  (omit for "trip on first guarded call").
- `--xid CODE` — surface this Xid code through the NVML event set once the
  device trips (delivered for any tripped failure mode with a Xid configured,
  e.g. `--mode ecc_uncorrectable --xid 79`).

`fail --mode healthy` is how you *recover* a single device (it deletes the
`failure` block from that bucket). See the failure-injection section of the
[mock NVML README](../pkg/gpu/mocknvml/README.md#failure-injection-optional) for
the full per-mode semantics.

### `temp` / `power` / `fan` / `util` / `clocks` / `throttle` / `pstate` — pin a common metric

These convenience commands pin the most-tweaked readings to a fixed value.
Except for `throttle` they take a single positional argument:

| command                            | argument       | pins                                              |
| ---------------------------------- | -------------- | ------------------------------------------------- |
| `temp --gpu <t> <celsius>`         | 0–200 °C       | `nvidia-smi ... temperature.gpu`                  |
| `power --gpu <t> <watts>`          | watts (≥0)     | `nvidia-smi ... power.draw` (converted to mW)     |
| `fan --gpu <t> <percent>`          | 0–100 %        | `nvidia-smi ... fan.speed`                        |
| `util --gpu <t> <percent>`         | 0–100 %        | `nvidia-smi ... utilization.gpu,utilization.memory` |
| `clocks --gpu <t> <mhz>`           | 0–100000 MHz   | `nvidia-smi ... clocks.sm,clocks.gr`              |
| `throttle --gpu <t> <reason>...`   | reason name(s) | `nvidia-smi ... clocks_throttle_reasons.*`        |
| `pstate --gpu <t> <0-15>`          | P-state number | `nvidia-smi ... pstate`                           |

They exist because pinning these fields by hand is fiddly (see [Dynamic metrics
mask their static counterparts](#dynamic-metrics-mask-their-static-counterparts)
below). Each command does the right thing regardless of how the profile is
configured:

- **`temp`** and **`power`** write *both* the static block **and** a
  zero-variation dynamic block (`ramp_c`/`variance_c` = 0, `variance_mw` = 0), so
  the reading is deterministic whether or not the profile runs the dynamic-metrics
  simulator. The engine rebuilds the simulator on the next TTL, so running
  consumers converge without a restart. `power` accepts **watts** (the unit
  `nvidia-smi` displays) and converts to the milliwatts NVML uses; the value is
  still clamped to the profile's `[min_limit_mw, max_limit_mw]` envelope.
- **`fan`** sets `fan.speed_percent` and forces `fan.count` to at least 1 (a
  larger baseline count is preserved). Liquid/passively-cooled profiles ship
  `fan.count: 0`, which makes `fan.speed` report `[N/A]`; forcing the count makes
  the pinned speed observable. There is no dynamic fan simulator, so this touches
  only the static fan block.
- **`util`** pins GPU **and** memory utilization to the same percent. It sets the
  static `utilization` block and **disables** the dynamic utilization
  sub-simulator (writes `dynamic_metrics.utilization: null`), so the value is
  deterministic for any percent — including `0`, which a zero-variation dynamic
  block could not express (the simulator treats `min==max==0` as "unbounded").
- **`clocks`** pins the reported SM and graphics clocks (`clocks.sm_current` and
  `clocks.graphics_current`). There is no dynamic clock simulator, so it
  hot-reloads directly. Memory/video clocks keep their profile baseline — use
  `set clocks.memory_current=<mhz>` to change those.
- **`throttle`** sets the active clock-throttle reasons. It is *authoritative*:
  the requested reasons are turned on and every other reason is turned off, so
  repeated calls replace (not accumulate) state. Pass one or more reason names,
  or `none` (on its own) to clear them all. Accepted names are the
  `clocks_throttle_reasons` field names plus short aliases: `thermal`
  (`hw_thermal_slowdown`), `sw_thermal` (`sw_thermal_slowdown`), `power`
  (`sw_power_cap`), `power_brake` (`hw_power_brake_slowdown`), `idle`
  (`gpu_idle`), `app_clocks` (`applications_clocks_setting`),
  `display_clocks` (`display_clocks_setting`), plus `hw_slowdown` and
  `sync_boost`.
- **`pstate`** pins the performance state to `P<n>` for `n` in `0–15`.

`reset` clears these overrides and returns the metric to the profile baseline
(varying again, if the profile drives it dynamically). For anything these
commands don't cover, use `set` below.

### `set` — set arbitrary fields

`set` takes one or more `key.path=value` pairs. The path is the YAML/JSON path
into the device config; the value is parsed as a YAML scalar (so numbers, bools,
and strings get their natural type). Example paths: `thermal.temperature_gpu_c`,
`utilization.gpu`, `ecc.mode_current`, `power.current_draw_mw`.

#### Dynamic metrics mask their static counterparts

When the profile enables **dynamic metrics** (`gpu.dynamicMetrics.enabled=true`,
which the demo/e2e charts do), the simulator drives `temperature`, `power`, and
`utilization` and **masks the static blocks** for those fields. In that mode:

- `set thermal.temperature_gpu_c=<n>` has **no visible effect** — the simulator
  keeps producing its own reading.
- To pin temperature, override the dynamic block and zero its variation so the
  reading is deterministic:

  ```bash
  nvml-mock-ctl set --gpu 0 \
    dynamic_metrics.temperature.base_c=85 \
    dynamic_metrics.temperature.ramp_c=0 \
    dynamic_metrics.temperature.variance_c=0
  ```

  The engine rebuilds the simulator on the next TTL, so a running consumer sees
  the pinned value without a restart; `reset` returns it to the varying baseline.
  The **`temp` command above does exactly this for you** — prefer it (and the
  `power`/`util` commands) unless you need a field the convenience commands don't
  cover.

If dynamic metrics is **disabled**, the static `thermal.temperature_gpu_c` (and
`power.*`, `utilization.*`) are authoritative and hot-reload directly. The same
masking applies to `power` (`dynamic_metrics.power`) and `utilization`
(`dynamic_metrics.utilization`). Note `util` handles this by *disabling* the
dynamic utilization sub-simulator rather than zeroing its variation, so it pins
correctly even at `0%`.

To change several fields at once, pass multiple `key.path=value` pairs to a
single `set` invocation.

### `status` — inspect active overrides

`status` prints the current `overrides.yaml`. `status --gpu <idx>` filters to a
single device's bucket plus the shared `all` bucket. **`status --gpu` only
accepts an integer index** (not `all` or a UUID). With no active overrides it
prints `no active overrides`.

### `reset` — remove overrides

`reset --gpu <t>` removes the override bucket for the target. `reset` with **no**
`--gpu` clears **everything** (equivalent to `reset --gpu all`). State reverts to
the pristine profile within one TTL.

## Reset semantics

| Action | Effect on runtime overrides | Result |
| ------ | --------------------------- | ------ |
| `nvml-mock-ctl reset [--gpu <t>]` | clears the targeted bucket(s) from `overrides.yaml` | device(s) revert to pristine profile within one TTL |
| `nvml-mock-ctl fail --gpu <t> --mode healthy` | removes just the `failure` block for the target | that device recovers within one TTL; other overrides stay |
| DaemonSet pod restart | `setup.sh` deletes `overrides.yaml` on startup | **all** overrides wiped; back to pristine profile |
| Consumer pod restart | none — the overlay lives on the node, not in the consumer | consumer re-reads and picks up the *current* overlay (does **not** reset it) |
| `helm upgrade` (profile/values change) | rolls the DaemonSet pod (config checksum + `RollingUpdate`), so `setup.sh` wipes `overrides.yaml` on the new pod | **all** overrides reset to the new pristine config; only an upgrade that does not recreate the nvml-mock pod leaves an overlay in place |

## Worked examples

All examples assume `$POD` is set as shown in [Where it runs](#where-it-runs).

```bash
# 1) Force uncorrectable ECC on GPU 0, deliver Xid 79
kubectl -n nvml-mock exec "$POD" -- nvml-mock-ctl fail --gpu 0 --mode ecc_uncorrectable --after-calls 1 --xid 79
# verify from any consumer pod:
kubectl exec <consumer> -- nvidia-smi --query-gpu=ecc.errors.uncorrected.aggregate.total --format=csv,noheader
```

```bash
# 2) Mark ALL GPUs lost
kubectl -n nvml-mock exec "$POD" -- nvml-mock-ctl fail --gpu all --mode lost
```

```bash
# 3) Pin GPU 3's reported temperature to 85 C (works whether or not the
# profile drives temperature dynamically — the temp command handles both).
kubectl -n nvml-mock exec "$POD" -- nvml-mock-ctl temp --gpu 3 85
# verify from any consumer pod:
kubectl exec <consumer> -- nvidia-smi --id=3 --query-gpu=temperature.gpu --format=csv,noheader,nounits
```

```bash
# 3b) Pin power draw to 350 W on all GPUs, and fan speed to 60% on GPU 0.
kubectl -n nvml-mock exec "$POD" -- nvml-mock-ctl power --gpu all 350
kubectl -n nvml-mock exec "$POD" -- nvml-mock-ctl fan --gpu 0 60
# verify from any consumer pod:
kubectl exec <consumer> -- nvidia-smi --query-gpu=index,power.draw,fan.speed --format=csv,noheader
```

```bash
# 3c) Pin utilization, clocks, a throttle reason and the P-state on GPU 0.
kubectl -n nvml-mock exec "$POD" -- nvml-mock-ctl util --gpu 0 90
kubectl -n nvml-mock exec "$POD" -- nvml-mock-ctl clocks --gpu 0 1200
kubectl -n nvml-mock exec "$POD" -- nvml-mock-ctl throttle --gpu 0 thermal
kubectl -n nvml-mock exec "$POD" -- nvml-mock-ctl pstate --gpu 0 8
# verify from any consumer pod:
kubectl exec <consumer> -- nvidia-smi --id=0 \
  --query-gpu=utilization.gpu,clocks.sm,clocks_throttle_reasons.hw_thermal_slowdown,pstate \
  --format=csv,noheader
# clear the throttle reason again:
kubectl -n nvml-mock exec "$POD" -- nvml-mock-ctl throttle --gpu 0 none
```

```bash
# 4) Set several fields on GPU 0 in one call
kubectl -n nvml-mock exec "$POD" -- nvml-mock-ctl set --gpu 0 \
  ecc.mode_current=disabled \
  utilization.gpu=100
```

```bash
# 5) Target by UUID (requires an explicit uuid: in the profile for that device)
kubectl -n nvml-mock exec "$POD" -- nvml-mock-ctl fail --gpu GPU-12345678-1234-1234-1234-123456780000 --mode fallen_off_bus
```

```bash
# 6) Inspect active overrides
kubectl -n nvml-mock exec "$POD" -- nvml-mock-ctl status
# or just GPU 0 (integer index only):
kubectl -n nvml-mock exec "$POD" -- nvml-mock-ctl status --gpu 0
```

```bash
# 7) Recover one GPU, then reset everything
kubectl -n nvml-mock exec "$POD" -- nvml-mock-ctl fail --gpu 0 --mode healthy
kubectl -n nvml-mock exec "$POD" -- nvml-mock-ctl reset --gpu all
```

```bash
# 8) Full reset via pod restart (setup.sh wipes overrides.yaml on startup)
kubectl -n nvml-mock delete pod "$POD"
```

## Troubleshooting

- **Changes aren't visible immediately.** Propagation is bounded by the overlay
  TTL (~1s default, `MOCK_NVML_OVERLAY_TTL`). Wait one TTL and re-check.
  `nvidia-smi` spawns a fresh process on every call, so it always reflects the
  current overlay once the TTL has elapsed; long-lived in-process NVML clients
  pick up the change on their next getter after the TTL.
- **Confirm what's actually applied.** Run `nvml-mock-ctl status` (optionally
  `--gpu <idx>`), or read `overrides.yaml` directly on the node.
- **The command was rejected.** Unknown fields or bad value types fail the write
  (nothing is applied). Check the field path against the profile schema in the
  [Configuration Reference](configuration.md) — e.g. it's
  `thermal.temperature_gpu_c`, not `temperature.gpu_temp_c`.
- **UUID target won't resolve.** The device likely has an auto-generated UUID
  (no `uuid:` in the profile). Target it by index instead.
- **Nothing changed on other nodes.** Scope is per-node. Repeat the command
  against each node's DaemonSet pod.
- **An identity field didn't change.** Device `name`, `architecture`, `brand`,
  `compute_capability`, `uuid`, and PCI `bus_id` are baked at construction and
  are not hot-reloadable in v1. Change the profile/Helm values and restart the
  pod.
