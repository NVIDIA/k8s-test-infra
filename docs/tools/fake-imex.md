# fake-imex (daemon and ctl)

**Deprecated.** Both binaries are slated for removal. The ComputeDomain
simulation now runs the real `nvidia-imex` daemon in NO GPU mode through
[`imex-nogpu-shim`](imex-nogpu-shim.md). Each fake prints a deprecation notice
at runtime.

`cmd/fake-imex/` holds two drop-in stand-ins for the upstream IMEX binaries.
They coordinate through marker files on a shared hostPath: the daemon writes one
file named after its own pod IP, and the ctl reports READY when a marker exists
for every peer.

Marker semantics are presence-only. A SIGKILL, an OOM kill or a node crash
leaves a stale marker behind, and the ctl keeps reporting READY.

Both read the same two variables:

| Variable | Default | Description |
|----------|---------|-------------|
| `IMEX_STATE_DIR` | `/var/lib/nvml-mock/imex-state` | shared marker directory |
| `IMEX_NODES_CONFIG` | `/imexd/nodes.cfg` | peer list written by the upstream compute-domain-daemon |

A malformed peer entry in `nodes.cfg` is rejected rather than stat'ed, so a
crafted entry cannot walk a marker path out of the state directory.

## fake-imex-daemon

Installed as `/usr/bin/nvidia-imex`. It requires `POD_IP`, writes an empty
marker at `<IMEX_STATE_DIR>/<POD_IP>`, then loops forever. Every 2s it
re-asserts its own marker (so it self-heals if something deletes it) and logs
the peer count parsed out of `nodes.cfg`. SIGUSR1 re-reads `nodes.cfg` and logs
the peer count, matching the upstream daemon's DNS-update signal. SIGTERM or
SIGINT removes the marker, prints `fake-imex: clean shutdown`, and returns 0.

A missing `POD_IP` or an unwritable marker is fatal (exit 1).

### Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-c` | empty | imexd config file; parsed and discarded |

`-c` exists only so callers do not have to special-case the mock. The daemon
never reads `imexd.cfg`. Nothing in this repo starts it: it is baked into the
image at the path the upstream compute-domain-daemon looks for on `$PATH`, and
that pod spawns it with its own hard-coded argv.

### Environment

| Variable | Required | Description |
|----------|----------|-------------|
| `POD_IP` | yes | the pod's own IP, normally from the downward API; empty is fatal |

### Usage

```bash
export IMEX_STATE_DIR=/var/lib/nvml-mock/imex-state
export IMEX_NODES_CONFIG=/imexd/nodes.cfg
export POD_IP=10.244.1.7

nvidia-imex -c /imexd/imexd.cfg
```

## fake-imex-ctl

Installed as `/usr/bin/nvidia-imex-ctl`. It supports only the upstream
`-c <config> -q` invocation. It reads the peer list from `nodes.cfg`, adds the
local `POD_IP` to the expected set, stats one marker per expected peer, and
prints exactly `READY\n` with exit 0 when all of them exist.

Including `POD_IP` is load-bearing: without it a dead local daemon with live
peers would still report READY.

The deprecation notice goes to stderr on failure paths only. The upstream
readiness probe compares the combined output against exactly `READY\n`, so the
success path stays silent on both streams.

### Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-c` | empty | imexd config file; parsed and discarded |
| `-q` | `false` | query readiness; the only supported mode |

### Exit codes

| Code | Meaning |
|------|---------|
| `0` | every expected peer has a marker; `READY` on stdout |
| `1` | `nodes.cfg` could not be read, or a peer's marker is missing (named on stderr) |
| `2` | `-q` was not passed |

An unreadable `nodes.cfg` is deliberately NOT-READY rather than READY, so the
controller cannot mark a domain ready before its members start. An empty peer
list with an empty `POD_IP` counts as READY, which is the single-node clique
case.

### Who runs it

The kubelet, indirectly: the upstream compute-domain-daemon's readiness probe
shells out to `nvidia-imex-ctl -c <cfg> -q`. It is also runnable by hand through
`kubectl exec` when debugging a KIND ComputeDomain simulation.

### Usage

```bash
nvidia-imex-ctl -c /imexd/imexd.cfg -q
```

## See also

- [Tools index](README.md)
- [imex-nogpu-shim](imex-nogpu-shim.md)
- [Compute Domain demo](../demo/compute-domain/README.md)
