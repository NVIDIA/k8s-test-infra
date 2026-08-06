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

## Tested Versions

The discovery contract in this guide was derived on 2026-07-31 from these two trees. FGO is neither vendored nor pinned by this repository, so re-check their loader before you rely on any of it.

| Side | Ref | Date |
|---|---|---|
| `run-ai/fake-gpu-operator` | `main` @ `cf586f41` | 2026-07-28 |
| `NVIDIA/k8s-test-infra` | `main` @ `e9106453` | 2026-07-31 |

What was verified against which:

- **The contract fields** (`gpu-profile-<name>`, `profile.yaml`, the namespace env var) were read from FGO's loader source at `cf586f41`. FGO itself was not run.
- **The emitted shape** was verified against `e9106453` by Helm render, by the `integrations_fgo_test.yaml` unit suite, and by the `fgo`-labelled end-to-end case on a live kind cluster.
- **A live FGO-plus-nvml-mock interop run was not performed.** The two sides have not been exercised together in CI.

Two known gaps on FGO's side, both current as of `cf586f41`:

- FGO pins this project's image to `ghcr.io/nvidia/nvml-mock:sha-1706195`, with a comment that the v0.2.0 build broke their `e2e-mock` on `libnvidia-ml.so` loading. That report is not filed on this side.
- FGO vendors this repository's GPU profiles into its chart from commit `497fa04` (2026-05-25), which is 78 commits behind `e9106453`. All seven profile files have changed since that pin.

## Setup

### Prerequisites

