<!--
Copyright 2026 NVIDIA CORPORATION
SPDX-License-Identifier: Apache-2.0
-->

# NVSentinel (GPU health monitoring + fault remediation)

[NVSentinel](https://github.com/NVIDIA/nvsentinel) — NVIDIA's GPU health
monitoring and fault-remediation service — runs unmodified against the mock GPUs,
through the real NVIDIA DCGM. Two manual triggers then prove the whole
remediation loop on a cluster with no GPUs: a GPU crossing its hardware slowdown
limit is **detected** through DCGM, the node is **remediated** by cordon + drain
so the GPU workload moves to the healthy worker, and cooling the GPU
**auto-recovers** the node with no DCGM restart.

The fault is a **thermal-margin violation**. `GpuThermalMarginWatch` compares
each GPU's live *signed* T.Limit margin (DCGM field 153) against the per-GPU
hardware slowdown offset. That offset is not a DCGM field: the NVSentinel
`metadata-collector` reads it once from NVML field 194 and publishes it to
`gpu_metadata.json`.

```
nvml-mock (fake libnvidia-ml.so)
   │  GPU pinned hot → T.Limit margin (field 153) goes negative
   │  slowdown offset (NVML field 194) → metadata-collector → gpu_metadata.json
   ▼
GPU Operator standalone DCGM (nv-hostengine :5555)
   ▼
NVSentinel GPU Health Monitor  ──►  platform-connector  ──►  MongoDB (change streams)
   (GpuThermalMarginWatch)                                    │
                                          fault-quarantine ◄──┘  (cordon)
                                                  │
                                          node-drainer            (drain → workload reschedules)
```

On cooldown field 153 is a *live gauge*: the next poll sees the margin re-open,
the monitor emits healthy events, and `fault-quarantine` uncordons the node — no
DCGM restart, unlike a latched XID or ECC fault.

## Run

```bash
make cluster-create
tilt up -- --nv-sentinel
tilt up -- --nv-sentinel --observability   # with the Grafana view of the temperature step
```

`--nv-sentinel` implies `--gpu-operator`: the standalone DCGM (`nv-hostengine`)
NVSentinel polls is one of the Operator's operands, and the baseline operator
values disable it because nothing else polls one. Mutually exclusive with `--fgo`
(which replaces the Operator), `--compute-domain`, and `--multi-gpu-profile`
(whose a100 + t4 fleet predates the thermal margin this keys on).

**`--gpu-profile` defaults to `gb300` here, and pre-Ada profiles are rejected.**
`GpuThermalMarginWatch` arms only from the GPU's slowdown T.Limit offset, which
real hardware reports on Ada and later only — and the mock gates it the same way.
On `a100` or `t4` every Tilt resource still goes green while the watch stays
inert, logging *"missing slowdown TLIMIT threshold metadata"*: a heated GPU is
then detected by nothing, on a stack that looks perfectly healthy. The allow-list
is `h100`, `l40s`, `b200`, `gb200`, `gb300`, and it fails closed — a profile added
later is rejected until someone checks its architecture.

**First bring-up is slow.** The `gpu-health-monitor` image bundles DCGM 4.x and
the Operator's standalone DCGM image is ~2 GB; the monitor took **~14 minutes** to
pull on two cold workers, so `nvsentinel-ready` budgets 20 minutes for the waits
behind those pulls and prints what it is blocked on every minute. Cold bring-up is
8–15 minutes end to end, warm re-runs about a minute; adding `--observability` to
an already-warm cluster costs about 3½ minutes more the first time, while the
Prometheus and Grafana images pull. Do not kill a `tilt up` that looks stuck on
`nvsentinel-ready`.

## What lands in the cluster

| Tilt resource | Namespace | What it is |
|---|---|---|
| `jetstack` | — | Helm repo for cert-manager |
| `cert-manager` | `cert-manager` | v1.19.1, the TLS dependency of both MongoDB and NVSentinel |
| `mongodb-certs` | `nvsentinel` | The namespace, the self-signed CA and the two Certificates MongoDB serves TLS from |
| `mongodb-certs-ready` | — | `kubectl wait` on both Certificates, so the Secret exists before anything mounts it |
| `mongodb-ext` | `nvsentinel` | Single-node replica set (`rs0`) on the official multi-arch image, serving TLS from a cert-manager certificate; `rs.initiate()` runs in a `postStart` hook, and Ready means writable primary |
| `nvsentinel` | `nvsentinel` | The chart, v1.15.0, installed deliberately **without** `--wait` |
| `nvsentinel-ready` | — | Waits out the post-install hook, restarts the pods that raced it, and asserts the thermal watch armed |
| `gpu-sample-workload` | `default` | One pause pod requesting `nvidia.com/gpu` — the drainer's eviction target |
| `gpu-operator` | `gpu-operator` | Standalone DCGM DaemonSet + `nvidia-dcgm:5555` Service, re-enabled by this consumer's overlay |
| `nvml-mock` | `mokka` | `gpu.dynamicMetrics` enabled by this consumer's overlay |
| `quarantine-node`, `recover-node` | — | The two manual triggers, under the `nv-sentinel-tests` label |

MongoDB and NVSentinel's Deployments (`fault-quarantine`, `node-drainer`,
`labeler`) are pinned to the control-plane, so draining a GPU worker never evicts
the pipeline doing the draining. The DaemonSets spread wider: `gpu-health-monitor`
onto the GPU workers only, `metadata-collector` and `platform-connectors` onto
every node the labeler marks driver-installed.

