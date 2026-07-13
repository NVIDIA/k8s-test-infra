# Node-Wide nvml-mock Injection Demo

This demo shows the NRI-based node-wide injection path: ordinary pods can run
`nvidia-smi` without requesting `nvidia.com/gpu`, adding annotations, or having
their pod specs mutated by an admission webhook.

It also demonstrates that node-wide injection carries **ComputeDomain fabric
identity**: on a multi-node cluster with a topology overlay, each NRI-injected
pod reports the NVLink clique / cluster UUID assigned to *its* node — with no
`nvidia.com/gpu` request and no `MOCK_*` env in the pod spec. This reuses the
same topology mechanism as the [compute-domain demo](../compute-domain), but
delivered ambiently through NRI instead of the nvml-mock DaemonSet pod.

## Prerequisites

Install these tools locally before running the demo:

- `docker`
- `kind`
- `kubectl`
- `helm`

## How ComputeDomain identity reaches injected pods

1. `topology.enabled=true` renders a cluster-level topology ConfigMap and
   mounts it into the nvml-mock DaemonSet pod.
2. `setup.sh` copies that topology document into the overlay tree
   (`/var/lib/nvml-mock/topology/topology.yaml`) that the NRI plugin
   bind-mounts into workloads, and stages the `check-fabric` consumer.
3. The `nvml-mock-nri` plugin, knowing its own `NODE_NAME` (downward API),
   injects `NODE_NAME` and `MOCK_TOPOLOGY_CONFIG` into each container whenever
   a topology document is staged.
4. Inside the workload, the mock NVML engine's `applyTopologyOverlay()` looks
   up `NODE_NAME` and rewrites every GPU's `clusterUuid` / `cliqueId`, so
   `nvidia-smi -q` and `check-fabric` report the node's ComputeDomain identity.

## What It Does

1. Creates a 4-worker Kind cluster with containerd NRI enabled.
2. Builds and loads the local `nvml-mock` image.
3. Installs the Helm chart with the `nvml-mock-nri` DaemonSet enabled in the
   `nvml-mock-system` namespace, plus the ComputeDomain topology overlay
   (`gb200` profile; workers 1-2 -> clique 0, workers 3-4 -> clique 1).
4. Uses `default` as the workload namespace. The NRI plugin excludes its own
   Helm release namespace and `kube-system`, so keeping workloads in `default`
   demonstrates injection into ordinary application pods.
5. Starts an ordinary `gpu-agent` DaemonSet in the workload namespace:
   - no `nvidia.com/gpu` request;
   - no hostPath or mock-library volumes;
   - no `LD_PRELOAD`, `MOCK_*`, or `PATH` env.
   Its self-test asserts the ambient overlay (`/opt/nvml-mock`) and `nvidia-smi`
   are present, then runs `check-fabric`; the script asserts every node reports
   its assigned clique / cluster UUID (skip with `WITH_COMPUTE_DOMAIN=false`).

## Quick Start

```bash
./run.sh
```

The script is safe to re-run. It reuses the existing Kind cluster unless
`FORCE_RECREATE=true` is set, rebuilds the local image, reloads it into Kind,
and redeploys the demo workloads.

Optional overrides:

```bash
GPU_PROFILE=t4 GPU_COUNT=4 WITH_COMPUTE_DOMAIN=false ./run.sh
NVML_MOCK_NAMESPACE=my-nvml-mock-system ./run.sh
WORKLOAD_NAMESPACE=my-demo ./run.sh
FORCE_RECREATE=true ./run.sh
```

The ComputeDomain checks require a fabric-attached profile (default `gb200`).
Set `WITH_COMPUTE_DOMAIN=false` to run plain node-wide injection on a
non-fabric profile such as `t4`.

`WORKLOAD_NAMESPACE` must be different from `NVML_MOCK_NAMESPACE` and should not
be `kube-system`, because those namespaces are excluded from NRI injection.

## Trust Boundary

The NRI plugin treats the configured device annotation
(`nvml-mock.nvidia.com/devices=true` by default) as pod-authored opt-in for
mounting host GPU device nodes from the staged mock overlay. Run the demo only
in trusted workload namespaces, or add namespaces to `nri.excludedNamespaces`
when pod authors should not control that device opt-in.

## Manual Checks

After the script completes, the important checks are:

```bash
kubectl -n nvml-mock-system get daemonset nvml-mock nvml-mock-nri
kubectl get daemonset gpu-agent
kubectl logs daemonset/gpu-agent --tail=80
```

The `gpu-agent` pod spec stays plain; the mock GPU stack is injected by
containerd NRI when each container is created.

## Clean Up

```bash
kind delete cluster --name nvml-mock-node-wide-demo
```

If you used a shared cluster instead of deleting the Kind cluster, remove just
the demo resources:

```bash
kubectl delete daemonset gpu-agent --ignore-not-found
helm uninstall nvml-mock --namespace nvml-mock-system --ignore-not-found
kubectl delete namespace nvml-mock-system --ignore-not-found
```
