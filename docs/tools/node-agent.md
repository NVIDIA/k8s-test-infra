# node-agent

Compiles a mock NVML profile into on-host state and keeps it there. It is the
process the nvml-mock DaemonSet runs: a single `start` command that watches the
profile, fans every change out to the simulators, and serves the two probes the
kubelet reads.

Eight simulators run under it, each owning one slice of the mock driver tree:
`gpudriver`, `pcibus`, `cdi`, `imex`, `nvlink`, `fabricmanager`, `ib` and
`kernellog`. All but the last write below `--host-root`, so the tree a workload
later sees is entirely a function of that flag plus the profile; `kernellog`
writes no tree at all, announcing injected Xids on the node's kernel log.

## Reconcile contract

`--config` and `--topology` are polled every 5s rather than watched, because a
ConfigMap update swaps the atomic `..data` symlink and a watch on the file
itself would pin the replaced inode. The agent emits the current state once on
startup and then only when the bytes of either document change.

Each state change runs one reconcile:

1. `Stage` on all eight simulators concurrently. A failing simulator does not
   cancel its siblings; every error is collected and the rest of the reconcile
   is skipped, because the later steps read what `Stage` wrote.
2. The daemons (`fabricmanager`, `ib` and `kernellog`) start on that barrier,
   once per process lifetime. Later states reach an already running daemon
   through `Reload`, so a profile edit does not need a pod restart.
3. `Apply` on the three simulators that publish artifacts off-node
   (`gpudriver`, `pcibus`, `cdi`). This wave fails fast: the CDI spec refers to
   device nodes `gpudriver` must have staged first.

A read error on either document logs a warning and keeps the cached state, so a
briefly unreadable ConfigMap does not tear the node's mock GPUs down. A
`--topology` path that is unset, or that names a file which does not exist, is
read as "this cluster declares no topology" rather than as an error; any other
read failure is an error, because a nil document retracts the topology already
staged on the node.

On SIGINT or SIGTERM the agent runs `Revoke` then `Discard` on a fresh context
bounded by `--shutdown-timeout`, so teardown still runs after the run context
is already cancelled.

Before the first reconcile, the runtime overrides that `nvml-mock-ctl` writes
are cleared, at both locations the mock NVML engine resolves:
`<host-root>/var/lib/nvml-mock/config/overrides.yaml` and
`<host-root>/var/lib/nvml-mock/driver/config/overrides.yaml`. A pod restart is
therefore the documented way back to the pristine profile.

Afterwards the second of those two documents is polled every second, by
`kernellog` alone: an Xid that appears on it is announced on `--kernel-log`, the
way a driver's printk announces a fault. Every other simulator takes its input
from the profile, and none of them reads the overrides — they are the runtime
surface, hot-reloaded by the engine inside each NVML consumer.

Each fault is announced once, not for as long as it stands. Clearing a fault
prints nothing, as it does for a real driver, and re-injecting it prints again —
unless the clear and the re-injection both land between two reads, in which case
the document that comes back is the one already announced and the repeat is
silent. The kernel log then holds one line where a driver would have printed
two; the fault it reports, and the absence of any retraction, are the same
either way.

## Probes

`--health-addr` serves both; an empty value disables them.

| Endpoint | Passes when |
|----------|-------------|
| `GET /healthz` | the last `Stage` wave completed without error. A failure names the simulators that failed, and it recovers on the next successful wave. |
| `GET /readyz` | every simulator reports ready. The response attributes the result per simulator, so a red probe names the one that is not serving. |

## Flags

All flags belong to the `start` subcommand. Each also reads one environment
variable; the flag wins when both are set.

| Flag | Environment variable | Default | Description |
|------|----------------------|---------|-------------|
| `--config` | `MOKKA_AGENT_CONFIG` | none | path to the mock-nvml YAML profile. Required: an unset value fails startup with `--config is required` |
| `--topology` | `MOKKA_AGENT_TOPOLOGY` | empty | path to the cluster ComputeDomain topology document; unset where the cluster declares no topology |
| `--host-root` | `MOKKA_AGENT_HOST_ROOT` | `/host` | where the host filesystem is mounted in the agent's namespace |
| `--health-addr` | `MOKKA_AGENT_HEALTH_ADDR` | `:9090` | address for `/healthz` and `/readyz` |
| `--shutdown-timeout` | `MOKKA_AGENT_SHUTDOWN_TIMEOUT` | `30s` | maximum time to wait for simulators to revoke and discard on SIGINT/SIGTERM |
| `--log-level` | `MOKKA_LOG_LEVEL` | `info` | `debug`, `info`, `warn` or `error`. `warning` is accepted as an alias of `warn`, and an empty value falls back to `info` |
| `--log-format` | `MOKKA_LOG_FORMAT` | `json` | `json` or `plain`. An empty value falls back to `json` |
| `--ib-mode` | `MOCK_IB` | `off` | InfiniBand simulation tier: `off`, `sysfs` (render only) or `full` (adds the mock-ib daemon). An empty value reads as `off` |
| `--ib-fabric` | `MOCK_IB_PING_FABRIC` | `false` | enable the cross-pod fabric relay; required for multi-node `ibping` and `iblinkinfo` |
| `--ib-fabric-port` | `MOCK_IB_PING_PORT` | `18515` | TCP port for the cross-pod mock-ib fabric relay |
| `--fabricmanager-init-delay` | `MOCK_FABRICMANAGER_INIT_DELAY` | `0` | withhold fabric readiness for this long, simulating NVSwitch registration latency |
| `--kernel-log` | `MOCK_NVML_KMSG` | `/dev/kmsg` | kernel log to announce injected Xids on, as a driver's printk does. Empty announces nowhere, which is how the chart states that the container was not given a writable one — it sets this empty unless `nodeAgent.kernelLog.enabled` asks for the device and the privilege writing it takes. Not rooted at `--host-root`: the deployment grants the device at its own path |

An unrecognized `--log-level`, `--log-format` or `--ib-mode` is a startup error
rather than a silent fallback, so a typo in a Helm value fails the pod instead
of running it in the wrong mode. Any error out of `start` is printed as
`node-agent: <error>` on stderr and exits `1`.

Some inputs have no flag in front of them at all: `GPU_COUNT`,
`DRIVER_VERSION`, `NODE_NAME`, `HOSTNAME`, `MOCK_FABRICMANAGER_STATE_DIR` and
`IMEX_MOCK_CHANNELS` (with the `IMEX_CHANNEL_MAJOR`, `IMEX_CAPS_MAJOR` and
`IMEX_CHANNEL_COUNT` values it gates). They are read where the profile is
compiled into state, not by any simulator, which is why no `--flag` shadows
them. The chart sets them; see [Configuration](../configuration.md).

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

The rendered `--shutdown-timeout` is `nodeAgent.shutdownTimeout`, 5s by
default, not the 30s the binary compiles in: a default chart install gives
teardown 5s. The 30s in the table above is what a bare `node-agent start` uses.

The InfiniBand and fabricmanager flags are not templated. The chart drives
those simulators through their environment variables instead (`MOCK_IB`,
`MOCK_IB_PING_FABRIC`, `MOCK_IB_PING_PORT`,
`MOCK_FABRICMANAGER_INIT_DELAY`), which is the same knob reaching the same
field.

The binary is installed in the nvml-mock image at
`/usr/local/bin/node-agent`.

## See also

- [Components index](README.md)
- [Configuration](../configuration.md)
- [Runtime Control](../nvml-mock-ctl.md)
- [Helm Chart](../helm-chart.md)
