# Use in CI/CD

Run tests that need GPUs on ordinary CPU runners. Mokka installs into a
throwaway Kubernetes cluster inside the job, your software talks to it as though
the hardware were real, and the job tears everything down at the end.

This is the use case Mokka exists for: GPU runners are scarce and expensive, and
most of what GPU software does in CI is *discover* and *schedule* hardware
rather than compute on it.

## The shape of a job

Four steps, in this order:

1. Create a Kubernetes cluster on the runner — Kind is the usual choice.
2. Install the Mokka chart, pinned to a version.
3. Wait for the DaemonSet to be ready.
4. Run your tests against the cluster.

## GitHub Actions

```yaml
name: GPU tests

on: [push, pull_request]

jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - name: Create a cluster
        uses: helm/kind-action@v1
        with:
          cluster_name: mokka

      - name: Install Mokka
        run: |
          helm install mokka \
            oci://ghcr.io/nvidia/k8s-test-infra/chart/nvml-mock \
            --version 0.3.0 \
            --namespace mokka --create-namespace \
            --set gpu.profile=gb300 \
            --wait --timeout 5m

      - name: Run tests
        run: ./run-my-gpu-tests.sh
```

`--wait` is what makes this reliable: without it the next step starts before the
DaemonSet is serving, and your tests fail on a node that does not report GPUs
yet.

## Pin the chart version

`--version 0.3.0` rather than floating. An unpinned chart means an upstream
release can change what your CI sees on a day you changed nothing, and the
failure will not look like a version change.

## Choosing a profile

The profile decides what hardware your software believes it found, so pick the
one your tests assert against:

```yaml
--set gpu.profile=a100      # 8 GPUs, 40 GiB each
--set gpu.profile=t4        # 4 GPUs, 16 GiB each — the smallest
--set gpu.profile=gb200     # 4 GPUs with NVLink fabric
```

To exercise more than one, run a job matrix over profiles rather than
installing several releases into one cluster.

## Testing failure handling

Mokka can present broken hardware, which is otherwise almost impossible to
arrange in CI. A GPU that has fallen off the bus, an uncorrectable ECC error, or
a device that reports as lost are all configuration:

```yaml
--set gpu.failureInjection.enabled=true \
--set gpu.failureInjection.mode=ecc_uncorrectable
```

To move a GPU between healthy and broken *within* a single job, use
[runtime control](../nvml-mock-ctl.md) instead of reinstalling — it takes effect
in about a second and needs no rollout.

## What needs more than a plain cluster

The mock driver and `nvidia-smi` work on any Kind cluster. Consumers that
allocate GPUs need more:

| Testing this | Also needs |
|---|---|
| The mock driver, `nvidia-smi`, node labels | nothing beyond a cluster |
| The NVIDIA device plugin, DRA driver, or GPU Operator | a Kind node image with CDI enabled in containerd |
| Node-wide NRI injection | containerd with the NRI socket enabled |

[Installation](../helm-chart.md) covers building a node image for those paths.

## Keeping jobs fast

Most of the wall-clock is image pulls, not Mokka. Cache the runner's image layers
if your CI supports it, and prefer the smallest profile that still exercises what
you are testing — `t4` presents four GPUs instead of eight.

## Related

| To read about                   | See                                              |
|---------------------------------|--------------------------------------------------|
| Every chart value               | [Installation](../helm-chart.md)                 |
| What a profile defines          | [Configuration](../configuration.md)             |
| Changing GPU state mid-job      | [Runtime Control](../nvml-mock-ctl.md)           |
| A failure-injection walkthrough | [Failure injection](failure-injection/README.md) |
