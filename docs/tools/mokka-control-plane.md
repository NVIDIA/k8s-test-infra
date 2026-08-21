# mokka-control-plane

Entry point for the Mokka Control Plane described in MEP-0001. The current
slice serves two HTTP endpoints, `GET /healthz` and `GET /readyz`. Both return
`200` with the body `ok` and `Content-Type: text/plain; charset=utf-8`. sGPU
inventory, node-agent heartbeats and runtime-policy fan-out land on the same
binary in follow-up work.

## Who runs it

A Deployment rendered by the Helm chart when `controlPlane.enabled` is `true`
(default `false`). The container image is built from
`deployments/mokka-control-plane/Dockerfile` on
`gcr.io/distroless/static-debian12:nonroot`, exposes port 8080, and uses
`/readyz` as its readiness probe and `/healthz` as its liveness probe. The pod
mounts no service account token, because this slice makes no calls to the
Kubernetes API.

For local work, `tilt up -- --control-plane` builds the image and installs the
chart with `controlPlane.enabled=true`.

## Flags

Every flag also reads an environment variable. A flag given on the command line
wins over the variable.

| Flag | Environment variable | Default | Description |
|------|----------------------|---------|-------------|
| `--listen-addr` | `MOKKA_CP_LISTEN_ADDR` | `:8080` | address for the HTTP server, e.g. `:8080` |
| `--log-level` | `MOKKA_CP_LOG_LEVEL` | `info` | `debug`, `info`, `warn` or `error` |
| `--shutdown-timeout` | `MOKKA_CP_SHUTDOWN_TIMEOUT` | `5s` | maximum time to wait for in-flight requests to drain on SIGINT/SIGTERM |

An unrecognized `--log-level` is a startup error rather than a silent fallback
to `info`, so a typo in a Helm value fails the pod instead of running it at the
wrong verbosity. Logs are JSON on stdout.

The HTTP server's `ReadHeaderTimeout` is fixed at 5s and has no flag.

## Usage

Run it against the source tree and hit both probes:

```bash
go run ./cmd/mokka-control-plane --listen-addr :9090 --log-level debug

curl -s localhost:9090/healthz   # ok
curl -s localhost:9090/readyz    # ok
```

The chart renders this command line, taking the port from
`controlPlane.service.port` and the level from `controlPlane.logLevel`:

```text
/usr/local/bin/mokka-control-plane --listen-addr=:8080 --log-level=info
```

`--shutdown-timeout` is not templated, so deployed pods drain with the 5s
default. `controlPlane.terminationGracePeriodSeconds` (30s by default) has to
cover the 10s preStop sleep plus that drain; the kubelet sends SIGKILL at the
grace boundary regardless.

## See also

- [Tools index](README.md)
- [Helm Chart](../helm-chart.md)
