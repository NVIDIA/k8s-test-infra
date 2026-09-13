# NVIDIA Device Plugin

Advertise mock GPUs as `nvidia.com/gpu` allocatable resources, then schedule a
workload that requests one.

This is the simplest way to make Kubernetes itself believe a node has GPUs. The
real NVIDIA device plugin runs unmodified — it discovers devices through NVML,
and Mokka is what answers.

## Prerequisites

- [Docker](https://docs.docker.com/get-started/get-docker/) and
  [Kind](https://kind.sigs.k8s.io/)
- [Helm 3.8 or newer](https://helm.sh/docs/intro/install/)
- [kubectl](https://kubernetes.io/docs/reference/kubectl/)

Takes about 5 minutes.

## Step 1 — Create a cluster and install Mokka

```bash
kind create cluster --name mokka-device-plugin

helm install nvml-mock oci://ghcr.io/nvidia/k8s-test-infra/chart/nvml-mock \
  --namespace mokka --create-namespace \
  --wait --timeout 120s
```

## Step 2 — Label the node

The device plugin manifest below targets the simulated-GPU node pool, so the
node has to carry that label:

```bash
kubectl label node --all mokka.nvidia.com/type=sgpu
```

## Step 3 — Deploy the device plugin

```bash
kubectl apply -f - <<'EOF'
apiVersion: apps/v1
kind: DaemonSet
metadata:
  name: nvidia-device-plugin-mock
  namespace: kube-system
spec:
  selector:
    matchLabels:
      name: nvidia-device-plugin-mock
  template:
    metadata:
      labels:
        name: nvidia-device-plugin-mock
    spec:
      nodeSelector:
        mokka.nvidia.com/type: sgpu
      tolerations:
        - operator: Exists
      containers:
        - name: nvidia-device-plugin
          image: nvcr.io/nvidia/k8s-device-plugin:v0.18.2
          args:
            - "--nvidia-driver-root=/var/lib/nvml-mock/driver"
            - "--driver-root-ctr-path=/var/lib/nvml-mock/driver"
            - "--device-discovery-strategy=nvml"
            - "--pass-device-specs=true"
          securityContext:
            privileged: true
          volumeMounts:
            - name: mock-root
              mountPath: /var/lib/nvml-mock
              readOnly: true
            - name: device-plugins
              mountPath: /var/lib/kubelet/device-plugins
      volumes:
        - name: mock-root
          hostPath:
            path: /var/lib/nvml-mock
        - name: device-plugins
          hostPath:
            path: /var/lib/kubelet/device-plugins
EOF

kubectl -n kube-system wait --for=condition=ready \
  pod -l name=nvidia-device-plugin-mock --timeout=120s
```

| Argument | Why |
|---|---|
| `--nvidia-driver-root`, `--driver-root-ctr-path` | Point the plugin at Mokka's staged tree instead of a real driver root |
| `--device-discovery-strategy=nvml` | Discover through NVML, which is the interface Mokka implements |
| `--pass-device-specs=true` | Deliver the device nodes into the container. Required if you also run [node-wide NRI injection](node-wide-injection/README.md), so the two do not both inject |

## Step 4 — Verify allocatable GPUs

```bash
kubectl get nodes -o custom-columns='NODE:.metadata.name,GPUS:.status.allocatable.nvidia\.com/gpu'
# NODE                                GPUS
# mokka-device-plugin-control-plane   4
```

The count comes from the profile — `gb300` is the default and carries four
devices.

## Step 5 — Schedule a workload

```bash
kubectl apply -f - <<'EOF'
apiVersion: v1
kind: Pod
metadata:
  name: gpu-pod
spec:
  restartPolicy: Never
  containers:
    - name: app
      image: busybox:1.36
      command: ["sleep", "300"]
      resources:
        limits:
          nvidia.com/gpu: 1
EOF

kubectl wait --for=condition=ready pod/gpu-pod --timeout=120s
kubectl get nodes -o custom-columns='NODE:.metadata.name,GPUS:.status.allocatable.nvidia\.com/gpu'
```

The pod scheduling is the result: the kubelet accepted a GPU request on a node
that has none, because the plugin allocated one of Mokka's.

## Different GPU models on different nodes

One release covers one node pool, so a heterogeneous fleet is just several
releases that do not overlap. Start from a cluster with more than one worker:

```bash
cat > kind-fleet.yaml <<'EOF'
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
nodes:
  - role: control-plane
  - role: worker
    labels:
      nvml-mock/profile: a100
  - role: worker
    labels:
      nvml-mock/profile: t4
EOF

kind create cluster --name mokka-fleet --config kind-fleet.yaml
kubectl label node --all mokka.nvidia.com/type=sgpu
```

Install one release per pool, each selecting its own nodes:

```bash
helm install nvml-mock-a100 oci://ghcr.io/nvidia/k8s-test-infra/chart/nvml-mock \
  --namespace mokka --create-namespace \
  --set gpu.profile=a100 --set gpu.count=4 \
  --set "nodeSelector.nvml-mock/profile=a100" --wait --timeout 120s

helm install nvml-mock-t4 oci://ghcr.io/nvidia/k8s-test-infra/chart/nvml-mock \
  --namespace mokka \
  --set gpu.profile=t4 --set gpu.count=2 \
  --set "nodeSelector.nvml-mock/profile=t4" --wait --timeout 120s
```

Deploy the device plugin as in Step 3, and each worker reports its own count:

```bash
kubectl get nodes -l nvml-mock/profile \
  -o custom-columns='NODE:.metadata.name,GPUS:.status.allocatable.nvidia\.com/gpu'
```

!!! warning "The `nodeSelector` is what keeps the pools apart"

    The chart's hostPath mounts — `/var/lib/nvml-mock`, `/var/run/cdi`,
    `/run/nvidia` and the NFD features directory — are the same for every
    release, and the DaemonSet tolerates every taint. Drop the `nodeSelector`
    and both releases land on both workers and overwrite each other's per-node
    state. A node belongs to exactly one pool.

## Node labels

The device plugin advertises the resource, but it does not label the node.
Labels under `nvidia.com/` come from Node Feature Discovery and GPU Feature
Discovery, exactly as on real hardware — see the
[GPU Operator guide](gpu-operator.md), which deploys both.

## Troubleshooting

**Allocatable stays at zero.** The plugin is running but found no devices.
Check its logs for an NVML error, and confirm Mokka staged the driver root:

```bash
kubectl -n kube-system logs -l name=nvidia-device-plugin-mock --tail=30
```

**The plugin pod is `Pending`.** The node is missing
`mokka.nvidia.com/type=sgpu`.

**A pod requesting one GPU sees all of them.** The NRI plugin is also injecting.
`--pass-device-specs=true` is what lets Mokka's plugin detect the allocation and
stand down — see [NRI Plugin](../components/nri-plugin.md).

## Clean up

```bash
kind delete cluster --name mokka-device-plugin
```

## Related

| To read about | See |
|---|---|
| Every chart value | [Installation](../helm-chart.md) |
| Node labelling and the full operand stack | [NVIDIA GPU Operator](gpu-operator.md) |
| Claim-based allocation instead of counters | [NVIDIA DRA Driver](dra.md) |
| GPUs without a resource request | [Node-Wide Injection](node-wide-injection/README.md) |
