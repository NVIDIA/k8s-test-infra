# Run:ai Fake GPU Operator

Run Mokka and Run:ai's [fake-gpu-operator](https://github.com/run-ai/fake-gpu-operator)
(FGO) in one cluster, each serving the nodes it is better at.

## Why pair them

FGO simulates GPUs at the Kubernetes API level. It advertises GPU resources to
the scheduler without touching the driver stack, which makes it very fast and
lets it back hundreds of KWOK virtual nodes.

Mokka simulates at the driver level. A real `libnvidia-ml.so` inside the pod
means `nvidia-smi`, DCGM and the GPU Operator validator all work.

Pairing them gives a **mixed cluster**: a handful of nodes with full driver
fidelity, and a large fleet of cheap virtual ones.

| | FGO alone | Mokka alone | Together |
|---|---|---|---|
| Advertises GPUs to the scheduler | yes | yes | yes |
| KWOK virtual nodes | yes | no | yes |
| Real NVML inside pods | no | yes | on the Mokka nodes |
| Real `nvidia-smi` output | limited | yes | on the Mokka nodes |
| DCGM metrics | 3 synthetic | real | real on the Mokka nodes |
| GPU Operator validation | no | yes | on the Mokka nodes |
| Scale to 1000+ nodes | yes | no | yes |

!!! warning "This pairing is not exercised in CI"
    Mokka's side of the contract is verified — the ConfigMap shape is asserted
    by unit tests and by the `fgo` end-to-end case. But the two projects have
    never been run together in CI, and FGO is neither vendored nor pinned here.
    Re-check FGO's loader before relying on any of this.

## The discovery contract

This is the part that breaks silently, so it is worth understanding before you
install anything.

FGO does **not** watch for profile ConfigMaps or select them by label. Its
loader does a direct `Get` by name and reads one data key. Three things must
match exactly, and each is fatal on its own:

| Field | Value | If it is wrong |
|---|---|---|
| Name | `gpu-profile-<profile>` | `NotFound` |
| Data key | `profile.yaml` | "missing key" |
| Namespace | FGO's own release namespace | `NotFound` — the loader has no cross-namespace fallback |

Because the names carry no release prefix, two Mokka releases in one namespace
collide on these seven ConfigMaps. Enable the integration on one release per
namespace.

!!! warning "Set FGO's `builtinProfiles.enabled=false` first"
    FGO ships its own profiles under the same seven names, owned by its own
    Helm release. Helm 3 refuses to adopt resources owned by another release,
    so whichever chart installs second fails with `invalid ownership metadata`.
    The two profile sets are alternatives, not complements.

## Prerequisites

- [Docker](https://docs.docker.com/get-started/get-docker/) and
  [Kind](https://kind.sigs.k8s.io/) — this guide creates its own cluster
  (`nvml-mock-fgo-demo`) and never touches your current context.
- [Helm 3.8 or newer](https://helm.sh/docs/intro/install/) — the chart is served
  from an OCI registry.
- [kubectl](https://kubernetes.io/docs/reference/kubectl/)
- A clone of this repository, for the Kind topology file.

Takes about 10 minutes.

## Step 1 — Create a cluster

The topology labels one worker `integration` and two `scale`. Without those
labels Helm installs successfully and produces no pods for the missing pool.

```bash
kind create cluster --name nvml-mock-fgo-demo --config=docs/guides/kind.yaml
```

## Step 2 — Install Mokka on the integration pool

The ConfigMaps carry FGO's namespace in their own metadata, so that namespace
has to exist before Mokka writes them:

```bash
kubectl --context kind-nvml-mock-fgo-demo create namespace gpu-operator

helm install nvml-mock oci://ghcr.io/nvidia/k8s-test-infra/chart/nvml-mock \
  --kube-context kind-nvml-mock-fgo-demo \
  --set integrations.fakeGpuOperator.enabled=true \
  --set integrations.fakeGpuOperator.targetNamespace=gpu-operator \
  --set gpu.profile=h100 \
  --set gpu.count=8 \
  --set "nodeSelector.run\.ai/simulated-gpu-node-pool=integration" \
  --wait --timeout 120s
```

`integrations.fakeGpuOperator.enabled=true` is what emits the profile
ConfigMaps in the shape FGO's loader reads. `targetNamespace` puts them where
it reads: without it they land in Mokka's release namespace, which FGO never
looks in.

## Step 3 — Install FGO on the scale pool

```bash
helm upgrade --install gpu-operator \
  oci://ghcr.io/run-ai/fake-gpu-operator/fake-gpu-operator \
  --kube-context kind-nvml-mock-fgo-demo \
  -n gpu-operator \
  --set builtinProfiles.enabled=false \
  --wait --timeout 120s -f - <<EOF
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

`backend: mock` hands the pool to Mokka; `backend: fake` keeps it on FGO's own
shim. `builtinProfiles.enabled=false` is what lets this install succeed at all
now that Mokka owns those seven ConfigMap names — see
[the discovery contract](#the-discovery-contract).

## Step 4 — Verify

```bash
CTX=kind-nvml-mock-fgo-demo

# Mokka runs only on the integration worker.
kubectl --context $CTX get pods -l app.kubernetes.io/name=nvml-mock -o wide

# The profile ConfigMaps exist, in the namespace FGO reads.
kubectl --context $CTX -n gpu-operator get cm -l fake-gpu-operator/gpu-profile=true

# A real nvidia-smi, on a node with no GPU.
kubectl --context $CTX exec ds/nvml-mock -- nvidia-smi

# FGO runs on the scale workers.
kubectl --context $CTX get pods -l app=fake-gpu-operator -o wide
```

You should end up with:

| Node   | Pool          | Backend | GPUs come from | Mokka DaemonSet |
|--------|---------------|---------|----------------|-----------------|
| worker | `integration` | `mock`  | Mokka          | yes             |
| worker | `scale`       | `fake`  | FGO            | no              |

## When FGO does not pick up the profiles

Check the name and namespace, not the label — the label is not on FGO's load
path:

```bash
kubectl get cm gpu-profile-h100 -n gpu-operator -o name
```

A `NotFound` means one of three things: the ConfigMaps are still in Mokka's
namespace (set `targetNamespace`), the name is wrong, or the install hit the
ownership error above. If the ConfigMap exists, confirm the body is under
`profile.yaml`:

```bash
kubectl get cm gpu-profile-h100 -n gpu-operator -o jsonpath='{.data.profile\.yaml}' | head -5
```

Restart FGO's controller after fixing any of these:

```bash
kubectl rollout restart deployment fake-gpu-operator -n gpu-operator
```

## Custom labels on the ConfigMaps

`profileLabels` adds labels but cannot remove the contract ones, so an override
here cannot break discovery:

```yaml
integrations:
  fakeGpuOperator:
    enabled: true
    profileLabels:
      my-org/custom-label: "gpu-sim"
```

## Clean up

```bash
kind delete cluster --name nvml-mock-fgo-demo
```

## Related

| To read about | See |
|---|---|
| Every chart value | [Installation](../../helm-chart.md) |
| What a profile defines | [Configuration](../../configuration.md) |
| Mokka on its own | [Quick Start](../../quickstart.md) |
