# Standalone nvml-mock Demo

This demo deploys nvml-mock on a local Kind cluster with FGO-style labels
enabled. It does not require any external GPU operator -- nvml-mock itself
generates the GPU profile ConfigMaps and node labels that downstream
consumers expect.

## What it does

1. Creates a 3-node Kind cluster (`nvml-mock-demo`: 1 control-plane, 2 workers).
2. Builds the `nvml-mock:demo` container image from the repository root.
3. Loads the image into the Kind cluster.
4. Labels worker nodes with `run.ai/simulated-gpu-node-pool` to simulate
   FGO topology (first worker gets `integration`, remaining workers get `scale`).
5. Installs the nvml-mock Helm chart with
   `integrations.fakeGpuOperator.enabled=true`, an H100 profile, and 8 GPUs
   per node.
6. Verifies the deployment:
   - DaemonSet pods are running on all workers.
   - Six GPU profile ConfigMaps are created (one per profile field group).
   - `nvidia-smi` runs successfully inside a pod.
   - Node labels are present.

## Quick start

```bash
./demo.sh
```

## Clean up

```bash
kind delete cluster --name nvml-mock-demo
```
