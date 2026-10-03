# Set Up NRI Injection

Mokka's [NRI plugin](../components/nri-plugin.md) is how a workload that was
given GPUs the usual way runs `nvidia-smi` and loads the mock NVML library, with
no Mokka-specific pod spec changes. This page covers what the node needs, how to
install Mokka with the plugin, and how to check that it injects.

## What the plugin does

The plugin runs as the `nvml-mock-nri` container in the node DaemonSet. As each
container is created, it adds the mock driver to the ones that should have it:

- A container holding a GPU allocation from the NVIDIA device plugin or the
  NVIDIA DRA driver gets the mock driver and keeps exactly its allocated GPUs.
- A pod annotated `nvml-mock.nvidia.com/devices: "true"`, such as a node-wide
  monitoring agent, gets every mock GPU on the node without a GPU request.
- InfiniBand and IMEX channels are selected separately, each by its own
  annotation.
- Every other container is left exactly as authored.

[Which containers are injected](../components/nri-plugin.md#which-containers-are-injected)
holds the full rules, and [Annotations](../components/nri-plugin.md#annotations)
lists every annotation.

## Prerequisites

- containerd 1.7 or later, with NRI enabled on every GPU node. Check a node's
  effective configuration:

    ```bash
    containerd config dump | grep -A3 'io.containerd.nri.v1.nri'
    #   [plugins.'io.containerd.nri.v1.nri']
    #     disable = false
    #     socket_path = '/var/run/nri/nri.sock'
    #     plugin_path = '/opt/nri/plugins'
    ```

    That is containerd 2.x. containerd 1.7 prints the same keys with double
    quotes.

- For a NVIDIA device plugin, `--pass-device-specs=true` or the `cdi-cri`
  device list strategy, which the GPU Operator uses with CDI enabled. With
  neither, the allocation leaves nothing in the container the plugin can
  recognise; see
  [Recognising a GPU allocation](../components/nri-plugin.md#recognising-a-gpu-allocation).

On Kind, create the cluster with NRI enabled in containerd:

```yaml title="kind.yaml"
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
containerdConfigPatches:
  - |-
    [plugins."io.containerd.nri.v1.nri"]
      disable = false
      disable_connections = false
      socket_path = "/var/run/nri/nri.sock"
nodes:
  - role: control-plane
  - role: worker
```

```bash
kind create cluster --name mokka-nri --config kind.yaml
```

## Install Mokka with the plugin

Install Mokka into its own namespace with `nri.enabled=true`. The plugin never
injects its own release namespace or `kube-system`, so installing into a shared
namespace would leave that namespace's workloads without the mock.

=== "New install"

    ```bash
    helm install nvml-mock oci://ghcr.io/nvidia/k8s-test-infra/chart/nvml-mock \
      --namespace mokka --create-namespace \
      --set nri.enabled=true \
      --wait --timeout 180s
    ```

=== "Existing install"

    ```bash
    helm upgrade nvml-mock oci://ghcr.io/nvidia/k8s-test-infra/chart/nvml-mock \
      --namespace mokka --reuse-values \
      --set nri.enabled=true \
      --wait --timeout 180s
    ```

    Changing `nri.*` rolls the node DaemonSet. See
    [NRI pod lifecycle](../helm-chart.md#nri-pod-lifecycle) for what that
    costs on a running node.

Each node pod now runs the NRI plugin next to the node agent, and is Ready only
once the plugin has registered with containerd:

```bash
kubectl -n mokka get pods -l app.kubernetes.io/name=nvml-mock \
  -o custom-columns='POD:.metadata.name,NODE:.spec.nodeName,READY:.status.containerStatuses[*].ready,CONTAINERS:.spec.containers[*].name'
```

## Verify injection

Run a pod that opts in with the `devices` annotation, so the check needs no GPU
consumer:

```bash
kubectl apply -f - <<'EOF'
apiVersion: v1
kind: Pod
metadata:
  name: nri-check
  annotations:
    nvml-mock.nvidia.com/devices: "true"
spec:
  restartPolicy: Never
  containers:
    - name: app
      image: debian:bookworm-slim
      command: ["sleep", "300"]
EOF

kubectl wait --for=condition=ready pod/nri-check --timeout=120s
kubectl exec nri-check -- nvidia-smi -L
```

The image is debian because the injected `nvidia-smi` is a glibc binary: a musl
image such as `busybox` or `alpine` cannot run it. `nvidia-smi -L` lists every mock GPU on the node. A pod without the annotation
and without a GPU request sees none, as on a real GPU node.

To check the allocation path, schedule a GPU request through the
[device plugin](device-plugin.md) or a ResourceClaim through the
[DRA driver](dra.md) on a debian-based image: `nvidia-smi -L` in that pod lists
only the GPUs it was allocated.

```bash
kubectl delete pod nri-check
```

## Keep a workload out

| To | Do |
|---|---|
| Skip one pod | Annotate it `nvml-mock.nvidia.com/inject: "false"` |
| Skip a namespace | Add it to `nri.excludedNamespaces` |
| Turn the plugin off | `helm upgrade ... --reuse-values --set nri.enabled=false` |

Turning the plugin off affects only containers created afterwards. Running
containers keep what they were given until they restart.

## When it does not inject

The plugin fails open: if it cannot inject, the container starts without the
mock rather than failing. Start with
[A pod gets no mock GPUs even though NRI is enabled](../troubleshooting.md#a-pod-gets-no-mock-gpus-even-though-nri-is-enabled),
then [NRI plugin failure modes](../helm-chart.md#nri-plugin-failure-modes).
