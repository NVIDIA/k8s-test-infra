# mokka-kubelet-helper

Prepares Mokka-owned kubelet state before the node starts kubelet. The node
agent installs this helper and its systemd service when
[`nodeAgent.numa.enabled`](../helm-chart.md#numa-filesystem-simulation)
is enabled. Systemd runs it on node boot; normal NUMA setup and restoration run
in the privileged DaemonSet pod.

## Command

```text
mokka-kubelet-helper [--host-root /] prestart
```

| Option | Default | Purpose |
|---|---|---|
| `--host-root` | `/` | Node filesystem root. The service uses the node's own root; an alternate root supports isolated tests. |

The node agent stages the statically linked executable at
`/var/lib/nvml-mock/kubelet/mokka-kubelet-helper`. Its systemd service runs:

```text
/var/lib/nvml-mock/kubelet/mokka-kubelet-helper prestart
```

## Boot reset

The node agent registers boot cleanup with an empty marker file at
`/var/lib/nvml-mock/kubelet/boot-reset.enabled`. If this file exists, the helper
removes `cpu_manager_state` and `memory_manager_state` from
`/var/lib/kubelet` before kubelet starts. It records completion in
`/run/mokka/kubelet-reset.done`, so later kubelet restarts in the same node boot
keep their allocation state. Without the registration marker, the helper leaves
kubelet state unchanged.

The helper returns an error if the marker cannot be inspected or checkpoint
cleanup fails. Failed cleanup leaves completion unmarked so a later attempt can retry.

For the mount lifecycle and disable procedure, see
[NUMA filesystem simulation](../helm-chart.md#numa-filesystem-simulation).
