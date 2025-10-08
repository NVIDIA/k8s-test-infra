# GPU Mock on kind

Mock NVIDIA driver filesystem infrastructure for testing GPU-aware
Kubernetes applications on local kind clusters without physical GPUs.

## Overview

This deployment creates a local kind cluster with a mock NVIDIA driver
filesystem that mimics the structure and device nodes typically created
by the NVIDIA driver on GPU-enabled nodes. It allows testing of
GPU-aware Kubernetes workloads without requiring actual GPU hardware.

The mock includes:

- Character device nodes (`/dev/nvidia*`, `/dev/nvidiactl`,
  `/dev/nvidia-uvm*`)
- NVIDIA proc filesystem structure
  (`/proc/driver/nvidia/gpus/{PCI}/information`)
- GPU topology and device information

## Quick Start

### Prerequisites

- [kind](https://kind.sigs.k8s.io/) v0.20.0+
- [kubectl](https://kubernetes.io/docs/tasks/tools/) v1.28.0+
- [Docker](https://www.docker.com/) 20.10+
- [Go](https://golang.org/) 1.22+

### Run End-to-End

From the repository root:

```bash
make -C deployments/devel/gpu-mock all
```

This command will:

1. Create a kind cluster named `gpu-mock`
2. Build the `local/gpu-mockctl:dev` container image
3. Load the image into the kind cluster
4. Deploy the namespace, ConfigMap, and Job
5. Wait for the Job to complete (max 180s)
6. Display the Job logs

**Expected output (last line):**

```
ALL CHECKS PASSED
```

### Step-by-Step Execution

If you prefer to run each step manually:

```bash
cd deployments/devel/gpu-mock

make kind-up    # Create the kind cluster
make image      # Build the container image
make load       # Load the image into kind
make apply      # Deploy Kubernetes resources
make wait       # Wait for Job completion
make logs       # View Job logs
```

### Cleanup

Delete the kind cluster and all resources:

```bash
make -C deployments/devel/gpu-mock clean
```

Or from the `deployments/devel/gpu-mock/` directory:

```bash
make clean
```

## Architecture

### Components

- **`gpu-mockctl`**: CLI tool that generates the mock NVIDIA driver
  filesystem structure using topology data from
  [go-nvml](https://github.com/NVIDIA/go-nvml) mock providers.

- **Kubernetes Job**: Two-container Job that creates and verifies the
  mock filesystem:
  - **initContainer (`setup-mock`)**: Runs `gpu-mockctl` with
    privileged permissions to create character device nodes via
    `mknod(2)`.
  - **container (`verify`)**: Executes a shell script to validate the
    filesystem structure and device nodes.

- **Shared Volume**: An `emptyDir` volume mounted at
  `/run/nvidia/driver` allows the initContainer to write and the verify
  container to read the mock filesystem.

### Mock Filesystem Structure

```
/run/nvidia/driver/
├── dev/
│   ├── nvidia0
│   ├── nvidia1
│   ├── nvidia2
│   ├── nvidia3
│   ├── nvidia4
│   ├── nvidia5
│   ├── nvidia6
│   ├── nvidia7
│   ├── nvidiactl
│   ├── nvidia-uvm
│   └── nvidia-uvm-tools
└── proc/
    └── driver/
        └── nvidia/
            ├── version
            └── gpus/
                ├── 0000:00:00.0/
                │   └── information
                ├── 0000:01:00.0/
                │   └── information
                ├── 0000:02:00.0/
                │   └── information
                ├── 0000:03:00.0/
                │   └── information
                ├── 0000:04:00.0/
                │   └── information
                ├── 0000:05:00.0/
                │   └── information
                ├── 0000:06:00.0/
                │   └── information
                └── 0000:07:00.0/
                    └── information
```

### Machine Types

Currently supported machine type:

- **`dgxa100`**: DGX A100 with 8× NVIDIA A100-SXM4-40GB GPUs

Future machine types (when added to go-nvml):

- `dgxh100`: DGX H100 with 8× H100-SXM5-80GB
- `dgxh200`: DGX H200 with 8× H200-HBM3e
- `dgxb200`: DGX B200 with 8× B200

## Configuration

### Environment Variables

The `gpu-mockctl` tool supports the following environment variables:

- **`MACHINE_TYPE`**: Machine type for topology (default: `dgxa100`)
- **`ALLOW_UNSUPPORTED`**: If `true`, use fallback synthetic topology
  when the specified machine type is unsupported (default: unset)

### CLI Flags

```bash
gpu-mockctl [flags]

Flags:
  --base string      Mock driver root directory (default: "/run/nvidia/driver")
  --machine string   Machine type (only dgxa100 supported) (default: "dgxa100")
  --help, -h         Show help
```

## Development

### Building Locally

Build the `gpu-mockctl` binary:

```bash
CGO_ENABLED=1 go build -o /tmp/gpu-mockctl ./cmd/gpu-mockctl
/tmp/gpu-mockctl --help
```

### Building the Container Image

From the repository root:

```bash
docker build -t local/gpu-mockctl:dev \
  -f deployments/devel/gpu-mock/Dockerfile .
```

### Running Manually

Run `gpu-mockctl` locally (requires `CAP_MKNOD` or root):

```bash
sudo /tmp/gpu-mockctl --base /tmp/nvidia-mock --machine dgxa100
ls -lR /tmp/nvidia-mock
```

### Extending with New Machine Types

When `go-nvml` adds new mock machine types, extend the registry:

1. Edit `pkg/gpu/mocktopo/provider.go`
2. Add a new `Register()` call in `init()`:

```go
Register("dgxh100", func() (*Topology, error) {
    srv := dgxh100.New()
    var gpus []GPUInfo
    for _, d := range srv.Devices {
        dev := d.(*dgxh100.Device)
        gpus = append(gpus, GPUInfo{
            PCI:   dev.PciBusID,
            UUID:  dev.UUID,
            Model: dev.Name,
        })
    }
    if len(gpus) == 0 {
        return nil, errors.New("dgxh100 mock returned zero GPUs")
    }
    return &Topology{GPUs: gpus}, nil
})
```

3. Update the Job manifest to use the new machine type:

```yaml
env:
  - name: MACHINE_TYPE
    value: "dgxh100"
```

No changes are needed to `gpu-mockctl`, `mockfs`, or the verify script.

## Troubleshooting

### Job fails with "MISSING: <path>"

**Cause**: The initContainer failed to create the filesystem structure.

**Solution**:

1. Check initContainer logs:
   ```bash
   kubectl -n gpu-mock logs <pod-name> -c setup-mock
   ```
2. Verify `privileged: true` is set in the Job manifest for the
   initContainer.

### Build fails with "undefined: Return"

**Cause**: `go-nvml` requires CGO, but `CGO_ENABLED=0` was set.

**Solution**: Ensure `CGO_ENABLED=1` in the Dockerfile builder stage
(already configured).

### kind load hangs or fails

**Cause**: Docker daemon is not running or kind cluster doesn't exist.

**Solution**:

1. Verify Docker is running: `docker ps`
2. Check kind clusters: `kind get clusters`
3. Recreate the cluster: `make clean && make kind-up`

### Job times out waiting for completion

**Cause**: The verify container may be failing repeatedly.

**Solution**:

1. Check Job status:
   ```bash
   kubectl -n gpu-mock get jobs
   kubectl -n gpu-mock describe job gpu-mock-verify
   ```
2. Check pod status:
   ```bash
   kubectl -n gpu-mock get pods
   kubectl -n gpu-mock logs <pod-name> -c verify
   ```

## Files

- **`00-namespace.yaml`**: Creates the `gpu-mock` namespace
- **`10-configmap-verify-script.yaml`**: Shell script that validates the
  mock filesystem
- **`20-job-gpu-mock-verify.yaml`**: Kubernetes Job with initContainer
  (setup) and container (verify)
- **`Dockerfile`**: Multi-stage build for `gpu-mockctl` (CGO-enabled
  static binary)
- **`Makefile`**: Convenience targets for build/deploy/test workflow

## License

Copyright (c) 2024, NVIDIA CORPORATION. All rights reserved.

Licensed under the Apache License, Version 2.0. See the repository root
`LICENSE` file for details.

