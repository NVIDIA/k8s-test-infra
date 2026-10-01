# control-plane

The binary that runs the [Mokka controller](../mokka-controller.md) and serves
HTTP health probes. It reconciles declarative simulated GPU (sGPU) inventory
through the Kubernetes API. The architecture page covers placement, Node
projection, leader election and the [Stage 1 exclusions](../mokka-controller.md#stage-1-exclusions);
this page describes the command line and probes.

It is off by default: the chart renders a Deployment only when
`controlPlane.enabled` is `true`.

## Kubernetes prerequisites

The controller requires Kubernetes API access; there is no health-only or
offline mode. Chart installations require Kubernetes 1.30+ for stable
`ValidatingAdmissionPolicy`. Install the Mokka CustomResourceDefinitions (CRDs)
before starting the controller; see [controller installation](../mokka-controller.md#install).

In a cluster, the binary uses in-cluster configuration and a mounted
ServiceAccount token. The chart provisions the controller's ServiceAccount and
role-based access control (RBAC) permissions. Outside a cluster, pass
`--kubeconfig` or set `MOKKA_CP_KUBECONFIG` to a kubeconfig file. Leaving that
path empty uses in-cluster configuration, not `KUBECONFIG` or the default
`~/.kube/config`.

For a local process against a chart-managed installation, the kubeconfig must
authenticate as the chart's controller ServiceAccount. Its admission policy
restricts `SGPURack` creation and updates to that identity; RBAC permissions
alone are not enough. Use the same leader-election namespace and Lease name as
the deployed replicas so that only one process reconciles at a time. The Lease
namespace must exist and the identity must have permission to get, create and
update Leases there. The chart's [RBAC rules](https://github.com/NVIDIA/k8s-test-infra/blob/main/deployments/nvml-mock/helm/nvml-mock/templates/controlplane/rbac.yaml)
define the required Node and Mokka resource permissions.

## Flags

Flags with an environment variable listed below read it as a fallback; the
flag wins when both are set. A dash means there is no environment-variable
binding for that flag.

| Flag | Environment variable | Default | Description |
|------|----------------------|---------|-------------|
| `--feature-gates` | `MOKKA_FEATURE_GATES` | empty | Comma-separated startup overrides. See [Feature Gates](../contributing/feature-gates.md) |
| `--listen-addr` | `MOKKA_CP_LISTEN_ADDR` | `:8080` | Address for the HTTP probe server |
| `--log-level` | `MOKKA_LOG_LEVEL` | `info` | `debug`, `info`, `warn` or `error`. `warning` is an alias of `warn`; empty falls back to `info` |
| `--log-format` | `MOKKA_LOG_FORMAT` | `json` | `json` or `plain`; empty falls back to `json` |
| `--shutdown-timeout` | `MOKKA_CP_SHUTDOWN_TIMEOUT` | `5s` | Budget for in-flight HTTP requests to drain on SIGINT/SIGTERM |
| `--kubeconfig` | `MOKKA_CP_KUBECONFIG` | empty | Path to a kubeconfig; empty uses in-cluster configuration |
| `--leader-election-namespace` | `MOKKA_CP_LEADER_ELECTION_NAMESPACE` | `POD_NAMESPACE`, or `default` if empty | Namespace containing the leader-election Lease |
| `--leader-election-name` | `MOKKA_CP_LEADER_ELECTION_NAME` | `control-plane.mokka.nvidia.com` | Name of the leader-election Lease |
| `--leader-election-lease-duration` | — | `15s` | Lease duration before another replica can take over |
| `--leader-election-renew-deadline` | — | `10s` | How long the leader retries renewing the Lease before giving up leadership |
| `--leader-election-retry-period` | — | `2s` | Interval between leader-election attempts |
| `--workers` | — | `2` | Workers per controller queue |
| `--status-debounce` | — | `100ms` | Quiet period before an aggregate status update |
| `--status-progress-interval` | — | `1s` | Maximum aggregate status staleness during continuous changes; zero uses the larger of `1s` and the debounce interval |
| `--live-node-get-timeout` | `MOKKA_CP_LIVE_NODE_GET_TIMEOUT` | `2s` | Timeout for an exact Node GET after it leaves the filtered cache |
| `--kube-api-qps` | — | `50` | Client-side Kubernetes API request rate limit |
| `--kube-api-burst` | — | `100` | Client-side Kubernetes API request burst limit |

An unrecognized `--log-level` or `--log-format` fails startup rather than
falling back silently. Workers, the live Node GET timeout, API QPS and burst
must be positive; status intervals must be non-negative. A nonzero status
progress interval must not be shorter than the debounce interval.
Leader-election durations must satisfy `lease > renew > retry × 1.2` and be
positive.

## HTTP probes

| Endpoint | Response |
|----------|----------|
| `GET /healthz` | HTTP 200 with `{"ok":true}` while the HTTP server is serving; it does not check Kubernetes API access |
| `GET /readyz` | HTTP 200 with `{"ok":true}` when the controller is ready; otherwise HTTP 503 with `{"ok":false,"reason":"controller is not ready"}` |

Readiness follows the [controller's cache and leader-election state](../mokka-controller.md#observe-and-troubleshoot),
not inventory convergence. A healthy HTTP listener alone does not make the
replica ready.

## Usage

For local development from the repository root, first prepare a kubeconfig
satisfying the [Kubernetes prerequisites](#kubernetes-prerequisites). This
example assumes the owning Helm release is in namespace `mokka` and uses the
default Lease name:

```bash
go run ./cmd/control-plane \
  --kubeconfig /path/to/controller.kubeconfig \
  --leader-election-namespace mokka \
  --listen-addr :9090 \
  --log-level debug
```

In another terminal, inspect the probes (readiness may initially return 503):

```bash
curl -i localhost:9090/healthz
curl -i localhost:9090/readyz
```

With default values, the chart renders these arguments. Kubernetes expands
`$(POD_NAMESPACE)` to the pod's namespace:

```text
/usr/local/bin/control-plane \
  --listen-addr=:8080 \
  --log-level=info \
  --log-format=json \
  --shutdown-timeout=5s \
  --leader-election-name=control-plane.mokka.nvidia.com \
  --leader-election-namespace=$(POD_NAMESPACE) \
  --workers=2 \
  --kube-api-qps=50 \
  --kube-api-burst=100
```

The chart takes these values from `controlPlane.service.port`,
`controlPlane.logging.level`, `controlPlane.logging.format`,
`controlPlane.shutdownTimeout`, `controlPlane.leaderElection.name`,
`controlPlane.workers`, `controlPlane.kubeAPIQPS` and
`controlPlane.kubeAPIBurst`. Other flags retain the binary defaults.

!!! note "Give the drain room inside the grace period"

    Shutdown is a 10s preStop sleep — which keeps the endpoint out of Service
    rotation before the process stops — followed by the drain. Both have to fit
    inside `controlPlane.terminationGracePeriodSeconds`, 30s by default,
    because the kubelet sends SIGKILL at that boundary whatever is still in
    flight. `--shutdown-timeout` bounds HTTP draining, not controller cleanup;
    follow the [safe disable and uninstall procedure](../mokka-controller.md#disable-or-uninstall-safely)
    before removing the controller.

## Deployment

The image builds from `deployments/control-plane/Dockerfile` on
`gcr.io/distroless/static-debian12:nonroot`, exposes port 8080, and uses
`/readyz` and `/healthz` as its readiness and liveness probes.

For local work, `tilt up -- --control-plane` builds the image and installs the
CRDs and chart with `controlPlane.enabled=true`.

## See also

- [Command-line tools](README.md)
- [Mokka controller](../mokka-controller.md) — architecture and lifecycle
- [Installation](../helm-chart.md) — the nvml-mock chart
