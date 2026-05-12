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
* **Fake `nvidia-imex` / `nvidia-imex-ctl`** — file-based readiness
  protocol over a shared hostPath. `nvidia-imex-ctl` exits 0 + prints
  `READY` only when every peer in `nodes.cfg` has dropped a marker
  file; SIGTERM-ing a daemon removes its marker and trips the probe.

The demo lives in its own cluster (`nvml-mock-compute-domain`) and its
own 4-worker Kind topology
([`tests/e2e/kind-compute-domain-config.yaml`](../../../tests/e2e/kind-compute-domain-config.yaml))
that mounts `/tmp/nvml-mock-imex-state` from the host into every worker
node container — the cross-node shared volume the real
compute-domain-daemon would normally get from a CSI driver.

## What the script does

1. (Re)creates the dedicated Kind cluster (idempotent: an existing
   `nvml-mock-compute-domain` cluster is reused) and clears
   `/tmp/nvml-mock-imex-state` so the IMEX assertions start from a
   clean slate.
2. Builds and loads the `nvml-mock:compute-domain` image, which
   bundles three new binaries on top of the standard nvml-mock image:
   `/usr/bin/nvidia-imex`, `/usr/bin/nvidia-imex-ctl`, and
   `/usr/local/bin/check-fabric`.
3. Installs the chart with:

   ```text
   gpu.profile=gb200
   topology.enabled=true
   topology.domains=<demo topology>
   imex.enabled=true
   ```

   This renders both ConfigMaps (`nvml-mock-config` and
   `nvml-mock-topology`) and mounts the shared hostPath state
   directory into every pod.

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
5. **Scenario 2 — fake IMEX coordination**. Hand-writes a `nodes.cfg`
   listing both clique-0 pod IPs and walks the readiness probe
   through three transitions:
   * 1 of 2 markers present → `nvidia-imex-ctl` exits 1,
   * 2 of 2 markers present → `nvidia-imex-ctl` prints `READY`,
   * peer SIGTERMed → marker removed → `nvidia-imex-ctl` exits 1.

   The script reads the markers directly off the host filesystem
   (`/tmp/nvml-mock-imex-state/<pod-ip>`) to prove the coordination
   actually traverses the shared volume.
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

## How the fakes fit alongside the real compute-domain-daemon

The upstream daemon spawns `nvidia-imex` as a subprocess and runs
`nvidia-imex-ctl -c /imexd/imexd.cfg -q` from its readiness probe. With
this demo's image installed, both binaries are already in `$PATH` at
the canonical locations. To run the real daemon against this cluster
without modifying it, point its container image at
[`deployments/nvml-mock/Dockerfile.compute-domain-daemon`](../../../deployments/nvml-mock/Dockerfile.compute-domain-daemon),
which is a 2-line `FROM upstream-daemon` overlay that copies in the
fakes from the nvml-mock image.

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
rm -rf /tmp/nvml-mock-imex-state
```
