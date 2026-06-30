# nvml-mock Demos

This directory contains end-to-end demos showing how to deploy nvml-mock on a
local Kind cluster.

## Available Demos

### Standalone

Deploy nvml-mock with FGO-style GPU labels on a Kind cluster. No external
GPU operator is required -- nvml-mock generates the labels and ConfigMaps
itself.

**Requirements:** Docker, Kind, Helm

```bash
cd standalone && ./demo.sh
```

### With fake-gpu-operator

Full integration with Run:ai's fake-gpu-operator. nvml-mock handles the
"integration" node pool (real NVML shim) while FGO handles the "scale" pool
(lightweight fake shim).

**Requirements:** Docker, Kind, Helm, fake-gpu-operator Helm chart

See [with-fgo/README.md](with-fgo/README.md) for the step-by-step guide.

### Failure injection

Dedicated cluster (`nvml-mock-failure-demo`) that deploys nvml-mock with
GPU failure injection enabled and verifies the engine actually trips
the configured fault. Demonstrates `ecc_uncorrectable` end-to-end and
prints copy-pasteable commands to switch the running release into
`lost` / `fallen_off_bus` mode.

**Requirements:** Docker, Kind, Helm

```bash
cd failure-injection && ./run.sh
```

See [failure-injection/README.md](failure-injection/README.md) for the
walkthrough.

### ComputeDomain (NVLink fabric)

Dedicated cluster (`nvml-mock-compute-domain`) with 4 workers sharing a
hostPath state directory. Exercises the mock NVML fabric APIs
(`nvmlDeviceGetGpuFabricInfo` / `…InfoV`) driven by a cluster-level
topology ConfigMap, plus the fake `nvidia-imex` /
`nvidia-imex-ctl` binaries coordinating peer readiness through marker
files on the shared volume. Concludes with a `helm upgrade` that
rebinds every node into a new clique without rebuilding the image.

**Requirements:** Docker, Kind, Helm

```bash
cd compute-domain && ./run.sh
```

See [compute-domain/README.md](compute-domain/README.md) for the
walkthrough.

### Topograph (network topology discovery)

Dedicated cluster (`nvml-mock-topograph`, 1 control-plane + 4 workers) that
runs [NVIDIA/topograph](https://github.com/NVIDIA/topograph) against an
nvml-mock-simulated GB200 cluster. topograph's node-data-broker reads each
node's NVLink clique from the mock `nvidia-smi -q` and the server applies
`network.topology.nvidia.com/accelerator` labels that partition the workers
into two NVLink accelerator domains — all without real GPUs, HCAs, or
switches. (nvml-mock's fabric is switchless, so only the `accelerator` label
is produced, not `leaf`/`spine`/`core`; see the README for the documented
follow-up.)

**Requirements:** Docker, Kind, Helm (+ network access to the topograph chart)

```bash
cd topograph && ./run.sh
```

See [topograph/README.md](topograph/README.md) for the walkthrough.
