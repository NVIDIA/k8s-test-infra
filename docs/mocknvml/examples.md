# Examples

Common usage patterns and scenarios for Mock NVML.

## Basic Usage

### Default Configuration (8x A100)

```bash
cd pkg/gpu/mocknvml
make
LD_LIBRARY_PATH=. nvidia-smi
```

### With A100 YAML Profile

```bash
LD_LIBRARY_PATH=. MOCK_NVML_CONFIG=configs/mock-nvml-config-a100.yaml nvidia-smi
```

### With GB200 YAML Profile

```bash
LD_LIBRARY_PATH=. MOCK_NVML_CONFIG=configs/mock-nvml-config-gb200.yaml nvidia-smi
```

## nvidia-smi Commands

### List GPUs

```bash
LD_LIBRARY_PATH=. nvidia-smi -L
```

Output:
```
GPU 0: NVIDIA A100-SXM4-40GB (UUID: GPU-12345678-1234-1234-1234-123456780000)
GPU 1: NVIDIA A100-SXM4-40GB (UUID: GPU-12345678-1234-1234-1234-123456780001)
...
```

### Full Query

```bash
LD_LIBRARY_PATH=. MOCK_NVML_CONFIG=configs/mock-nvml-config-a100.yaml nvidia-smi -q
```

### Query Specific Details

```bash
# Memory only
LD_LIBRARY_PATH=. nvidia-smi -q -d MEMORY

# Temperature only
LD_LIBRARY_PATH=. nvidia-smi -q -d TEMPERATURE

# Power only
LD_LIBRARY_PATH=. nvidia-smi -q -d POWER

# Clocks only
LD_LIBRARY_PATH=. nvidia-smi -q -d CLOCK

# ECC only
LD_LIBRARY_PATH=. nvidia-smi -q -d ECC

# PCIe only
LD_LIBRARY_PATH=. nvidia-smi -q -d PCIE

# Utilization only
LD_LIBRARY_PATH=. nvidia-smi -q -d UTILIZATION
```

### XML Output

```bash
LD_LIBRARY_PATH=. nvidia-smi -x -q > gpu-info.xml
```

### CSV Output

```bash
LD_LIBRARY_PATH=. nvidia-smi --query-gpu=index,name,uuid,memory.total,power.draw,temperature.gpu --format=csv
```

Output:
```
index, name, uuid, memory.total [MiB], power.draw [W], temperature.gpu
0, NVIDIA A100-SXM4-40GB, GPU-12345678-1234-1234-1234-123456780000, 40960 MiB, 72.00 W, 33
1, NVIDIA A100-SXM4-40GB, GPU-12345678-1234-1234-1234-123456780001, 40960 MiB, 72.00 W, 34
...
```

### Query Specific GPU

```bash
LD_LIBRARY_PATH=. nvidia-smi -i 0 -q
```

## Custom GPU Profiles

### Single GPU Configuration

```yaml
# single-gpu.yaml
version: "1.0"

system:
  driver_version: "550.163.01"
  nvml_version: "12.550.163.01"
  cuda_version: "12.4"
  cuda_version_major: 12
  cuda_version_minor: 4

device_defaults:
  name: "NVIDIA RTX 4090"
  architecture: "ada"
  memory:
    total_bytes: 25769803776  # 24 GiB
  power:
    default_limit_mw: 450000
    current_draw_mw: 50000
  thermal:
    temperature_gpu_c: 45

devices:
  - index: 0
    uuid: "GPU-RTX4090-0000-0000-0000-000000000000"
    pci:
      bus_id: "00000000:01:00.0"
```

```bash
LD_LIBRARY_PATH=. MOCK_NVML_CONFIG=single-gpu.yaml nvidia-smi
```

### Mixed GPU Configuration

```yaml
# mixed-gpus.yaml
version: "1.0"

system:
  driver_version: "550.163.01"
  cuda_version_major: 12
  cuda_version_minor: 4

device_defaults:
  name: "NVIDIA A100"
  architecture: "ampere"
  memory:
    total_bytes: 42949672960

devices:
  - index: 0
    uuid: "GPU-A100-0000"
    name: "NVIDIA A100-SXM4-80GB"
    memory:
      total_bytes: 85899345920  # 80 GiB
    pci:
      bus_id: "00000000:07:00.0"
  
  - index: 1
    uuid: "GPU-H100-0001"
    name: "NVIDIA H100"
    architecture: "hopper"
    memory:
      total_bytes: 85899345920
    pci:
      bus_id: "00000000:0F:00.0"
```

### Simulating GPU Under Load

