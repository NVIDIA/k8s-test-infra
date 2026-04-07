# fake-gpu-operator Integration Guide

## Overview

[fake-gpu-operator](https://github.com/run-ai/fake-gpu-operator) (FGO) by Run:ai simulates GPUs at the Kubernetes API level. It creates fake device plugin and GPU feature discovery services that advertise GPU resources to the scheduler without requiring physical hardware. FGO excels at scale testing with KWOK virtual nodes, where hundreds of simulated GPU nodes can be spun up in seconds.

nvml-mock provides driver-level simulation. It exposes a real NVML shared library (`libnvidia-ml.so`) inside pods, enabling tools like `nvidia-smi`, the GPU Operator validator, and DCGM to function as if a physical GPU were present.

Together, they enable **mixed clusters** where a small set of real nodes run nvml-mock for driver-level fidelity (GPU Operator validation, DCGM metrics, nvidia-smi output) while a large fleet of KWOK virtual nodes use FGO for Kubernetes API-level scale simulation.

## Why Combine Them

| Capability | FGO Alone | nvml-mock Alone | FGO + nvml-mock |
|---|---|---|---|
| K8s API simulation | Yes | Yes | Yes |
| KWOK virtual nodes | Yes | No | Yes |
| Real NVML API inside pods | No | Yes | Yes |
| Real nvidia-smi output | Limited | Yes | Yes |
| DCGM metrics | 3 synthetic | N/A | Real DCGM on mock nodes |
| GPU Operator validation | No | Yes | Yes |
| Fractional GPUs | Yes | No | Yes (KWOK nodes) |
| Scale testing (1000+ nodes) | Yes | No | Yes (KWOK + mock) |

## Architecture

```
+-----------------------------------------------------------------------+
|                          Kubernetes Cluster                            |
|                                                                       |
|  +-----------------------------+   +-------------------------------+  |
|  |     Real Nodes (2-5)        |   |     KWOK Nodes (100-500)      |  |
|  |     backend: mock           |   |     backend: fake             |  |
|  |                             |   |                               |  |
|  |  +------------------------+ |   |  +-------------------------+  |  |
|  |  | nvml-mock DaemonSet    | |   |  | FGO fake device plugin  |  |  |
|  |  | - libnvidia-ml.so      | |   |  | - Advertises GPU        |  |  |
|  |  | - libcuda.so.1         | |   |  |   resources to K8s API  |  |  |
|  |  | - nvidia-smi           | |   |  +-------------------------+  |  |
|  |  +------------------------+ |   |                               |  |
|  |                             |   |  +-------------------------+  |  |
|  |  +------------------------+ |   |  | FGO fake GFD            |  |  |
|  |  | GPU Operator           | |   |  | - Node labels           |  |  |
|  |  | - Validator (pass)     | |   |  +-------------------------+  |  |
|  |  | - DCGM (real metrics)  | |   |                               |  |
|  |  | - GFD (real discovery)  | |   +-------------------------------+  |
|  |  +------------------------+ |                                      |
|  |                             |                                      |
|  +-----------------------------+                                      |
|                                                                       |
|  +------------------------------------------------------------------+ |
|  |           fake-gpu-operator (cluster-wide controller)             | |
|  |  - Manages topology across all nodes                              | |
|  |  - Reads GPU profiles from ConfigMaps                             | |
|  |  - Coordinates fake + mock backends                               | |
|  +------------------------------------------------------------------+ |
+-----------------------------------------------------------------------+
```

## Setup

### Prerequisites

- Kubernetes cluster (v1.24+)
- Helm 3.x
- nvml-mock Helm chart (`deployments/nvml-mock/helm/nvml-mock`)
- fake-gpu-operator installed ([installation docs](https://github.com/run-ai/fake-gpu-operator#installation))

### Step 1: Install nvml-mock with FGO Integration

Enable the FGO integration flag when installing the Helm chart. This creates GPU profile ConfigMaps that FGO can discover and use for its topology.

```bash
helm install nvml-mock deployments/nvml-mock/helm/nvml-mock \
  --set integrations.fakeGpuOperator.enabled=true
```

### Step 2: Configure FGO Topology

Create a topology ConfigMap that tells FGO which backend to use for each node group. Real nodes use `backend: mock` (served by nvml-mock), while KWOK virtual nodes use `backend: fake` (served by FGO).

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: fake-gpu-operator-topology
  namespace: gpu-operator
data:
  topology.yaml: |
    nodeGroups:
      - name: mock-nodes
        backend: mock
        nodeSelector:
          nvidia.com/gpu-driver-mock: "true"
        gpuCount: 8
        gpuModel: A100-SXM4-80GB
        gpuMemory: 80Gi

      - name: kwok-nodes
        backend: fake
        nodeSelector:
          type: kwok
        gpuCount: 8
        gpuModel: A100-SXM4-80GB
        gpuMemory: 80Gi
        nodeCount: 200
```

### Step 3: Verify

On mock nodes, confirm that nvidia-smi returns expected output:

```bash
# Exec into any pod on a mock node
kubectl exec -it <pod-on-mock-node> -- nvidia-smi
```

On KWOK/fake nodes, confirm that GPU resources are advertised:

```bash
kubectl get nodes -l type=kwok -o custom-columns=\
NAME:.metadata.name,\
GPUS:.status.capacity.nvidia\.com/gpu,\
MODEL:.metadata.labels.nvidia\.com/gpu\.product
```

## GPU Profile Discovery

When `integrations.fakeGpuOperator.enabled=true` is set, the Helm chart creates ConfigMaps labeled with `run.ai/gpu-profile=true`. FGO watches for these ConfigMaps and uses them to populate its topology with accurate GPU specifications.

List discovered profiles:

```bash
kubectl get cm -l run.ai/gpu-profile=true
```

The following profiles are created by default:

| ConfigMap Name | GPU Model | Memory |
|---|---|---|
| `nvml-mock-profile-a100` | NVIDIA A100-SXM4-40GB | 40 GiB |
| `nvml-mock-profile-h100` | NVIDIA H100 80GB HBM3 | 80 GiB |
| `nvml-mock-profile-b200` | NVIDIA B200 | 192 GiB |
| `nvml-mock-profile-gb200` | NVIDIA GB200 NVL | 192 GiB |
| `nvml-mock-profile-l40s` | NVIDIA L40S | 48 GiB |
| `nvml-mock-profile-t4` | NVIDIA T4 | 16 GiB |

## Custom Labels

You can override the labels applied to profile ConfigMaps to match your FGO topology requirements:

```yaml
# values.yaml
integrations:
  fakeGpuOperator:
    enabled: true
    profileLabels:
      run.ai/gpu-profile: "true"
      my-org/custom-label: "gpu-sim"
```

Install with the override:

```bash
helm install nvml-mock deployments/nvml-mock/helm/nvml-mock \
  -f values.yaml
```

## Troubleshooting

### Profile ConfigMaps Not Appearing

Verify that the FGO integration is enabled:

```bash
helm get values nvml-mock | grep -A2 fakeGpuOperator
```

Expected output:

```
fakeGpuOperator:
  enabled: true
```

If the value is missing or `false`, upgrade the release with the flag enabled:

```bash
helm upgrade nvml-mock deployments/nvml-mock/helm/nvml-mock \
  --set integrations.fakeGpuOperator.enabled=true
```

### FGO Does Not Pick Up Profiles

Ensure the ConfigMaps have the correct label that FGO watches for:

```bash
kubectl get cm -l run.ai/gpu-profile=true -o name
```

If ConfigMaps exist but FGO is not using them, check that FGO is configured to read profiles from the same namespace where nvml-mock is installed. Restart the FGO controller pod after correcting the namespace configuration:

```bash
kubectl rollout restart deployment fake-gpu-operator -n gpu-operator
```

### NVML Not Working on Mock Nodes

Confirm the nvml-mock DaemonSet is running on the expected nodes:

```bash
kubectl get ds -l app.kubernetes.io/name=nvml-mock
```

Check that the mock libraries are mounted correctly by exec-ing into a pod and verifying the library path:

```bash
kubectl exec -it <pod-on-mock-node> -- ls -la /usr/lib/x86_64-linux-gnu/libnvidia-ml.so*
```

If the libraries are missing, check the DaemonSet pod logs for errors:

```bash
kubectl logs -l app.kubernetes.io/name=nvml-mock --tail=50
```

## Cleanup

To remove the FGO + nvml-mock setup, uninstall both Helm releases and delete
the topology ConfigMap:

```bash
# Uninstall nvml-mock
helm uninstall nvml-mock

# Uninstall fake-gpu-operator
helm uninstall fake-gpu-operator -n gpu-operator

# Delete the topology ConfigMap
kubectl delete configmap fake-gpu-operator-topology -n gpu-operator
```
