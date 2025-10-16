# GPU Mockctl Documentation

Welcome to the GPU Mockctl documentation! This tool enables you to create mock NVIDIA GPU environments for testing Kubernetes GPU workloads without requiring physical GPU hardware.

## Quick Links

- **[User Guide](USER_GUIDE.md)** - Get started quickly with common use cases
- **[Architecture](ARCHITECTURE.md)** - Understand the system design and components  
- **[API Reference](API_REFERENCE.md)** - Detailed API documentation and examples

## What is GPU Mockctl?

GPU Mockctl is a comprehensive solution for simulating NVIDIA GPUs in Kubernetes environments. It provides:

- ✅ **Mock NVIDIA Driver** - Simulates driver filesystem and device nodes
- ✅ **Mock NVML Library** - Fully functional NVIDIA Management Library implementation
- ✅ **Kubernetes Integration** - Works with official NVIDIA device plugin
- ✅ **DGX A100 Simulation** - Simulates 8x NVIDIA A100 GPUs by default
- ✅ **Production Ready** - Thread-safe, well-tested implementation

## Why Use GPU Mockctl?

### For Developers
- Test GPU applications without expensive hardware
- Develop locally with KIND or Docker Desktop
- Debug GPU scheduling and allocation issues

### For CI/CD
- Add GPU testing to pipelines without GPU runners
- Test multiple GPU configurations quickly
- Validate GPU workloads in PR checks

### For Platform Teams
- Test GPU operator deployments
- Validate scheduling policies
- Simulate GPU failures and recovery

## Documentation Overview

### [User Guide](USER_GUIDE.md)
Start here if you want to:
- Deploy mock GPUs quickly
- Test GPU workloads
- Troubleshoot common issues
- Learn best practices

### [Architecture](ARCHITECTURE.md)
Read this to understand:
- System components and design
- Data flow and interactions
- Extension points
- Performance characteristics

### [API Reference](API_REFERENCE.md)
Consult this for:
- Command line options
- NVML function implementations
- Configuration details
- Integration examples

## Quick Start

```bash
# Clone repository
git clone https://github.com/NVIDIA/k8s-test-infra
cd k8s-test-infra/deployments/devel/gpu-mock

# Deploy everything
make all

# Test GPU allocation
make test-gpu

# Check status
make status
```

## Key Features

### 1. Realistic GPU Simulation
- 8x NVIDIA A100-SXM4-40GB GPUs
- 40GB memory per GPU
- CUDA Compute Capability 8.0
- NVLink topology support

### 2. Complete NVML Implementation
- 50+ NVML functions implemented
- Reference counting for init/shutdown
- Thread-safe operations
- Error handling matching real NVML

### 3. Kubernetes Native
- DaemonSet deployment
- Node labeling
- Device plugin support
- Resource allocation

### 4. Developer Friendly
- Enhanced logging with levels
- JSON and text output formats
- Comprehensive error messages
- Easy debugging

## Support Matrix

### Kubernetes Versions
- ✅ 1.28+
- ✅ 1.27
- ✅ 1.26
- ⚠️ 1.25 (limited testing)

### Container Runtimes
- ✅ containerd
- ✅ Docker
- ✅ CRI-O
- ⚠️ Others (untested)

### Device Plugin Versions
- ✅ v0.17.4 (recommended)
- ✅ v0.16.x
- ✅ v0.15.x
- ⚠️ Older versions (may work)

## Common Commands

```bash
# Deploy mock driver only
kubectl apply -f 30-daemonset-mock-driver.yaml

# Deploy device plugin only  
kubectl apply -f 50-device-plugin.yaml

# Test GPU allocation
kubectl apply -f 80-test-gpu-pod.yaml

# Check logs
kubectl logs -n gpu-mock -l app=nvidia-device-plugin

# Clean up
kubectl delete namespace gpu-mock
```

## Troubleshooting Quick Reference

| Issue | Check | Fix |
|-------|-------|-----|
| No GPUs available | Node labels | `kubectl label node <node> nvidia.com/gpu.present=true` |
| NVML init failed | Library permissions | Restart mock driver DaemonSet |
| Pod pending | Resource availability | Check node GPU capacity |
| Device plugin crash | Logs for errors | Update to latest version |

## Getting Help

### Community Support
- GitHub Issues: [k8s-test-infra/issues](https://github.com/NVIDIA/k8s-test-infra/issues)
- Discussions: [k8s-test-infra/discussions](https://github.com/NVIDIA/k8s-test-infra/discussions)

### Contributing
We welcome contributions! See [CONTRIBUTING.md](../CONTRIBUTING.md) for:
- Development setup
- Coding standards
- Pull request process
- Testing requirements

### Reporting Issues
When reporting issues, please include:
1. GPU Mockctl version
2. Kubernetes version
3. Device plugin version
4. Relevant logs
5. Steps to reproduce

## License

GPU Mockctl is licensed under the Apache License 2.0. See [LICENSE](../LICENSE) for details.

---

*This documentation is for GPU Mockctl version 0.1.0. For other versions, check the git tags.*
