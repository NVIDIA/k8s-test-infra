# mock-ib

Renders a fake InfiniBand sysfs tree from an nvml-mock profile and serves the
`libibmockumad` / `libibmockverbs` backend over a Unix socket. It is what makes
`ibstat`, `ibping`, `sminfo`, `iblinkinfo` and `ibnetdiscover` answer on a node
with no HCA: the shims are `LD_PRELOAD`ed into every process in the pod and
forward UMAD and verbs traffic to this daemon.

## Modes

`main()` runs the modes in this order:

1. **Render.** Whenever `-config` is non-empty, the profile YAML is read and the
   fake sysfs tree is written under `-ib-root` using `-gpu-count` and
   `-node-name`. A profile with `infiniband.enabled: false` renders nothing. Any
   render error is fatal.
2. **Dry run.** `-dry-run` validates the config and exits 0 without writing.
   It lives inside the `-config` branch, so `-dry-run` on its own does nothing
   and the process continues to the daemon.
3. **Render only.** `-render-only` returns before any server is built, so no
   socket is bound. This is the fail-fast mode the DaemonSet uses.
4. **Serve.** Scans `-ib-root`, creates the socket directory, removes any stale
   socket, listens, and chmods the socket to 0666. With `-fabric` it also starts
   the TCP listener on `-port`. Shutdown is on SIGINT or SIGTERM.

A fifth one-shot path, `-register-peers`, builds the server and writes a
REGISTER frame to each peer instead of listening. Peers come from
`MOCK_IB_PEERS`, or, when that is empty, from the pod IPs resolved from
`MOCK_IB_PING_SERVICE_HOST`.

## Who runs it

The nvml-mock DaemonSet's container, through `setup.sh`. Step 9 runs the
render-only pass synchronously so a bad profile fails the pod under `set -e`,
then backgrounds `/scripts/start-mock-ib.sh`, which runs the serving daemon for
the pod's lifetime and tees its output to `/tmp/mock-ib.log`.

Both are gated on the `MOCK_IB` tier variable (`off`, `sysfs` or `full`), which
is read by the shell scripts, not by the binary: the daemon starts only at
`full`. The e2e suite and the ibping demo also call the binary directly through
`kubectl exec` for the one-shot `-register-peers` step.

The binary is built into the nvml-mock image at `/usr/local/bin/mock-ib`.

## Flags

Flags are single-dash, as invoked. Each default falls back to an environment
variable first; an unparseable `-port` or `-fabric` value silently falls back to
the compiled-in default.

| Flag | Environment variable | Default | Description |
|------|----------------------|---------|-------------|
| `-socket` | `MOCK_IB_PING_SOCKET` | `/run/mock-ib.sock` | Unix socket path |
| `-ib-root` | `MOCK_IB_ROOT` | `/var/lib/nvml-mock/ib` | sysfs tree that the shims redirect into |
| `-port` | `MOCK_IB_PING_PORT` | `18515` | TCP fabric port |
| `-fabric` | `MOCK_IB_PING_FABRIC` | `false` | enable the TCP fabric relay; only `1` or `true` count as true |
| `-register-peers` | none | `false` | register local ports with the peers and exit |
| `-config` | `MOCK_IB_CONFIG` | empty | profile YAML; empty skips rendering entirely |
| `-gpu-count` | `GPU_COUNT` | `0` | GPU count for the HCA layout when `hca_count` is unset |
| `-node-name` | `NODE_NAME` | empty | node name for per-node GUID and LID |
| `-render-only` | none | `false` | render sysfs from `-config` and exit |
| `-dry-run` | none | `false` | validate `-config` and exit without writing |

Additional variables read outside flag defaults:

| Variable | Used for |
|----------|----------|
| `MOCK_IB_PEERS` | comma-separated peer IP list for `-register-peers` |
| `MOCK_IB_PING_SERVICE_HOST` | headless Service resolved to peer pod IPs when `MOCK_IB_PEERS` is empty |
| `POD_IP`, `MOCK_IB_POD_IP` | self IP in the REGISTER frame; falls back to the first non-loopback IPv4 address, then `127.0.0.1` |

## Usage

Render only, the way `setup.sh` runs it inside the DaemonSet container:

```bash
/usr/local/bin/mock-ib \
  -config /etc/nvml-mock/config.yaml \
  -gpu-count "$GPU_COUNT" \
  -node-name "$NODE_NAME" \
  -ib-root /host/var/lib/nvml-mock/ib \
  -render-only
```

Long-running daemon, the argv `/scripts/start-mock-ib.sh` assembles (it appends
`-fabric` only when `MOCK_IB_PING_FABRIC=1`):

```bash
/usr/local/bin/mock-ib \
  -config /etc/nvml-mock/config.yaml \
  -gpu-count 8 -node-name "$NODE_NAME" \
  -socket /var/lib/nvml-mock/run/mock-ib.sock \
  -ib-root /var/lib/nvml-mock/ib \
  -port 18515 -fabric
```

Validate a profile locally without writing anything:

```bash
go run ./cmd/mock-ib -config <profile>.yaml -ib-root /tmp/ib -dry-run
```

## See also

- [Tools index](README.md)
- [Configuration](../configuration.md)
- [Helm Chart](../helm-chart.md)
