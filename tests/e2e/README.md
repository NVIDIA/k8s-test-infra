# GPU Mock E2E Tests

End-to-end tests that deploy NVIDIA GPU consumers on a Kind cluster using the
gpu-mock chart (mock NVML + CUDA libraries instead of real hardware).

## What runs in CI

The `e2e-device-plugin` and `e2e-dra` jobs run automatically on every push.
They verify:

- gpu-mock DaemonSet deploys and creates mock device files
- NVIDIA device plugin discovers mock GPUs and registers `nvidia.com/gpu`
- DRA driver discovers mock GPUs and publishes ResourceSlices

## What requires NGC credentials

GFD and CUDA validator tests are **disabled in CI** (`if: false`) because
their container images live on `nvcr.io` which requires authentication.

To run the full suite locally:

```bash
# 1. Authenticate with NGC
docker login nvcr.io -u '$oauthtoken' -p <NGC_API_KEY>

# 2. Create Kind cluster
kind create cluster --name gpu-mock-e2e

# 3. Build and load gpu-mock image
docker build -t gpu-mock:e2e -f deployments/gpu-mock/Dockerfile .
kind load docker-image gpu-mock:e2e --name gpu-mock-e2e

# 4. Install gpu-mock chart
helm install gpu-mock deployments/gpu-mock/helm/gpu-mock \
  --set image.repository=gpu-mock --set image.tag=e2e --set gpu.count=2 \
  --wait --timeout 120s

# 5. Deploy device plugin
kubectl apply -f tests/e2e/device-plugin-mock.yaml
kubectl -n kube-system wait --for=condition=ready pod -l name=nvidia-device-plugin-mock --timeout=120s

# 6. Pull, load, and deploy GFD
docker pull nvcr.io/nvidia/gpu-feature-discovery:v0.17.0
kind load docker-image nvcr.io/nvidia/gpu-feature-discovery:v0.17.0 --name gpu-mock-e2e
kubectl apply -f tests/e2e/gfd-mock.yaml

# 7. Pull, load, and run CUDA validator
docker pull nvcr.io/nvidia/k8s/cuda-sample:vectoradd-cuda12.5.0
kind load docker-image nvcr.io/nvidia/k8s/cuda-sample:vectoradd-cuda12.5.0 --name gpu-mock-e2e
kubectl apply -f tests/e2e/validator-mock.yaml
kubectl wait --for=condition=complete job/gpu-validator-mock --timeout=120s
```

## Enabling GFD/validator in CI

When `NGC_API_KEY` is added as a GitHub Actions secret:

1. Add a docker login step before the GFD/validator steps
2. Remove the `if: false` conditions from the GFD and validator steps
3. The image pull + kind load is already embedded in the step scripts

## Files

| File | Purpose |
|---|---|
| `device-plugin-mock.yaml` | Device plugin DaemonSet for mock GPUs |
| `gfd-mock.yaml` | GPU Feature Discovery DaemonSet |
| `validator-mock.yaml` | CUDA vectorAdd validator Job |
| `gpu-operator-values.yaml` | GPU Operator Helm values overlay |
| `kind-dra-config.yaml` | Kind config with DRA feature gates |
| `VERSION-MATRIX.md` | Tested component versions |