## Files

| File | Purpose |
|---|---|
| `nv_sentinel.tiltfile` | Installs cert-manager, MongoDB, NVSentinel and the workload; registers the two triggers |
| `gpu-operator.values.yaml` | Overlay re-enabling the standalone `dcgm` DaemonSet, pointed at the mock driver root |
| `nvml-mock.values.yaml` | Overlay enabling `gpu.dynamicMetrics`, so a heated GPU reads differently from its idle siblings |
| `mongodb.k8s.yaml` | The external MongoDB: cert chain, headless Service, StatefulSet with the `rs.initiate()` `postStart` hook, and the URI Secret NVSentinel reads |
| `nvsentinel.values.yaml` | The chart values — external datastore, DCGM endpoint, and the options below |
| `nvsentinel-ready.sh` | The post-install choreography `--wait` cannot express (see below) |
| `gpu-workload.k8s.yaml` | The GPU workload the drain evicts |
| `scenarios/quarantine-node.sh` | Heats a GPU, asserts the cordon and a real eviction |
| `scenarios/recover-node.sh` | Cools it, asserts the uncordon with no DCGM restart |

## The two triggers

Both appear in the Tilt UI under the `nv-sentinel-tests` label and are manual
(`auto_init=False`), so `tilt ci` skips them. Faults go in through `nvml-mock-ctl`
rather than a `helm upgrade`, which would recycle DCGM along with the reading.
Each **fails if NVSentinel does not react**, so the remediation path is asserted
rather than eyeballed. Run them from the UI, or directly:

```bash
bash local/nv-sentinel/scenarios/quarantine-node.sh
bash local/nv-sentinel/scenarios/recover-node.sh
```

**`quarantine-node`** clears every override on every mock node, waits until every
GPU node is schedulable and the workload is Running where a fault can be injected,
then heats one GPU past its slowdown limit. It asserts the node is cordoned by
NVSentinel and that *this* workload pod was evicted and replaced on another node —
a pod merely being elsewhere proves nothing. Measured 10–21 s from injection to
cordon, with the eviction in the same second or within one 5 s poll of it — the
watch reads DCGM every 15 s, so where the injection lands in that cycle is most
of the spread.

