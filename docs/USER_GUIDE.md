# GPU Mockctl User Guide

## Table of Contents

1. [Getting Started](#getting-started)
2. [Common Use Cases](#common-use-cases)
3. [Deployment Scenarios](#deployment-scenarios)
4. [Troubleshooting](#troubleshooting)
5. [Advanced Usage](#advanced-usage)
6. [Best Practices](#best-practices)

## Getting Started

### Prerequisites

- Docker or compatible container runtime
- Kubernetes cluster (or KIND for local testing)
- Basic understanding of Kubernetes concepts

### Quick Start

The fastest way to get a mock GPU environment running:

```bash
# Clone the repository
git clone https://github.com/NVIDIA/k8s-test-infra
cd k8s-test-infra/deployments/devel/gpu-mock

# Deploy everything
make all

# Verify deployment
make status
```

### Understanding the Components

1. **gpu-mockctl**: CLI tool that creates mock driver files
2. **Mock NVML Library**: Simulates NVIDIA Management Library
3. **Device Plugin**: Official NVIDIA device plugin configured for mock environment
4. **Test Pod**: Validates GPU allocation works correctly

## Common Use Cases

### 1. Local Development with KIND

Perfect for developers who need to test GPU workloads locally:

```bash
# Create KIND cluster
make kind-up

# Deploy mock environment
make deploy-all

# Test GPU allocation
make test-gpu

# Check logs if needed
make logs
```

### 2. CI/CD Pipeline Testing

Add GPU testing to your CI/CD pipeline without GPU hardware:

```yaml
# .github/workflows/gpu-test.yml
name: GPU Integration Tests

on: [push, pull_request]

jobs:
  test:
    runs-on: ubuntu-latest
    steps:
    - uses: actions/checkout@v3
    
    - name: Create KIND cluster
      run: |
        kind create cluster --name gpu-test
        
    - name: Deploy mock GPU environment
      run: |
        cd deployments/devel/gpu-mock
        make deploy-all
        
    - name: Run GPU tests
      run: |
        kubectl apply -f my-gpu-workload.yaml
        kubectl wait --for=condition=complete job/my-gpu-job --timeout=300s
        
    - name: Cleanup
      if: always()
      run: kind delete cluster --name gpu-test
```

### 3. Multi-GPU Application Testing

Test applications that require multiple GPUs:

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: multi-gpu-test
  namespace: gpu-mock
spec:
  containers:
  - name: app
    image: myapp:latest
    resources:
      limits:
        nvidia.com/gpu: 4  # Request 4 GPUs
    env:
    - name: NVIDIA_VISIBLE_DEVICES
      value: "all"  # Or specific GPU UUIDs
```

### 4. Testing GPU Scheduling Policies

Test Kubernetes scheduling behavior with GPU resources:

```yaml
# pod-anti-affinity.yaml
apiVersion: v1
kind: Pod
metadata:
  name: gpu-exclusive-pod
spec:
  affinity:
    podAntiAffinity:
      requiredDuringSchedulingIgnoredDuringExecution:
      - labelSelector:
          matchExpressions:
          - key: gpu-workload
            operator: In
            values: ["true"]
        topologyKey: kubernetes.io/hostname
  containers:
  - name: app
    image: busybox
    resources:
      limits:
        nvidia.com/gpu: 1
  nodeSelector:
    nvidia.com/gpu.present: "true"
```

## Deployment Scenarios

### Scenario 1: Development Environment

For developers who need a quick GPU mock:

```bash
# Option 1: Using Makefile (recommended)
cd deployments/devel/gpu-mock
make all

# Option 2: Manual deployment
kubectl create namespace gpu-mock
kubectl apply -f 00-namespace.yaml
kubectl apply -f 30-daemonset-mock-driver.yaml
kubectl apply -f 50-device-plugin.yaml
kubectl apply -f 80-test-gpu-pod.yaml
```

### Scenario 2: Integration Testing

For testing GPU-dependent services:

```bash
# 1. Deploy mock environment
make deploy-all

# 2. Deploy your service
kubectl apply -f your-gpu-service.yaml

# 3. Run integration tests
kubectl apply -f integration-tests.yaml

# 4. Monitor results
kubectl logs -f job/integration-tests
```

### Scenario 3: Performance Testing

For testing resource allocation and limits:

```bash
# Deploy multiple GPU pods simultaneously
for i in {1..10}; do
  kubectl apply -f - <<EOF
apiVersion: v1
kind: Pod
metadata:
  name: gpu-load-test-$i
  namespace: gpu-mock
spec:
  containers:
  - name: test
    image: busybox
    command: ["sleep", "300"]
    resources:
      limits:
        nvidia.com/gpu: 1
EOF
done

# Check allocation
kubectl get pods -n gpu-mock -o wide
kubectl describe nodes | grep -A5 "Allocated resources"
```

## Troubleshooting

### Issue: No GPUs Available

**Symptoms:**
- Pods stuck in Pending state
- `nvidia.com/gpu: 0` in node capacity

**Diagnosis:**
```bash
# Check node labels
kubectl get nodes --show-labels | grep nvidia

# Check device plugin status
kubectl get pods -n gpu-mock -l app=nvidia-device-plugin

# Check device plugin logs
kubectl logs -n gpu-mock -l app=nvidia-device-plugin
```

**Solutions:**
1. Ensure mock driver DaemonSet is running:
   ```bash
   kubectl rollout status ds/nvidia-mock-driver -n gpu-mock
   ```

2. Verify NVML library is accessible:
   ```bash
   kubectl exec -n gpu-mock <driver-pod> -- ls -la /host/var/lib/nvidia-mock/driver/lib64/
   ```

3. Restart device plugin:
   ```bash
   kubectl rollout restart ds/nvidia-device-plugin -n gpu-mock
   ```

### Issue: NVML Initialization Failed

**Symptoms:**
- Device plugin crashes with NVML errors
- "NVML was not first initialized" in logs

**Diagnosis:**
```bash
# Check library permissions
kubectl exec -n gpu-mock <driver-pod> -- ls -la /host/var/lib/nvidia-mock/driver/lib64/libnvidia-ml*

# Test NVML directly
kubectl run nvml-test --rm -it --image=busybox -- /bin/sh
# Inside container:
LD_LIBRARY_PATH=/var/lib/nvidia-mock/driver/lib64 /test-nvml
```

**Solutions:**
1. Fix library permissions:
   ```bash
   kubectl exec -n gpu-mock <driver-pod> -- chmod +x /host/var/lib/nvidia-mock/driver/lib64/*.so*
   ```

2. Verify library symlinks:
   ```bash
   kubectl exec -n gpu-mock <device-plugin-pod> -- ls -la /usr/lib64/libnvidia-ml*
   ```

### Issue: Pod Cannot Access GPU

**Symptoms:**
- Pod running but no GPU access
- Empty NVIDIA_VISIBLE_DEVICES

**Diagnosis:**
```bash
# Check pod environment
kubectl exec <pod-name> -- env | grep NVIDIA

# Check resource allocation
kubectl describe pod <pod-name> | grep -A10 "Containers:"
```

**Solutions:**
1. Ensure resource request is correct:
   ```yaml
   resources:
     limits:
       nvidia.com/gpu: 1
   ```

2. Check if node has available GPUs:
   ```bash
   kubectl describe node | grep -E "nvidia.com/gpu|Allocated"
   ```

## Advanced Usage

### Custom GPU Configuration

Modify the number or type of GPUs:

1. Edit `pkg/gpu/mocknvml/data/devices.h`
2. Rebuild the image:
   ```bash
   make image
   ```
3. Redeploy:
   ```bash
   make redeploy
   ```

### Debug Logging

Enable detailed logging for troubleshooting:

```bash
# In DaemonSet
env:
- name: GPU_MOCKCTL_LOG_LEVEL
  value: "trace"

# Command line
gpu-mockctl --log-level=trace driver --with-compiled-nvml

# JSON format for log aggregation
gpu-mockctl --log-format=json --log-level=debug all
```

### Performance Tuning

Optimize for large clusters:

```yaml
# Increase device plugin resources
resources:
  requests:
    cpu: 100m
    memory: 128Mi
  limits:
    cpu: 500m
    memory: 512Mi

# Add node affinity for GPU nodes
affinity:
  nodeAffinity:
    requiredDuringSchedulingIgnoredDuringExecution:
      nodeSelectorTerms:
      - matchExpressions:
        - key: node-role.kubernetes.io/gpu
          operator: Exists
```

### Monitoring

Add Prometheus metrics:

```yaml
# ServiceMonitor for device plugin
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: nvidia-device-plugin
  namespace: gpu-mock
spec:
  selector:
    matchLabels:
      app: nvidia-device-plugin
  endpoints:
  - port: metrics
    interval: 30s
    path: /metrics
```

## Best Practices

### 1. Resource Management

- Always set resource limits for GPU pods
- Use node selectors for GPU workloads
- Monitor GPU allocation across nodes

### 2. Deployment Strategy

- Use namespaces to isolate mock environment
- Apply RBAC for production deployments
- Version your mock configurations

### 3. Testing Strategy

- Start with single GPU tests
- Progress to multi-GPU scenarios
- Test failure conditions

### 4. Maintenance

- Regularly update to latest device plugin
- Monitor for deprecated NVML functions
- Keep documentation current

### 5. Security

- Run with minimal privileges where possible
- Use network policies to isolate test workloads
- Audit mock environment access

## Example Workflows

### Workflow 1: Testing GPU Memory Allocation

```bash
# Deploy a pod that checks GPU memory
kubectl apply -f - <<EOF
apiVersion: v1
kind: Pod
metadata:
  name: gpu-memory-test
  namespace: gpu-mock
spec:
  containers:
  - name: test
    image: nvcr.io/nvidia/cuda:12.4.0-base-ubuntu22.04
    command: ["nvidia-smi", "-q", "-d", "MEMORY"]
    resources:
      limits:
        nvidia.com/gpu: 1
EOF

# Check results
kubectl logs gpu-memory-test -n gpu-mock
```

### Workflow 2: Simulating GPU Failures

```bash
# Temporarily remove GPUs from a node
kubectl exec -n gpu-mock <driver-pod> -- rm -f /host/dev/nvidia*

# Watch device plugin response
kubectl logs -n gpu-mock -l app=nvidia-device-plugin -f

# Restore GPUs
kubectl rollout restart ds/nvidia-mock-driver -n gpu-mock
```

### Workflow 3: Load Testing GPU Scheduler

```bash
# Create many GPU pods at once
for i in {1..50}; do
  kubectl run gpu-test-$i --image=busybox --rm --restart=Never \
    --overrides='{"spec":{"containers":[{"name":"test","resources":{"limits":{"nvidia.com/gpu":"1"}}}]}}' \
    -- sleep 60 &
done

# Monitor scheduling decisions
watch kubectl get pods -o wide | grep gpu-test
```

## Conclusion

The GPU mockctl provides a powerful way to test GPU workloads without physical hardware. By following this guide, you can:

- Set up mock GPU environments quickly
- Test complex GPU scheduling scenarios
- Integrate GPU testing into CI/CD pipelines
- Troubleshoot common issues effectively

For more information, see:
- [Architecture Documentation](ARCHITECTURE.md)
- [API Reference](API_REFERENCE.md)
- [Contributing Guide](../CONTRIBUTING.md)
