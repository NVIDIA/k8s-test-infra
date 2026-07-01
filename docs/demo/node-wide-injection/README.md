# nvml-mock node-wide injection Demo

End-to-end walkthrough of the **nvml-mock node-wide mutating admission webhook**
turning an **ordinary pod into a working mock GPU node** — **no physical GPUs,
HCAs, or switches, and the workload requests no GPU and mounts no mock libraries
of its own.**

The demo runs a plain `gpu-agent` DaemonSet (`debian:bookworm-slim`, no
`nvidia.com/gpu` request, no hostPath mounts) and lets the **nvml-mock injector
webhook** add the mock GPU stack at admission time. The pod then successfully
runs `nvidia-smi` — proving that **injection alone is enough to make any pod
believe a GPU is present.**

The demo reuses the shared [`docs/demo/kind.yaml`](../kind.yaml) topology
(cluster `nvml-mock-injection`, 1 control-plane + 4 workers) and partitions the
workers into two NVLink cliques, so `nvidia-smi` inside each pod reports its
node's clique:

```text
domain "demo-domain"              uuid 00000000-0000-0000-0000-0000000000ab
  clique 0:
    - nvml-mock-injection-worker
    - nvml-mock-injection-worker2
  clique 1:
    - nvml-mock-injection-worker3
    - nvml-mock-injection-worker4
```

`run.sh` asserts that the injected `nvidia-smi` reports
`<ClusterUUID>.<CliqueId>` = `…ab.0` on the clique-0 nodes and `…ab.1` on the
clique-1 nodes — distinct values, proving the per-node (NODE_NAME-keyed)
injection works.

## Prerequisites

The demo expects the following tools on `$PATH`:

| Tool      | Tested version | Notes |
|---        |---             |---    |
| `docker`  | 24+            | Daemon must be running. Multi-stage build uses the Go base image. |
| `kind`    | v0.24+         | Creates the `nvml-mock-injection` cluster from the shared `docs/demo/kind.yaml` (1 cp + 4 workers). |
| `kubectl` | v1.30+         | Used for `exec`, `rollout`, `get`/`apply` against in-cluster pods. |
| `helm`    | v3.13+         | Installs the nvml-mock chart (with injector). |
| `bash`    | 3.2+           | `run.sh` uses `set -euo pipefail` — no bash 4+ features. |

## Quick start

```bash
./run.sh
```

The script is idempotent. By default an existing `nvml-mock-injection`
cluster is reused; pass `FORCE_RECREATE=true ./run.sh` to tear it down and
start clean. Env knobs:

| Variable | Default | Purpose |
|---       |---      |---      |
| `GPU_PROFILE` | `gb200` | nvml-mock GPU profile. |
| `NVML_NS` | `nvml-mock` | Namespace for the nvml-mock release. Must differ from the injected workload's namespace (`default`), since the webhook excludes its own release namespace. |
| `FORCE_RECREATE` | `false` | Tear down an existing cluster first. |

## How it works

Three moving parts: nvml-mock stages the mock GPU stack on the host and runs the
injector webhook; the ordinary `gpu-agent` pod gets that stack injected; the pod
then runs `nvidia-smi`.

1. **nvml-mock stages the mock GPU stack and installs the injector.** The chart
   is installed with `topology.enabled=true` + a two-clique topology document
   ([`clique-topology.yaml`](./clique-topology.yaml)) and
   `injector.enabled=true`. It is installed into its **own `nvml-mock`
   namespace** — the webhook always excludes its release namespace, so it must
   not share a namespace with the injected `gpu-agent` (which runs in
   `default`). The nvml-mock DaemonSet stages the mock `libnvidia-ml.so.1` /
   `nvidia-smi` and the topology config onto each node's host filesystem, and
   the injector registers a `MutatingWebhookConfiguration` (`nvml-mock-injector`)
   that mutates every non-excluded pod.

2. **The webhook injects an ordinary pod.** [`gpu-agent.yaml`](./gpu-agent.yaml)
   is a plain DaemonSet — **no `nvidia.com/gpu` request, no hostPath mounts,
   stock `debian:bookworm-slim`.** At admission the webhook adds the mock
   overlay hostPath mount plus `PATH` / `LD_LIBRARY_PATH` / `LD_PRELOAD` /
   `MOCK_*` env, and stamps the pod with `nvml-mock.nvidia.com/injected=true`.
   The **only** GPU-relevant thing the pod supplies itself is `NODE_NAME`
   (downward API) — the field the mock NVML engine keys the per-node NVLink
   clique override on.

3. **The pod runs nvidia-smi.** `gpu-agent`'s container runs `nvidia-smi` at
   startup and then `nvidia-smi -L` on a loop (see
   `kubectl logs -n default ds/gpu-agent`). Because injection put the mock
   `nvidia-smi` on `PATH` and `libnvidia-ml.so.1` on `LD_PRELOAD`, and NODE_NAME
   resolves the clique, it reports a full mock GPU and the node's fabric:

   ```text
   Fabric
       CliqueId                          : 0
       ClusterUUID                       : 00000000-0000-0000-0000-0000000000ab
   ```

   `run.sh` fails fast if the annotation is missing, if the pod somehow requests
   a GPU, or if the injected `nvidia-smi` does not run / report the expected
   per-node clique.