**`recover-node`** clears the overrides on every cordoned GPU node and asserts it
becomes schedulable *and* that `GpuThermalMarginWatch` reports it healthy, then
that no `nvidia-dcgm` pod restarted across the recovery. It never runs `kubectl
uncordon` — that would make its own assertion true. Measured 10–16 s from cooling
to recovered.

Tunables, read from the environment: `POLL_ATTEMPTS` (default `60`) and
`POLL_INTERVAL_S` (`5`) in both; `TARGET_GPU` (`0`) and `HOT_TEMP_C` in
`quarantine-node` only. `HOT_TEMP_C` defaults to the loaded profile's
`slowdown_threshold_c + 3`, capped one degree below `shutdown_threshold_c`, read
out of the mock's own config rather than hardcoded — slowdown is 90 °C on the
default `gb300` (so 93 °C is injected), 87 °C on `h100`, 90 °C on `b200`/`gb200`
and 93 °C on `l40s`, and the mock clamps at shutdown. An override outside that band is rejected before anything is injected.

## The six options that make this work

Most of `nvsentinel.values.yaml` just restates this deployment's topology. Six
keys are load-bearing, each with its reason inline in that file:

- **`global.metadataCollector.enabled: true`** — the `metadata-collector`
  DaemonSet is what publishes the slowdown T.Limit offset. Without it
  `GpuThermalMarginWatch` never arms. This is the chart's own default; it is set
  explicitly so a future default flip cannot silently disarm the scenario.
- **`labeler.assumeDriverInstalled: true`** — the metadata-collector only
  schedules on nodes labelled `nvsentinel.dgxc.nvidia.com/driver.installed=true`,
  which the labeler normally sets only when it sees a real
  `nvidia-driver-daemonset`. Here the mock supplies `libnvidia-ml.so` instead, so
  the labeler is told to assume the driver — the same knob NVIDIA documents for
  hosts with pre-baked drivers.
- **`gpu-health-monitor.dcgmFieldsMonitoring.gpuTempLimitStoreOnly: false`** — the
  watch ships in dry-run: it emits events but never touches the node. Turning
  store-only off is what lets a closing margin drive the cordon and drain.
- **`gpu-health-monitor.dcgmHealthCheck.suppressedErrorCodes`** — includes
  `DCGM_FR_NVLINK_EFFECTIVE_BER_THRESHOLD`, which the mock reports on every GPU at
  boot. Left active it is a failing check forever, so the node stays quarantined
  after the GPU cools and the recovery never completes.
- **`fault-quarantine.circuitBreaker.enabled: false`** — the breaker trips when
  ≥ 50 % of GPU nodes are cordoned within 5 minutes and then halts *all* event
  processing, including the uncordon. With two GPU workers one legitimate cordon
  already meets that threshold. **Leave it enabled on real clusters.**
- **`node-drainer.userNamespaces[*].mode: Immediate`** — the default
  `AllowCompletion` waits for each pod to finish, and the sample workload never
  does, so it would never be evicted and the drain would not be observable. Real
  clusters typically keep `AllowCompletion`.

## Things that look like bugs and are not

**NVSentinel pods in `CrashLoopBackOff` between the `nvsentinel` release going
green and `nvsentinel-ready` finishing.** The collection-setup Job is a Helm
post-install hook, and `platform-connector`, `fault-quarantine` and `node-drainer`
cannot become Ready until it has created the MongoDB collections. They start
before it runs, and crash until it does.

**The `nvsentinel` release does not pass `--wait`, on purpose.** Helm runs
post-install hooks only *after* `--wait` is satisfied, so `--wait` would block the
release on pods that are waiting on the hook that `--wait` is holding back. Adding
it deadlocks the install. `nvsentinel-ready` is the wait, and it also deletes the
pods that crash-looped so they start clean rather than serving out an exponential
backoff behind a red Tilt resource.

