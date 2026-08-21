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
* **NRI-delivered mock stack** — the chart stages mock files on every
  node, and containerd NRI injects that overlay, the node-specific topology
  environment, and annotated IMEX channel nodes into a separate demo workload
  DaemonSet. The demo workload has no mock mounts or mock environment.
* **Real `nvidia-imex` in NO GPU mode** — the demo workload image fronts
  the real daemon with `imex-nogpu-shim` (`/usr/bin/nvidia-imex` exec's
  `/usr/bin/nvidia-imex.real --nogpu`), so IMEX readiness is the real
  gRPC peer protocol over the pod network: `nvidia-imex-ctl -q` prints
  `READY`, `-N -j` reports the domain `UP` with version `NO_GPU`, and
  killing a peer daemon degrades the domain.

The demo lives in its own cluster (`nvml-mock-compute-domain`) and its
own 4-worker Kind topology
([`tests/e2e/kind-compute-domain-config.yaml`](https://github.com/NVIDIA/k8s-test-infra/blob/main/tests/e2e/kind-compute-domain-config.yaml)).

## Prerequisites

The demo expects the following tools on `$PATH`:

| Tool      | Tested version | Notes |
|---        |---             |---    |
| `docker`  | 24+            | Daemon must be running. Multi-stage builds use the repo's pinned Go base (hack/golang-version.sh). |
| `kind`    | v0.24+         | Provisions the demo's dedicated 4-worker cluster. |
| `kubectl` | v1.30+         | Used for `exec`, `rollout`, `get` against the in-cluster pods. |
| `helm`    | v3.13+ (v4 works) | Chart install + `helm upgrade --reuse-values`. |
| `bash`    | 3.2+           | `run.sh` uses `set -euo pipefail` — no bash 4+ features. |
| `jq`      | any recent     | Scenario 2 parses `nvidia-imex-ctl -N -j` JSON. |

## What the script does

1. Creates the dedicated Kind cluster. An existing cluster is reused only when
   containerd NRI is enabled on every expected node. A legacy cluster created
   by an older version of the demo is rejected; set `FORCE_RECREATE=true` to
   explicitly delete and recreate it.
2. Builds and loads the standard `nvml-mock:compute-domain` image, then
   builds the `nvml-mock:compute-domain-workload` image from the demo's
   [`Dockerfile`](./Dockerfile). It contains the real `nvidia-imex` (NO GPU
   mode via `imex-nogpu-shim`) but no mock GPU files.
3. Installs the chart with:

   ```text
   gpu.profile=gb200
   topology.enabled=true
   topology.domains=<demo topology>
   nri.enabled=true
   imex.mockChannels.enabled=true
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
4. Recycles the staging and NRI DaemonSets in order, then creates a separate
   `compute-domain-workload` namespace and deploys the freshly restarted
   `compute-domain-demo-workload` DaemonSet. Its only mock-related configuration
   is the `nvml-mock.nvidia.com/imex-channels: "true"` annotation. NRI supplies
   the mock NVML overlay and per-node topology identity at container creation.
   The demo workload manifest also installs its own ingress NetworkPolicy,
   allowing TCP 50000 and 50005 only from peer
   `app.kubernetes.io/name=compute-domain-demo-workload` pods in that same
   namespace while leaving egress unrestricted.
   Before installation the script selects two character-device majors unused
   across every Kind node. This avoids collisions with host-kernel assignments
   such as `hidraw` on major 236.
5. **Scenario 1 — per-node fabric identity**. Runs NRI-injected
   `check-fabric` inside one demo workload pod per worker and asserts:
   * `nvml-mock-compute-domain-worker` / `-worker2` report **clique 0**,
   * `-worker3` / `-worker4` report **clique 1**,
   * every node reports the demo cluster UUID
     `00000000-0000-0000-0000-0000000000ab` and `state=completed`.
6. **Scenario 2 — real IMEX domain (NO GPU mode)**. First verifies the
   NRI-injected channel device, then renders a per-pod
   IMEX config, starts the real `nvidia-imex` (the shim appends
   `--nogpu`) in both clique-0 pods, and asserts three transitions:
   * daemon A alone → local `-q` probe `READY`, domain not `UP`,
   * daemon B joins → `-N -j` reports `UP`, 2/2 nodes `READY`,
     version `NO_GPU` (real gRPC over the pod network),
   * daemon B SIGTERMed → domain leaves `UP` (real liveness — the
     deprecated marker files couldn't detect a dead peer).
7. **Scenario 3 — topology rebinding (no image rebuild)**. A
   `helm upgrade --reuse-values` swaps the topology document so every
   node is now a member of clique 99 in a brand-new domain UUID. After
   a forced DaemonSet recycle, `check-fabric` reflects the new
   identity on every worker. This is the gear the real
   compute-domain-controller would shift between integration tests.

## Quick start

```bash
./docs/demo/compute-domain/run.sh
```

To explicitly replace an existing cluster, including a legacy cluster that
predates the demo's NRI configuration:

```bash
FORCE_RECREATE=true ./docs/demo/compute-domain/run.sh
```

`FORCE_RECREATE=true` deletes the entire `nvml-mock-compute-domain` Kind
cluster before recreating it. The default is to preserve a compatible existing
cluster and to stop with an actionable error for an incompatible one.

The script locates the repository itself, so it works from any
directory; the **manual-reproduction commands below assume you run
them from the repository root**.

Expect roughly 10–20 minutes on a first run (Kind cluster creation
plus two image builds dominate; Scenario 2's domain convergence alone
may legitimately take up to 4 minutes — see Troubleshooting). Reruns
reuse the existing cluster and build caches, but deliberately recycle the
staging, NRI, and demo workload DaemonSets so same-tag image rebuilds, restored
topology, and cleanup after a failed run are deterministic.

> **One runner per host.** The cluster name, Helm release, and image
> tags are fixed, so two concurrent runs of this demo on the same
> machine will corrupt each other's Helm revisions and topology
> assertions. Run one at a time.

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

# 2. Build the standard mock image, then the separate real-IMEX demo workload
#    image. nvidia-imex is proprietary but comes
#    from the PUBLIC Ubuntu 22.04 multiverse repo (nvidia-imex-595) —
#    no NVIDIA credentials or internal access needed. Local build only:
#    never publish the resulting image.
docker build -t nvml-mock:compute-domain -f deployments/nvml-mock/Dockerfile .
docker build -t nvml-mock:compute-domain-workload \
    --build-arg GOLANG_VERSION=$(hack/golang-version.sh) \
    -f docs/demo/compute-domain/Dockerfile .

# 3. Load both images into the Kind cluster.
kind load docker-image nvml-mock:compute-domain nvml-mock:compute-domain-workload \
    --name nvml-mock-compute-domain

# 4. Pick two character-device majors that are unused on every Kind node.
#    Reusing a host-kernel major would make the mock /proc/devices inconsistent.
KIND_NODES=(
  nvml-mock-compute-domain-control-plane
  nvml-mock-compute-domain-worker
  nvml-mock-compute-domain-worker2
  nvml-mock-compute-domain-worker3
  nvml-mock-compute-domain-worker4
)
used_majors=$(for node in "${KIND_NODES[@]}"; do
  docker exec "${node}" awk '$1 ~ /^[0-9]+$/ { print $1 }' /proc/devices
done | sort -nu | tr '\n' ' ')

for candidate in $(seq 240 4095); do
  case " ${used_majors} " in
    *" ${candidate} "*) ;;
    *) IMEX_CHANNEL_MAJOR=${candidate}; break ;;
  esac
done
if [[ -z "${IMEX_CHANNEL_MAJOR:-}" ]]; then
  echo "Could not find an unused IMEX channel device major" >&2
  exit 1
fi
used_majors="${used_majors} ${IMEX_CHANNEL_MAJOR}"

for candidate in $(seq 240 4095); do
  case " ${used_majors} " in
    *" ${candidate} "*) ;;
    *) IMEX_CAPS_MAJOR=${candidate}; break ;;
  esac
done
if [[ -z "${IMEX_CAPS_MAJOR:-}" ]]; then
  echo "Could not find an unused IMEX caps device major" >&2
  exit 1
fi
printf 'Using IMEX device majors: channels=%s, caps=%s\n' \
    "${IMEX_CHANNEL_MAJOR}" "${IMEX_CAPS_MAJOR}"

# 5. Install mock staging + the node-local NRI plugin. The chart's
#    DaemonSet uses the standard mock image; the real IMEX binary stays in a
#    separate demo workload image.
helm upgrade --install nvml-mock deployments/nvml-mock/helm/nvml-mock \
    --kube-context kind-nvml-mock-compute-domain \
    --namespace mokka --create-namespace \
    -f docs/demo/compute-domain/topology.yaml \
    --set image.repository=nvml-mock \
    --set image.tag=compute-domain \
    --set gpu.profile=gb200 \
    --set nri.enabled=true \
    --set imex.mockChannels.enabled=true \
    --set imex.mockChannels.channelMajor="${IMEX_CHANNEL_MAJOR}" \
    --set imex.mockChannels.capsMajor="${IMEX_CAPS_MAJOR}" \
    --set-string updateStrategy.rollingUpdate.maxUnavailable=100% \
    --set terminationGracePeriodSeconds=1 \
    --wait --timeout 180s

# 6. Refresh staging first and NRI second so the plugin registers the current
#    files and topology before any demo workload container is created.
kubectl --context kind-nvml-mock-compute-domain -n mokka \
    rollout restart daemonset/nvml-mock
kubectl --context kind-nvml-mock-compute-domain -n mokka \
    rollout status daemonset/nvml-mock --timeout=180s
kubectl --context kind-nvml-mock-compute-domain -n mokka \
    rollout restart daemonset/nvml-mock-nri
kubectl --context kind-nvml-mock-compute-domain -n mokka \
    rollout status daemonset/nvml-mock-nri --timeout=180s

# 7. Idempotently create the workload namespace, apply the demo workload
#    and its namespace-local peer ingress policy, then restart the DaemonSet so
#    reruns discard stale IMEX processes and injected data.
kubectl --context kind-nvml-mock-compute-domain \
    create namespace compute-domain-workload --dry-run=client -o yaml | \
  kubectl --context kind-nvml-mock-compute-domain apply -f -
kubectl --context kind-nvml-mock-compute-domain -n compute-domain-workload \
    apply -f docs/demo/compute-domain/demo-workload.yaml
kubectl --context kind-nvml-mock-compute-domain -n compute-domain-workload \
    rollout restart daemonset/compute-domain-demo-workload
kubectl --context kind-nvml-mock-compute-domain -n compute-domain-workload \
    rollout status daemonset/compute-domain-demo-workload --timeout=180s

# 8. Verify the NRI-delivered per-node fabric overlay (Scenario 1).
for node in nvml-mock-compute-domain-{worker,worker2,worker3,worker4}; do
  pod=$(kubectl --context kind-nvml-mock-compute-domain \
    -n compute-domain-workload get pods \
    -l app.kubernetes.io/name=compute-domain-demo-workload \
    --field-selector="spec.nodeName=${node},status.phase=Running" \
    -o jsonpath='{.items[0].metadata.name}')
  echo "=== ${node} (pod=${pod}) ==="
  kubectl --context kind-nvml-mock-compute-domain \
    -n compute-domain-workload exec "${pod}" -- \
    sh -c 'test -c /dev/nvidia-caps-imex-channels/channel0 && check-fabric | head -6'
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
[`deployments/nvml-mock/Dockerfile.compute-domain-daemon`](https://github.com/NVIDIA/k8s-test-infra/blob/main/deployments/nvml-mock/Dockerfile.compute-domain-daemon).

> **Using the upstream daemon chart.** With NRI enabled, its workload pod
> does not need a mock driver mount, a topology ConfigMap mount, or manually
> authored `NODE_NAME` / `MOCK_TOPOLOGY_CONFIG` variables: the node-local NRI
> plugin injects those at container creation. It does need the
> `nvml-mock.nvidia.com/imex-channels: "true"` annotation while the mock uses
> channel nodes, and its namespace must not be one excluded by the NRI chart.

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

## Troubleshooting

* **Scenario 2 seems stuck on "domain status UP".** First convergence
  after a fresh rollout can take a few minutes: kind's CNI (kindnetd)
  reconciles NetworkPolicy asynchronously, and the IMEX daemon retries
  failed peer connections with exponential backoff (15s, 31s, 62s,
  125s…). The script already waits up to 240s — let it finish before
  concluding failure. On timeout it prints the daemon log tail.
* **Peers never connect at all.** The demo workload has its own NetworkPolicy
  in [`demo-workload.yaml`](./demo-workload.yaml). It selects
  `app.kubernetes.io/name=compute-domain-demo-workload` and admits TCP 50000
  (IMEX gRPC peer) and 50005 (command/status) only from matching peer pods in
  the same
  `compute-domain-workload` namespace; egress remains unrestricted. kindnetd
  **enforces** NetworkPolicy on current kind releases, so add any new workload
  listener to that policy. The chart's separate `network-policy-ibping.yaml`
  selects nvml-mock daemon pods, not this NRI-injected demo workload. Its IMEX
  port allowances remain for backwards compatibility and custom chart images
  that run IMEX inside the chart-managed DaemonSet.
* **Rerunning after a failed Scenario 2.** A run that dies mid-scenario
  can leave real `nvidia-imex` daemons holding port 50000 inside the pods.
  Normal reruns restart the `compute-domain-demo-workload` DaemonSet after
  refreshing staging and NRI, so they discard those stale processes
  automatically. To recycle the demo workload pods manually, run
  `kubectl --context kind-nvml-mock-compute-domain -n compute-domain-workload
  delete pods -l app.kubernetes.io/name=compute-domain-demo-workload`; the
  DaemonSet recreates them clean.
* **`nvidia-imex-ctl -N -j` shows the peer `UNAVAILABLE` with an empty
  version while connections look established.** Node status and version
  are exchanged over the IMEX *command* port (50005), separate from the
  gRPC peer port (50000). If only 50000 is reachable the domain sticks
  at `DEGRADED` — check that both ports are admitted (see the
  NetworkPolicy bullet above).

## Clean up

```bash
kind delete cluster --name nvml-mock-compute-domain
# Optional: also remove the locally built demo images.
docker rmi nvml-mock:compute-domain nvml-mock:compute-domain-workload
```
