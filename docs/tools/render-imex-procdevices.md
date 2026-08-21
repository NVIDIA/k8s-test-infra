# render-imex-procdevices

A one-shot renderer that writes a substitute `/proc/devices` carrying NVIDIA
caps character-device entries.

On a Mokka node there is no NVIDIA kernel module, so the real `/proc/devices`
has no `nvidia-caps-imex-channels` line and the NVIDIA DRA driver's
compute-domain kubelet plugin aborts at startup. Bind-mounting over
`/proc/devices` is not an option, because runc rejects a mount inside `/proc`.
The DRA driver's chart therefore exposes an `altProcDevices` value (the
`ALT_PROC_DEVICES_PATH` env indirection), and this command produces the file it
points at.

It reads `--source`, appends the two device entries, creates the parent
directory of `--output` with mode 0755, and writes the file with mode 0644
(world-readable by design, since another container consumes it). On success it
prints `wrote <output> (nvidia-caps-imex-channels major=N, nvidia-caps major=M)`
to stdout. Any error goes to stderr with the prefix `render-imex-procdevices: `
and the process exits 1.

Rendering is idempotent by construction: pre-existing
`nvidia-caps-imex-channels` and `nvidia-caps` lines are stripped from the
character-devices section before the new pair is appended, so re-running with a
changed major replaces the entry rather than duplicating it, and a DaemonSet
restart re-runs it safely.

The renderer validates the input structurally, requiring a `Character devices:`
header followed by a `Block devices:` header, and rejects a major above 4095 or
one already bound to a different driver.

## Who runs it

The nvml-mock DaemonSet's main container at startup, not an init container and
not a human. `entrypoint.sh` execs `setup.sh`, which invokes the binary inside
an `IMEX_MOCK_CHANNELS` guard. The chart gates the whole block on
`imex.mockChannels.enabled` and feeds the majors in as environment variables,
which `setup.sh` reads and passes on as flags.

The binary is built into the nvml-mock image at
`/usr/local/bin/render-imex-procdevices`. A human debugging a node can run it by
hand against any source and output pair; nothing in the code assumes a cluster.

## Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--source` | `/proc/devices` | path to the real `/proc/devices` to derive from |
| `--output` | none, required | path to write the rendered file; a missing value exits 1 |
| `--imex-major` | `235` | device major to advertise for `nvidia-caps-imex-channels` |
| `--caps-major` | `236` | device major to advertise for `nvidia-caps` |

`render-imex-procdevices` reads no environment variables. `IMEX_MOCK_CHANNELS`,
`IMEX_CHANNEL_MAJOR` and `IMEX_CAPS_MAJOR` are consumed by `setup.sh`, which
translates them into these flags.

## Usage

What `setup.sh` runs inside the DaemonSet container when `IMEX_MOCK_CHANNELS` is
`true`:

```bash
render-imex-procdevices \
    --source /proc/devices \
    --output /var/lib/nvml-mock/imex/proc-devices \
    --imex-major 235 \
    --caps-major 236
```

The consumer is then pointed at the file, on a DRA driver release that supports
it:

```text
--set altProcDevices=/var/lib/nvml-mock/imex/proc-devices
--set resources.computeDomains.enabled=true
```

## See also

- [Tools index](README.md)
- [Compute Domain demo](../demo/compute-domain/README.md)
- [Helm Chart](../helm-chart.md)
