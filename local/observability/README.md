# Observability (Prometheus + Grafana)

Prometheus scrapes the real, unmodified NVIDIA `dcgm-exporter` while it reads
Mokka's mock `libnvidia-ml.so`, and Grafana renders the result, on a cluster with
no GPUs. Two manual triggers then inject a GPU fault and **fail if it never
reaches Prometheus**, so the whole scrape path is asserted rather than eyeballed.

`tilt up -- --observability` serves Grafana at <http://localhost:3000/d/mokka-gpu>
(`admin` / `mokka`) once the `grafana` resource's health probe passes. The stack is
trimmed for Kind (no Alertmanager, 2h retention); `kube-prometheus-stack.values.yaml`
says why.

## The two fault triggers

Both are under the `observability-tests` label in the Tilt UI and manual: run them
against a live stack while watching the dashboard.

**`inject-thermal`** pins GPU 0 to 90 °C and asserts Prometheus serves exactly that
while its siblings keep their own readings. In the *GPU temperature* panel, one line
steps up and goes flat.

**`inject-xid`** raises an uncorrectable-ECC Xid on GPU 0 and asserts the code
reaches `DCGM_FI_DEV_XID_ERRORS`. *Last Xid code reported* shows the latched Xid
**code**, not a count.

The *Component health* panel shows `nvml-mock` and `dcgm-exporter` DaemonSet ready vs
desired plus per-pod restarts, so it names the component that broke.

## Things that look like bugs and are not

**The Xid panel is empty until a fault fires.** `DCGM_FI_DEV_XID_ERRORS` has no
series on a healthy cluster: the mock delivers Xids through the NVML event set, and
`dcgm-exporter` omits the field while it holds no value for it.

**The injected Xid code alternates between runs** (79, then 48, then 79...). DCGM
latches the last Xid per device, so a repeated code would prove nothing.

**Injected temperatures are clamped** to the GPU profile's `shutdown_threshold_c`.
The default 90 °C is the one band valid for every `--gpu-profile`: above the
simulator's 73 °C ceiling, so a sibling GPU cannot wander onto the injected value and
fake a scope leak, and at or below the lowest threshold of any profile (92 °C on
a100/h100). A higher `HOT_TEMP_C` is rejected up front rather than timing out on a
value the mock would never report.

## Silent failures

Three ways the stack comes up with empty GPU panels or no dashboard and no error.
Each is pinned with a comment where it lives:

- **The `ServiceMonitor` `release` label** (`gpu-operator.values.yaml`). Prometheus
  only discovers ServiceMonitors labelled `release: kube-prometheus-stack`.
- **The dashboard sidecar label** (`kube-prometheus-stack.values.yaml`,
  `dashboards/mokka-gpu-dashboard.yaml`). The ConfigMap must carry
  `grafana_dashboard: "1"`.
- **The `ServiceMonitor` scrape interval** (`gpu-operator.values.yaml`). A value
  shorter than the scrape timeout makes the Prometheus Operator skip the
  ServiceMonitor with only a warning in its own log.

## Troubleshooting

Every GPU panel empty? Check that Prometheus discovered the exporter, which catches
the first and third failures above:

```bash
kubectl get --raw '/api/v1/namespaces/monitoring/services/kube-prometheus-stack-prometheus:9090/proxy/api/v1/targets?state=active' \
  | jq -r '.data.activeTargets[] | select(.labels.job|test("dcgm")) | "\(.scrapePool) \(.health)"'
```

Expect one `up` line per GPU worker, on a `serviceMonitor/...` scrape pool. No line
at all, with the label correct? Ask the Prometheus Operator why it skipped the
ServiceMonitor:

```bash
kubectl -n monitoring logs deploy/kube-prometheus-stack-operator | grep 'skipping object'
```

Dashboard missing in Grafana? Ask Grafana rather than trusting the ConfigMap:

```bash
kubectl -n monitoring exec deploy/kube-prometheus-stack-grafana -c grafana -- \
  curl -sS -u admin:mokka 'http://localhost:3000/api/search?query=Mokka'
```
