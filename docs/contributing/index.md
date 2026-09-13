---
title: Contributing
---

# Contributing

## Prerequisites

- Go matching the version in `go.mod`, with CGo enabled
- A C toolchain — the mock NVML library is built through CGo
- `docker`, `kind`, `tilt`, `helm`, `kubectl`

## The loop

```bash
make cluster-create          # once
tilt up -- --gpu-operator    # leave running; redeploys on save

make lint-fix                # before committing
make test
```

[Local Development](local-development.md) covers the cluster shapes and every
Tilt flag. [Testing](testing.md) covers the gates.

## Where things live

```text
cmd/            binaries: node-agent, nri-plugin, nvml-mock-ctl,
                control-plane, check-fabric, generate-bridge
internal/       node daemon and its simulators, NRI plugin, control-plane API
pkg/gpu/        the mock NVML library, mock CUDA, allocation watcher
shims/          LD_PRELOAD and execve shims
deployments/    Helm charts, Dockerfiles, GPU profiles
local/          Tiltfiles, Kind configs, local Helm values
tests/          e2e suite, chart tests, integration tests
enhancements/   Mokka Enhancement Proposals
docs/           this site
```

The [architecture overview](../architecture.md) explains how those fit together
at runtime; each component has its own page.

## Common changes

### Adding an NVML function

1. Implement the method on `ConfigurableDevice` in
   `pkg/gpu/mocknvml/engine/device.go`, returning `ERROR_NOT_SUPPORTED` when
   the profile does not configure the value.
2. Add any new configuration fields to `engine/config_types.go`.
3. Add the C-exported wrapper to the matching file under
   `pkg/gpu/mocknvml/bridge/`.
4. Run `make gen`. The generator scans for `//export` directives and drops the
   stub it had been generating for that function.
5. Test it.

The generator is driven by NVML's own header, so a hand-written implementation
always replaces its stub — you never delete one by hand. See
[Libraries and Shims](../components/libraries-and-shims.md) for how the layers fit.

### Adding a GPU profile

Profiles are YAML. The deployed ones live in
`deployments/nvml-mock/helm/nvml-mock/profiles/` and are the source of truth;
`pkg/gpu/mocknvml/configs/` holds standalone equivalents for running the library
outside Kubernetes.

Add the file, then deploy it with `tilt up -- --gpu-profile <name>` and check
`nvidia-smi` reports what you intended. [Configuration](../configuration.md)
documents every field.

!!! note "Profiles have a drift guard"
    `tests/e2e/go/profile` asserts derived expectations across **all** known
    profiles regardless of which one you are running, so a malformed or
    inconsistent profile fails the unit tests rather than a cluster run.

### Debugging

```bash
MOCK_NVML_DEBUG=1 nvidia-smi     # trace every NVML call the mock answers
```

## Style

- Simple, idiomatic Go. High cohesion, low coupling.
- Errors are wrapped with context, handled, or logged — never silently
  dropped. If ignoring one is correct, say why in a comment.
- Comments explain intent where it is not obvious: *why*, not *what*. Write
  them for someone reading the file in a year, not for the current review.
- `testify/require` for assertions; `t.Parallel()` where the test allows it.

## Related

| To read about | See |
|---|---|
| Bringing up a cluster | [Local Development](local-development.md) |
| Running the gates | [Testing](testing.md) |
| Reading NVLink fabric state off a node | [check-fabric](../tools/check-fabric.md) |
| Regenerating the NVML bridge stubs | [generate-bridge](../tools/generate-bridge.md) |
| Proposing a design | [Enhancement Proposals](enhancements.md) |
| Submitting the change | [Pull Requests](pull-requests.md) |
