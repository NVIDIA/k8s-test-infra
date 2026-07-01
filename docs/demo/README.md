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

### Node-wide injection

Dedicated cluster (`nvml-mock-injection`) that installs nvml-mock with the
node-wide mutating injector enabled, then runs an **ordinary `gpu-agent` pod**
(no GPU request, no mock mounts, stock `debian` image) that the webhook turns
into a working mock GPU node at admission time. The pod runs `nvidia-smi`
successfully and reports its node's NVLink clique -- proving injection alone is
enough to make any pod believe a GPU is present.

**Requirements:** Docker, Kind, Helm

```bash
cd node-wide-injection && ./run.sh
```

See [node-wide-injection/README.md](node-wide-injection/README.md) for the
walkthrough.
