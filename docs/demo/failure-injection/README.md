# nvml-mock Failure-Injection Demo

End-to-end walkthrough of the GPU failure-injection feature on a
dedicated Kind cluster. Unlike [`../standalone/`](../standalone/), this
demo deploys nvml-mock with `gpu.failureInjection.enabled=true` from
the start and verifies that the engine actually trips the configured
fault.

The demo lives in its own cluster (`nvml-mock-failure-demo`) so it
never collides with the standalone demo or any other nvml-mock
deployment you may already have running.

## What the script does

1. Creates a small Kind cluster (1 control-plane, 1 worker — see
   [`kind.yaml`](./kind.yaml)).
2. Builds and loads the `nvml-mock:failure-demo` image.
3. Installs the chart with:
   ```
   gpu.failureInjection.enabled = true
   gpu.failureInjection.mode    = ecc_uncorrectable
   gpu.failureInjection.after_calls = 3
   gpu.failureInjection.xid.code    = 79
   ```
   `ecc_uncorrectable` is the entry-point mode because the device
   stays addressable: `nvidia-smi` keeps running inside the pod and
   the script can verify counters growing instead of the GPU
   disappearing from the API surface.
4. Verifies the rendered ConfigMap actually carries the
   `failure:` block under `device_defaults:` (a regression guard
   against the helper template silently dropping the overlay).
5. Runs `nvidia-smi -q -d ECC` inside a pod and asserts the aggregate
   uncorrectable counter is **non-zero**. `after_calls: 3` is met by
   the third guarded NVML call within that single `nvidia-smi`
   process, so the trip fires while the same process keeps running
   and subsequent ECC reads return the running call count.
6. Prints the YAML the engine actually loaded from
   `/etc/nvml-mock/config.yaml` for sanity.
7. Prints copy-pasteable follow-up commands for the other two failure
   modes (`lost` and `fallen_off_bus`), which are NOT run
   automatically because they make `nvidia-smi -L` return an error
   and that's not a great closing impression for an automated demo.

## Quick start

```bash
./demo.sh
```

Reruns are idempotent — if the cluster already exists the script reuses
it, and `helm upgrade --install` covers both first-time install and
subsequent upgrades.

## Manual exploration

After `./demo.sh` completes, the script prints commands for switching
the running release between failure modes. A few useful follow-ups:

### Watch the ECC counter grow over time

The mock's call counter is **per-process**, so each `nvidia-smi`
invocation starts fresh. To see the counter increment monotonically,
keep one process alive:

```bash
POD=$(kubectl get pods -l app.kubernetes.io/name=nvml-mock \
  -o jsonpath='{.items[0].metadata.name}')

kubectl exec "$POD" -- nvidia-smi \
  --query-gpu=index,ecc.errors.uncorrected.aggregate.total \
  --format=csv -l 1
```

The first two rows print `0`; from the third row onward the counter
is strictly increasing.

### Switch to `lost` mode

```bash
helm upgrade nvml-mock deployments/nvml-mock/helm/nvml-mock \
  --reuse-values \
  --set gpu.failureInjection.mode=lost \
  --set gpu.failureInjection.after_calls=1
kubectl rollout restart daemonset/nvml-mock
kubectl rollout status   daemonset/nvml-mock --timeout=60s
kubectl exec "$POD" -- nvidia-smi -L || true   # expect: 'GPU is lost'
```

### Observe the Xid event

`nvidia-smi` does **not** subscribe to the NVML event set, so it never
prints the configured Xid. To see the Xid delivered through
`nvmlEventSetWait_v2` (`NVML_EVENT_TYPE_XID_CRITICAL_ERROR`), use any
of:

* the NVIDIA device plugin (its health monitor consumes the same
  event and marks the GPU `Unhealthy`),
* `dcgm-exporter` (`DCGM_FI_DEV_XID_ERRORS` metric),
* a small Go program calling `nvml.EventSetCreate` /
  `RegisterEvents(EventTypeXidCriticalError)` / `EventSetWait` — the
  mock delivers the configured Xid exactly once per engine lifetime,
  matching real NVML semantics.

For full per-mode behaviour see
[`pkg/gpu/mocknvml/README.md#failure-injection-optional`](../../../pkg/gpu/mocknvml/README.md#failure-injection-optional).

## Clean up

```bash
kind delete cluster --name nvml-mock-failure-demo
```

## Why a separate demo

The standalone demo (`../standalone/demo.sh`) intentionally keeps
failure injection *disabled* so its post-install verification steps
(`nvidia-smi`, ConfigMap counts, node labels) always succeed. Folding
failure injection into that script would make those checks flaky on
first run because every `nvidia-smi` invocation would trip the GPU
mid-flight. Splitting it out lets each demo make assertions that
match its own intent.
