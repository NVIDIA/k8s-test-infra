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

## Step 1 -- Create a Kind cluster

```bash
kind create cluster --name nvml-mock-fgo-demo --config=docs/demo/kind.yaml
```

## Step 2 (Optional) -- Build and load the nvml-mock image

```bash
docker build -t nvml-mock:demo -f deployments/nvml-mock/Dockerfile .
kind load docker-image nvml-mock:demo --name nvml-mock-fgo-demo
```

> **Note:** This step is only needed if you want to test local changes. If skipped, the chart defaults to `ghcr.io/nvidia/nvml-mock:latest`.

## Step 3 -- Install nvml-mock

```bash
helm install nvml-mock oci://ghcr.io/nvidia/k8s-test-infra/chart/nvml-mock \
  --set integrations.fakeGpuOperator.enabled=true \
  --set gpu.profile=h100 \
  --set gpu.count=8 \
  --set "nodeSelector.run\.ai/simulated-gpu-node-pool=integration" \
  --wait --timeout 120s
```

> **Tip:** To use the locally built image from Step 2, add `--set image.repository=nvml-mock --set image.tag=demo` to the command above.

## Step 4 -- Install fake-gpu-operator

Follow the official FGO installation instructions. A minimal example:

```bash

helm upgrade --install gpu-operator  oci://ghcr.io/run-ai/fake-gpu-operator/fake-gpu-operator \
  -n gpu-operator --create-namespace \
  --wait --timeout 120s  -f - <<EOF
topology:
    nodePools:
      integration:
        backend: mock
        gpuCount: 8
        gpuProfile: h100
      scale:
        backend: fake
        gpuCount: 8
        gpuProfile: h100
EOF
```

The topology is passed inline via Helm values above. The `integration` pool
uses `backend: mock` (nvml-mock provides the NVML shim), while the `scale`
pool uses `backend: fake` (FGO provides the shim).

## Step 5 -- Verify

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

# InfiniBand diagnostic tools see one mock ConnectX-7 NDR HCA per GPU.
kubectl exec "${POD}" -- ibstat
kubectl exec "${POD}" -- ibstatus
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
