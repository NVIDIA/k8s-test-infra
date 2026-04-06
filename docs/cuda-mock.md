# Mock CUDA Library

The mock CUDA library provides a minimal implementation of CUDA Driver and Runtime
APIs for container validation workloads. It is built alongside the mock NVML library
and deployed via the same DaemonSet.

**Status:** Early stage -- 15 functions implemented. Sufficient for basic validation
workloads (e.g., `cuda-sample:vectoradd`) but not for complex CUDA applications.

## Implemented Functions

| Category          | Functions                                                                  | Notes                                                  |
|-------------------|----------------------------------------------------------------------------|--------------------------------------------------------|
| Initialization    | `cuInit`, `cudaDriverGetVersion`, `cudaRuntimeGetVersion`                  | Returns configured driver/runtime versions             |
| Device Management | `cudaGetDeviceCount`, `cudaSetDevice`, `cudaGetDevice`, `cudaDeviceReset`  | Tracks active device index                             |
| Memory            | `cudaMalloc`, `cudaFree`, `cudaMemcpy`                                     | Real host allocation via malloc; device copies are no-ops |
| Execution         | `cudaLaunchKernel`, `cudaDeviceSynchronize`                                | No-ops (no actual computation)                         |
| Error Handling    | `cudaGetErrorString`, `cudaGetLastError`, `cudaPeekAtLastError`            | Thread-local error tracking                            |

## How It Works

The library is built as `libcuda.so.<version>` alongside mock NVML. The DaemonSet
setup script creates the following symlink chain:

```
libcuda.so.550.163.01    (versioned library)
libcuda.so.1        ->   libcuda.so.550.163.01
libcuda.so          ->   libcuda.so.1
libcudart.so.12     ->   libcuda.so.1          (runtime API compat)
libcudart.so        ->   libcudart.so.12
```

The runtime API symlinks (`libcudart.so*`) point to the same mock library. This is a
compatibility workaround -- applications that link against `libcudart.so` (such as
`cuda-sample:vectoradd`) resolve to the mock without needing a separate runtime library.

## Architecture

```
┌──────────────────────────────────┐
│   CGo Bridge (bridge/cuda.go)    │
│   15 C function exports          │
│   C <-> Go type conversion       │
└──────────────┬───────────────────┘
               │
               v
┌──────────────────────────────────┐
│   Engine (engine/cuda.go)        │
│   Singleton lifecycle            │
│   Device index tracking          │
│   Memory allocation tracking     │
└──────────────────────────────────┘
```

The CGo bridge layer exports C-ABI functions that the dynamic linker resolves when
applications call CUDA APIs. Each exported function delegates to the Go engine, which
manages state (active device index, allocated memory pointers, error codes).

## Limitations

- **No actual computation** -- kernel launches are no-ops.
- **No multi-GPU** -- device selection is tracked but has no effect.
- **No streams/events** -- async operations are not implemented.
- **Memory copies** -- only host-to-host actually copies data; other directions are no-ops.
- **No CUDA context management** -- `cuCtxCreate`, `cuCtxDestroy`, etc. are not implemented.

## Building

```bash
cd pkg/gpu/mockcuda
make
```

## Source Code

| File                                    | Description                                |
|-----------------------------------------|--------------------------------------------|
| `pkg/gpu/mockcuda/bridge/cuda.go`       | CGo bridge -- 15 exported C functions      |
| `pkg/gpu/mockcuda/bridge/cuda_types.h`  | C type definitions                         |
| `pkg/gpu/mockcuda/engine/cuda.go`       | Go engine -- device and memory management  |
| `pkg/gpu/mockcuda/engine/types.go`      | Error codes and type definitions           |
| `pkg/gpu/mockcuda/Makefile`             | Build configuration                        |
