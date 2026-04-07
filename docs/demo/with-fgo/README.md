# nvml-mock with fake-gpu-operator

This guide walks through a full integration of nvml-mock with Run:ai's
fake-gpu-operator (FGO). nvml-mock serves the "integration" node pool with a
real NVML shim while FGO handles the "scale" pool with its lightweight fake
shim.

## Prerequisites

- Docker
- Kind
- Helm
- kubectl
- fake-gpu-operator Helm chart (see
  [Run:ai fake-gpu-operator docs](https://github.com/run-ai/fake-gpu-operator))

## Step 1 -- Create a 4-node Kind cluster

```bash
cat <<EOF | kind create cluster --name nvml-mock-fgo-demo --config=-
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
nodes:
  - role: control-plane
  - role: worker
  - role: worker
  - role: worker
EOF
```

## Step 2 -- Build and load the nvml-mock image

```bash
docker build -t nvml-mock:demo -f deployments/nvml-mock/Dockerfile .
kind load docker-image nvml-mock:demo --name nvml-mock-fgo-demo
```

## Step 3 -- Label worker nodes

Assign each worker to a pool. The first worker uses the `integration` pool
(handled by nvml-mock). The remaining workers use the `scale` pool (handled
by FGO).

```bash
WORKERS=($(kubectl get nodes --no-headers -o custom-columns=":metadata.name" \
  | grep -v control-plane))

kubectl label node "${WORKERS[0]}" run.ai/simulated-gpu-node-pool=integration --overwrite

for node in "${WORKERS[@]:1}"; do
  kubectl label node "${node}" run.ai/simulated-gpu-node-pool=scale --overwrite
done
```

## Step 4 -- Install nvml-mock

```bash
helm install nvml-mock deployments/nvml-mock/helm/nvml-mock \
  --set image.repository=nvml-mock \
  --set image.tag=demo \
  --set integrations.fakeGpuOperator.enabled=true \
  --set gpu.profile=h100 \
  --set gpu.count=8 \
  --set "nodeSelector.run\.ai/simulated-gpu-node-pool=integration" \
  --wait --timeout 120s
```

## Step 5 -- Install fake-gpu-operator

Follow the official FGO installation instructions. A minimal example:

```bash
helm repo add run-ai https://run-ai.github.io/fake-gpu-operator
helm repo update

helm install fake-gpu-operator run-ai/fake-gpu-operator \
  --wait --timeout 120s
```

## Step 6 -- Configure topology

Create a topology ConfigMap that tells FGO which backend each pool uses.
The `integration` pool uses `backend: mock` (nvml-mock provides the NVML
shim), and the `scale` pool uses `backend: fake` (FGO provides the shim).

```bash
kubectl apply -f - <<'EOF'
apiVersion: v1
kind: ConfigMap
metadata:
  name: fake-gpu-operator-topology
  namespace: gpu-operator
data:
  topology.yaml: |
    nodeGroups:
      - name: integration
        backend: mock
        nodeSelector:
          run.ai/simulated-gpu-node-pool: "integration"
        gpuCount: 8
        gpuModel: NVIDIA H100 80GB HBM3
        gpuMemory: 80Gi

      - name: scale
        backend: fake
        nodeSelector:
          run.ai/simulated-gpu-node-pool: "scale"
        gpuCount: 8
        gpuModel: NVIDIA H100 80GB HBM3
        gpuMemory: 80Gi
EOF
```

## Step 7 -- Verify

### Integration pool (nvml-mock)

```bash
# DaemonSet pods should be running on the integration worker.
kubectl get pods -l app.kubernetes.io/name=nvml-mock -o wide

# Profile ConfigMaps should exist.
kubectl get configmaps -l run.ai/gpu-profile=true

# nvidia-smi should work inside the pod.
POD=$(kubectl get pods -l app.kubernetes.io/name=nvml-mock \
  -o jsonpath='{.items[0].metadata.name}')
kubectl exec "${POD}" -- nvidia-smi
```

### Scale pool (fake-gpu-operator)

```bash
# FGO pods should be running on the scale workers.
kubectl get pods -l app=fake-gpu-operator -o wide
```

## Expected outcome

| Node role | Pool | Backend | GPU provider | nvml-mock DaemonSet runs |
|---|---|---|---|---|
| Real worker | integration | mock | nvml-mock | Yes |
| Real worker | scale | fake | FGO shim | No |

The integration-pool worker runs the nvml-mock DaemonSet and exposes a full
NVML shim (nvidia-smi, profile ConfigMaps, device nodes). The scale-pool
workers are managed entirely by FGO and do not run nvml-mock.

## Clean up

```bash
kind delete cluster --name nvml-mock-fgo-demo
```
