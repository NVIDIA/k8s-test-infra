# nri-plugin

A containerd [NRI](https://github.com/containerd/nri) plugin that injects the
nvml-mock overlay into containers as they are created, so a workload sees mock
GPUs without being modified. It registers with the runtime over the NRI socket,
subscribes to `CreateContainer` only, and decides per container whether and how
to inject.

When it injects, the adjustment carries:

- a bind mount of the host overlay onto the container overlay path
  (`rbind,ro,nosuid,nodev`), with a narrower writable bind layered over
  `driver/config` inside it, because the container writes back through that
  directory when `nvidia-smi --gpu-reset` clears a device's overrides;
- an `LD_PRELOAD` entry for each shim in `--ld-preload-shims`;
- `NODE_NAME` and `MOCK_TOPOLOGY_CONFIG`, when `--node-name` is set and a
  ComputeDomain topology document is staged in the overlay;
- mock GPU device nodes and IMEX channel nodes, each behind its own pod
  annotation.

Three pod annotations gate the behaviour independently: `--opt-out-annotation`
set to `false` disables injection entirely, `--device-annotation` set to `true`
adds `/dev/nvidia*`, and `--imex-channel-annotation` set to `true` adds
`/dev/nvidia-caps-imex-channels/*`. Namespaces listed in
`--excluded-namespaces` are skipped before any of that runs.

The plugin fails open. A device that cannot be stat'ed is logged and skipped
rather than failing creation of the whole container, and a container the NVIDIA
device plugin already served is left untouched so its allocation is not
widened.

## Who runs it

A DaemonSet of its own, one pod per node, rendered by the chart when
`nri.enabled` is `true` (default `false`). It is separate from the main
nvml-mock DaemonSet, and nothing orders it after the DaemonSet that stages the
overlay tree, which is why the device path degrades instead of failing.

The pod runs as root with `allowPrivilegeEscalation: false` and no service
account token, and mounts three hostPaths: the NRI socket directory
(read-write), the overlay directory (read-only) and the CDI spec directory
(read-only). It talks to containerd over the NRI socket and is not a Kubernetes
API client.

The binary is installed in the nvml-mock image at
`/usr/local/bin/nri-plugin`. The name it registers with the runtime under is a
separate thing: that comes from `--plugin-name`, which the chart sets from
`nri.pluginName` (`nvml-mock`). Only a standalone run falls back to the
compiled-in `mokka-nri-plugin`.

## Device delivery modes

`--device-injection-mode` picks how the device opt-in delivers GPUs:

- `raw` (default) stages device entries built by stat'ing each host node under
  `--device-host-path`. Anything that is not a character device is rejected.
  This is the only mode that works where the runtime has no CDI support.
- `cdi` emits the fully qualified device name from `--cdi-device-name` and lets
  the runtime resolve it. The plugin first checks that `--cdi-spec-host-path`
  exists; a missing spec logs a warning and falls back to raw, because
  containerd fails container creation outright on an unresolvable CDI device.

Any other value is rejected at startup. A typo that silently resolved to `raw`
would look exactly like a working CDI deployment, and the difference is only
visible in the OCI spec of an already running pod.

The CDI vendor is deliberately not `nvidia.com`: that namespace belongs to the
device plugin and the container toolkit, and keeping ours distinct is what
makes "exactly one component emits CDI device references for a container"
observable rather than merely asserted.

## Probes

Because a fail-open plugin fails invisibly, `--health-addr` serves two probes.
An empty value disables them.

`GET /readyz` returns 503 while the plugin is not registered with the runtime,
so every window in which injection has silently stopped shows up as a NotReady
pod. Registration is set when containerd calls `Configure`, and cleared when
the runtime closes the connection.

`GET /healthz` returns 503 only when a `CreateContainer` handler has been in
flight longer than the runtime's own reported request timeout multiplied by
two. The multiplier buys one whole extra timeout before the kubelet is asked to
restart the plugin, so a single slow but completing request cannot cause a
restart. containerd's default request timeout is 2s, which puts the default
wedge threshold at 4s.

Both write the failure reason as the response body, so it is legible in
`kubectl describe pod`.

## Flags

Every flag also reads an environment variable; the flag wins when both are set.

| Flag | Environment variable | Default | Description |
|------|----------------------|---------|-------------|
| `--log-level` | `MOKKA_LOG_LEVEL` | `info` | `debug`, `info`, `warn` or `error`. `warning` is accepted as an alias of `warn`, and an empty value falls back to `info` |
| `--log-format` | `MOKKA_LOG_FORMAT` | `json` | `json` or `plain`. An empty value falls back to `json` |
| `--health-addr` | `MOKKA_NRI_HEALTH_ADDR` | `:8080` | address for the `/healthz` and `/readyz` endpoints; empty disables them |
| `--socket-path` | `MOKKA_NRI_SOCKET_PATH` | `/var/run/nri/nri.sock` | NRI socket path |
| `--plugin-name` | `MOKKA_NRI_PLUGIN_NAME` | `mokka-nri-plugin` | name this plugin registers with the runtime under |
| `--plugin-index` | `MOKKA_NRI_PLUGIN_INDEX` | `10` | order against other registered plugins; later indices adjust a container after earlier ones |
| `--excluded-namespaces` | `MOKKA_NRI_EXCLUDED_NAMESPACES` | `kube-system` | comma-separated namespaces to skip; an empty value excludes nothing |
| `--opt-out-annotation` | `MOKKA_NRI_OPT_OUT_ANNOTATION` | `nvml-mock.nvidia.com/inject` | pod annotation key; value `false` disables injection |
| `--overlay-host-path` | `MOKKA_NRI_OVERLAY_HOST_PATH` | `/var/lib/nvml-mock` | host path for the nvml-mock overlay |
| `--overlay-mount-path` | `MOKKA_NRI_OVERLAY_MOUNT_PATH` | `/opt/nvml-mock` | container path for the nvml-mock overlay |
| `--ld-preload-shims` | `MOKKA_NRI_LD_PRELOAD_SHIMS` | the four `libibmockumad`, `libibmockverbs`, `libibmocksys` and `libpcisysfs` shims under `driver/usr/local/lib` | comma-separated shim paths relative to the overlay mount, or absolute paths. Preload order is the list order, so a symbol defined by more than one resolves to the first |
| `--node-name` | `NODE_NAME` | empty | Kubernetes node name; enables ComputeDomain topology injection when a topology document is staged in the overlay |
| `--topology-host-path` | `MOKKA_NRI_TOPOLOGY_HOST_PATH` | `<overlay-host-path>/topology/topology.yaml` | host path checked for the staged topology document |
| `--topology-mount-path` | `MOKKA_NRI_TOPOLOGY_MOUNT_PATH` | `<overlay-mount-path>/topology/topology.yaml` | container path injected as `MOCK_TOPOLOGY_CONFIG` |
| `--device-annotation` | `MOKKA_NRI_DEVICE_ANNOTATION` | `nvml-mock.nvidia.com/devices` | pod annotation key; value `true` adds `/dev/nvidia*` device nodes |
| `--device-host-path` | `MOKKA_NRI_DEVICE_HOST_PATH` | `<overlay-host-path>/driver/dev` | host path containing mock `/dev/nvidia*` nodes |
| `--device-injection-mode` | `MOKKA_NRI_DEVICE_INJECTION_MODE` | `raw` | `raw` (device nodes) or `cdi` (CDI device reference) |
| `--cdi-device-name` | `MOKKA_NRI_CDI_DEVICE_NAME` | `nvml-mock.nvidia.com/gpu=all` | fully qualified CDI device injected in `cdi` mode |
| `--cdi-spec-host-path` | `MOKKA_NRI_CDI_SPEC_HOST_PATH` | `/var/run/cdi/nvml-mock-nri.yaml` | staged CDI spec checked before a CDI reference is emitted; a missing spec falls back to raw injection |
| `--imex-channel-annotation` | `MOKKA_NRI_IMEX_CHANNEL_ANNOTATION` | `nvml-mock.nvidia.com/imex-channels` | pod annotation key; value `true` adds `/dev/nvidia-caps-imex-channels/*` nodes |
| `--imex-channel-host-path` | `MOKKA_NRI_IMEX_CHANNEL_HOST_PATH` | `<overlay-host-path>/driver/dev/nvidia-caps-imex-channels` | host path containing the mock IMEX channel nodes staged by `imex.mockChannels` |

The three paths shown as `<overlay-...>` derivations are resolved against
whatever the overlay flags ended up being, not against the packaged defaults.
An error out of the plugin is printed as `nri-plugin: <error>` on stderr and
exits `1`.

## Usage

The chart renders this command line, with the release namespace prepended to
the excluded namespaces and `NODE_NAME` supplied through the downward API:

```text
/usr/local/bin/nri-plugin \
  --socket-path=/var/run/nri/nri.sock \
  --plugin-name=nvml-mock \
  --plugin-index=10 \
  --overlay-host-path=/var/lib/nvml-mock \
  --overlay-mount-path=/opt/nvml-mock \
  --device-host-path=/var/lib/nvml-mock/driver/dev \
  --opt-out-annotation=nvml-mock.nvidia.com/inject \
  --device-annotation=nvml-mock.nvidia.com/devices \
  --device-injection-mode=raw \
  --cdi-spec-host-path=/var/run/cdi/nvml-mock-nri.yaml \
  --imex-channel-annotation=nvml-mock.nvidia.com/imex-channels \
  --imex-channel-host-path=/var/lib/nvml-mock/driver/dev/nvidia-caps-imex-channels \
  --excluded-namespaces=<release-namespace>,kube-system \
  --node-name=$(NODE_NAME) \
  --health-addr=:8080 \
  --log-level=info \
  --log-format=json
```

`--cdi-device-name`, the two topology flags and `--ld-preload-shims` are not
templated, so a deployed plugin runs them at their compiled-in defaults.

The opt-in a workload author writes:

```yaml
metadata:
  annotations:
    nvml-mock.nvidia.com/devices: "true"
    nvml-mock.nvidia.com/imex-channels: "true"
```

Probing it by hand from a debug pod:

```bash
curl -sS http://<pod-ip>:8080/readyz    # 503 plus a reason while unregistered
curl -sS http://<pod-ip>:8080/healthz   # 503 only when a handler is wedged
```

## See also

- [Components index](README.md)
- [Node-Wide Injection demo](../demo/node-wide-injection/README.md)
- [Helm Chart](../helm-chart.md)
