# nvml-mock-guest

Runs inside a KubeVirt guest and keeps the guest's mock NVML overrides in step
with the node's, so a change made with [`nvml-mock-ctl`](../nvml-mock-ctl.md)
on the node, such as an injected Xid or a pinned temperature, also shows in
the guest. [Runtime changes in a
guest](../components/nri-plugin.md#runtime-changes-in-a-guest) describes the
path a change takes from the node to the guest.

Every second it reads the live area at the end of the guest's bundle disk and,
when the node's document changed, writes it to the guest's override file. An
empty document removes the file. Each change it applies is logged on stdout:

```text
2026-10-16T09:01:20.063Z  info  applied host overrides  {"sha256": "09bfcc6a…", "overrides": "/etc/nvml-mock/overrides.yaml"}
```

The SHA-256 is that of the node's `overrides.yaml`, so comparing it with
`sha256sum` on the node shows whether the guest is up to date. A failure, such
as a disk without a live area, is logged once and retried every interval
until it clears. The override file keeps its last applied content meanwhile.

## Usage

```text
nvml-mock-guest [--once] [--disk PATH] [--overrides PATH] [--interval DURATION] [--log-level LEVEL]
```

| Flag | Default | Meaning |
|---|---|---|
| `--disk` | `/dev/disk/by-id/virtio-nvml-mock-guest` | Bundle disk whose live area carries the node's overrides |
| `--overrides` | `/etc/nvml-mock/overrides.yaml` | Override file the guest's mock library reads |
| `--interval` | `1s` | How often to read the live area |
| `--once` | off | Apply the current overrides and exit; exits `1` when that fails |
| `--log-level` | `info` | `debug`, `info`, `warn` or `error` |

The disk path assumes the VM attaches the bundle with `serial: nvml-mock-guest`,
as the [KubeVirt scenario](https://github.com/NVIDIA/k8s-test-infra/blob/main/local/kubevirt/README.md)
does.

## Where it comes from

It is built into the nvml-mock image at `/usr/local/bin/nvml-mock-guest` as a
static binary, because it runs on whatever Linux the guest boots. The NRI
plugin copies it into every guest bundle with a systemd unit,
`nvml-mock-guest.service`. The unit runs a `--once` pass first, so
`systemctl enable --now nvml-mock-guest` returns after the guest has the node's
current overrides, then keeps the program running and sends its log to the
serial console.

## See also

- [Command-line tools](README.md)
- [KubeVirt launchers](../components/nri-plugin.md#kubevirt-launchers): what the bundle holds and how a VM declares it
