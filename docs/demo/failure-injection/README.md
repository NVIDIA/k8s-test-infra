# nvml-mock Failure-Injection Demo

End-to-end walkthrough of the GPU failure-injection feature on the cluster
your current context points at. Unlike [`../standalone/`](../standalone/README.md), this
demo exercises every supported failure mode (`healthy` →
`ecc_uncorrectable` → `lost` → `fallen_off_bus`) by re-deploying the
chart between scenarios with `helm upgrade --reuse-values`, and
asserts each mode's expected behaviour against `nvidia-smi` output
inside the pod.

With `BUILD_LOCAL=true` the demo gets its own cluster
(`nvml-mock-failure-demo`), which is the only configuration that fully
isolates it from the standalone demo.

On the default path it shares whatever cluster you point it at, so it installs
a release called `nvml-mock-failure` into a namespace of its own,
`mokka-failure` (override with `NAMESPACE=...`), both distinct from the
standalone demo's `nvml-mock` in `mokka`. The release name is what does the
real work here, for the reason below; the namespace just keeps the two demos'
namespaced objects from interleaving.

> **Do not point `NAMESPACE=` at the standalone demo's namespace.** The demo
> refuses to install if the other demo's release is already there, and exits
> `4`. That refusal exists because **the two demos share per-node host state
> whatever namespace they use.** Both DaemonSets mount the same hostPaths,
> `/var/lib/nvml-mock`, `/var/run/cdi`, `/run/nvidia` and the NFD features
> directory, and none of those is scoped by release or by namespace.
>
> Measured on a live cluster: after a co-located run, the single shared
> `/var/lib/nvml-mock/driver/config/config.yaml`, which the CDI spec at
> `/var/run/cdi/nvidia.yaml` points `MOCK_NVML_CONFIG` at, was left reading
> `failure: {after_calls: 1, mode: fallen_off_bus, code: 79}`.
>
> The two demos' own pods do not notice, because each mounts its own
> ConfigMap. **Any real GPU workload scheduled on those nodes does**: it
> silently consumes the failure-injected config, which is precisely what this
> mock exists to provide honestly. Both demos exit `0` while that happens,
> which is why the refusal is a hard stop rather than a warning.
>
> `DEMO_ASSUME_YES=true` overrides it. If you use that, treat the affected
> nodes as dirty until one of the demos is reinstalled on its own.

**A namespace alone cannot separate the two demos, so the release name does
the work.** The chart creates a ClusterRole and a ClusterRoleBinding named
after the release (`templates/rbac.yaml`), and those are cluster-scoped: with
both demos installing a release called `nvml-mock`, the second one fails
outright, before creating anything, with

```
Error: unable to continue with install: ClusterRole "nvml-mock" in namespace ""
exists and cannot be imported into the current release: invalid ownership
metadata; annotation validation error: key "meta.helm.sh/release-namespace"
must equal "mokka-failure": current value is "mokka"
```

That is why this demo's release is **`nvml-mock-failure`**, not `nvml-mock`.
The namespace split is still needed, but it is the release name that keeps the
cluster-scoped objects apart.

**Even with both, this is not isolation.** The chart's hostPath mounts are
fixed and release-independent (`/var/lib/nvml-mock`, `/var/run/cdi`,
`/run/nvidia`, and the NFD features directory), and it ships `nodeSelector: {}`
with `tolerations: [{operator: Exists}]`, so both DaemonSets land on every node
and write the same per-node host state whatever they are called. Do not run
this demo and the standalone demo against the same cluster at the same time.

The Kind topology itself is the shared
[`../kind.yaml`](../kind.yaml) (1 control-plane + 3 workers with
FGO-style labels) — failure injection doesn't need its own topology.

## Prerequisites

- **A Kubernetes cluster and a valid `KUBECONFIG`.** This demo installs into
  whatever cluster your current context points at. Check yours with
  `kubectl config current-context`.
- **Helm 3.6 or newer.** The demo installs the chart from this checkout, not
  from a registry, so 3.6 is the floor: the chart's `_helpers.tpl` uses a
  multi-line `dict` that only the Go 1.16 template parser in Helm 3.6 accepts.
  On 3.5 and older, rendering fails with `unclosed action`. Install it from the
  official docs: <https://helm.sh/docs/intro/install/>
- `kubectl`, matching your cluster version.