**MongoDB is external and runs the official image.** The chart's `mongodb-store`
subchart uses the Bitnami chart, whose images are published amd64-only and whose
containers run Bitnami-only startup scripts — unusable on Apple Silicon. So this
runs a plain `mongo:8.0.3` single-node replica set (change streams need one) as an
external datastore, with TLS from cert-manager.

**Plaintext is possible, but it takes two settings, and getting only the obvious
one is worse than leaving TLS on.** `global.datastore.tls.enabled: false` merely
renders `MONGODB_TLS_ENABLED` into a ConfigMap. Each component separately defaults
`clientCertMountPath` to a real path, which the chart passes as
`--database-client-cert-mount-path`, and the binary reads `ca.crt` from there
before connecting; with no CA mounted it logs `Failed to read CA certificate,
retrying` for 360s and exits 1 while **reporting Ready the whole time**, so the UI
is green for six minutes and nothing is remediated. Dropping TLS therefore means
`tls.enabled: false`, no `caSecretName`, and `clientCertMountPath: ""` on every
datastore consumer — which makes the templates pass `--tls-enabled=false`. The
chart ships that recipe as `values-tilt-mongodb-tls-disabled.yaml`. This consumer
keeps TLS because it mirrors the standalone demo and a real deployment.

**The control-plane hosts NVSentinel's pipeline but is not a GPU node.** The mock
runs on workers only (`mokka.nvidia.com/type=sgpu`), and the GPU Operator's device
plugin tolerates `nvidia.com/gpu:NoSchedule` but not
`node-role.kubernetes.io/control-plane:NoSchedule`, so nothing advertises
`nvidia.com/gpu` there either way. Both scenarios filter on the advertised
resource rather than on a node label, which is also why the workload needs no node
selector: the resource request already pins it to a worker.

**`dcgm-exporter` crash-looping for a few minutes on a cold cluster.** Enabling
the standalone DCGM makes the Operator hand `dcgm-exporter` a
`DCGM_REMOTE_HOSTENGINE_INFO` of `nvidia-dcgm:5555`, so the exporter drops its
embedded hostengine and connects to the shared one — which does not exist until
the DaemonSet's ~2 GB image has pulled. Measured ~3 minutes of crash-looping, then
it self-heals. Under `--observability` this is a multi-minute gap in the GPU
dashboard that a plain `--observability` session does not have. It belongs to the
cold DCGM pull, not to the flag pair: adding `--observability` to a stack whose
`nvidia-dcgm` is already serving restarted no exporter pod at all.

**Prometheus or Grafana restarting when `quarantine-node` runs, under
`--observability`.** `node-drainer` is configured with `userNamespaces` `"*"` in
`Immediate` mode, so the drain evicts every non-DaemonSet pod on the quarantined
worker — including anything from `monitoring` that landed there. Measured:
`quarantine-node` picked the worker hosting `prometheus-...-0`, which was evicted
and rescheduled onto the other one. `kube-prometheus-stack` runs here without
persistent storage, so its TSDB restarts empty and the temperature step from
before the fault is gone from the dashboard. The remediation worked exactly as
asserted; if you want the whole step in one graph, check which worker Prometheus
is on first.

**`inject-thermal` quarantining a worker, under `--observability`.** Not a bug
either, but the one interaction that leaves the cluster worse off, so know it
before you run both sets of triggers. `--observability`'s `inject-thermal` pins
GPU 0 to a fixed **90 °C**, chosen to clear the *shutdown* threshold of every
`--gpu-profile` — nothing about the *slowdown* threshold this consumer keys on.
`GpuThermalMarginWatch` fails when the pinned temperature is **strictly above**
that threshold, so whether the observability trigger doubles as a fault injection
depends entirely on the active profile:

| `--gpu-profile` | `slowdown_threshold_c` | 90 °C pin |
|---|---|---|
| `h100` | 87 °C | **quarantines the worker** |
| `gb300` (default), `b200`, `gb200` | 90 °C | lands exactly on the limit, no fault |
| `l40s` | 93 °C | no fault |

