# nvml-mock ComputeDomain Demo

End-to-end walkthrough of the ComputeDomain (NVLink fabric) simulation
on a dedicated Kind cluster. This demo exercises every component
introduced by [NVIDIA/k8s-test-infra#304](https://github.com/NVIDIA/k8s-test-infra/issues/304):

* **Mock NVML fabric APIs** — `nvmlDeviceGetGpuFabricInfo` and
  `nvmlDeviceGetGpuFabricInfoV` return the cluster UUID, clique ID, and
  registration state that the cluster-level topology ConfigMap assigned
  to the current node (via `NODE_NAME`).
* **Cluster-level topology ConfigMap** — declares the GB200 NVLink
  domains and which Kubernetes nodes belong to which clique. The mock
  NVML library overlays it on top of the per-profile YAML at
  `LoadConfig()` time.
* **Real `nvidia-imex` in NO GPU mode** — the demo overlay image fronts
  the real daemon with `imex-nogpu-shim` (`/usr/bin/nvidia-imex` exec's
  `/usr/bin/nvidia-imex.real --nogpu`), so IMEX readiness is the real
  gRPC peer protocol over the pod network: `nvidia-imex-ctl -q` prints
  `READY`, `-N -j` reports the domain `UP` with version `NO_GPU`, and
  killing a peer daemon degrades the domain.

The demo lives in its own cluster (`nvml-mock-compute-domain`) and its
own 4-worker Kind topology
([`tests/e2e/kind-compute-domain-config.yaml`](../../../tests/e2e/kind-compute-domain-config.yaml)).

## Prerequisites

The demo expects the following tools on `$PATH`:

| Tool      | Tested version | Notes |
|---        |---             |---    |
| `docker`  | 24+            | Daemon must be running. Multi-stage build uses Go 1.25 base. |
| `kind`    | v0.24+         | Provisions the demo's dedicated 4-worker cluster. |
| `kubectl` | v1.30+         | Used for `exec`, `rollout`, `get` against the in-cluster pods. |
| `helm`    | v3.13+         | Chart install + `helm upgrade --reuse-values`. |
| `bash`    | 3.2+           | `run.sh` uses `set -euo pipefail` — no bash 4+ features. |
| `jq`      | any recent     | Scenario 2 parses `nvidia-imex-ctl -N -j` JSON. |

## What the script does

1. (Re)creates the dedicated Kind cluster (idempotent: an existing
   `nvml-mock-compute-domain` cluster is reused).
2. Builds and loads the `nvml-mock:compute-domain` image (bundles
   `/usr/local/bin/check-fabric` on top of the standard nvml-mock
   image), then builds the `nvml-mock:compute-domain-imex` overlay via
   the `demo` target of
   [`Dockerfile.compute-domain-daemon`](../../../deployments/nvml-mock/Dockerfile.compute-domain-daemon),
   which layers the real `nvidia-imex` (NO GPU mode via
   `imex-nogpu-shim`) on top — the image the DaemonSet actually runs.
3. Installs the chart with:

   ```text
   gpu.profile=gb200
   topology.enabled=true
   topology.domains=<demo topology>
   ```

   This renders both ConfigMaps (`nvml-mock-config` and
   `nvml-mock-topology`).

   The script intentionally does **not** pass `--set gpu.count=...`.
   That flag only sizes the host-side CDI spec produced by
   `scripts/setup.sh`; the in-pod ConfigMap at
   `/etc/nvml-mock/config.yaml` — which is what `check-fabric` loads
   — always reflects the chosen profile's full device list (8 GPUs
   for `gb200`). For ComputeDomain verification this is actually
   *stronger* evidence: every one of the 8 GPUs on each node must
   report the same `cliqueId` / `clusterUuid`, exercising the
   topology overlay over the full device list rather than a subset.
4. **Scenario 1 — per-node fabric identity**. Runs `check-fabric`
   inside one pod per worker and asserts:
   * `nvml-mock-compute-domain-worker` / `-worker2` report **clique 0**,
   * `-worker3` / `-worker4` report **clique 1**,
   * every node reports the demo cluster UUID
     `00000000-0000-0000-0000-0000000000ab` and `state=completed`.
