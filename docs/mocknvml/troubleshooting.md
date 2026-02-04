# Troubleshooting Guide

Common issues and solutions for Mock NVML.

## Build Issues

### CGo Not Enabled

**Error**:
```
cgo: C compiler "gcc" not found: exec: "gcc": executable file not found in $PATH
```

**Solution**:
```bash
# Ubuntu/Debian
sudo apt-get install build-essential

# RHEL/CentOS
sudo yum groupinstall "Development Tools"

# macOS
xcode-select --install
```

### Go Version Too Old

**Error**:
```
go: go.mod file indicates go 1.23
```

**Solution**:
```bash
# Install Go 1.23+
wget https://go.dev/dl/go1.23.0.linux-amd64.tar.gz
sudo tar -C /usr/local -xzf go1.23.0.linux-amd64.tar.gz
export PATH=$PATH:/usr/local/go/bin
```

### Build on macOS

**Problem**: Cannot build shared library on macOS for Linux.

**Solution**: Use Docker build:
```bash
make docker-build
```

## Runtime Issues

### Library Not Found

**Error**:
```
nvidia-smi: error while loading shared libraries: libnvidia-ml.so.1: cannot open shared object file
```

**Solution**:
```bash
# Verify library exists
ls -la pkg/gpu/mocknvml/libnvidia-ml.so*

# Set library path correctly
export LD_LIBRARY_PATH=$(pwd)/pkg/gpu/mocknvml:$LD_LIBRARY_PATH

# Or use absolute path
LD_LIBRARY_PATH=/full/path/to/pkg/gpu/mocknvml nvidia-smi
```

### Symbol Not Found

**Error**:
```
nvidia-smi: symbol lookup error: nvidia-smi: undefined symbol: nvmlSomeFunction
```

**Solution**:

1. Check if symbol is exported:
```bash
nm -D pkg/gpu/mocknvml/libnvidia-ml.so | grep nvmlSomeFunction
```

2. If missing, check if it's a stub or needs implementation:
```bash
# Check if it's in stubs
grep "nvmlSomeFunction" pkg/gpu/mocknvml/bridge/stubs_generated.go

# If stub, implement it in appropriate bridge file, then:
go generate ./pkg/gpu/mocknvml/bridge/
make -C pkg/gpu/mocknvml clean
make -C pkg/gpu/mocknvml
```

### Segmentation Fault

**Error**:
```
Segmentation fault (core dumped)
```

**Causes and Solutions**:

1. **Handle dereferencing issue**:
   - Enable debug mode: `MOCK_NVML_DEBUG=1`
   - Check which function crashes
   - Verify handle table is working

2. **CGo memory issue**:
   - Run with address sanitizer:
   ```bash
   CGO_CFLAGS="-fsanitize=address" go build -buildmode=c-shared ...
   ```

3. **nvidia-smi version mismatch**:
   - Ensure driver version in config matches nvidia-smi expectation
   - Try different nvidia-smi versions

### YAML Config Not Loading

**Error**:
```
[CONFIG] Failed to load YAML config from /path/to/config.yaml: ...
```

**Solution**:

1. Verify file exists and is readable:
```bash
cat $MOCK_NVML_CONFIG
```

2. Validate YAML syntax:
```bash
python3 -c "import yaml; yaml.safe_load(open('$MOCK_NVML_CONFIG'))"
```

3. Check required fields:
```yaml
version: "1.0"                    # Required
system:
  driver_version: "550.163.01"    # Required
```

4. Enable debug to see specific error:
```bash
MOCK_NVML_DEBUG=1 LD_LIBRARY_PATH=. nvidia-smi
```

### nvidia-smi Shows Wrong Values

**Problem**: Values don't match YAML config.

**Solution**:

1. Verify config is loaded:
```bash
MOCK_NVML_DEBUG=1 LD_LIBRARY_PATH=. nvidia-smi 2>&1 | head -5
# Should show: [CONFIG] Loaded YAML config: N devices, driver X.Y.Z
```

2. Check device override vs defaults:
   - Per-device settings override defaults
   - Check `devices:` section in YAML