On `h100` the pin is the very fault `quarantine-node` injects, so NVSentinel
cordons the worker and drains it — evicting Prometheus itself if it lives there.
Two consequences then follow:

- **`inject-thermal` can red-fail a run that did nothing wrong.** Detection is
  ~15 s while the scenario budgets 25–45 s for the pin to reach Prometheus, so the
  drain can land on top of its own closing queries.
- **The worker stays cordoned.** `inject-thermal` leaves the pin in place on
  purpose, so nothing lifts the quarantine on its own.

Clear the pin and NVSentinel uncordons within ~15 s; `recover-node` does both and
asserts it:

```bash
bash local/nv-sentinel/scenarios/recover-node.sh
```

To keep the dashboard step on any profile without the remediation, pin below
every slowdown threshold and above the simulator's own 73 °C ceiling:
`HOT_TEMP_C=80 bash local/observability/scenarios/inject-thermal.sh`.

**Slow bring-up under host CPU pressure.** This stack runs the GPU Operator,
DCGM, MongoDB and the full NVSentinel pipeline, so on a busy machine — several
Kind clusters at once — cert-manager and the DCGM image pulls dominate and every
cold-path number above gets worse.

**The GPU workload does not move back after a recovery.** A Deployment does not
relocate a Running pod, so it stays on the worker the drain sent it to. The node
coming back means it can be scheduled there again, not that anything migrates.

## Driving it by hand

**Do not re-run `tilt ci` or `tilt up` between injecting a fault and observing the
verdict.** `nvsentinel-ready` deletes every Running pod labelled
`app.kubernetes.io/instance=nvsentinel` on each run, so it restarts the whole
pipeline and loses the in-flight state the trigger is waiting on.

**`recover-node` is not a cleanup button.** It refuses to run on a healthy
cluster, on a node cordoned by hand, and on a quarantined node with no GPU pinned
at or above the profile's slowdown threshold — whether the override was already
cleared or was never hot. In each case the uncordon it would assert would happen
without it. So the order is `quarantine-node` then `recover-node`. It is not
*required* between runs, though: `quarantine-node` clears every pin fleet-wide
before it injects. To tidy up by hand, clear the overrides and wait, or `kubectl
uncordon`.

**`kubectl uncordon` drops `fault-quarantine`'s quarantine annotation** along with
the cordon, so that annotation is not a record of "this node was quarantined". The
`GpuThermalMarginWatch` node condition is: it stays `True` until the GPU actually
cools.

## One thing that looks fine and is not

Deleting the `mongodb-ext` pod costs you the **`HealthEventsDatabase`
collections**, and only the chart's own setup hook creates them. The replica set
itself recovers unattended — `rs.initiate()` runs in a `postStart` hook, so it
re-runs with the pod, and the readiness probe asserts a writable primary rather
than a bare ping — but `fault-quarantine` and `node-drainer` then crash-loop on
`no collection with name HealthEvents for DB HealthEventsDatabase was found`
until the release is upgraded again. Re-running the bring-up does both, since it
re-runs the hook Job and `nvsentinel-ready` restarts the pods that crash-looped:

```bash
tilt ci -- --nv-sentinel
```

The cause is `emptyDir`, which is deliberate for a dev stack: anything the setup
hook created has to be recreated by that hook. Unlike the replica set, the
collections cannot re-create themselves.

The same crash-loop shows up on a **cold** bring-up and is not this problem: the
components start before the setup hook has run, and `nvsentinel-ready` waits it
out and restarts them.

## Without Tilt

[`docs/demo/nv-sentinel/`](../../docs/demo/nv-sentinel/) is the same stack as a
standalone `run.sh` on its own dedicated Kind cluster, with the two phases run
back to back instead of as triggers. It remains the version to use outside the
Tilt environment; this consumer skips its dedicated cluster and its per-worker
`nvidia-container-toolkit` install, because the shared Kind node image already
bakes both in.