5. **Scenario 2 — real IMEX domain (NO GPU mode)**. Renders a per-pod
   IMEX config, starts the real `nvidia-imex` (the shim appends
   `--nogpu`) in both clique-0 pods, and asserts three transitions:
   * daemon A alone → local `-q` probe `READY`, domain not `UP`,
   * daemon B joins → `-N -j` reports `UP`, 2/2 nodes `READY`,
     version `NO_GPU` (real gRPC over the pod network),
   * daemon B SIGTERMed → domain leaves `UP` (real liveness — the
     deprecated marker files couldn't detect a dead peer).
6. **Scenario 3 — topology rebinding (no image rebuild)**. A
   `helm upgrade --reuse-values` swaps the topology document so every
   node is now a member of clique 99 in a brand-new domain UUID. After
   a forced DaemonSet recycle, `check-fabric` reflects the new
   identity on every worker. This is the gear the real
   compute-domain-controller would shift between integration tests.

## Quick start

```bash
./run.sh
```

The script is idempotent — rerun it as often as you like; the
existing cluster is reused and `helm upgrade --install` covers both
first-time install and follow-up upgrades.

## Manual reproduction

If you want to follow along without `run.sh` — for debugging,
demo-tweaking, or just to understand the moving parts — these are the
commands the script issues. They're written for the default
`nvml-mock-compute-domain` cluster name; see the "Custom cluster name"
note at the end of this section if you need to rename it.

```bash
# 1. Create the dedicated cluster.
kind create cluster --name nvml-mock-compute-domain \
    --config tests/e2e/kind-compute-domain-config.yaml

# 2. Build the demo image (bundles check-fabric on top of the standard
#    nvml-mock image), then layer the REAL nvidia-imex (NO GPU mode
#    via imex-nogpu-shim) on top. Local build only — this image
#    repackages the proprietary nvidia-imex.
docker build -t nvml-mock:compute-domain -f deployments/nvml-mock/Dockerfile .
docker build -t nvml-mock:compute-domain-imex \
    --target demo \
    --build-arg NVML_MOCK_IMAGE=nvml-mock:compute-domain \
    --build-arg GOLANG_VERSION=$(hack/golang-version.sh) \
    -f deployments/nvml-mock/Dockerfile.compute-domain-daemon .

# 3. Load the demo image into the Kind cluster.
kind load docker-image nvml-mock:compute-domain-imex --name nvml-mock-compute-domain

# 4. Install the chart. The --set image.* flags point the DaemonSet at
#    the locally-loaded image (these are required — without them the
#    chart pulls the default upstream image which does not have the
#    real IMEX layer baked in).
helm install nvml-mock deployments/nvml-mock/helm/nvml-mock \
    -f docs/demo/compute-domain/topology.yaml \
    --set image.repository=nvml-mock \
    --set image.tag=compute-domain-imex \
    --set gpu.profile=gb200 \
    --wait --timeout 180s

# 5. Verify the per-node fabric overlay (Scenario 1).
kubectl rollout status daemonset/nvml-mock --timeout=120s
for node in nvml-mock-compute-domain-{worker,worker2,worker3,worker4}; do
  pod=$(kubectl get pods -l app.kubernetes.io/name=nvml-mock \
    --field-selector="spec.nodeName=${node},status.phase=Running" \
    -o jsonpath='{.items[0].metadata.name}')
  echo "=== ${node} (pod=${pod}) ==="
  kubectl exec "${pod}" -- check-fabric | head -6
done
```

`-worker` / `-worker2` should report `cliqueId : 0`, `-worker3` /
`-worker4` should report `cliqueId : 1`, all four should report the
demo `clusterUuid : 00000000-0000-0000-0000-0000000000ab` and
`state : completed (3)`.

Scenarios 2 and 3 are best read directly from
[`run.sh`](./run.sh) — they involve rendering a per-pod `nodes.cfg`,
running the real `nvidia-imex` daemons, and a
`helm upgrade --reuse-values` with a substituted topology. None of
those steps are non-obvious once Scenario 1 works.

**Custom cluster name.** If you rename the Kind cluster (e.g., to
parallelise demos), two things need to change in lockstep:

1. The `nodes:` lists in [`topology.yaml`](./topology.yaml) — each
   Kind worker is named `<cluster-name>-worker[N]`, so renaming the
   cluster renames every entry in the topology.
2. Cluster name in every `kind` / `kubectl --context` / `kind load`
   call below.

The script doesn't expose this as a flag because the demo is
documentation-by-example; the canonical name keeps the example
faithful to what's checked in.

## How the real IMEX fits alongside the compute-domain-daemon

The upstream daemon spawns `nvidia-imex` as a subprocess and probes
readiness with `nvidia-imex-ctl -c /imexd/imexd.cfg -q`,
comparing the combined output to exactly `READY`. With this demo's
overlay installed both paths hold the real binaries: the shim at
`/usr/bin/nvidia-imex` execs `/usr/bin/nvidia-imex.real --nogpu`, so
the upstream daemon runs unmodified — same argv, same probe, real
protocol, no GPUs. Point its container image at the default (`daemon`)
target of
[`deployments/nvml-mock/Dockerfile.compute-domain-daemon`](../../../deployments/nvml-mock/Dockerfile.compute-domain-daemon).

> **Heads up — patching the upstream chart.** The nvml-mock chart wires
> `NODE_NAME` (downward API), `MOCK_TOPOLOGY_CONFIG`, and the topology
> ConfigMap mount onto its own DaemonSet, which is why `check-fabric`
> running inside the mock pod sees the per-node fabric identity for
> free. The upstream `compute-domain-daemon` pod gets *none* of that
> from its own chart, so if you swap its image for the
> `Dockerfile.compute-domain-daemon` overlay above you also have to
> patch the upstream values to:
>
> 1. inject `NODE_NAME` via the downward API
>    (`fieldRef: spec.nodeName`),
> 2. set `MOCK_TOPOLOGY_CONFIG=/config/topology.yaml` (or mount the
>    topology ConfigMap at whatever path you prefer and point this env
>    var at it),
> 3. mount the `nvml-mock-topology` ConfigMap at that path.
>
> Without those three additions the linked `libnvidia-ml.so` cannot
> look up the node in the topology and falls back to the per-profile
> defaults — the daemon will think every node is in the same clique.

## Topology / clique layout used by the demo

```text
domain "demo-domain"           uuid 00000000-0000-0000-0000-0000000000ab
  clique 0:
    - nvml-mock-compute-domain-worker
    - nvml-mock-compute-domain-worker2
  clique 1:
    - nvml-mock-compute-domain-worker3
    - nvml-mock-compute-domain-worker4
```

The full values fragment lives at [`topology.yaml`](./topology.yaml)
and is passed to Helm with `-f topology.yaml` (not `--set-file`, which
would inline the file as a string literal rather than as a parsed
list).

## Clean up

```bash
kind delete cluster --name nvml-mock-compute-domain
```