```yaml
# gpu-under-load.yaml
version: "1.0"

system:
  driver_version: "550.163.01"
  cuda_version_major: 12
  cuda_version_minor: 4

device_defaults:
  name: "NVIDIA A100-SXM4-40GB"
  memory:
    total_bytes: 42949672960
    used_bytes: 32212254720    # 30 GiB used
    free_bytes: 10737418240
  power:
    current_draw_mw: 350000    # 350W (near limit)
  thermal:
    temperature_gpu_c: 72      # Hot
  utilization:
    gpu: 95                    # 95% GPU utilization
    memory: 75                 # 75% memory bandwidth
  clocks:
    graphics_current: 1350
    sm_current: 1350
  clocks_throttle_reasons:
    sw_power_cap: true

devices:
  - index: 0
    uuid: "GPU-LOAD-0000"
    pci:
      bus_id: "00000000:07:00.0"
    processes:
      - pid: 12345
        type: "C"
        name: "python"
        used_memory_mib: 30720
```

## Testing Scenarios

### Testing NVIDIA Device Plugin

```bash
# Build mock library
make -C pkg/gpu/mocknvml

# Set up environment
export LD_LIBRARY_PATH=$(pwd)/pkg/gpu/mocknvml
export MOCK_NVML_CONFIG=$(pwd)/pkg/gpu/mocknvml/configs/mock-nvml-config-a100.yaml

# Run device plugin
./k8s-device-plugin --mps-root=""
```

### Testing DCGM

```bash
export LD_LIBRARY_PATH=$(pwd)/pkg/gpu/mocknvml
export MOCK_NVML_CONFIG=$(pwd)/pkg/gpu/mocknvml/configs/mock-nvml-config-a100.yaml

# Run DCGM exporter
dcgm-exporter
```

### CI/CD Pipeline Example

```yaml
# .github/workflows/gpu-tests.yaml
name: GPU Tests

on: [push, pull_request]

jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      
      - name: Set up Go
        uses: actions/setup-go@v5
        with:
          go-version: '1.23'
      
      - name: Build Mock NVML
        run: make -C pkg/gpu/mocknvml docker-build
      
      - name: Run GPU Tests
        env:
          LD_LIBRARY_PATH: ./pkg/gpu/mocknvml
          MOCK_NVML_CONFIG: ./pkg/gpu/mocknvml/configs/mock-nvml-config-a100.yaml
        run: go test ./... -tags=gpu -v
      
      - name: Verify nvidia-smi
        env:
          LD_LIBRARY_PATH: ./pkg/gpu/mocknvml
          MOCK_NVML_CONFIG: ./pkg/gpu/mocknvml/configs/mock-nvml-config-a100.yaml
        run: nvidia-smi -L
```

### Docker Integration

```dockerfile
# Dockerfile
FROM golang:1.23

# Copy mock NVML library
COPY pkg/gpu/mocknvml/libnvidia-ml.so* /usr/local/lib/
COPY pkg/gpu/mocknvml/configs/mock-nvml-config-a100.yaml /etc/mock-nvml-config.yaml

# Set environment
ENV LD_LIBRARY_PATH=/usr/local/lib
ENV MOCK_NVML_CONFIG=/etc/mock-nvml-config.yaml

# Your application
COPY myapp /app/myapp
CMD ["/app/myapp"]
```

```bash
docker build -t myapp-with-mock-gpu .
docker run myapp-with-mock-gpu nvidia-smi
```

### Kubernetes ConfigMap

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: mock-nvml-config
data:
  config.yaml: |
    version: "1.0"
    system:
      driver_version: "550.163.01"
      cuda_version_major: 12
      cuda_version_minor: 4
    device_defaults:
      name: "NVIDIA A100-SXM4-40GB"
      memory:
        total_bytes: 42949672960
    devices:
      - index: 0
        uuid: "GPU-TEST-0000"
        pci:
          bus_id: "00000000:00:00.0"
---
apiVersion: v1
kind: Pod
metadata:
  name: gpu-test
spec:
  containers:
    - name: test
      image: myapp:latest
      env:
        - name: LD_LIBRARY_PATH
          value: /mock-nvml
        - name: MOCK_NVML_CONFIG
          value: /config/config.yaml
      volumeMounts:
        - name: mock-nvml
          mountPath: /mock-nvml
        - name: config
          mountPath: /config
  volumes:
    - name: mock-nvml
      hostPath:
        path: /path/to/mock-nvml
    - name: config
      configMap:
        name: mock-nvml-config
```

## Debugging

### Enable Debug Logging

```bash
MOCK_NVML_DEBUG=1 LD_LIBRARY_PATH=. nvidia-smi
```

Output includes:
```
[CONFIG] Loaded YAML config: 8 devices, driver 550.163.01
[ENGINE] Creating devices from YAML config
[DEVICE 0] Created: name=NVIDIA A100-SXM4-40GB uuid=GPU-12345678-...
[NVML] nvmlInit_v2
[NVML] nvmlDeviceGetCount
[NVML] nvmlDeviceGetHandleByIndex(0)
[NVML] nvmlDeviceGetName -> NVIDIA A100-SXM4-40GB
[NVML] nvmlDeviceGetTemperature(sensor=0) -> 33
...
```

### Verify Library Loading

```bash
# Check library is being loaded
LD_DEBUG=libs LD_LIBRARY_PATH=. nvidia-smi 2>&1 | grep nvml

# List exported symbols
nm -D pkg/gpu/mocknvml/libnvidia-ml.so | grep nvml | head -20
```
