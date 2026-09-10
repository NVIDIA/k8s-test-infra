# Guides

Runnable walkthroughs, each one a specific thing you can do with Mokka.

Every guide needs [Docker](https://docs.docker.com/get-started/get-docker/),
[Kind](https://kind.sigs.k8s.io/),
[Helm 3.8 or newer](https://helm.sh/docs/intro/install/) and
[kubectl](https://kubernetes.io/docs/reference/kubectl/). Some also need this
repository checked out, and not all of them create a throwaway cluster of their
own — each guide states what it targets, and what it needs, before it installs
anything.

## Scenarios

Standing Mokka up alongside a real consumer. Roughly in order of how much they
ask of you — start at the top if you are new.

| Guide | What it shows | Time |
|---|---|---|
| [NVIDIA Device Plugin](device-plugin.md) | Mock GPUs advertised as `nvidia.com/gpu`, and a workload scheduled against them | ~5 min |
| [NVIDIA GPU Operator](gpu-operator.md) | The real operator stack — device plugin, GFD, DCGM and the validator — against mock GPUs | ~15 min |
| [NVIDIA DRA Driver](dra.md) | Mock GPUs published as ResourceSlices, and a pod scheduled through a ResourceClaim | ~10 min |
| [Run:ai fake-gpu-operator](runai-fgo/README.md) | Two node pools — Mokka serving one with a real NVML shim, FGO serving the other | ~10 min |
| [Node-wide injection](node-wide-injection/README.md) | A plain pod running `nvidia-smi` with no GPU request, no annotation and no pod-spec change, via NRI | ~10 min |
| [ComputeDomain](compute-domain/README.md) | NVLink fabric identity, with a real `nvidia-imex` forming a live domain over mock GPUs | 10–20 min |
| [NVSentinel](nv-sentinel/README.md) | The full health loop: detect a thermal-margin crossing, cordon and drain, then auto-recover on cooldown | ~30 min |

The last two are the most involved: ComputeDomain needs a four-worker cluster
with containerd NRI enabled, and NVSentinel pulls the GPU Operator,
cert-manager and NVSentinel before it can start.

## Tasks

Things you do *with* Mokka, whichever consumer you are running.

| Guide | What it covers |
|---|---|
| [Failure injection](failure-injection/README.md) | Present a broken GPU — uncorrectable ECC, lost, fallen off the bus — and watch consumers react |
| [Use in CI/CD](ci-cd.md) | Run GPU-dependent tests on CPU runners |
| [Runtime control](../nvml-mock-ctl.md) | Change temperature, power, utilisation or health on a running node, with no redeploy |

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
