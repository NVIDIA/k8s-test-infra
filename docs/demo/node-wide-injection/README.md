# Node-Wide nvml-mock Injection Demo

This demo shows the NRI-based node-wide injection path: ordinary pods can run
`nvidia-smi` without requesting `nvidia.com/gpu`, adding annotations, or having
their pod specs mutated by an admission webhook.

It also demonstrates that node-wide injection carries **ComputeDomain fabric
identity**: on a multi-node cluster with a topology overlay, each NRI-injected
pod reports the NVLink clique / cluster UUID assigned to *its* node — with no
`nvidia.com/gpu` request and no `MOCK_*` env in the pod spec. This reuses the
same topology mechanism as the [compute-domain demo](../compute-domain/README.md), but
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
   `mokka` namespace, plus the ComputeDomain topology overlay
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

The demo installs no device plugin, so no component allocates GPUs. The NRI overlay and environment are injected ambiently into containers in non-excluded namespaces. Host device node injection remains opt-in (via `nvidia.com/gpu` requests or the `nvml-mock.nvidia.com/devices: "true"` annotation). Unannotated pods without GPU requests will still report GPUs if `nvidia-smi` is run inside them. Where the NVIDIA device plugin is installed and allocates GPUs, the NRI plugin leaves that allocation intact (MEP-0002). Tests expecting non-GPU pods to see zero GPUs should keep NRI disabled or run in an excluded namespace. See [Device injection mode](../../helm-chart.md#device-injection-mode).

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
NVML_MOCK_NAMESPACE=my-mokka ./run.sh
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

The boundary is the same whichever mechanism delivers the devices. Setting
`nri.deviceInjectionMode=cdi` makes the runtime resolve them from a CDI spec
instead of having the plugin stage them, but the annotation that triggers it is
still pod-authored. See
[Device injection mode](../../helm-chart.md#device-injection-mode).

## When Injection Stops

The plugin writes the mock GPU stack into each container's OCI spec **at
creation time**. A pod that is already running keeps everything it was given,
whatever happens to the plugin afterwards. Only containers created after a
failure are affected, and they are affected silently.

So a demo whose pods still print GPUs is not evidence that the node is still
injecting. There are two ways it stops:

| | Fail-closed | Fail-open |
|---|---|---|
| What the runtime does | Refuses to create the container | Unregisters the timed-out plugin, then creates containers without it |
| What you see | Pods stuck in `ContainerCreating` / `CreateContainerError` | Pods start normally, with **no mock GPU stack** |

Fail-closed announces itself. Fail-open is the one to plan for: containerd
decides it, the plugin cannot prevent it, and nothing in the workload reports it.

The `nvml-mock-nri` DaemonSet carries two probes that make that window visible
rather than preventing it:

- **`/readyz`** reports serving only while the plugin is registered with the
  runtime *and* answering. A node that has stopped injecting shows up as a
  NotReady pod and a short DaemonSet count. It restarts nothing; it is purely
  the detection surface.
- **`/healthz`** fails only when a container-creation request has been in flight
  past the wedge threshold, and restarts the container into a fresh
  registration.

This demo happens to catch the loss, because `gpu-agent` asserts on
`/opt/nvml-mock` before anything else and crashes when it is missing. That is a
property of this workload, not of NRI: a pod without such a self-test starts
normally, exits 0, and simply never sees a GPU.

For the posture behind both probes, the wedge threshold's relationship to
containerd's `plugin_request_timeout`, and per-node triage, see
[NRI plugin failure modes](../../helm-chart.md#nri-plugin-failure-modes)
in the chart README.

## Manual Checks

`run.sh` pins every call to the demo's own kubeconfig context so it can never
act on another cluster. Do the same by hand: `kind create cluster` makes that
context current only when it first creates the cluster, so on the documented
re-run path these commands otherwise resolve against whatever context happens to
be current — which may have a `mokka` namespace of its own and answer from the
wrong cluster.

```bash
# Is the node still injecting? READY must equal DESIRED on nvml-mock-nri.
kubectl --context kind-nvml-mock-node-wide-demo -n mokka get daemonset nvml-mock nvml-mock-nri

# Which nodes are injecting right now, and why one is not
kubectl --context kind-nvml-mock-node-wide-demo -n mokka get pods -l app.kubernetes.io/name=nvml-mock-nri -o wide
kubectl --context kind-nvml-mock-node-wide-demo -n mokka describe pod -l app.kubernetes.io/name=nvml-mock-nri

# The workload itself
kubectl --context kind-nvml-mock-node-wide-demo -n default get daemonset gpu-agent
kubectl --context kind-nvml-mock-node-wide-demo -n default logs daemonset/gpu-agent --tail=80
```

Substitute the namespaces if you set `NVML_MOCK_NAMESPACE` or
`WORKLOAD_NAMESPACE`. Both mock DaemonSets run on the control-plane node as well,
so they report one more pod than `gpu-agent`, which is pinned to the four
workers.

The `gpu-agent` pod spec stays plain; the mock GPU stack is injected by
containerd NRI when each container is created.

## Clean Up

```bash
kind delete cluster --name nvml-mock-node-wide-demo
```

If you used a shared cluster instead of deleting the Kind cluster, remove just
the demo resources:

```bash
kubectl --context kind-nvml-mock-node-wide-demo -n default delete daemonset gpu-agent --ignore-not-found
helm uninstall nvml-mock --kube-context kind-nvml-mock-node-wide-demo --namespace mokka --ignore-not-found
kubectl --context kind-nvml-mock-node-wide-demo delete namespace mokka --ignore-not-found
```
