# fake-fabricmanager (daemon and ctl)

`cmd/fake-fabricmanager/` holds a stand-in for `nv-fabricmanager` on NVSwitch
platforms (HGX H100, GB200, GB300) and a matching readiness query. On real
hardware the fabric manager registers the GPUs with the NVSwitch fabric before
they are usable; here the daemon writes a node-local readiness marker and the
mock NVML engine reads the same file.

That coupling is the point: a profile with `device_defaults.fabric.state: auto`
reports `IN_PROGRESS` until the daemon is up and `COMPLETED` afterwards, which
mirrors how a real fabric manager gates GPU readiness. Unlike the IMEX fakes,
readiness here is node-local: one marker, no peer set.

Both binaries resolve the state directory the same way:

| Variable | Default | Description |
|----------|---------|-------------|
| `MOCK_FABRICMANAGER_STATE_DIR` | `/var/lib/nvml-mock/fabric-state` | directory holding `fabricmanager.ready` |

The chart sets it when fabricmanager is enabled, and `setup.sh` propagates it
into CDI-injected workload containers so the mock library inside user pods sees
the same gate.

## fake-fabricmanager-daemon

Installed as `/usr/bin/nv-fabricmanager`. It optionally sleeps a simulated
registration delay, creates the empty marker
`<MOCK_FABRICMANAGER_STATE_DIR>/fabricmanager.ready`, then loops, re-asserting
the marker every 2s so it self-heals if something deletes it. SIGTERM or SIGINT
removes the marker, prints `fake-fabricmanager: clean shutdown`, and returns 0.
A failed marker write is fatal (exit 1).

### Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-c` | empty | fabricmanager config file; parsed and discarded |

### Environment

| Variable | Default | Description |
|----------|---------|-------------|
| `MOCK_FABRICMANAGER_STATE_DIR` | `/var/lib/nvml-mock/fabric-state` | where the marker is written |
| `MOCK_FABRICMANAGER_INIT_DELAY_SEC` | unset | integer seconds to delay the marker write; unset, non-numeric or `<= 0` means no delay |

`MOCK_FABRICMANAGER_INIT_DELAY_SEC` is not wired by any chart value or script.
It is an operator and test knob for exercising the `IN_PROGRESS` to `COMPLETED`
transition.

### Who runs it

The nvml-mock DaemonSet's entrypoint script, in the background. Step 11 of
`setup.sh` reads `MOCK_FABRICMANAGER` (`off` or `on`, validated), and when it is
not `off` it clears any stale marker and starts the daemon. A missing binary is
a hard failure rather than a warning, because without the daemon those GPUs sit
at `IN_PROGRESS` forever.

The chart sets `MOCK_FABRICMANAGER` to `on` only when the profile declares
NVSwitches or sets `fabric.state: auto`, unless `fabricmanager.enabled` forces
it. `MOCK_FABRICMANAGER` itself is read by `setup.sh`, never by the binary.

### Usage

```bash
export MOCK_FABRICMANAGER_STATE_DIR=/var/lib/nvml-mock/fabric-state
mkdir -p "$MOCK_FABRICMANAGER_STATE_DIR"
rm -f "$MOCK_FABRICMANAGER_STATE_DIR/fabricmanager.ready"   # clear a stale marker
/usr/bin/nv-fabricmanager &
```

Simulate 10s of fabric registration latency:

```bash
MOCK_FABRICMANAGER_INIT_DELAY_SEC=10 /usr/bin/nv-fabricmanager
```

## fake-fabricmanager-ctl

Installed as `/usr/bin/nv-fabricmanager-ctl`. A one-shot probe: with `-q` it
stats the marker and prints `READY\n` on exit 0 when it exists. It prints no
deprecation notice, unlike the IMEX ctl.

### Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-c` | empty | fabricmanager config file; parsed and discarded |
| `-q` | `false` | query readiness; the only supported mode |

### Exit codes

| Code | Meaning |
|------|---------|
| `0` | marker present; `READY` on stdout |
| `1` | marker absent (fabric still `IN_PROGRESS`) |
| `2` | `-q` was not passed |

It reports marker presence only. It cannot tell a running daemon from a stale
marker left behind by a killed one.

### Who runs it

The Go e2e suite and humans debugging a mock NVSwitch node. The fabric gate in
the suite reads `MOCK_FABRICMANAGER` off the deployed DaemonSet and, only when
it is `on`, polls the ctl inside the pod until it reports READY. Callers run
that gate before asserting NV# topology, so the real ordering (fabric ready,
then NVLink) is preserved. Nothing in the chart wires it as a Kubernetes probe.

### Usage

```bash
kubectl exec -n <namespace> <nvml-mock-pod> -- \
  sh -c '/usr/bin/nv-fabricmanager-ctl -q 2>/dev/null || true'
```

## See also

- [Tools index](README.md)
- [check-fabric](check-fabric.md)
- [Configuration](../configuration.md)
