# node-agent

The process the nvml-mock DaemonSet runs. A single `start` command compiles a
GPU profile into on-host state, keeps the node converged as that profile
changes, and serves the two probes the kubelet reads.

[Node Daemon](../components/node-daemon.md) covers what it does on the node —
the simulators it drives, the reconcile waves and teardown. This page is the
command line.

## Flags

All flags belong to the `start` subcommand. Each also reads one environment
variable; the flag wins when both are set.

| Flag | Environment variable | Default | Description |
|------|----------------------|---------|-------------|
| `--config` | `MOKKA_AGENT_CONFIG` | none | Path to the mock NVML YAML profile. Required — an unset value fails startup with `--config is required` |
| `--topology` | `MOKKA_AGENT_TOPOLOGY` | empty | Path to the cluster ComputeDomain topology document |
| `--host-root` | `MOKKA_AGENT_HOST_ROOT` | `/host` | Where the host filesystem is mounted in this process's namespace |
| `--health-addr` | `MOKKA_AGENT_HEALTH_ADDR` | `:9090` | Address for `/healthz` and `/readyz`; empty disables both |
| `--shutdown-timeout` | `MOKKA_AGENT_SHUTDOWN_TIMEOUT` | `30s` | Budget for teardown on SIGINT/SIGTERM |
| `--log-level` | `MOKKA_LOG_LEVEL` | `info` | `debug`, `info`, `warn` or `error`. `warning` is an alias of `warn`; empty falls back to `info` |
| `--log-format` | `MOKKA_LOG_FORMAT` | `json` | `json` or `plain`; empty falls back to `json` |
| `--ib-mode` | `MOCK_IB` | `off` | InfiniBand tier: `off`, `sysfs` (render only) or `full` (adds the mock-ib daemon). Empty reads as `off` |
| `--ib-fabric` | `MOCK_IB_PING_FABRIC` | `false` | Cross-pod fabric relay; required for multi-node `ibping` and `iblinkinfo` |
| `--ib-fabric-port` | `MOCK_IB_PING_PORT` | `18515` | TCP port for that relay |
| `--fabricmanager-init-delay` | `MOCK_FABRICMANAGER_INIT_DELAY` | `0` | Withhold fabric readiness for this long, simulating NVSwitch registration latency |
| `--kernel-log` | `MOCK_NVML_KMSG` | `/dev/kmsg` | Kernel log to announce injected Xids on, the way a driver's printk does. Empty announces nowhere, which is what the chart sets unless `nodeAgent.kernelLog.enabled` grants the device. Not rooted at `--host-root` |

An unrecognized `--log-level`, `--log-format` or `--ib-mode` fails startup
rather than falling back silently, so a typo in a Helm value stops the pod
instead of running it in the wrong mode. Any error out of `start` prints as
`node-agent: <error>` on stderr and exits `1`.

### Inputs with no flag

`GPU_COUNT`, `DRIVER_VERSION`, `NODE_NAME`, `HOSTNAME`,
`MOCK_FABRICMANAGER_STATE_DIR` and `IMEX_MOCK_CHANNELS` (with the
`IMEX_CHANNEL_MAJOR`, `IMEX_CAPS_MAJOR` and `IMEX_CHANNEL_COUNT` values it
gates) are read where the profile is compiled into state rather than by any
simulator, so no flag shadows them. The chart sets them — see
[Configuration](../configuration.md).

## Behaviour the flag list does not show

- `--config` and `--topology` are re-read every 5s. State is emitted once at
  startup and then only when the bytes of either document change.
- An unset `--topology`, or one naming a file that does not exist, reads as
  *this cluster declares no topology*. Any other read failure is an error,
  because a nil document would retract the topology already staged on the node.
- A read error on either document keeps the cached state, so a briefly
  unreadable ConfigMap does not tear the node's mock GPUs down.
- Before the first reconcile, the runtime overrides `nvml-mock-ctl` writes are
  cleared at both locations the mock NVML engine resolves:
  `<host-root>/var/lib/nvml-mock/config/overrides.yaml` and
  `<host-root>/var/lib/nvml-mock/driver/config/overrides.yaml`. Restarting the
  pod is therefore the way back to the pristine profile.

## Usage

Against the source tree, with a profile from the repo:

```bash
go run ./cmd/node-agent start \
  --config ./deployments/nvml-mock/helm/nvml-mock/profiles/a100.yaml \
  --host-root /tmp/mokka-host \
  --health-addr :9091
```

The chart renders this command line into the nvml-mock DaemonSet, dropping
`--topology` when `topology.enabled` is false:

```text
/usr/local/bin/node-agent start \
  --config=/etc/nvml-mock/config.yaml \
  --topology=/etc/nvml-mock/topology/topology.yaml \
  --host-root=/host \
  --health-addr=:9091 \
  --log-level=info \
  --log-format=json \
  --shutdown-timeout=5s
```

Two of those differ from the binary's own defaults, so a chart install does not
behave like a bare `node-agent start`: the probes listen on `:9091`, and
teardown gets `nodeAgent.shutdownTimeout` — 5s by default — rather than 30s.

The InfiniBand and fabricmanager flags are not templated. The chart drives those
simulators through `MOCK_IB`, `MOCK_IB_PING_FABRIC`, `MOCK_IB_PING_PORT` and
`MOCK_FABRICMANAGER_INIT_DELAY`, which reach the same fields.

The binary is installed in the nvml-mock image at `/usr/local/bin/node-agent`.

## See also

- [Command-line tools](README.md)
- [Node Daemon](../components/node-daemon.md) — what it does on the node
- [Configuration](../configuration.md) — every profile field it compiles
- [Runtime Control](../nvml-mock-ctl.md) — changing state without a restart
