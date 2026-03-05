# GPU Mock PoC Validation

Reproducible scripts for validating NVIDIA GPU consumers (device plugin, DRA driver) against the gpu-mock infrastructure on Kind clusters.

## Prerequisites

- Docker (with daemon running)
- kind
- helm
- kubectl
- jq

## Quick Start

```bash
# Full validation (device plugin + DRA driver)
./run-all.sh --profile a100 --gpu-count 8

# Results in ./logs/
```

## Individual Scripts

```bash
# Phase 1: Device Plugin
./setup-kind-cluster.sh --profile a100 --gpu-count 8
./deploy-device-plugin.sh --expected-gpus 8
./capture-nvml-traces.sh

# Phase 2: DRA Driver (needs DRA-enabled cluster)
./setup-kind-cluster.sh --profile a100 --gpu-count 8 --dra
./deploy-dra-driver.sh --expected-gpus 8
./capture-nvml-traces.sh
```

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `GPU_PROFILE` | `a100` | GPU profile (a100, h100, b200, gb200, l40s, t4) |
| `GPU_COUNT` | `8` | Number of mock GPUs |
| `CLUSTER_NAME` | `gpu-mock-poc` | Kind cluster name |
| `MOCK_NVML_DEBUG` | `1` | Enable NVML debug traces |
| `GOLANG_VERSION` | `1.25` | Go version for building gpu-mock image |

## Output

Logs and traces are saved to `./logs/`:

- `device-plugin-nvml-calls.log` - Filtered NVML calls from device plugin
- `dra-driver-nvml-calls.log` - Filtered NVML calls from DRA driver
- `nvml-trace-summary.md` - Summary of all NVML functions called
- `resourceslices.yaml` - DRA ResourceSlice output
- `node-status.json` - Node status with allocatable GPUs
