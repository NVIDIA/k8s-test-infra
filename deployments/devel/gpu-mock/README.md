# GPU Mock on kind

Complete mock NVIDIA driver and toolkit infrastructure for testing
GPU-aware Kubernetes applications on local kind clusters without
physical GPUs.

## Overview

This deployment creates a local kind cluster with a comprehensive mock
NVIDIA GPU environment that mimics both the driver filesystem and the
NVIDIA Container Toolkit (CDI) configuration typically found on
GPU-enabled nodes. It enables end-to-end testing of GPU-aware
Kubernetes workloads without requiring actual GPU hardware.

The mock includes:

**Step 1: Mock Driver Filesystem**
- Character device nodes (`/dev/nvidia*`, `/dev/nvidiactl`,
  `/dev/nvidia-uvm*`)
- NVIDIA proc filesystem structure
  (`/proc/driver/nvidia/gpus/{PCI}/information`)
- GPU topology and device information (8× A100 GPUs via `go-nvml`
  dgxa100 mock)

**Step 2: Mock Driver + Toolkit + CDI**
- Mock NVIDIA driver libraries (versioned, empty files following
  nvidia-container-toolkit test conventions)
- Mock NVIDIA toolkit binaries (`nvidia-smi`, `nvidia-debugdump`, etc.)
- Production-grade CDI specification generated via
  `nvidia-container-toolkit/pkg/nvcdi`
- DaemonSet deployment for node-level mock setup
- Full integration with NVIDIA's production CDI generation APIs

## Quick Start

### Prerequisites

