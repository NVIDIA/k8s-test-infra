# nri-plugin

The binary behind the NRI DaemonSet. It registers with containerd over the
[NRI](https://github.com/containerd/nri) socket, subscribes to
`CreateContainer` only, and edits containers as they are created so an
unmodified workload sees mock GPUs.

[NRI Plugin](../components/nri-plugin.md) covers what it decides and why — the
two injection layers, when a container is left alone, how it composes with the
NVIDIA device plugin, and why it fails open. This page is the command line.

It is off by default: the chart renders the DaemonSet only when `nri.enabled`
is `true`. That DaemonSet is separate from the main nvml-mock one. Its pod runs
as root with `allowPrivilegeEscalation: false` and no service account token,
and mounts three hostPaths — the NRI socket directory read-write, and the
overlay and CDI spec directories read-only.

## Probes

A plugin that fails open fails invisibly, so `--health-addr` serves two probes.
An empty value disables them.

`GET /readyz` returns `503` while the plugin is not registered with the runtime,
which turns every window in which injection has silently stopped into a NotReady
pod. Registration is set when containerd calls `Configure` and cleared when the
runtime closes the connection.

`GET /healthz` returns `503` only when a `CreateContainer` handler has been in
flight for longer than **twice** the request timeout the runtime reported at
registration. The multiplier buys one whole extra timeout before the kubelet is
asked to restart the plugin, so a single slow but completing request cannot
cause a restart. Where the runtime reports no timeout, the NRI client library's
2s fallback applies, putting the threshold at 4s.

Both write the failure reason as the response body, so it is legible in
`kubectl describe pod`.

## Flags

Every flag also reads an environment variable; the flag wins when both are set.

| Flag | Environment variable | Default | Description |
|------|----------------------|---------|-------------|
| `--log-level` | `MOKKA_LOG_LEVEL` | `info` | `debug`, `info`, `warn` or `error`. `warning` is an alias of `warn`; empty falls back to `info` |
| `--log-format` | `MOKKA_LOG_FORMAT` | `json` | `json` or `plain`; empty falls back to `json` |
| `--health-addr` | `MOKKA_NRI_HEALTH_ADDR` | `:8080` | Address for `/healthz` and `/readyz`; empty disables them |
| `--socket-path` | `MOKKA_NRI_SOCKET_PATH` | `/var/run/nri/nri.sock` | NRI socket path |
| `--plugin-name` | `MOKKA_NRI_PLUGIN_NAME` | `mokka-nri-plugin` | Name this plugin registers with the runtime under |
| `--plugin-index` | `MOKKA_NRI_PLUGIN_INDEX` | `10` | Order against other registered plugins; later indices adjust a container after earlier ones |
| `--excluded-namespaces` | `MOKKA_NRI_EXCLUDED_NAMESPACES` | `kube-system` | Comma-separated namespaces to skip; empty excludes nothing |
| `--opt-out-annotation` | `MOKKA_NRI_OPT_OUT_ANNOTATION` | `nvml-mock.nvidia.com/inject` | Pod annotation key; value `false` disables injection |
| `--overlay-host-path` | `MOKKA_NRI_OVERLAY_HOST_PATH` | `/var/lib/nvml-mock` | Host path for the overlay |
| `--overlay-mount-path` | `MOKKA_NRI_OVERLAY_MOUNT_PATH` | `/opt/nvml-mock` | Container path for the overlay |
| `--ld-preload-shims` | `MOKKA_NRI_LD_PRELOAD_SHIMS` | `libibmockumad`, `libibmockverbs`, `libibmocksys` and `libpcisysfs` under `driver/usr/local/lib` | Comma-separated shim paths, relative to the overlay mount or absolute. Preload order is list order, so a symbol defined by more than one resolves to the first |
| `--node-name` | `NODE_NAME` | empty | Enables ComputeDomain topology injection when a topology document is staged in the overlay |
| `--topology-host-path` | `MOKKA_NRI_TOPOLOGY_HOST_PATH` | `<overlay-host-path>/topology/topology.yaml` | Host path checked for the staged topology document |
| `--topology-mount-path` | `MOKKA_NRI_TOPOLOGY_MOUNT_PATH` | `<overlay-mount-path>/topology/topology.yaml` | Container path injected as `MOCK_TOPOLOGY_CONFIG` |
| `--device-annotation` | `MOKKA_NRI_DEVICE_ANNOTATION` | `nvml-mock.nvidia.com/devices` | Pod annotation key; value `true` adds `/dev/nvidia*` nodes |
| `--device-host-path` | `MOKKA_NRI_DEVICE_HOST_PATH` | `<overlay-host-path>/driver/dev` | Host path containing the mock `/dev/nvidia*` nodes |
| `--device-injection-mode` | `MOKKA_NRI_DEVICE_INJECTION_MODE` | `raw` | `raw` (device nodes) or `cdi` (CDI reference). Any other value is rejected at startup |
| `--cdi-device-name` | `MOKKA_NRI_CDI_DEVICE_NAME` | `nvml-mock.nvidia.com/gpu=all` | Fully qualified CDI device injected in `cdi` mode |
| `--cdi-spec-host-path` | `MOKKA_NRI_CDI_SPEC_HOST_PATH` | `/var/run/cdi/nvml-mock-nri.yaml` | Spec checked before a CDI reference is emitted; a missing spec falls back to raw injection |
| `--imex-channel-annotation` | `MOKKA_NRI_IMEX_CHANNEL_ANNOTATION` | `nvml-mock.nvidia.com/imex-channels` | Pod annotation key; value `true` adds `/dev/nvidia-caps-imex-channels/*` nodes |
| `--imex-channel-host-path` | `MOKKA_NRI_IMEX_CHANNEL_HOST_PATH` | `<overlay-host-path>/driver/dev/nvidia-caps-imex-channels` | Host path containing the mock IMEX channel nodes staged by `imex.mockChannels` |

The three `<overlay-...>` derivations resolve against whatever the overlay flags
ended up being, not against the packaged defaults. An error out of the plugin
prints as `nri-plugin: <error>` on stderr and exits `1`.

!!! note "The CDI vendor is deliberately not `nvidia.com`"

    `nvml-mock.nvidia.com` keeps our device references out of the namespace the
    device plugin and container toolkit own, which is what makes "exactly one
    component emits CDI device references for this container" something you can
    observe in the OCI spec rather than have to assume.

!!! note "The registered name is not the binary name"

    `--plugin-name` is what containerd knows the plugin as. The chart sets it
    from `nri.pluginName`, which is `nvml-mock`; the compiled-in
    `mokka-nri-plugin` applies only to a standalone run.

## Usage

The chart renders this command line, prepending the release namespace to the
excluded namespaces and supplying `NODE_NAME` through the downward API:

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

The binary is installed in the nvml-mock image at `/usr/local/bin/nri-plugin`.

## See also

- [Command-line tools](README.md)
- [NRI Plugin](../components/nri-plugin.md) — what it injects, and what it skips
- [Node-Wide Injection](../guides/node-wide-injection/README.md) — a runnable walkthrough
- [Installation](../helm-chart.md) — every `nri` chart value
