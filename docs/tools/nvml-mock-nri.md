# nvml-mock-nri

A containerd [NRI](https://github.com/containerd/nri) plugin that injects the
nvml-mock overlay into containers as they are created. It registers with the
runtime over the NRI socket, subscribes to `CreateContainer` only, and for each
container decides whether and how to inject.

When it injects, the adjustment carries:

- a read-only bind mount of the host overlay onto the container overlay path
  (`rbind,ro,nosuid,nodev`);
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

The plugin is fail-open. A device that cannot be stat'ed is logged and skipped
rather than failing creation of the whole container, and a container the NVIDIA
device plugin already served is left untouched so its allocation is not widened.

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

The binary is built into the nvml-mock image at
`/usr/local/bin/nvml-mock-nri`.

## Device delivery modes

`--device-injection-mode` picks how the device opt-in delivers GPUs:

- `raw` (default) stages `api.LinuxDevice` entries built by stat'ing each host
  node under `--device-host-path`. Anything that is not a character device is
  rejected. This is the only mode that works where the runtime has no CDI
  support.
- `cdi` emits the fully qualified device name from `--cdi-device-name` and lets
  the runtime resolve it. The plugin first checks that `--cdi-spec-host-path`
  exists; a missing spec logs a warning and falls back to raw, because
  containerd fails container creation outright on an unresolvable CDI device.

Any other value is rejected at startup with a fatal error. A typo that silently
resolved to `raw` would look exactly like a working CDI deployment, and the
difference is only visible in the OCI spec of an already-running pod.

## Health endpoints

Because a fail-open plugin fails invisibly, `--health-addr` serves two probes.

`GET /readyz` returns 503 while the plugin is not registered with the runtime,
so every window in which injection has silently stopped shows up as a NotReady
pod. `GET /healthz` returns 503 only when a `CreateContainer` handler has been
in flight longer than the runtime's own reported request timeout multiplied by
two; containerd's default request timeout is 2s, so the default wedge threshold
is 4s. Both write the failure reason as the response body, so it is legible in
`kubectl describe pod`.

The listener is bound before the plugin starts serving, so a port clash fails
startup instead of leaving a process with no probes that reads healthy forever.

## Flags

Every flag also reads an environment variable; the flag wins when both are set,
and an empty variable is ignored.

| Flag | Environment variable | Default | Description |
|------|----------------------|---------|-------------|
| `--socket-path` | `NRI_SOCKET_PATH` | `/var/run/nri/nri.sock` | NRI socket path |
| `--health-addr` | `NVML_MOCK_HEALTH_ADDR` | `:8080` | address for `/healthz` and `/readyz`; empty disables them |
| `--plugin-name` | `NRI_PLUGIN_NAME` | `nvml-mock` | NRI plugin name |
| `--plugin-index` | `NRI_PLUGIN_INDEX` | `10` | NRI plugin index |
| `--overlay-host-path` | `NVML_MOCK_OVERLAY_HOST_PATH` | `/var/lib/nvml-mock` | host path for the nvml-mock overlay |
| `--overlay-mount-path` | `NVML_MOCK_OVERLAY_MOUNT_PATH` | `/opt/nvml-mock` | container path for the overlay |
| `--node-name` | `NODE_NAME` | empty | Kubernetes node name; enables ComputeDomain topology injection when a topology document is staged |
| `--topology-host-path` | `NVML_MOCK_TOPOLOGY_HOST_PATH` | `<overlay-host-path>/topology/topology.yaml` | host path checked for the staged topology document |
| `--topology-mount-path` | `NVML_MOCK_TOPOLOGY_MOUNT_PATH` | `<overlay-mount-path>/topology/topology.yaml` | container path injected as `MOCK_TOPOLOGY_CONFIG` |
| `--device-host-path` | `NVML_MOCK_DEVICE_HOST_PATH` | `/var/lib/nvml-mock/driver/dev` | host path containing mock `/dev/nvidia*` nodes |
| `--device-injection-mode` | `NVML_MOCK_DEVICE_INJECTION_MODE` | `raw` | `raw` (device nodes) or `cdi` (CDI device reference) |
| `--cdi-device-name` | `NVML_MOCK_CDI_DEVICE_NAME` | `nvml-mock.nvidia.com/gpu=all` | fully qualified CDI device injected in `cdi` mode |
| `--cdi-spec-host-path` | `NVML_MOCK_CDI_SPEC_HOST_PATH` | `/var/run/cdi/nvml-mock-nri.yaml` | staged CDI spec checked before a reference is emitted |
| `--opt-out-annotation` | `NVML_MOCK_OPT_OUT_ANNOTATION` | `nvml-mock.nvidia.com/inject` | pod annotation key; value `false` disables injection |
| `--device-annotation` | `NVML_MOCK_DEVICE_ANNOTATION` | `nvml-mock.nvidia.com/devices` | pod annotation key; value `true` adds `/dev/nvidia*` |
| `--imex-channel-annotation` | `NVML_MOCK_IMEX_CHANNEL_ANNOTATION` | `nvml-mock.nvidia.com/imex-channels` | pod annotation key; value `true` adds IMEX channel nodes |
| `--imex-channel-host-path` | `NVML_MOCK_IMEX_CHANNEL_HOST_PATH` | `<overlay-host-path>/driver/dev/nvidia-caps-imex-channels` | host path containing the staged IMEX channel nodes |
| `--excluded-namespaces` | `NVML_MOCK_EXCLUDED_NAMESPACES` | `kube-system` | comma-separated namespaces to skip |
| `--ld-preload-shims` | `NVML_MOCK_LD_PRELOAD_SHIMS` | the four `libib*`/`libpcimocksys` shims under `driver/usr/local/lib` | comma-separated shim paths, relative to the overlay mount or absolute |

The CDI device vendor is deliberately not `nvidia.com`: that namespace belongs
to the device plugin and the container toolkit, and keeping ours distinct is
what makes "exactly one component emits CDI device references for a container"
observable.

## Usage

The chart renders this command line, with the release namespace prepended to
the excluded namespaces and `NODE_NAME` supplied through the downward API:

```text
/usr/local/bin/nvml-mock-nri \
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
  --health-addr=:8080
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

- [Tools index](README.md)
- [Node-Wide Injection demo](../demo/node-wide-injection/README.md)
- [Helm Chart](../helm-chart.md)
