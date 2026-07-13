# VectorAdd nvml-mock Demo

This demo deploys nvml-mock on a local Kind cluster and runs the GPU Operator
validator mock from [`tests/e2e/validator-mock.yaml`](../../../tests/e2e/validator-mock.yaml).
The validator runs `cuda-vectorAdd` against the mock `libcuda.so`, which proves
that a normal GPU workload can request `nvidia.com/gpu` and launch a basic CUDA
kernel through the mock driver root.

## What it does

1. Creates a Kind cluster (`nvml-mock-vectoradd-demo`: 1 control-plane, 3 workers).
2. Builds the `nvml-mock:demo` container image from the repository root.
3. Loads the image into the Kind cluster.
4. Installs the nvml-mock Helm chart into the `nvml-mock-system` namespace
   (override with `NAMESPACE=...`) with the H100 profile and 8 GPUs per node by
   default.
5. Deploys [`tests/e2e/device-plugin-mock.yaml`](../../../tests/e2e/device-plugin-mock.yaml)
   so kubelet advertises `nvidia.com/gpu` capacity for regular workloads.
6. Deploys [`tests/e2e/validator-mock.yaml`](../../../tests/e2e/validator-mock.yaml),
   waits for `job/gpu-validator-mock` to complete, and prints its logs.

## Quick start

```bash
./demo.sh
```

The default GPU setup is H100 with 8 GPUs per node. Override it the same way as
the standalone demo:

```bash
GPU_PROFILE=gb200 GPU_COUNT=8 ./demo.sh
```

The validator waits 75 seconds by default and the job itself has a 60-second deadline. Override the wait if you are debugging slow image pulls:

```bash
VALIDATOR_TIMEOUT=120s ./demo.sh
```

## Clean up

```bash
# Remove just the release and validator job:
helm uninstall nvml-mock -n nvml-mock-system
kubectl --context kind-nvml-mock-vectoradd-demo -n default delete job gpu-validator-mock

# Or tear down the whole cluster:
kind delete cluster --name nvml-mock-vectoradd-demo
```
