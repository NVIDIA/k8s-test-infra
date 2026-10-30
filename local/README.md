# Local development environment

Tiltfiles, Helm value overrides and Kind cluster configs for running Mokka on a
laptop. The workflow (creating the cluster, the Tilt flags, what composes,
overriding Helm values) is in
[Local Development](../docs/contributing/local-development.md). This page holds
what is specific to this directory.

## Cluster

Both cluster shapes use the stock node image the Makefile pins (`KIND_NODE_IMAGE`):
the Mokka node daemon installs the NVIDIA container runtime and registers the
`nvidia` handler with containerd itself (see
[Container runtime setup](../docs/components/node-daemon.md#container-runtime-setup)).

An Xid injected with `nvml-mock-ctl fail --xid` is readable from the node journal.
Kind names node containers `<cluster>-worker[N]` rather than after the node names the
configs pin, so `worker-0` is `mokka-worker`:

```bash
docker exec mokka-worker journalctl -k | grep 'NVRM: Xid'
```

## Compute-domain scenario

With `--dra`, the DRA driver runs ComputeDomains with its upstream images. The
node agent stages IMEX for them, from an `imex.binaries.image` set in
`local/nvml-mock.values.local.yaml` or, while the `IMEXDownload` feature gate is
on, a download from NVIDIA. Tilt installs the driver once every node has IMEX.

## Consumers

Notes the Local Development page does not carry.

### FGO

`--fgo` runs the mock on the `integration` worker and FGO's fake backend on the
`scale` worker; `--multi-gpu-profile --fgo` produces the same shape. To check:

```bash
kubectl get pods -n mokka -l app.kubernetes.io/name=nvml-mock -o wide   # the mock, on the integration worker
kubectl get configmaps -A -l run.ai/gpu-profile=true                    # profiles shaped for FGO's loader
kubectl get pods -n gpu-operator -o wide                                # FGO, on the scale worker
```

### topograph

See [topograph/README.md](topograph/README.md).

### Observability

See [observability/README.md](observability/README.md).

### Dynamo

The worker sees the GPU profile `--gpu-profile` selected only through the nvml-mock
NRI plugin, which runs by default; the
[NVIDIA Dynamo guide](../docs/guides/dynamo/README.md) explains why. Once
`dynamo-frontend` is ready, the OpenAI API is on <http://localhost:8000>:

```bash
curl -s localhost:8000/v1/chat/completions -H 'Content-Type: application/json' \
  -d '{"model":"Qwen/Qwen3-0.6B","messages":[{"role":"user","content":"hi"}],"max_tokens":16}'
```

### Slinky

`slurmd` detects GPUs once at startup and `slurmctld` keeps a node's first
GPU-to-CPU binding, so after reinstalling nvml-mock or switching `--gpu-profile`,
delete the node records (`scontrol delete nodename=...`) and then the `slurmd` pods.
Submit a job from the controller:

```bash
kubectl -n slurm exec slurm-controller-0 -c slurmctld -- srun -N1 --gres=gpu:2 nvidia-smi -L
```

The [Slinky guide](../docs/guides/slinky/README.md) walks through the same setup from
published artifacts.