- Kubernetes cluster (v1.24+)
- Helm 3.x
- nvml-mock Helm chart (`deployments/nvml-mock/helm/nvml-mock`)
- fake-gpu-operator installed ([installation docs](https://github.com/run-ai/fake-gpu-operator#installation))

### Step 1: Install nvml-mock with FGO Integration

Enable the FGO integration flag when installing the Helm chart. This creates GPU profile ConfigMaps in the shape FGO's loader reads.

```bash
helm install nvml-mock oci://ghcr.io/nvidia/k8s-test-infra/chart/nvml-mock \
  --set integrations.fakeGpuOperator.enabled=true
```

The ConfigMaps land in the nvml-mock release namespace by default. FGO loads profiles from its own namespace, so see [Targeting FGO's namespace](#targeting-fgos-namespace) before you rely on FGO reading them.

### Step 2: Configure FGO Topology

Create a topology ConfigMap that tells FGO which backend to use for each node pool. Real nodes use `backend: mock` (served by nvml-mock), while KWOK virtual nodes use `backend: fake` (served by FGO).

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: fake-gpu-operator-topology
  namespace: gpu-operator
data:
  topology.yaml: |
    nodePools:
      mock-nodes:
        backend: mock
        gpuCount: 8
        gpuProfile: a100
      kwok-nodes:
        backend: fake
        gpuCount: 8
        gpuProfile: a100
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

When `integrations.fakeGpuOperator.enabled=true` is set, the Helm chart creates one profile ConfigMap per shipped GPU profile.

FGO does not watch for these ConfigMaps and does not select them by label. Its loader (`internal/common/profile/profile.go`) does a direct `Get` by name and then reads one data key. Three fields must therefore match exactly, and each one is fatal on its own:

| Field | Value | Why it matters |
|---|---|---|
| Name | `gpu-profile-<profile>` | The loader builds this literal from `CmNamePrefix`. Any other name is a `NotFound`. |
| Data key | `profile.yaml` | Read from `CmProfileKey`. A different key is a "missing key" error. |
| Namespace | FGO's own release namespace | Read from `FAKE_GPU_OPERATOR_NAMESPACE`. The loader has no cross-namespace fallback. |

The chart also emits `fake-gpu-operator/gpu-profile: "true"`, which is FGO's published discovery label, and keeps `run.ai/gpu-profile: "true"` for the demo scripts and examples in this repository. Neither label is on FGO's load path today.

List the profile ConfigMaps:

```bash
kubectl get cm -l fake-gpu-operator/gpu-profile=true
```

The following profiles are created by default:

| ConfigMap Name | GPU Model | Memory |
|---|---|---|
| `gpu-profile-a100` | NVIDIA A100-SXM4-40GB | 40 GiB |
| `gpu-profile-h100` | NVIDIA H100 80GB HBM3 | 80 GiB |
| `gpu-profile-b200` | NVIDIA B200 | 192 GiB |
| `gpu-profile-gb200` | NVIDIA GB200 | 192 GiB |
| `gpu-profile-gb300` | NVIDIA GB300 NVL | 288 GiB |
| `gpu-profile-l40s` | NVIDIA L40S | 48 GiB |
| `gpu-profile-t4` | NVIDIA T4 | 16 GiB |

These names carry no release prefix, because FGO's `Get` is for that literal name. Two nvml-mock releases in one namespace therefore collide on these seven ConfigMaps. Enable the integration on one release per namespace, or give each release its own `targetNamespace`.

### Targeting FGO's namespace

By default the ConfigMaps land in the nvml-mock release namespace, where FGO's loader does not look. To make them loadable, point them at FGO's release namespace:

```yaml
integrations:
  fakeGpuOperator:
    enabled: true
    targetNamespace: gpu-operator
```

**Warning: set FGO's `builtinProfiles.enabled=false` before you do this.** FGO ships its own builtin profiles under the same seven names, owned by its own Helm release. Helm 3 refuses to adopt resources owned by another release, so whichever chart installs second fails with an `invalid ownership metadata` error. The two profile sets are alternatives, not complements.

## Custom Labels

`profileLabels` adds labels to the profile ConfigMaps. It cannot remove the contract labels `fake-gpu-operator/gpu-profile` and `nvml-mock/profile-name`, which the template always emits, so an override here cannot break discovery:

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
helm install nvml-mock oci://ghcr.io/nvidia/k8s-test-infra/chart/nvml-mock \
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
helm upgrade nvml-mock oci://ghcr.io/nvidia/k8s-test-infra/chart/nvml-mock \
  --set integrations.fakeGpuOperator.enabled=true
```

### FGO Does Not Pick Up Profiles

FGO looks up profiles by name in its own namespace, so check the name and the namespace, not the label. Substitute FGO's release namespace and the profile you referenced in the topology:

```bash
kubectl get cm gpu-profile-a100 -n gpu-operator -o name
```

A `NotFound` here means one of three things:

- The ConfigMaps are still in the nvml-mock release namespace. Set `integrations.fakeGpuOperator.targetNamespace` to FGO's release namespace.
- The name is wrong. FGO reads `gpu-profile-<profile>` exactly.
- The install failed with an ownership error. FGO's own builtin profiles use the same names; set FGO's `builtinProfiles.enabled=false`.

If the ConfigMap exists under the right name and namespace, confirm the body is under the `profile.yaml` key:

```bash
kubectl get cm gpu-profile-a100 -n gpu-operator -o jsonpath='{.data.profile\.yaml}' | head -5
```

Restart the FGO controller pod after correcting any of the above:

```bash
kubectl rollout restart deployment fake-gpu-operator -n gpu-operator
```

### NVML Not Working on Mock Nodes

Confirm the nvml-mock DaemonSet is running on the expected nodes:

```bash
kubectl get ds -l app.kubernetes.io/name=nvml-mock
```

Check that the mock libraries are staged correctly. The chart writes them under
`/var/lib/nvml-mock` on the host and mounts that path into consumer pods — it
never populates the distribution library directory, so looking in
`/usr/lib/x86_64-linux-gnu` reports "No such file or directory" on a healthy
install:

```bash
kubectl exec -it <pod-on-mock-node> -- ls -la /var/lib/nvml-mock/driver/usr/lib64/libnvidia-ml.so*
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
