# nvml-mock E2E Tests

End-to-end tests that deploy NVIDIA GPU consumers on a Kind cluster using the
nvml-mock chart (mock NVML + CUDA libraries instead of real hardware).

## What runs in CI

The `e2e-device-plugin` and `e2e-dra` jobs run automatically on every push.
They verify:

- nvml-mock DaemonSet deploys and creates mock device files
- NVIDIA device plugin discovers mock GPUs and registers `nvidia.com/gpu`
- DRA driver discovers mock GPUs and publishes ResourceSlices

## Standalone GFD/validator steps (disabled)

The `e2e-device-plugin` job has standalone GFD and CUDA validator steps that
pull directly from `nvcr.io`. These are disabled (`if: false`) because the
standalone images may require NGC authentication. GPU Operator images are
public and do not require NGC credentials -- the separate `e2e-gpu-operator`
job uses that path and works without auth.

To run the standalone steps locally (NGC auth may be needed):

```bash
# 1. Create Kind cluster
kind create cluster --name nvml-mock-e2e

# 2. Build and load nvml-mock image
docker build -t nvml-mock:e2e -f deployments/nvml-mock/Dockerfile .
kind load docker-image nvml-mock:e2e --name nvml-mock-e2e

# 3. Install nvml-mock chart
helm install nvml-mock deployments/nvml-mock/helm/nvml-mock \
  --set image.repository=nvml-mock --set image.tag=e2e --set gpu.count=2 \
  --wait --timeout 120s

# 4. Deploy device plugin
kubectl apply -f tests/e2e/device-plugin-mock.yaml
kubectl -n kube-system wait --for=condition=ready pod -l name=nvidia-device-plugin-mock --timeout=120s

# 5. Pull, load, and deploy GFD (may require: docker login nvcr.io)
docker pull nvcr.io/nvidia/gpu-feature-discovery:v0.17.0
kind load docker-image nvcr.io/nvidia/gpu-feature-discovery:v0.17.0 --name nvml-mock-e2e
kubectl apply -f tests/e2e/gfd-mock.yaml

# 6. Pull, load, and run CUDA validator (may require: docker login nvcr.io)
docker pull nvcr.io/nvidia/k8s/cuda-sample:vectoradd-cuda12.5.0
kind load docker-image nvcr.io/nvidia/k8s/cuda-sample:vectoradd-cuda12.5.0 --name nvml-mock-e2e
kubectl apply -f tests/e2e/validator-mock.yaml
kubectl wait --for=condition=complete job/gpu-validator-mock --timeout=120s
```

## Enabling standalone GFD/validator in CI

Once confirmed that the standalone `nvcr.io` images are publicly accessible:

1. Remove the `if: false` conditions from the GFD and validator steps
2. The image pull + kind load is already embedded in the step scripts

## Files

| File | Purpose |
|---|---|
| `device-plugin-mock.yaml` | Device plugin DaemonSet for mock GPUs |
| `gfd-mock.yaml` | GPU Feature Discovery DaemonSet |
| `validator-mock.yaml` | CUDA vectorAdd validator Job |
| `gpu-operator-values.yaml` | GPU Operator Helm values overlay |
| `kind-dra-config.yaml` | Kind config with DRA feature gates |
| `VERSION-MATRIX.md` | Tested component versions |
