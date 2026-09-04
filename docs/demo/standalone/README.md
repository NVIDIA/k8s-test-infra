# Standalone nvml-mock Demo

This demo deploys nvml-mock with FGO-style labels enabled into the cluster
your current context points at. It does not require any external GPU operator
-- nvml-mock itself
generates the GPU profile ConfigMaps, the fake InfiniBand sysfs tree, and
the node labels that downstream consumers expect.

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

> **One nvml-mock demo per cluster at a time.** The chart's hostPath mounts
> (`/var/lib/nvml-mock`, `/var/run/cdi`, `/run/nvidia`, the NFD features
> directory) are scoped by neither release nor namespace, and the DaemonSet
> tolerates every node, so two nvml-mock releases write the same per-node host
> state even from different namespaces. The demo refuses to install with exit
> `4` if the [failure-injection demo](../failure-injection/README.md) is
> already present in the target namespace.
>
> This matters beyond the demos: the shared
> `/var/lib/nvml-mock/driver/config/config.yaml` is what the CDI spec points
> `MOCK_NVML_CONFIG` at, so after a co-located run **any real GPU workload on
> those nodes reads whichever config was written last**, which may be a
> failure-injected one. Each demo's own pods mount their own ConfigMap and so
> look fine throughout.

Docker and Kind are needed only for `BUILD_LOCAL=true`, which builds the image
from source and side-loads it into a Kind cluster named `nvml-mock-demo`,
creating that cluster if it is missing. The default path pulls the published
image and never creates or deletes a cluster.

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
HELM_TIMEOUT=30m ./demo.sh
```

Note that `ghcr.io/nvidia/nvml-mock:latest` is a floating tag, so a cluster
holding an older cached `:latest` can run a build that does not match this
chart. Pin `NVML_MOCK_IMAGE` to a released tag if you need a fixed pairing.

## What it does

1. Announces the target: context, API server, node count, and the namespace
   the workload lands in. Unless that target is a throwaway Kind cluster, it
   then asks you to type the context name back before installing anything.
   The chart runs a privileged DaemonSet with hostPath mounts, so this is the
   guard that replaced the old hardcoded Kind context.

   "Throwaway" means the context is named `kind-*` **and** its API server is
   loopback. The name alone is not enough: `kubectl config rename-context prod
   kind-prod` would otherwise buy a silent pass.

   It **fails closed** when nobody can answer. A non-interactive run (a CI
   job, a wrapper script, `./demo.sh </dev/null`) against anything but a
   throwaway cluster exits `4` rather than installing unattended. To proceed
   deliberately without a terminal, set `DEMO_ASSUME_YES=true`:

   ```bash
   DEMO_ASSUME_YES=true ./demo.sh
   ```

   Exit codes: `2` no usable cluster, `3` missing or too-old tooling, `4`
   confirmation declined, impossible, or refused because the other demo is
   already installed in the target namespace, `5` a configuration the chart cannot
   express (a digest-pinned `NVML_MOCK_IMAGE`).
2. With `BUILD_LOCAL=true` only: creates the `nvml-mock-demo` Kind cluster
   (1 control-plane, 3 workers) if missing, builds the `nvml-mock:demo` image
   from the repository root, and loads it into that cluster. Otherwise it uses
   `ghcr.io/nvidia/nvml-mock:latest` (override with `NVML_MOCK_IMAGE`).
3. Installs the nvml-mock Helm chart into a dedicated `mokka`
   namespace (override with `NAMESPACE=...`) with
   `integrations.fakeGpuOperator.enabled=true`, an H100 profile, and 8 GPUs
   per node. The demo sets this namespace as the current context default so the
   validation helpers resolve pods in it.
4. Verifies the deployment:
   - DaemonSet pods are running on all workers.
   - Seven GPU profile ConfigMaps are created, one per GPU model.
   - `nvidia-smi` runs successfully inside a pod.
   - `ibstat` lists 8 simulated ConnectX-7 NDR HCAs (see
     [`internal/ib/README.md`](https://github.com/NVIDIA/k8s-test-infra/blob/main/internal/ib/README.md)).
   - `ibv_devinfo -l` enumerates every mock HCA (via libmlx5) and
     `ibstatus` confirms ACTIVE / LinkUp ports, both driven by
     [`tests/e2e/validate-ibv-devinfo.sh`](https://github.com/NVIDIA/k8s-test-infra/blob/main/tests/e2e/validate-ibv-devinfo.sh).
   - Cross-node `ibping` between two worker pods via
     [`tests/e2e/validate-ibping.sh`](https://github.com/NVIDIA/k8s-test-infra/blob/main/tests/e2e/validate-ibping.sh).
   - Cross-node `iblinkinfo` fabric direct-route scan via
     [`tests/e2e/validate-iblinkinfo.sh`](https://github.com/NVIDIA/k8s-test-infra/blob/main/tests/e2e/validate-iblinkinfo.sh).
   - Node labels are present.

   One check is Kind-only: the NVLink / NVSwitch topology assertion runs the
   host-driver-root `nvidia-smi` through `docker exec` on the node container,
   so it is skipped, not failed, when the node is not a local Kind container.

## Quick start

```bash
# Installs into your current context, using the published image.
./demo.sh

# Or build from source and side-load into a throwaway Kind cluster:
BUILD_LOCAL=true ./demo.sh
```

## Clean up

```bash
# Remove just the release (keeps the cluster). The -n and --kube-context are
# load-bearing: without them helm resolves the release from whatever context
# and namespace happen to be current, which is not necessarily where the demo
# installed it. The demo prints this line with both filled in.
helm uninstall nvml-mock -n mokka --kube-context <your-context>

# The demo set the namespace default on your context. It prints the exact
# command to restore whatever was there before; it does NOT assume "default".
kubectl config set-context <your-context> --namespace=<your-previous-namespace>
```

If you ran with `BUILD_LOCAL=true`, that run also created the Kind cluster, so
tear it down too:

```bash
kind delete cluster --name nvml-mock-demo
```
