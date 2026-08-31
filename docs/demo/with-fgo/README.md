# nvml-mock with fake-gpu-operator

This guide walks through a full integration of nvml-mock with Run:ai's
fake-gpu-operator (FGO). nvml-mock serves the "integration" node pool with a
real NVML shim while FGO handles the "scale" pool with its lightweight fake
shim.

## Prerequisites

- **A Kubernetes cluster and a valid `KUBECONFIG`.** This demo installs into
  whatever cluster your current context points at. Check yours with
  `kubectl config current-context`.
- **Helm 3.8 or newer.** The chart is served from an OCI registry, which
  needs 3.8+. Install it from the official docs:
  <https://helm.sh/docs/intro/install/>
- `kubectl`, matching your cluster version.
- The fake-gpu-operator Helm chart (see
  [Run:ai fake-gpu-operator docs](https://github.com/run-ai/fake-gpu-operator)).
- **Nodes labelled for both pools.** This demo splits work across two node
  pools, so you need at least one node labelled
  `run.ai/simulated-gpu-node-pool=integration` (Step 3 pins nvml-mock there)
  and at least one labelled `run.ai/simulated-gpu-node-pool=scale` (Step 4
  gives that pool to FGO). A cluster missing either label silently produces no
  pods for that pool, and Helm reports no error.
  [`../kind.yaml`](../kind.yaml) labels one worker `integration` and two
  `scale`.

> **No cluster yet?** The [quick start](../../quickstart.md) creates a
> throwaway one with Kind in about a minute, then come back here.

This guide has no script, so there is no `BUILD_LOCAL` switch to set, and no
`DEMO_ASSUME_YES` either: the scripted demos run a preflight that announces
the target cluster and makes you confirm it before installing a privileged
DaemonSet, but here you are running `helm` yourself. Nothing checks the
target on your behalf, so confirm it before Step 3:

```bash
kubectl config current-context
kubectl config view --minify -o jsonpath='{.clusters[0].cluster.server}'
```

Docker and Kind are needed only for two optional steps you can skip on a
cluster you already have: Step 1, which creates a throwaway Kind cluster, and
Step 2, which builds the image from source. Skip both and Step 3 installs the
published image.

## Step 1 (Optional) -- Create a Kind cluster

Skip this if your `KUBECONFIG` already points at a cluster whose nodes carry
the two pool labels listed above.

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

The image is about 100 MB and a cold pull was measured at roughly 8 minutes
per node, so `--wait` gets a 15-minute budget below rather than the 2 minutes
this guide used to show. A long silence there is the pull, not a hang. A first
install already pulls on every node at once; `maxUnavailable=100%` is there so
a later `helm upgrade` does not fall back to rolling one node at a time.
`ghcr.io/nvidia/nvml-mock:latest` is also a floating tag: a cluster holding an
older cached `:latest` can run a build that does not match this chart, so pin
a released tag if you need a fixed pairing.

```bash
helm install nvml-mock oci://ghcr.io/nvidia/k8s-test-infra/chart/nvml-mock \
  --set integrations.fakeGpuOperator.enabled=true \
  --set gpu.profile=h100 \
  --set gpu.count=8 \
  --set "nodeSelector.run\.ai/simulated-gpu-node-pool=integration" \
  --set-string updateStrategy.rollingUpdate.maxUnavailable=100% \
  --wait --timeout 15m
```

> **Tip:** To use the locally built image from Step 2, add `--set image.repository=nvml-mock --set image.tag=demo` to the command above.

## Step 4 -- Install fake-gpu-operator

Follow the official FGO installation instructions. A minimal example:

```bash

helm upgrade --install gpu-operator  oci://ghcr.io/run-ai/fake-gpu-operator/fake-gpu-operator \
  -n gpu-operator --create-namespace \
  --wait --timeout 15m  -f - <<EOF
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
