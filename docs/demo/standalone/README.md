# Standalone nvml-mock Demo

This demo deploys nvml-mock on a local Kind cluster with FGO-style labels
enabled. It does not require any external GPU operator -- nvml-mock itself
generates the GPU profile ConfigMaps, the fake InfiniBand sysfs tree, and
the node labels that downstream consumers expect.

## What it does

1. Creates a Kind cluster (`nvml-mock-demo`: 1 control-plane, 3 workers).
2. Builds the `nvml-mock:demo` container image from the repository root.
3. Loads the image into the Kind cluster.
4. Installs the nvml-mock Helm chart with
   `integrations.fakeGpuOperator.enabled=true`, an H100 profile, and 8 GPUs
   per node.
5. Verifies the deployment:
   - DaemonSet pods are running on all workers.
   - Six GPU profile ConfigMaps are created (one per profile field group).
   - `nvidia-smi` runs successfully inside a pod.
   - `ibstat` lists 8 simulated ConnectX-7 NDR HCAs (see
     [`pkg/network/mockibsysfs/README.md`](../../../pkg/network/mockibsysfs/README.md)).
   - Node labels are present.

## Quick start

```bash
./demo.sh
```

## Clean up

```bash
kind delete cluster --name nvml-mock-demo
```