> **No cluster yet?** The [quick start](../../quickstart.md) creates a
> throwaway one with Kind in about a minute, then come back here.

Docker and Kind are needed only for `BUILD_LOCAL=true`, which builds the image
from source and side-loads it into a Kind cluster named
`nvml-mock-failure-demo`, creating that cluster if it is missing. The default
path pulls the published image and never creates or deletes a cluster.

### First run on a cold cluster is slow

The image is about 100 MB. On a cluster that has never pulled it, a measured
cold pull took **roughly 8 minutes per node**, so the demo waits up to **15
minutes** rather than the 2 it used to. It prints a notice before installing;
a long silence there is the pull, not a hang.

A first install pulls on all nodes at once on its own, because a fresh
DaemonSet creates every pod immediately. The demo also sets
`updateStrategy.rollingUpdate.maxUnavailable=100%`, which matters on **re-runs**:
the chart's 25% default turns a rolling update on an existing release back
into one node at a time.

Raise the budget on a slow link:

```bash
HELM_TIMEOUT=30m ./run.sh
```

Note that `ghcr.io/nvidia/nvml-mock:latest` is a floating tag, so a cluster
holding an older cached `:latest` can run a build that does not match this
chart. Pin `NVML_MOCK_IMAGE` to a released tag if you need a fixed pairing.

## What the script does

1. Announces the target: context, API server, node count, and the namespace
   the workload lands in. Unless that target is a throwaway Kind cluster (a
   `kind-*` context whose API server is loopback, since the name alone proves
   nothing), it asks you to type the context name back before installing
   anything. The chart runs a privileged DaemonSet with hostPath mounts, so
   nothing is installed until the target is confirmed.

   It **fails closed** when nobody can answer: a non-interactive run against
   anything but a throwaway cluster exits `4` rather than installing
   unattended. Set `DEMO_ASSUME_YES=true` to proceed deliberately without a
   terminal.

   Exit codes: `2` no usable cluster, `3` missing or too-old tooling, `4`
   confirmation declined, impossible, or refused because the other demo is
   already installed in the target namespace, `5` a configuration the chart cannot
   express (a digest-pinned `NVML_MOCK_IMAGE`).

   Every subsequent `kubectl` and `helm` call is pinned to both the context
   **and** the namespace it announced.
2. With `BUILD_LOCAL=true` only: creates the `nvml-mock-failure-demo` Kind
   cluster if missing, then builds and loads the `nvml-mock:failure-demo`
   image. Otherwise it uses `ghcr.io/nvidia/nvml-mock:latest` (override with
   `NVML_MOCK_IMAGE`).
3. **Scenario 1 — healthy baseline**. Installs with
   `gpu.failureInjection.enabled=false` and asserts:
   * the rendered ConfigMap has **no** `failure:` block,
   * `nvidia-smi -L` lists at least one GPU. The script captures the
     observed count and reuses it for the subsequent scenarios — the
     in-pod GPU count comes from the chart-rendered ConfigMap (i.e.
     the chosen profile) and is not influenced by `--set gpu.count`,
     which only affects the host-side CDI spec.
   * the aggregate uncorrectable ECC counter starts at `0`.
4. **Scenario 2 — `ecc_uncorrectable` + Xid 79**. Upgrades the
   release with `mode=ecc_uncorrectable, after_calls=1, xid.code=79`,
   recycles the DaemonSet, and asserts:
   * the ConfigMap now contains `mode: ecc_uncorrectable`,
   * `nvidia-smi -L` **still** lists every GPU (mode contract: device
     stays addressable),
   * `nvidia-smi --query-gpu=ecc.errors.uncorrected.aggregate.total
     --format=csv,noheader,nounits` prints a strictly-positive integer
     for at least one GPU. Each device keeps its own call counter, so
     that query (one guarded ECC read per GPU) trips every GPU on its
     first read, inside the one `nvidia-smi` invocation.
5. **Scenario 3 — `lost`**. Upgrades with `mode=lost, after_calls=1`,
   recycles the DaemonSet, and asserts that
   `nvidia-smi --query-gpu=temperature.gpu --format=csv` surfaces an
   error marker (`[N/A]`, `[Unknown Error]`, etc.) instead of a clean
   integer — the first guarded metric call trips the device and every
   subsequent NVML call (metrics, identity getters, handle lookups)
   returns `ERROR_GPU_IS_LOST`.
