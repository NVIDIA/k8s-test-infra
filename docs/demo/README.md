# nvml-mock Demos

This directory contains end-to-end demos showing how to deploy nvml-mock on a
local Kind cluster.

## Available Demos

### Node-wide injection (NRI)

Dedicated cluster (`nvml-mock-node-wide-demo`) with containerd NRI enabled.
Installs the `nvml-mock-nri` DaemonSet and proves a plain pod can run
`nvidia-smi` without GPU requests, annotations, or pod-spec mutation.

**Requirements:** Docker, Kind, Helm

```bash
cd node-wide-injection && ./run.sh
```

See [node-wide-injection/README.md](node-wide-injection/README.md) for the
walkthrough.

### IMEX channels (NRI)

Dedicated cluster (`nvml-mock-nri-imex`) with containerd NRI enabled.
Provisions mock IMEX channel device nodes on the host and proves an
annotated workload sees `/dev/nvidia-caps-imex-channels/*` injected over
NRI, without GPU requests or pod-spec mutation.

**Requirements:** Docker, Kind, Helm

```bash
cd nri-imex && ./run.sh
```

See [nri-imex/README.md](nri-imex/README.md) for the walkthrough.

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

Dedicated cluster (`nvml-mock-compute-domain`) with 4 workers.
Exercises the mock NVML fabric APIs (`nvmlDeviceGetGpuFabricInfo` /
`…InfoV`) driven by a cluster-level topology ConfigMap, plus the REAL
`nvidia-imex` daemon in NO GPU mode (`--nogpu`, injected by
`imex-nogpu-shim`) forming a live gRPC IMEX domain over the pod
network — readiness, version handshake, and peer-death detection are
the real protocol, not a simulation. Concludes with a `helm upgrade`
that rebinds every node into a new clique without rebuilding the
image.

**Requirements:** Docker, Kind, Helm, kubectl, jq

```bash
cd compute-domain && ./run.sh
```

See [compute-domain/README.md](compute-domain/README.md) for the
walkthrough.
