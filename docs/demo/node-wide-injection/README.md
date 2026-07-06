# Node-Wide nvml-mock Injection Demo

This demo shows the NRI-based node-wide injection path: ordinary pods can run
`nvidia-smi` without requesting `nvidia.com/gpu`, adding annotations, or having
their pod specs mutated by an admission webhook.

## Prerequisites

Install these tools locally before running the demo:

- `docker`
- `kind`
- `kubectl`
- `helm`

## What It Does

1. Creates a Kind cluster with containerd NRI enabled.
2. Builds and loads the local `nvml-mock` image.
3. Installs the Helm chart with the `nvml-mock-nri` DaemonSet enabled in the
   `nvml-mock-system` namespace.
4. Uses `default` as the workload namespace. The NRI plugin excludes its own
   Helm release namespace and `kube-system`, so keeping workloads in `default`
   demonstrates injection into ordinary application pods.
5. Starts an ordinary `gpu-agent` DaemonSet in the workload namespace:
   - no `nvidia.com/gpu` request;
   - no hostPath or mock-library volumes;
   - no `LD_PRELOAD`, `MOCK_*`, or `PATH` env.
6. Starts three self-checking ordinary pods:
   - `node-wide-plain`: no annotations, no GPU request; verifies the ambient
     overlay and `nvidia-smi -L`.
   - `node-wide-opt-out`: `nvml-mock.nvidia.com/inject: "false"`; verifies the
     overlay and `nvidia-smi` are absent.
   - `node-wide-device-opt-in`: `nvml-mock.nvidia.com/devices: "true"`;
     verifies `/dev/nvidia0` is injected.
7. Verifies:
   - `gpu-agent` runs `nvidia-smi` from the ambient NRI injection;
- the plain pod self-check sees `/opt/nvml-mock` and can run `nvidia-smi -L`;
   - the pod spec still has no injected volumes or env;
- the opt-out pod self-check does not see the overlay;
- the device opt-in pod self-check gets `/dev/nvidia0`.

## Quick Start

```bash
./run.sh
```

The script is safe to re-run. It reuses the existing Kind cluster unless
`FORCE_RECREATE=true` is set, rebuilds the local image, reloads it into Kind,
and redeploys the demo workloads.

Optional overrides:

```bash
GPU_PROFILE=t4 GPU_COUNT=4 ./run.sh
NVML_MOCK_NAMESPACE=my-nvml-mock-system ./run.sh
WORKLOAD_NAMESPACE=my-demo ./run.sh
FORCE_RECREATE=true ./run.sh
```

`WORKLOAD_NAMESPACE` must be different from `NVML_MOCK_NAMESPACE` and should not
be `kube-system`, because those namespaces are excluded from NRI injection.

## Manual Checks

After the script completes, the important checks are:

```bash
kubectl -n nvml-mock-system get daemonset nvml-mock nvml-mock-nri
kubectl get daemonset gpu-agent
kubectl logs daemonset/gpu-agent --tail=80
kubectl get pods node-wide-plain node-wide-opt-out node-wide-device-opt-in
kubectl logs pod/node-wide-plain
kubectl logs pod/node-wide-opt-out
kubectl logs pod/node-wide-device-opt-in
```

The `gpu-agent` and demo pod specs should remain plain; the mock GPU stack is
injected by containerd NRI when each container is created.

## Clean Up

```bash
kind delete cluster --name nvml-mock-node-wide-demo
```

If you used a shared cluster instead of deleting the Kind cluster, remove just
the demo resources:

```bash
kubectl delete daemonset gpu-agent --ignore-not-found
kubectl delete pod node-wide-plain node-wide-opt-out node-wide-device-opt-in --ignore-not-found
helm uninstall nvml-mock --namespace nvml-mock-system --ignore-not-found
kubectl delete namespace nvml-mock-system --ignore-not-found
```
