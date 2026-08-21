# render-pci-sysfs

A one-shot renderer that turns the `pcie_topology:` block of a profile YAML into
a fake PCI sysfs tree, so topology-aware schedulers and `lspci` see the mock
GPUs.

It reads the profile, validates it, and resolves the effective topology. A
profile that declares no `pcie_topology:` block gets a flat single-root layout
covering every entry in `devices:`; `--strict` turns that fallback into a fatal
error instead. A profile with no devices at all prints
`no devices in <config>, nothing to render` to stderr and exits 0.

The tree is written under `<output>/sys/`:

- `sys/bus/pci/devices/<bdf>`, a relative symlink into
  `sys/devices/<root-complex-id>/<bdf>`, matching what the kernel emits so any
  `readlink()` consumer resolves the same canonical path;
- `numa_node` per device, carrying the root complex's NUMA node;
- the PCI identity attribute files (`vendor`, `device`, `class`, `config`, and
  the rest) that let `lspci` enumerate the mocks.

BDFs are lowercased on the way out, because libpciaccess and `lspci` compare
path strings literally. Rendering is idempotent: existing directories are
reused, files are truncated and rewritten, and symlinks are removed and
recreated so a stale relative target cannot linger.

Argument handling differs from the other renderers: a missing `--config` or
`--output` prints a one-line usage string and exits 2, while every other failure
prints `render-pci-sysfs: <error>` to stderr and exits 1.

## Who runs it

The nvml-mock DaemonSet's main container at startup, as step 10 of `setup.sh`,
guarded on the binary being executable and run with only `--config` and
`--output`. Failures are fatal because `setup.sh` runs under `set -e`, which is
deliberate: a topology typo would otherwise produce silently malformed sysfs
that downstream `dra.k8s.io/pcieRoot` attributes inherit.

The binary is built into the nvml-mock image at
`/usr/local/bin/render-pci-sysfs`. Its output is what the e2e suite and the
standalone demo assert against: the symlink count, the relative target and
`numa_node` under `/var/lib/nvml-mock/sys/bus/pci/devices`.

A developer can also run it against a checked-in profile to validate a
`pcie_topology:` block, which is what `--strict` and `--dry-run` exist for.
Neither is used by `setup.sh`, the chart or any demo script.

## Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--config` | none, required | path to the profile YAML |
| `--output` | none, required | fake-root directory; the tree is written under `<output>/sys/...` |
| `--strict` | `false` | fail if the profile does not declare `pcie_topology:` |
| `--dry-run` | `false` | validate the config and exit without writing files |

`--strict` is checked after the topology is resolved, so a profile with zero
devices returns early and never trips it.

`render-pci-sysfs` reads no environment variables.

## Usage

What `setup.sh` runs inside the DaemonSet container. `HOST` is the mounted node
root, so the tree lands at `/var/lib/nvml-mock/sys/...` on the node:

```bash
/usr/local/bin/render-pci-sysfs \
    --config /etc/nvml-mock/config.yaml \
    --output /host/var/lib/nvml-mock
```

Validate a profile's topology block without writing anything, and require the
block to be explicit:

```bash
render-pci-sysfs \
    --config deployments/nvml-mock/helm/nvml-mock/profiles/gb200.yaml \
    --output /tmp/fakeroot --strict --dry-run
```

What the rendered tree looks like to a consumer:

```bash
readlink /var/lib/nvml-mock/sys/bus/pci/devices/0000:07:00.0
# ../../../devices/pci0000:00/0000:07:00.0
```

## See also

- [Tools index](README.md)
- [Configuration](../configuration.md)
- [Helm Chart](../helm-chart.md)