3. Verify field mapping:
   - Some nvidia-smi fields map to different config fields
   - Check [Configuration Reference](configuration.md)

### NOT_SUPPORTED Errors

**Problem**: nvidia-smi shows "N/A" or errors for some queries.

**Explanation**: Functions return `NOT_SUPPORTED` when:
- YAML config doesn't provide the value
- Function is not implemented (stub)

**Solution**:

1. Add missing config values:
```yaml
device_defaults:
  thermal:
    temperature_gpu_c: 33    # Add this for temperature
```

2. Check if function is implemented:
```bash
# Check engine implementation
grep "GetXxx" pkg/gpu/mocknvml/engine/device.go

# Check bridge implementation
grep "nvmlDeviceGetXxx" pkg/gpu/mocknvml/bridge/*.go

# Check if it's just a stub
grep "nvmlDeviceGetXxx" pkg/gpu/mocknvml/bridge/stubs_generated.go
```

## Testing Issues

### Tests Fail with "undefined"

**Error**:
```
./device_test.go:XX: undefined: EnhancedDevice
```

**Solution**: Update test file to use `ConfigurableDevice`:
```go
// Old
dev := &EnhancedDevice{...}

// New
dev := &ConfigurableDevice{...}
```

### Integration Test Fails

**Error**:
```
Error: No GPU found
```

**Solution**:

1. Ensure mock library is built:
```bash
make -C pkg/gpu/mocknvml
```

2. Check library is accessible in Docker:
```bash
docker run -v $(pwd)/pkg/gpu/mocknvml:/mock -e LD_LIBRARY_PATH=/mock ...
```

### Race Conditions

**Error**:
```
WARNING: DATA RACE
```

**Solution**:
- All Engine methods should use mutex
- Check that new code acquires locks properly
- Run tests with race detector: `go test -race ./...`

## Performance Issues

### Slow Startup

**Problem**: nvidia-smi takes long to start.

**Causes**:
1. Large YAML config parsing
2. Debug logging enabled

**Solution**:
```bash
# Disable debug
unset MOCK_NVML_DEBUG

# Use simpler config for testing
```

### Memory Usage

**Problem**: High memory usage.

**Explanation**: 
- Each handle allocates ~40 bytes of C memory
- Error string cache grows with unique errors

This is typically not a problem for normal usage.

## Environment Issues

### LD_LIBRARY_PATH Not Working

**Problem**: System uses real NVML despite setting LD_LIBRARY_PATH.

**Solutions**:

1. Check library search order:
```bash
LD_DEBUG=libs nvidia-smi 2>&1 | grep nvml
```

2. Verify no cached libraries:
```bash
sudo ldconfig  # Refresh cache
```

3. Use absolute path:
```bash
LD_LIBRARY_PATH=/absolute/path/to/mocknvml nvidia-smi
```

4. Check for ld.so.conf entries:
```bash
grep nvml /etc/ld.so.conf.d/*
```

### Docker Issues

**Problem**: Library doesn't work in Docker container.

**Solutions**:

1. Mount library correctly:
```bash
docker run -v $(pwd)/pkg/gpu/mocknvml:/mock:ro \
           -e LD_LIBRARY_PATH=/mock \
           ubuntu nvidia-smi
```

2. Ensure nvidia-smi is available:
```bash
docker run ... which nvidia-smi
```

3. Use correct architecture:
```bash
# Build for container architecture
GOARCH=amd64 make -C pkg/gpu/mocknvml
```

## Getting Help

### Debug Information to Collect

When reporting issues, include:

1. **Environment**:
```bash
uname -a
go version
gcc --version
```

2. **Debug output**:
```bash
MOCK_NVML_DEBUG=1 LD_LIBRARY_PATH=. nvidia-smi 2>&1
```

3. **Library info**:
```bash
file pkg/gpu/mocknvml/libnvidia-ml.so
ldd pkg/gpu/mocknvml/libnvidia-ml.so
```

4. **Config file** (if using YAML)

5. **Expected vs actual output**

### Filing Issues

Include:
- Steps to reproduce
- Expected behavior
- Actual behavior
- Debug output
- Environment info
