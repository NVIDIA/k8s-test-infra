# nvml-mock Failure-Injection Demo

End-to-end walkthrough of the GPU failure-injection feature on a
dedicated Kind cluster. Unlike [`../standalone/`](../standalone/), this
demo exercises every supported failure mode (`healthy` →
`ecc_uncorrectable` → `lost` → `fallen_off_bus`) by re-deploying the
chart between scenarios with `helm upgrade --reuse-values`, and
asserts each mode's expected behaviour against `nvidia-smi` output
inside the pod.

The demo lives in its own cluster (`nvml-mock-failure-demo`) so it
never collides with the standalone demo or any other nvml-mock
deployment. The Kind topology itself is the shared
[`../kind.yaml`](../kind.yaml) (1 control-plane + 3 workers with
FGO-style labels) — failure injection doesn't need its own topology.

## What the script does

1. (Re)creates the dedicated Kind cluster (idempotent: an existing
   `nvml-mock-failure-demo` cluster is reused).
2. Builds and loads the `nvml-mock:failure-demo` image.
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
   release with `mode=ecc_uncorrectable, after_calls=3, xid.code=79`,
   recycles the DaemonSet, and asserts:
   * the ConfigMap now contains `mode: ecc_uncorrectable`,
   * `nvidia-smi -L` **still** lists every GPU (mode contract: device
     stays addressable),
   * `nvidia-smi -q -d ECC` reports a strictly-positive aggregate
     uncorrectable total — the third guarded NVML call within that
     same process meets `after_calls: 3`, so the trip fires while the
     same `nvidia-smi` invocation is still running.
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

`nvidia-smi` doesn't subscribe to `nvmlEventSetWait_v2`, so it never
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

### The injector counter is per-process

Each `kubectl exec ... -- nvidia-smi` is a fresh process with a fresh
`failureInjector` whose call counter resets to 0. That's why
Scenario 2 uses `after_calls: 3` (so a single
`nvidia-smi -q -d ECC` reaches the threshold during its own
invocation) and Scenarios 3-4 use `after_calls: 1` (the very first
guarded call trips). For interactive exploration, use a long-running
process — e.g.

```bash
kubectl exec -it "$POD" -- nvidia-smi \
  --query-gpu=index,ecc.errors.uncorrected.aggregate.total \
  --format=csv -l 1
```

— and watch the counter increment on every poll.

For the full per-mode behaviour contract see
[`pkg/gpu/mocknvml/README.md#failure-injection-optional`](../../../pkg/gpu/mocknvml/README.md#failure-injection-optional).

## Clean up

```bash
kind delete cluster --name nvml-mock-failure-demo
```
