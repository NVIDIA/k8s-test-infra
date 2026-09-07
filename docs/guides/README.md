# Guides

Runnable walkthroughs, each one a specific thing you can do with Mokka.

Every guide creates its own Kind cluster and leaves your current context alone,
and every one builds the image from a clone of this repository. So you need
[Docker](https://docs.docker.com/get-started/get-docker/),
[Kind](https://kind.sigs.k8s.io/),
[Helm 3.8 or newer](https://helm.sh/docs/intro/install/),
[kubectl](https://kubernetes.io/docs/reference/kubectl/), and the repo checked
out.

## Choosing one

Roughly in order of how much they ask of you. Start at the top if you are new.

| Guide | What it shows | Time |
|---|---|---|
| [Standalone](standalone/README.md) | Mokka on its own: mock GPUs, `nvidia-smi`, InfiniBand, and FGO-style labels, with no external operator | ~5 min |
| [With fake-gpu-operator](runai-fgo/README.md) | Two node pools — Mokka serving one with a real NVML shim, Run:ai's FGO serving the other | ~10 min |
| [Failure injection](failure-injection/README.md) | Every failure mode — healthy, uncorrectable ECC, lost GPU, fallen off the bus — asserted against `nvidia-smi` | ~15 min |
| [Node-wide injection](node-wide-injection/README.md) | A plain pod running `nvidia-smi` with no GPU request, no annotation and no pod-spec change, via NRI | ~10 min |
| [ComputeDomain](compute-domain/README.md) | NVLink fabric identity, with a real `nvidia-imex` forming a live domain over mock GPUs | 10–20 min |
| [NVSentinel](nv-sentinel/README.md) | The full health loop: detect a thermal-margin crossing, cordon and drain, then auto-recover on cooldown | ~30 min |

The last two are the most involved: ComputeDomain needs a four-worker cluster
with containerd NRI enabled, and NVSentinel pulls the GPU Operator,
cert-manager and NVSentinel before it can start.

## Observability (Prometheus + Grafana)

Not a standalone guide. It composes with the GPU Operator rather than replacing
it, so it lives in the Tilt environment instead of shipping its own cluster and
`run.sh`.

Prometheus scrapes the real, unmodified NVIDIA `dcgm-exporter` while it reads the
mock `libnvidia-ml.so`, and Grafana renders the result — on a cluster with no
GPUs. Two manual triggers then inject a temperature or Xid fault and fail if it
never reaches Prometheus, so the scrape path is asserted rather than eyeballed.

```bash
make cluster-create
tilt up -- --observability
```

See [Local Development](../contributing/local-development.md) for the Tilt
environment and every flag it takes.
