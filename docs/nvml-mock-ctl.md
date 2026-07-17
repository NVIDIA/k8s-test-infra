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

If you need to change any of those, edit the profile / Helm values and restart
the DaemonSet (or the affected pod).

## Where it runs

`nvml-mock-ctl` ships inside the nvml-mock DaemonSet image and runs via
`kubectl exec` into the DaemonSet pod on the **target node**. Its scope is
**per-node**: it only affects the node whose pod you exec into. To change several
nodes, repeat the command against each node's pod.

Inside the pod the overlay/config paths are already wired through environment
variables (`MOCK_NVML_OVERRIDES`, `MOCK_NVML_CONFIG`), so you normally run the
subcommands with no path flags.

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
  set    --gpu <idx|all|uuid> key.path=value [key.path=value ...]
  apply  --gpu <idx|all|uuid> -f patch.yaml
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
  device trips (pair with `ecc_uncorrectable`, e.g. `--xid 79`).

`fail --mode healthy` is how you *recover* a single device (it deletes the
`failure` block from that bucket). See the failure-injection section of the
[mock NVML README](../pkg/gpu/mocknvml/README.md#failure-injection-optional) for
the full per-mode semantics.

### `set` — set arbitrary fields

`set` takes one or more `key.path=value` pairs. The path is the YAML/JSON path
into the device config; the value is parsed as a YAML scalar (so numbers, bools,
and strings get their natural type). Example paths: `thermal.temperature_gpu_c`,
`utilization.gpu`, `ecc.mode_current`, `power.current_draw_mw`.

### `apply` — apply a multi-field YAML snippet

`apply --gpu <t> -f patch.yaml` deep-merges a YAML fragment (the same schema as a
device block in `config.yaml`) into the target bucket. Good for changing several
fields at once.

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
| `helm upgrade` (profile/values change) | rewrites the base `config.yaml` | changes boot-time state; existing overlay still merges on top |

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
# 3) Set an arbitrary field (raise GPU 3's reported temperature to 95 C)
kubectl -n nvml-mock exec "$POD" -- nvml-mock-ctl set --gpu 3 thermal.temperature_gpu_c=95
```

```bash
# 4) Apply a multi-field snippet to GPU 0
cat > /tmp/patch.yaml <<'EOF'
ecc:
  mode_current: disabled
utilization:
  gpu: 100
EOF
kubectl cp /tmp/patch.yaml "$POD":/tmp/patch.yaml -n nvml-mock
kubectl -n nvml-mock exec "$POD" -- nvml-mock-ctl apply --gpu 0 -f /tmp/patch.yaml
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
