# Observability (Prometheus + Grafana)

Prometheus scrapes the real, unmodified NVIDIA `dcgm-exporter` while it reads
Mokka's mock `libnvidia-ml.so`, and Grafana renders the result — on a cluster
with no GPUs. Two manual triggers then inject a GPU fault and **fail if it never
reaches Prometheus**, so the whole scrape path is asserted rather than eyeballed.

For a scale run this answers "which component broke, and when".

## Run

```bash
make cluster-create
tilt up -- --observability
```

Grafana: <http://localhost:3000/d/mokka-gpu> (`admin` / `mokka`), served by the
`grafana` resource once its health probe passes.

`--observability` implies `--gpu-operator`, because `dcgm-exporter` is one of the
Operator's operands.

## What lands in the cluster

| Release / resource     | Namespace     | Notes                                          |
|------------------------|---------------|------------------------------------------------|
| `kube-prometheus-stack`| `monitoring`  | Prometheus, Grafana, kube-state-metrics, node-exporter |
| `mokka-gpu-dashboard`  | `monitoring`  | ConfigMap the Grafana sidecar imports          |
| `gpu-operator`         | `gpu-operator`| `dcgm-exporter` re-enabled by this consumer's overlay |
| `nvml-mock`            | `mokka`       | dynamic metrics enabled by this consumer's overlay |

Alertmanager and the bundled control-plane alert rules are off, and retention is
2h — the stack is trimmed to fit the per-node footprint budget in issue #597.
node-exporter stays on, since host CPU and memory pressure is what a scale run
needs to correlate GPU symptoms against.

## Files

| File                                | Purpose                                                        |
|-------------------------------------|----------------------------------------------------------------|
| `observability.tiltfile`            | Installs the stack, provisions the dashboard, serves Grafana, registers the triggers |
| `kube-prometheus-stack.values.yaml` | Chart trimming, 5s scrape interval, dashboard sidecar          |
| `gpu-operator.values.yaml`          | Overlay re-enabling `dcgmExporter` and its `ServiceMonitor`    |
| `nvml-mock.values.yaml`             | Overlay enabling `gpu.dynamicMetrics`                          |
| `dashboards/mokka-gpu.json`         | The dashboard, in git rather than click-together UI state      |
| `scenarios/inject-thermal.sh`       | Pins one GPU's temperature, asserts it reaches Prometheus      |
| `scenarios/inject-xid.sh`           | Trips an uncorrectable ECC fault, asserts the Xid arrives      |

## The two fault triggers

Both appear in the Tilt UI under the `observability-tests` label and are manual
(`auto_init=False`) — they are things you do to a live stack while watching the
dashboard, not part of bring-up. Faults go in through `nvml-mock-ctl` rather than
a `helm upgrade`, which would recycle the exporter and tear a hole in the very
series the dashboard exists to render.

**`inject-thermal`** resets every GPU on the target node, waits for a
simulator-driven baseline, pins GPU 0 to 90 °C, then asserts Prometheus serves
exactly that — and that the siblings kept their own readings, so the per-GPU
story the dashboard tells is genuinely scoped to one device. Watch the *GPU
temperature* panel: one line steps up and goes flat.

**`inject-xid`** raises an uncorrectable-ECC Xid on GPU 0 and asserts the code
reaches `DCGM_FI_DEV_XID_ERRORS`. Watch *Last Xid code reported*.

Neither can pass on residue, which is the point of both being assertions:

- The thermal run resets and waits for a reading that *differs* from the value it
  is about to inject, so a re-run cannot pass on the previous run's pin.
- The Xid run rotates between codes 79 and 48 and injects whichever is provably
  absent. DCGM latches the last Xid per device and never retracts it, so a fixed
  code would be satisfied by the previous run's residue — and would pass even
  with `dcgm-exporter` dead, since Prometheus keeps serving the last sample for
  its lookback window.

## Reading the dashboard

| Panel                                        | Shows                                              |
|----------------------------------------------|----------------------------------------------------|
| GPU temperature                              | Per-GPU `DCGM_FI_DEV_GPU_TEMP`; where a thermal injection appears |
| Last Xid code reported                       | The latched Xid **code**, not a count              |
| Power draw                                   | Per-GPU watts                                      |
| GPU utilization                              | Per-GPU busy percentage                            |
| GPU inventory — advertised vs requested      | Whether the fleet's capacity is being consumed     |
| Component health — ready vs desired, and restarts | `nvml-mock` and `dcgm-exporter` DaemonSet ready vs desired, plus per-pod restarts — this is the panel that names the component that broke |

## Things that look like bugs and are not

**The Xid panel is empty until a fault fires.** `DCGM_FI_DEV_XID_ERRORS` has no
series at all on a healthy cluster: the mock delivers Xids through the NVML event
set, and `dcgm-exporter` omits the field entirely while it holds no value for it.

**The injected Xid code alternates between runs** (79, then 48, then 79...). That
is the rotation above, not flapping.

**Injected temperatures are clamped** to the GPU profile's
`shutdown_threshold_c`. The default 90 °C is the one band valid for every
`--gpu-profile`: above the simulator's 73 °C ceiling, so a sibling GPU cannot
wander onto the injected value and fake a scope leak, and at or below the lowest
threshold of any profile (92 °C on a100/h100). A higher `HOT_TEMP_C` is rejected
up front rather than timing out on a value the mock would never report.

## Two couplings that fail silently

Both are pinned in this directory with comments naming them, so they move
together or not at all. They are worth knowing because neither produces an error:

1. **The `ServiceMonitor` `release` label.** kube-prometheus-stack defaults to
   `serviceMonitorSelectorNilUsesHelmValues: true`, so Prometheus only discovers
   ServiceMonitors labelled `release: kube-prometheus-stack`. A mismatch leaves
   every GPU panel empty with no warning anywhere.
2. **The dashboard sidecar label.** The ConfigMap must carry
   `grafana_dashboard: "1"` to be imported. A mismatch applies cleanly and no
   dashboard ever appears.

Install order is also load-bearing: kube-prometheus-stack goes in **before** the
GPU Operator, because it ships the `ServiceMonitor` CRD the Operator's chart
needs. That is expressed with `resource_deps`, not call order, so reordering the
root Tiltfile cannot silently break it.

## Scenario tunables

Both scripts read these from the environment: `TARGET_GPU` (default `0`),
`POLL_ATTEMPTS` (`36`), `POLL_INTERVAL_S` (`5`); `inject-thermal` also takes
`HOT_TEMP_C` (`90`), and `inject-xid` takes `XID_CODE` (`79`) and `XID_CODE_ALT`
(`48`).

## Troubleshooting

Every GPU panel empty? Check that Prometheus actually discovered the exporter —
this is the `release`-label trap above:

```bash
kubectl get --raw '/api/v1/namespaces/monitoring/services/kube-prometheus-stack-prometheus:9090/proxy/api/v1/targets?state=active' \
  | jq -r '.data.activeTargets[] | select(.labels.job|test("dcgm")) | "\(.scrapePool) \(.health)"'
```

Expect one `up` line per GPU worker, on a `serviceMonitor/...` scrape pool.

Dashboard missing in Grafana? Ask Grafana rather than trusting the ConfigMap:

```bash
kubectl -n monitoring exec deploy/kube-prometheus-stack-grafana -c grafana -- \
  curl -sS -u admin:mokka 'http://localhost:3000/api/search?query=Mokka'
```