### Why `libnvidia-ml.so.1` is on `LD_PRELOAD`

The stock NVIDIA `nvidia-smi` probes a fixed set of directories / the `ld.so`
cache to locate `libnvidia-ml.so.1` and **ignores `LD_LIBRARY_PATH`**. A library
staged under the overlay (not a default/cached path) is therefore never found
(`NVIDIA-SMI couldn't find libnvidia-ml.so`). The injector preloads it (and
`libcuda.so.1` for CUDA consumers) so the mock driver resolves in any glibc
image without modifying the container's `ld.so` cache.

### Injection is glibc-only

The injected overlay ships **glibc** mock libraries (`libnvidia-ml.so.1` plus
the IB/PCI `LD_PRELOAD` shims). musl's dynamic loader cannot relocate glibc
objects, so injecting an **Alpine/musl** pod breaks every dynamically-linked
command (`Error relocating ... symbol not found`, exit 127). Only inject pods
whose libc is compatible (glibc ≥ the nvml-mock image's, e.g.
`debian:bookworm`); exclude musl images via `injector.excludedNamespaces` or the
`nvml-mock.nvidia.com/inject: "false"` opt-out annotation.

## Manual reproduction

The commands below mirror what `run.sh` issues, for the default
`nvml-mock-injection` cluster name.

```bash
# 1. Create the cluster from the shared topology (1 control-plane + 4 workers).
kind create cluster --name nvml-mock-injection \
    --config docs/demo/kind.yaml

# 2. Build + load the nvml-mock image.
docker build -t nvml-mock:injection-demo -f deployments/nvml-mock/Dockerfile .
kind load docker-image nvml-mock:injection-demo --name nvml-mock-injection

# 3. Install nvml-mock (gb200 + two NVLink cliques + FGO labels + injector).
#    NOTE: nvml-mock lives in its own namespace. The webhook always excludes its
#    release namespace, so it MUST NOT be the namespace where gpu-agent runs
#    (`default`) -- otherwise gpu-agent would never get injected.
helm upgrade --install nvml-mock deployments/nvml-mock/helm/nvml-mock \
    --namespace nvml-mock --create-namespace \
    -f docs/demo/node-wide-injection/clique-topology.yaml \
    --set image.repository=nvml-mock \
    --set image.tag=injection-demo \
    --set integrations.fakeGpuOperator.enabled=true \
    --set injector.enabled=true \
    --set 'injector.excludedNamespaces={kube-system,nvml-mock}' \
    --set gpu.profile=gb200 \
    --wait --timeout 180s
# Recycle so the freshly built (same-tagged) image is the one running.
kubectl rollout restart daemonset/nvml-mock -n nvml-mock
kubectl rollout status  daemonset/nvml-mock -n nvml-mock --timeout=120s
kubectl rollout status  deployment/nvml-mock-injector -n nvml-mock --timeout=120s

# 4. Deploy the ORDINARY gpu-agent pod and confirm the webhook injected it.
kubectl apply -f docs/demo/node-wide-injection/gpu-agent.yaml
kubectl rollout status daemonset/gpu-agent -n default --timeout=120s
POD=$(kubectl get pods -n default -l app=gpu-agent \
    --field-selector spec.nodeName=nvml-mock-injection-worker \
    -o jsonpath='{.items[0].metadata.name}')
kubectl get pod -n default "$POD" \
    -o jsonpath='{.metadata.annotations.nvml-mock\.nvidia\.com/injected}'   # -> true

# 5. Run nvidia-smi through the injected mock stack.
kubectl exec -n default "$POD" -- nvidia-smi
kubectl exec -n default "$POD" -- nvidia-smi -q | grep -E 'ClusterUUID|CliqueId'
kubectl logs -n default ds/gpu-agent
```

`-worker` / `-worker2` report clique `00000000-0000-0000-0000-0000000000ab.0`;
`-worker3` / `-worker4` report `…ab.1`.

> **Image tag gotcha.** The demo always tags the image
> `nvml-mock:injection-demo`. When you reuse an existing cluster, a rebuilt
> image keeps the same tag, so `helm upgrade` leaves the DaemonSet pod
> template untouched and Kubernetes never recycles the pods — they keep the
> previously loaded image. `run.sh` issues an explicit
> `kubectl rollout restart daemonset/nvml-mock` after the install to force the
> freshly built image into the running pods.

## Custom cluster name

If you rename the Kind cluster, the worker names in
[`clique-topology.yaml`](./clique-topology.yaml) must change in lockstep — each
Kind worker is named `<cluster-name>-worker[N]`, and the topology document keys
cliques by node name. `run.sh` derives the clique node arrays from
`CLUSTER_NAME`, but the checked-in `clique-topology.yaml` hard-codes the default
`nvml-mock-injection-*` names to keep the example faithful.

## Clean up

```bash
kind delete cluster --name nvml-mock-injection
```
