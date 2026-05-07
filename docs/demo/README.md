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
