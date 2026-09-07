# Shims

A shim makes an unmodified tool read Mokka's simulated files instead of the
host's real ones, without the tool knowing.

The [node daemon](node-daemon.md) stages a fake device tree under
`/var/lib/nvml-mock`, but `lspci` and `ibstat` look at `/sys/bus/pci` and
`/sys/class/infiniband`. Telling those tools to look elsewhere is not an option:
most take no flag for it, and a run against a substitute path would no longer
test that the tool reads the path it reads in production. The shims close that
gap by rewriting the lookup underneath the tool.

## When a shim works, and when it does not

Two shims are `LD_PRELOAD` libraries. They interpose on the libc wrappers a
program calls to touch the filesystem — `open`, `stat`, `readlink`, `opendir`
and friends — and splice a prefix onto any path that matches.

That only works for programs that go through libc.

!!! warning "Go binaries bypass `LD_PRELOAD` entirely"
    Go makes syscalls directly rather than through libc, so a preloaded library
    is never consulted. Most of the Kubernetes control plane is Go, which is why
    Mokka also bind-mounts the staged tree at the real paths. The shims serve
    the C tools; the mounts serve everything else.

Both libraries are no-ops unless their environment variable is set, so a process
that inherits them without the corresponding configuration sees the real host.

## The three shims

| Shim | Kind | Makes this work |
|---|---|---|
| `libpcisysfs` | `LD_PRELOAD` | `lspci` and topology-aware schedulers see mock GPU BDFs |
| `libibmock` | `LD_PRELOAD` | `ibstat`, `ibstatus`, `iblinkinfo`, `ibv_devinfo` see mock InfiniBand HCAs |
| `nvidia-imex-shim` | `execve` wrapper | `nvidia-imex` starts on a machine with no GPU |

### libpcisysfs

Builds `libpcisysfs.so`. Redirects lookups under `/sys/bus/pci`,
`/sys/bus/pci/devices` and `/sys/devices/pci` into the tree named by
`MOCK_PCI_ROOT`, and is a no-op when that variable is unset.

### libibmock

Builds three libraries, which is why the InfiniBand mock can be turned on in
degrees rather than all at once:

| Library | Covers |
|---|---|
| `libibmocksys.so` | sysfs and device-node path redirection |
| `libibmockumad.so` | the UMAD management interface |
| `libibmockverbs.so` | libibverbs |

`MOCK_IB` selects how much of the fabric is simulated. The chart exposes it as
`infiniband.mockTier`:

| `MOCK_IB` | Effect |
|---|---|
| `full` | Path redirection plus UMAD and verbs, backed by the `mock-ib` daemon |
| `sysfs` | Path redirection only — tools enumerate devices but cannot open a verbs context |
| `off`, unset, or unrecognised | Every shim becomes a true no-op and the process sees the real host |

Matching is case-insensitive, and there is no separate disable flag: leaving
`MOCK_IB` unset is how you turn the shims off. Redirection targets
`MOCK_IB_ROOT`, which the chart points at `/var/lib/nvml-mock/ib`.

The libraries target glibc 2.36 and later. Older `__xstat`-family symbols are
also intercepted so the shim still behaves on earlier libc versions.

### nvidia-imex-shim

Not an `LD_PRELOAD` library — a small Go program installed at
`/usr/bin/nvidia-imex`, with the real daemon moved to
`/usr/bin/nvidia-imex.real`.

The upstream compute-domain daemon hard-codes its command line with no flag
passthrough, so `--nogpu` cannot be injected from outside. The shim appends it
and `exec`s the real binary, preserving arguments, environment and stdio.
Because it `exec`s rather than forks, no wrapper process lingers and signals
reach the daemon directly.

This one is meant to disappear: it exists only until upstream supports passing
extra arguments ([#304](https://github.com/NVIDIA/k8s-test-infra/issues/304)).

## How they reach a process

The chart preloads all four libraries on the node daemon, most specific first:

```text
LD_PRELOAD=/usr/local/lib/libibmockumad.so.1:/usr/local/lib/libibmockverbs.so.1:\
/usr/local/lib/libibmocksys.so.1:/usr/local/lib/libpcisysfs.so.1
```

Workload containers receive them through the same CDI injection that delivers
the rest of the mock, so a container that gets mock GPUs also gets the shims
that make the C tools agree with them.

## Building and testing

```bash
make mockpcisysfs-shim     # build libpcisysfs.so
make test-mockpcisysfs     # integration tests against real C test binaries
```

`libpcisysfs` ships test binaries under `testbin/` that exercise the fortified
`open` and `fopen` variants, because glibc's `_FORTIFY_SOURCE` rewrites those
call sites and a shim that only hooks the plain symbols would silently miss
them.

## Related

| To read about | See |
|---|---|
| How the whole system fits together | [Architecture](../architecture.md) |
| What stages the tree the shims redirect into | [Node Daemon](node-daemon.md) |
| Turning InfiniBand mocking on and off | [Installation](../helm-chart.md#infiniband-mocking) |
