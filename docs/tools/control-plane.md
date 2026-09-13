# control-plane

The entry point for the Mokka Control Plane. What ships today is a health
surface and nothing more: `GET /healthz` and `GET /readyz`, both returning
`{"ok":true}` as JSON. It makes no calls to the Kubernetes API, and no other
Mokka component depends on it.

!!! warning "Mostly still a proposal"

    [MEP-0001](https://github.com/NVIDIA/k8s-test-infra/tree/main/enhancements/meps/0001-mokka-control-plane)
    designs this binary as the cluster-wide component behind sGPU inventory,
    node daemon heartbeats and runtime-policy fan-out. None of that is built.
    Treat the MEP as a design document, not a description of the binary.

It is off by default: the chart renders a Deployment only when
`controlPlane.enabled` is `true`.

## Flags

Every flag also reads an environment variable, and the flag wins when both are
set.

| Flag | Environment variable | Default | Description |
|------|----------------------|---------|-------------|
| `--listen-addr` | `MOKKA_CP_LISTEN_ADDR` | `:8080` | Address for the HTTP server |
| `--log-level` | `MOKKA_LOG_LEVEL` | `info` | `debug`, `info`, `warn` or `error`. `warning` is an alias of `warn`; empty falls back to `info` |
| `--log-format` | `MOKKA_LOG_FORMAT` | `json` | `json` or `plain`; empty falls back to `json` |
| `--shutdown-timeout` | `MOKKA_CP_SHUTDOWN_TIMEOUT` | `5s` | Budget for in-flight requests to drain on SIGINT/SIGTERM |

An unrecognized `--log-level` or `--log-format` fails startup rather than
falling back silently, so a typo in a Helm value stops the pod instead of
running it at the wrong verbosity or emitting logs nothing can parse.

## Usage

```bash
go run ./cmd/control-plane --listen-addr :9090 --log-level debug

curl -s localhost:9090/healthz   # {"ok":true}
curl -s localhost:9090/readyz    # {"ok":true}
```

The chart templates every flag, so a running pod never falls back to a
compiled-in default:

```text
/usr/local/bin/control-plane --listen-addr=:8080 --log-level=info --log-format=json --shutdown-timeout=5s
```

Those come from `controlPlane.service.port`, `controlPlane.logging.level`,
`controlPlane.logging.format` and `controlPlane.shutdownTimeout`.

!!! note "Give the drain room inside the grace period"

    Shutdown is a 10s preStop sleep — which keeps the endpoint out of Service
    rotation before the process stops — followed by the drain. Both have to fit
    inside `controlPlane.terminationGracePeriodSeconds`, 30s by default,
    because the kubelet sends SIGKILL at that boundary whatever is still in
    flight.

## Deployment

The image builds from `deployments/control-plane/Dockerfile` on
`gcr.io/distroless/static-debian12:nonroot`, exposes port 8080, and uses
`/readyz` and `/healthz` as its readiness and liveness probes. The pod mounts no
service account token.

For local work, `tilt up -- --control-plane` builds the image and installs the
chart with `controlPlane.enabled=true`.

## See also

- [Command-line tools](README.md)
- [Installation](../helm-chart.md) — every `controlPlane` value