6. **Scenario 4 — `fallen_off_bus` + Xid 79**. Same NVML surface as
   `lost` but with `xid.code=79` queued for the NVML event set.
   Asserts the same `nvidia-smi` error-marker behaviour and verifies
   the ConfigMap carries both `mode: fallen_off_bus` and
   `code: 79`.

Each scenario uses `helm upgrade --reuse-values` so only the
failure-injection knobs are touched between runs; everything else
(image, profile, count) is preserved from Scenario 1.

## Quick start

```bash
./run.sh
```

The script is idempotent — rerun it as often as you like; the
existing cluster is reused and `helm upgrade --install` covers both
first-time install and follow-up upgrades.

## Caveats

### The Xid event is delivered through the NVML event set, not nvidia-smi

`nvidia-smi` doesn't subscribe to `nvmlEventSetWait`, so it never
prints `Xid 79`. The mock delivers the configured Xid through the
standard NVML event set
(`NVML_EVENT_TYPE_XID_CRITICAL_ERROR`), exactly once per engine
lifetime — matching real NVML semantics. Real consumers see it via:

* the **NVIDIA device plugin** health monitor (marks the GPU
  `Unhealthy`),
* **dcgm-exporter** (`DCGM_FI_DEV_XID_ERRORS` metric),
* a small Go program calling `nvml.EventSetCreate` /
  `RegisterEvents(EventTypeXidCriticalError)` / `EventSetWait` —
  useful for ad-hoc verification:

  ```go
  set, _ := nvml.EventSetCreate()
  dev, _ := nvml.DeviceGetHandleByIndex(0)
  dev.RegisterEvents(nvml.EventTypeXidCriticalError, set)
  for i := 0; i < 5; i++ { _, _ = dev.GetTemperature(nvml.TEMPERATURE_GPU) }
  ev, _ := nvml.EventSetWait(set, 1000)
  // ev.EventType == 0x8 (XID_CRITICAL_ERROR), ev.EventData == 79
  ```

  Both `nvmlEventSetWait_v1` and `nvmlEventSetWait_v2` are exported and
  behave identically. As with real NVML, a wait with no event pending
  blocks for the full timeout (1000 ms above) and then returns
  `NVML_ERROR_TIMEOUT`.

  The `GetTemperature` loop above is load-bearing: the wait re-checks
  every 100 ms but only claims an Xid that is *already* pending, and a
  device trips its injector on guarded **device** calls, never on the
  wait. Drop those calls and the wait blocks out its timeout forever, no
  matter what `failure.xid` is configured.

### The injector counter is per-process, and per-device within it

Each `kubectl exec ... -- nvidia-smi` is a fresh process with a fresh
`failureInjector` whose call counter resets to 0, so a threshold has to
be met inside a single invocation or it is never met at all. The counter
is also per-device: each GPU counts its own guarded calls rather than
sharing one process-wide tally.

That is why all three injection scenarios (2, 3 and 4) use
`after_calls: 1`. A query issuing exactly one guarded call per GPU, such
as Scenario 2's `--query-gpu=ecc.errors.uncorrected.aggregate.total`,
still trips every GPU, because each device reaches its own threshold on
its own first read. A larger `after_calls` would need a single command
to hit the same device that many times, which the demo's one-shot
queries never do.

For interactive exploration, use a long-running process, for example

```bash
kubectl exec -it "$POD" -- nvidia-smi \
  --query-gpu=index,ecc.errors.uncorrected.aggregate.total \
  --format=csv -l 1
```

Watch the counter increment on every poll.

For the full per-mode behaviour contract see
[`pkg/gpu/mocknvml/README.md#failure-injection-optional`](https://github.com/NVIDIA/k8s-test-infra/blob/main/pkg/gpu/mocknvml/README.md#failure-injection-optional).

## Clean up

```bash
# Default path: the cluster was already yours, so only remove the release.
# The -n and --kube-context are load-bearing. Without them helm resolves the
# release from whatever context and namespace happen to be current, which on
# a shared cluster is how this can delete the standalone demo's release.
helm uninstall nvml-mock-failure -n mokka-failure --kube-context <your-context>

# BUILD_LOCAL=true also created a cluster, so tear that down too:
kind delete cluster --name nvml-mock-failure-demo
```
