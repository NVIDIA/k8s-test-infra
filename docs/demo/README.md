# nvml-mock Demos

End-to-end demos for nvml-mock.

Most run on any Kubernetes cluster: point your `KUBECONFIG` at one and go.
Two of them (node-wide injection and ComputeDomain) need containerd NRI
enabled on every node, so they provision a dedicated Kind cluster instead,
and say so in their own README. Every demo needs Helm
(<https://helm.sh/docs/intro/install/>), but the floor is not the same for all
of them: it ranges from 3.6 to 3.13 depending on whether the demo pulls a chart
from an OCI registry and which flags it uses. Each entry below states its own,
and each demo's README explains why. No cluster? Start with the
[quick start](../quickstart.md).

Run one nvml-mock demo per cluster at a time. The chart's hostPath mounts are
scoped by neither release nor namespace and the DaemonSet tolerates every
node, so two of these releases collide on per-node host state even from
different namespaces. The standalone and failure-injection demos refuse to
install over each other (exit `4`), because the shared mock config left behind
is what any real GPU workload on those nodes goes on to read.

## Available Demos

### Node-wide injection (NRI)

Dedicated cluster (`nvml-mock-node-wide-demo`) with containerd NRI enabled.
Installs the `nvml-mock-nri` DaemonSet and proves a plain pod can run
`nvidia-smi` without GPU requests, annotations, or pod-spec mutation.

**Requirements:** Docker, Kind, Helm 3.6+, kubectl (creates its own cluster)

```bash
cd node-wide-injection && ./run.sh
```

See [node-wide-injection/README.md](node-wide-injection/README.md) for the
walkthrough.

### Standalone

Deploy nvml-mock with FGO-style GPU labels on the cluster your current
context points at. No external GPU operator is required -- nvml-mock
generates the labels and ConfigMaps itself.

**Requirements:** KUBECONFIG, Helm 3.6+, kubectl

```bash
cd standalone && ./demo.sh
```

### With fake-gpu-operator

Full integration with Run:ai's fake-gpu-operator. nvml-mock handles the
"integration" node pool (real NVML shim) while FGO handles the "scale" pool
(lightweight fake shim).

**Requirements:** KUBECONFIG, Helm 3.8+ (OCI chart), kubectl, fake-gpu-operator Helm chart

See [with-fgo/README.md](with-fgo/README.md) for the step-by-step guide.

### Failure injection

Deploys nvml-mock into your current context with GPU failure injection
enabled (`BUILD_LOCAL=true` uses a dedicated `nvml-mock-failure-demo`
cluster instead) and verifies the engine actually trips
the configured fault. Exercises all four modes end to end as four
scenarios (`healthy`, `ecc_uncorrectable`, `lost`, `fallen_off_bus`),
upgrading the running release into each one and asserting the result
against `nvidia-smi` output.

**Requirements:** KUBECONFIG, Helm 3.6+, kubectl

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
`nvidia-imex-shim`) forming a live gRPC IMEX domain over the pod
network — readiness, version handshake, and peer-death detection are
the real protocol, not a simulation. Concludes with a `helm upgrade`
that rebinds every node into a new clique without rebuilding the
image.

**Requirements:** Docker, Kind, Helm 3.13+, kubectl, jq (creates its own cluster)

```bash
cd compute-domain && ./run.sh
```

See [compute-domain/README.md](compute-domain/README.md) for the
walkthrough.

### NVSentinel thermal-margin detection + remediation

Dedicated cluster (`nvml-mock-nvsentinel`) with 1 control-plane + 2 workers.
Wires the mock GPUs into the NVIDIA GPU Operator's standalone DCGM and then into
[NVSentinel](https://github.com/NVIDIA/nvsentinel). Heats one worker's GPU past
its slowdown limit and proves the full loop: NVSentinel **detects** the thermal
margin crossing via DCGM + the metadata-collector's slowdown offset,
**remediates** by cordoning + draining the node (the sample GPU workload
reschedules to the healthy worker), and then **auto-recovers** — cooling the GPU
uncordons the node automatically, with no DCGM restart.

**Requirements:** Docker, Kind, Helm 3.8+ (OCI chart), kubectl (jq optional; creates its own cluster)

```bash
cd nv-sentinel && ./run.sh
```

See [nv-sentinel/README.md](nv-sentinel/README.md) for the walkthrough.

### With the NVIDIA GPU Operator

Deploys the real upstream `nvidia/gpu-operator` chart against mock GPUs on the
cluster your current context points at. The device plugin, GFD, dcgm-exporter
and validator operands are unmodified; only the driver and container-toolkit
operands, which need real hardware, are disabled. Asserts that a node ends up
with `nvidia.com/gpu` in its allocatable and a GFD-published
`nvidia.com/gpu.product` label.

Needs a node with containerd in CDI mode and the `nvidia` runtime handler
registered, which `make cluster-create` provides and a plain
`kind create cluster` does not.

**Requirements:** KUBECONFIG, Helm 3.8+, kubectl

```bash
cd with-gpu-operator && ./run.sh
```

See [with-gpu-operator/README.md](with-gpu-operator/README.md) for the
walkthrough.

## Observability (Prometheus + Grafana)

Not a standalone demo. It composes with the GPU Operator rather than replacing
it, so it lives in the Tilt environment instead of shipping its own cluster and
`run.sh`.

Prometheus scrapes the real, unmodified NVIDIA `dcgm-exporter` while it reads the
mock `libnvidia-ml.so`, and Grafana renders the result — on a cluster with no
GPUs. Two manual triggers then inject a temperature or Xid fault and fail if it
never reaches Prometheus, so the scrape path is asserted rather than eyeballed.

**Requirements:** Docker, Kind, Helm, kubectl, jq, Tilt

```bash
make cluster-create
tilt up -- --observability
```

See [local/observability/README.md](https://github.com/NVIDIA/k8s-test-infra/blob/main/local/observability/README.md) for the
walkthrough, and [local/README.md](https://github.com/NVIDIA/k8s-test-infra/blob/main/local/README.md) for the other Tilt
flags.
