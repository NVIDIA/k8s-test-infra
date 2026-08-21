# imex-nogpu-shim

A transparent argv wrapper installed at `/usr/bin/nvidia-imex` that runs the
real IMEX daemon with `--nogpu` appended, so it starts on nodes with no GPU.

The upstream compute-domain-daemon hard-codes its daemon command line
(`nvidia-imex -c /imexd/imexd.cfg`) with no flag passthrough, so there is no
supported way to add `--nogpu`. The shim takes that path while the real binary
is moved to `/usr/bin/nvidia-imex.real`.

It builds argv as `[realPath] + caller args + "--nogpu"` and calls
`syscall.Exec`, which replaces the process image: environment, stdio, exit code
and signals all reach the real daemon directly, and no wrapper process lingers.

`--nogpu` is not duplicated if the caller already passed either spelling,
`--nogpu` or `-nogpu`. The check is an exact match, so an argument such as
`--nogpufoo` still gets the flag appended.

This is a temporary measure, to be removed once upstream supports passing extra
IMEX daemon arguments.

## Who runs it

Whatever invokes `nvidia-imex` inside the overlay images. It never appears in a
command line of its own.

`deployments/nvml-mock/Dockerfile.compute-domain-daemon` builds it in a Go stage
and installs it as `/usr/bin/nvidia-imex` in two targets: `daemon`, layered on
the upstream DRA driver image so compute-domain-daemon pods spawn the shimmed
real IMEX, and `demo`, layered on the nvml-mock image for the Tilt and demo
workflow. Both are local-build-only and are never published, because they
repackage the proprietary `nvidia-imex` binary.

## Flags

The shim defines no flags and never calls `flag.Parse`. Every argument is
forwarded verbatim to the real binary.

| Argument | Behaviour |
|----------|-----------|
| anything the caller passes | forwarded unchanged, in order |
| `--nogpu` | appended, unless the caller already passed `--nogpu` or `-nogpu` |

### Environment

| Variable | Default | Description |
|----------|---------|-------------|
| `IMEX_SHIM_REAL_BIN` | `/usr/bin/nvidia-imex.real` | path of the real `nvidia-imex`; a test hook and an escape hatch for non-standard installs |

The rest of the environment is passed through untouched.

Only one failure path exists: if the exec fails, the shim prints
`imex-nogpu-shim: exec <bin>: <err>` to stderr and exits 127, the conventional
command-not-found code.

## Usage

Upstream's hard-coded invocation, unchanged on the caller side:

```bash
nvidia-imex -c /imexd/imexd.cfg
# execs: /usr/bin/nvidia-imex.real -c /imexd/imexd.cfg --nogpu
```

Non-standard install location:

```bash
IMEX_SHIM_REAL_BIN=/opt/imex/nvidia-imex nvidia-imex -c /tmp/imex.cfg
```

## See also

- [Tools index](README.md)
- [fake-imex](fake-imex.md), the deprecated fakes this replaces
- [Compute Domain demo](../demo/compute-domain/README.md)