- [kind](https://kind.sigs.k8s.io/) v0.20.0+
- [kubectl](https://kubernetes.io/docs/tasks/tools/) v1.28.0+
- [Docker](https://www.docker.com/) 20.10+
- [Go](https://golang.org/) 1.25+

### Run End-to-End (Step 1 Only)

From the repository root:

```bash
make -C deployments/devel/gpu-mock all
```

This command will:

1. Create a kind cluster named `gpu-mock`
2. Build the `local/gpu-mockctl:dev` container image
3. Load the image into the kind cluster
4. Deploy the namespace, ConfigMap, and mock filesystem Job
5. Wait for the Job to complete (max 180s)
6. Display the Job logs

**Expected output (last line):**

```
ALL CHECKS PASSED
```

### Run End-to-End (Steps 1 + 2 with CDI)

For the complete mock environment including CDI:

```bash
make -C deployments/devel/gpu-mock all-cdi
```

This will execute all Step 1 targets plus:
- Deploy the CDI mock DaemonSet
- Wait for DaemonSet rollout
- Run the CDI smoke test Job
- Display comprehensive results

**Expected output:**
```
ALL CHECKS PASSED
✓ CDI spec exists
✓ Mock driver libraries present
✓ Mock binaries present
✓ CDI smoke test PASSED
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

## Step 2: Mock NVIDIA Userspace + CDI

**Step 2** extends the base mock filesystem (Step 1) to include:

1. **Mock driver/toolkit tree**: Placeholder NVIDIA libraries and
   binaries under `/var/lib/nvidia-mock/driver`
2. **CDI specification**: Valid CDI spec at `/etc/cdi/nvidia.yaml` for
   CDI-enabled container runtimes
3. **DaemonSet deployment**: Runs `gpu-mockctl` on every node to create
   mock files and CDI spec
4. **Smoke test**: Validates the presence of mock driver files and CDI
   spec

### Quick Start (Step 2)

Run the full end-to-end test including CDI:

```bash
make -C deployments/devel/gpu-mock all-cdi
```

This will:
1. Execute all Step 1 targets (cluster, image, fs mock, verify)
2. Deploy the CDI mock DaemonSet
3. Wait for DaemonSet rollout
4. Run the CDI smoke test Job
5. Display results

**Expected output:**
```
✓ CDI spec exists
✓ Mock driver libraries present
✓ Mock binaries present
✓ CDI smoke test PASSED
```

### Individual Step 2 Targets

```bash
make -C deployments/devel/gpu-mock apply-cdi   # Deploy DaemonSet
make -C deployments/devel/gpu-mock wait-cdi    # Wait for rollout
make -C deployments/devel/gpu-mock logs-cdi    # View DaemonSet logs
make -C deployments/devel/gpu-mock smoke       # Run smoke test
```

### Step 2 CLI Flags

The `gpu-mockctl` tool now supports CDI mode:

```bash
gpu-mockctl [flags]

Flags:
  --mode string           Operation mode: fs, cdi, or all (default: "all")
  --base string           Mock driver root for fs mode (default: "/run/nvidia/driver")
  --driver-root string    Host mock driver tree root (default: "/var/lib/nvidia-mock/driver")
  --cdi-output string     CDI spec output path (default: "/etc/cdi/nvidia.yaml")
  --machine string        Machine type (default: "dgxa100")
  --with-dri              Include DRI render node (/dev/dri/renderD128)
  --with-hook             Include CDI hook references (requires toolkit)
  --toolkit-root string   Toolkit root for hook paths (default: "/usr/local/nvidia-container-toolkit")
```

**Mode behavior:**
- **`fs`**: Create Step 1 mock filesystem only (proc/dev under `--base`)
- **`cdi`**: Create mock driver tree + CDI spec (host paths)
- **`all`**: Run both fs and cdi modes (default)

### Mock Driver Tree Structure

Located at `/var/lib/nvidia-mock/driver` on each node:

```
/var/lib/nvidia-mock/driver/
├── lib64/
│   ├── libcuda.so.550.54.15             # Empty file (0 bytes)
│   ├── libcuda.so.1 -> libcuda.so.550.54.15
│   ├── libcuda.so -> libcuda.so.1
│   ├── libnvidia-ml.so.550.54.15        # Empty file (0 bytes)
│   ├── libnvidia-ml.so.1 -> libnvidia-ml.so.550.54.15
│   ├── libnvidia-ml.so -> libnvidia-ml.so.1
│   ├── libnvidia-encode.so.550.54.15    # Empty file (0 bytes)
│   ├── libnvidia-encode.so.1 -> libnvidia-encode.so.550.54.15
│   ├── libnvidia-encode.so -> libnvidia-encode.so.1
│   ├── libnvcuvid.so.550.54.15          # Empty file (0 bytes)
│   ├── libnvcuvid.so.1 -> libnvcuvid.so.550.54.15
│   ├── libnvcuvid.so -> libnvcuvid.so.1
│   ├── libnvidia-ptxjitcompiler.so.550.54.15     # Empty file (0 bytes)
│   ├── libnvidia-ptxjitcompiler.so.1 -> libnvidia-ptxjitcompiler.so.550.54.15
│   ├── libnvidia-ptxjitcompiler.so -> libnvidia-ptxjitcompiler.so.1
│   ├── libnvidia-fatbinaryloader.so.550.54.15    # Empty file (0 bytes)
│   ├── libnvidia-fatbinaryloader.so.1 -> libnvidia-fatbinaryloader.so.550.54.15
│   └── libnvidia-fatbinaryloader.so -> libnvidia-fatbinaryloader.so.1
├── bin/
│   ├── nvidia-smi                  # Shell stub script
│   ├── nvidia-debugdump            # Shell stub script
│   ├── nvidia-persistenced         # Shell stub script
│   └── nvidia-modprobe             # Shell stub script
├── dev/                             # Mock device nodes (regular files)
│   ├── nvidia0..nvidia7            # 8 GPU devices
│   ├── nvidiactl
│   ├── nvidia-uvm
│   └── nvidia-uvm-tools
└── etc/
    └── nvidia-container-runtime/
        └── config.toml              # Placeholder config
```

**Note**: Libraries are **empty files** (following the nvidia-container-toolkit's
own testdata convention for testing). Binaries are shell script stubs. They
do **not** contain real NVML or CUDA functionality. Step 3 will introduce
NVML mocking.

**CDI Generation**: The CDI spec is generated using **nvidia-container-toolkit's
production `nvcdi` library** with:
- `go-nvml` dgxa100 mock for GPU topology
- `__NVCT_TESTING_DEVICES_ARE_FILES=true` environment variable (from
  nvidia-container-toolkit's own testing infrastructure)
- Empty files with proper version naming (`libcuda.so.550.54.15`)

This ensures our mock CDI specs match real-world NVIDIA deployments.

### CDI Specification

Generated at `/etc/cdi/nvidia.yaml`:

```yaml
cdiVersion: 1.0.0
kind: nvidia.com/gpu
containerEdits:
  env:
  - NVIDIA_VISIBLE_DEVICES=void
  hooks:
  - args:
    - nvidia-cdi-hook
    - create-symlinks
    - --link
    - libcuda.so.1::/lib64/libcuda.so
    hookName: createContainer
    path: /usr/local/nvidia-container-toolkit/bin/nvidia-cdi-hook
  - args:
    - nvidia-cdi-hook
    - enable-cuda-compat
    - --host-driver-version=550.54.15
    hookName: createContainer
    path: /usr/local/nvidia-container-toolkit/bin/nvidia-cdi-hook
  - args:
    - nvidia-cdi-hook
    - update-ldcache
    - --folder
    - /lib64
    hookName: createContainer
    path: /usr/local/nvidia-container-toolkit/bin/nvidia-cdi-hook
  mounts:
  - containerPath: /bin/nvidia-smi
    hostPath: /host/var/lib/nvidia-mock/driver/bin/nvidia-smi
    options: [ro, nosuid, nodev, bind]
  - containerPath: /lib64/libcuda.so.550.54.15
    hostPath: /host/var/lib/nvidia-mock/driver/lib64/libcuda.so.550.54.15
    options: [ro, nosuid, nodev, bind]
  - containerPath: /lib64/libnvidia-ml.so.550.54.15
    hostPath: /host/var/lib/nvidia-mock/driver/lib64/libnvidia-ml.so.550.54.15
    options: [ro, nosuid, nodev, bind]
  # ... (other library mounts)
devices:
- name: "0"
  containerEdits: {}
- name: "1"
  containerEdits: {}
# ... (devices 2-7)
```

**Note**: The actual CDI spec is generated by
`nvidia-container-toolkit/pkg/nvcdi` and includes proper device mounts,
library paths, and hook configurations for production compatibility.

### Enabling CDI in containerd (Optional)

To test CDI device injection with a real CDI-aware runtime, enable CDI
in containerd inside the kind node:

1. Access the kind node:
   ```bash
   docker exec -it gpu-mock-control-plane /bin/bash
   ```

2. Edit `/etc/containerd/config.toml`:
   ```toml
   [plugins."io.containerd.grpc.v1.cri"]
     enable_cdi = true
     cdi_spec_dirs = ["/etc/cdi"]
   ```

3. Restart containerd:
   ```bash
   systemctl restart containerd
   ```

4. Test with a pod using CDI device annotation:
   ```yaml
   annotations:
     cdi.k8s.io/nvidia: nvidia.com/gpu=all
   ```

**Note**: This is **not** required for Step 2 acceptance; the smoke test
validates file presence and CDI spec syntax only.

## Technical Details: nvidia-container-toolkit Integration

### How We Use Production nvcdi APIs

Step 2 uses the **same CDI generation library** that NVIDIA uses in
production (`github.com/NVIDIA/nvidia-container-toolkit/pkg/nvcdi`):

```go
// Create mock NVML interface (dgxa100 with MIG stub overrides)
mockNVML := newNVMLWrapper()

// Pass it to nvidia-container-toolkit's nvcdi
lib, err := nvcdi.New(
    nvcdi.WithMode(nvcdi.ModeNvml),
    nvcdi.WithNvmlLib(mockNVML),          // Our mock!
    nvcdi.WithDriverRoot(driverRoot),
    nvcdi.WithDevRoot(devRoot),
)

spec, err := lib.GetSpec()
```

### Testing Environment Variable

We use `__NVCT_TESTING_DEVICES_ARE_FILES=true` - the **same flag
nvidia-container-toolkit uses in its own test suite** (see
[testdata/](https://github.com/NVIDIA/nvidia-container-toolkit/tree/main/testdata)).

This flag tells nvcdi to:
- Accept regular files as device nodes (instead of character devices)
- Skip ELF binary validation
- Work with empty placeholder files

### File Structure Compatibility

Our mock follows nvidia-container-toolkit's testdata conventions:
- **Empty files** (0 bytes) for `.so` libraries
- **Versioned naming**: `libcuda.so.550.54.15` (matching dgxa100 mock version)
- **Proper symlink chains**: `libcuda.so` → `libcuda.so.1` → `libcuda.so.550.54.15`
- **Device nodes as files** when `__NVCT_TESTING_DEVICES_ARE_FILES=true`

### NVML Mock Wrapper

We extend `go-nvml/pkg/nvml/mock/dgxa100` with MIG method stubs (see
`pkg/gpu/mocktopo/nvmlwrapper.go`):

```go
// nvcdi requires these methods for device discovery
dev.GetMaxMigDeviceCountFunc = func() (int, nvml.Return) {
    return 0, nvml.SUCCESS  // No MIG support in mock
}
dev.GetMigModeFunc = func() (int, int, nvml.Return) {
    return 0, 0, nvml.ERROR_NOT_SUPPORTED
}
```

This approach ensures **Step 2 CDI specs are production-ready** and will work
seamlessly with real NVIDIA Container Toolkit deployments.

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

### CLI Flags (Complete Reference)

```bash
gpu-mockctl [flags]

Flags:
  --mode string           Operation mode: fs, cdi, or all (default: "all")
  --base string           Mock driver root for fs mode (default: "/run/nvidia/driver")
  --driver-root string    Host mock driver tree root for cdi mode (default: "/var/lib/nvidia-mock/driver")
  --cdi-output string     CDI spec output path (default: "/etc/cdi/nvidia.yaml")
  --machine string        Machine type (default: "dgxa100")
  --with-dri              Include DRI render node (/dev/dri/renderD128) (default: false)
  --with-hook             Include CDI hook references (default: false)
  --toolkit-root string   Toolkit root for hook paths (default: "/usr/local/nvidia-container-toolkit")
  --help, -h              Show help
```

**Mode behavior:**
- **`fs`**: Create Step 1 mock filesystem only (proc/dev under `--base`)
- **`cdi`**: Create mock driver tree + CDI spec (host paths)
- **`all`**: Run both fs and cdi modes (default)

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

**Step 1 (Mock Filesystem):**
- **`00-namespace.yaml`**: Creates the `gpu-mock` namespace
- **`10-configmap-verify-script.yaml`**: Shell script that validates the
  mock filesystem
- **`20-job-gpu-mock-verify.yaml`**: Kubernetes Job with initContainer
  (setup) and container (verify)

**Step 2 (CDI + Toolkit):**
- **`30-daemonset-cdi-mock.yaml`**: DaemonSet that runs `gpu-mockctl` in
  CDI mode on every node
- **`40-job-cdi-smoke.yaml`**: Smoke test Job for CDI validation

**Common:**
- **`Dockerfile`**: Multi-stage build for `gpu-mockctl` (CGO-enabled
  binary)
- **`Makefile`**: Convenience targets for build/deploy/test workflow
- **`README.md`**: This file

## Summary

This mock infrastructure provides:

✅ **Step 1**: Mock NVIDIA driver filesystem (device nodes + procfs)
✅ **Step 2**: Mock NVIDIA toolkit + production-grade CDI specs

**Key Features:**
- Uses `go-nvml` dgxa100 mock for authentic A100 topology (8 GPUs)
- Integrates `nvidia-container-toolkit/pkg/nvcdi` for CDI generation
- Follows NVIDIA's own testing conventions (empty files, versioned libs)
- Supports both isolated testing (Step 1) and full CDI integration (Step 2)
- Extensible design for future H100/H200/B200 flavors

**What's Next (Step 3):**
- NVIDIA Device Plugin integration
- NVIDIA DRA (Dynamic Resource Allocation) Driver integration
- Live GPU device allocation to pods

## License

Copyright (c) 2025, NVIDIA CORPORATION. All rights reserved.

Licensed under the Apache License, Version 2.0. See the repository root
`LICENSE` file for details.

