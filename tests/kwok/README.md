# Mokka controller KWOK scalability POC

This opt-in harness runs the real `mokka-control-plane` binary on the host against
a dedicated KWOK kubeconfig. KWOK Nodes do not run containers, so the Helm
Deployment is intentionally not installed.

Prerequisites are preinstalled `kwokctl` v0.8.0, `kubectl`, Docker, Go, `jq`,
`curl`, and POSIX process tools. The harness downloads no binaries. KWOK
component images are pinned in `run.sh` to the
[v0.8.0 release](https://github.com/kubernetes-sigs/kwok/releases/tag/v0.8.0)
defaults. Node creation uses KWOK's
[custom-resource scaling](https://kwok.sigs.k8s.io/docs/examples/scale-with-custom-resource/)
in one command per target size, not one shell process per Node.

Run the smoke tier (200 Nodes):

```bash
make kwok-scale KWOK_SCALE=1
```

Run one tier or the 1k/10k/25k/50k/100k matrix:

```bash
make kwok-scale KWOK_SCALE=1 KWOK_NODE_COUNT=10000
make kwok-scale-matrix KWOK_SCALE=1
```

`KWOK_NODE_COUNT` must be divisible by `KWOK_NODES_PER_RACK` (default 100),
must span at least two racks, and is capped by the Mokka CRD at 1024 slots per
rack.
Useful overrides include `KWOK_TIMEOUT_SECONDS`, `KWOK_WORKERS`,
`KWOK_API_QPS`, `KWOK_API_BURST`, `KWOK_CLUSTER_NAME`, and
`KWOK_ARTIFACT_ROOT`. Existing clusters are refused. The harness removes only
the cluster it created; timestamped results, snapshots, logs, and metrics stay
under `_artifacts/kwok`, including after failure.

The scenarios cover steady state, scale-up, host-controller restart with
assignment stability, capacity exhaustion, inventory shrink/grow, eligibility
churn, and same-name/new-UID Node replacement. Each state has bounded waits and
machine-readable assertions and timings. Durations include the triggering
scale, restart, or API mutation as well as final controller convergence.

KWOK proves Kubernetes API/informer/workqueue behavior, deterministic rack and
assignment semantics, cleanup, and control-plane resource cost at fake-Node
scale. It does not prove kubelet/container execution, Helm scheduling, device
drivers, GPU or network behavior, Node resource pressure, or real workload
performance.
