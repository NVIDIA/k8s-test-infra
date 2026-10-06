# NVIDIA DRA Driver

Expose mock GPUs through Dynamic Resource Allocation, schedule a pod that
claims one, and run `nvidia-smi` in it.

DRA is how Kubernetes is moving beyond `nvidia.com/gpu` counters: a driver
publishes **ResourceSlices** describing the devices on each node, and a workload
asks for what it needs through a **ResourceClaim**. Mokka lets you exercise that
path without hardware.

## Prerequisites

- [Docker](https://docs.docker.com/get-started/get-docker/) and
  [Kind](https://kind.sigs.k8s.io/)
- [Helm 3.8 or newer](https://helm.sh/docs/intro/install/)
- [kubectl](https://kubernetes.io/docs/reference/kubectl/)
- [jq](https://jqlang.github.io/jq/) for the verification step

Takes about 10 minutes.

## Step 1 — Create a cluster with DRA enabled

DRA needs a feature gate, a non-default API version, and CDI in containerd.
The claimed pod also needs containerd's Node Resource Interface (NRI), which
Mokka's NRI plugin uses to put the mock driver into it. None of them are on by
default, so the cluster config carries all four:

```bash
cat > kind-dra.yaml <<'EOF'
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
featureGates:
  DynamicResourceAllocation: true
containerdConfigPatches:
  - |-
    [plugins."io.containerd.grpc.v1.cri"]
      enable_cdi = true
    [plugins."io.containerd.nri.v1.nri"]
      disable = false
      disable_connections = false
      socket_path = "/var/run/nri/nri.sock"
nodes:
  - role: control-plane
    kubeadmConfigPatches:
      - |
        kind: ClusterConfiguration
        apiServer:
          extraArgs:
            runtime-config: "resource.k8s.io/v1beta1=true"
EOF

kind create cluster --name mokka-dra --config kind-dra.yaml
```

## Step 2 — Install Mokka

```bash
helm install nvml-mock oci://ghcr.io/nvidia/k8s-test-infra/chart/nvml-mock \
  --namespace mokka --create-namespace \
  --set nri.enabled=true \
  --wait --timeout 120s
```

`nri.enabled=true` runs the [NRI plugin](../components/nri-plugin.md), which
gives a container that holds a GPU allocation the mock driver, so the pod in
Step 5 can run `nvidia-smi` without a Mokka-specific pod spec.

## Step 3 — Install the DRA driver

The kubelet plugin's node affinity requires a GPU-presence label. Node Feature
Discovery derives one from the feature file Mokka writes; without NFD in the
cluster, set it by hand:

```bash
kubectl label node --all nvidia.com/gpu.present=true

helm repo add nvidia https://helm.ngc.nvidia.com/nvidia && helm repo update

helm install nvidia-dra-driver nvidia/dra-driver-nvidia-gpu \
  --version 0.5.0 \
  --namespace nvidia --create-namespace \
  --set nvidiaDriverRoot=/var/lib/nvml-mock/driver \
  --set gpuResourcesEnabledOverride=true \
  --set resources.computeDomains.enabled=false \
  --wait --timeout 180s
```

| Setting | Why |
|---|---|
| `nvidiaDriverRoot` | Points the driver at Mokka's staged tree instead of a real driver root |
| `gpuResourcesEnabledOverride` | The driver otherwise gates GPU resource publishing on checks a mock node does not satisfy |
| `resources.computeDomains.enabled=false` | ComputeDomain support needs an NVLink fabric — see the [ComputeDomain guide](compute-domain/README.md) for that path |

## Step 4 — Check the ResourceSlices

```bash
kubectl -n nvidia wait --for=condition=ready pod --all --timeout=120s

kubectl get resourceslices -o json | jq '[.items[].spec.devices // [] | length] | add // 0'
# 4 — the gb300 default profile has four devices
```

Each device the driver publishes came from Mokka answering NVML on that node.

## Step 5 — Schedule a pod that claims a GPU

```bash
kubectl apply -f - <<'EOF'
apiVersion: resource.k8s.io/v1beta1
kind: ResourceClaimTemplate
metadata:
  name: gpu-claim
spec:
  spec:
    devices:
      requests:
        - name: gpu
          deviceClassName: gpu.nvidia.com
---
apiVersion: v1
kind: Pod
metadata:
  name: gpu-test-pod
spec:
  restartPolicy: Never
  containers:
    - name: app
      image: debian:bookworm-slim
      command: ["sleep", "infinity"]
      resources:
        claims:
          - name: gpu
  resourceClaims:
    - name: gpu
      resourceClaimTemplateName: gpu-claim
EOF

kubectl wait --for=condition=ready pod/gpu-test-pod --timeout=120s
```

The pod reaching `Running` means the scheduler matched the claim to a
published device *and* the kubelet plugin's `NodePrepareResources` succeeded —
the step that would touch real hardware. The image is debian because the
injected `nvidia-smi` is a glibc binary, which `busybox` cannot run. The pod
sleeps until you delete it: once it exits, the claim generated for it is
deleted too, and Step 6 has nothing to read.

## Step 6 — Check the pod sees exactly its claimed GPU

Ask the claim which device the scheduler allocated, then look it up in the
ResourceSlices for its UUID:

```bash
claim=$(kubectl get pod gpu-test-pod -o jsonpath='{.status.resourceClaimStatuses[0].resourceClaimName}')
device=$(kubectl get resourceclaim "$claim" -o jsonpath='{.status.allocation.devices.results[0].device}')
pool=$(kubectl get resourceclaim "$claim" -o jsonpath='{.status.allocation.devices.results[0].pool}')

kubectl get resourceslices -o json | jq -r --arg d "$device" --arg p "$pool" \
  '.items[] | select(.spec.pool.name == $p) | .spec.devices[]
   | select(.name == $d) | (.basic.attributes // .attributes).uuid.string'
# GPU-…
```

Then ask the pod:

```bash
kubectl exec gpu-test-pod -- nvidia-smi -L
# GPU 0: NVIDIA GB300 NVL (UUID: GPU-…)
```

`nvidia-smi` lists one GPU, with the UUID the claim was allocated, not every
GPU on the node: the NRI plugin keeps a container to exactly the devices it
was allocated.

## Troubleshooting

**No ResourceSlices appear.** The driver's kubelet plugin is not scheduled, most
often because `nvidia.com/gpu.present=true` is missing from the nodes.

**The pod stays `Pending`.** Describe it — an unschedulable claim names the
device class it could not satisfy. Check `deviceClassName` matches what the
driver registered:

```bash
kubectl get deviceclasses
```

**`nvidia-smi` is not found in the pod.** The NRI plugin did not inject it.
Check that the nvml-mock pods run an `nvml-mock-nri` container and are Ready,
and that containerd has NRI enabled. See
[A pod gets no mock GPUs even though NRI is enabled](../troubleshooting.md#a-pod-gets-no-mock-gpus-even-though-nri-is-enabled).
The plugin only injects a container as it is created, so delete and recreate
the pod after fixing it.

**The API rejects the manifest.** `resource.k8s.io/v1beta1` is not served. The
`runtime-config` line in the cluster config is what enables it, and it only
takes effect at cluster creation.

## Clean up

```bash
kind delete cluster --name mokka-dra
```

## Related

| To read about | See |
|---|---|
| Every chart value | [Installation](../helm-chart.md) |
| Which containers the NRI plugin injects | [NRI Plugin](../components/nri-plugin.md#which-containers-are-injected) |
| The operator stack instead of DRA | [NVIDIA GPU Operator](gpu-operator.md) |
| NVLink fabric identity for ComputeDomains | [ComputeDomain](compute-domain/README.md) |
| Driving this from CI | [Use in CI/CD](ci-cd.md) |
