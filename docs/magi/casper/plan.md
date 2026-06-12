## MAGI Report: CASPER / gemini-3.1-pro

### Plan
- Risk areas:
  - **Performance/Concurrency**: `fabricReadinessCache` in `fabric_readiness.go` uses a mutex and `os.Stat`. Although it has a 1-second TTL, we must ensure it doesn't bottleneck hot NVML paths (e.g., `nvmlDeviceGetGpuFabricInfo`) if many processes query it concurrently.
  - **Infrastructure/Deployment**: `setup.sh` handles fake fabricmanager startup and CDI injection. We need to verify that the readiness marker directory (`MOCK_FABRICMANAGER_STATE_DIR`) is properly shared and bind-mounted into CDI-injected workloads. The `nvidia-smi` pinning in `Dockerfile` relies on an Ubuntu 22.04 repo; we must ensure architecture mapping (amd64/arm64) is robust.
  - **GPU/NVIDIA Domain**: NVLink counter simulation (`nvlink_counters.go`) uses `/proc/stat` btime or `MOCK_NVML_EPOCH`. We need to verify this is robust across different container environments. The NVLink topology logic (`topology.go`) must accurately reflect NVSwitch connections (e.g., `NVSwitchConnectedLinkCount`) for GB200/GB300 profiles.

- Files to inspect:
  - `pkg/gpu/mocknvml/engine/fabric_readiness.go` (concurrency, caching logic)
  - `pkg/gpu/mocknvml/engine/nvlink_counters.go` (epoch resolution, `/proc/stat` parsing)
  - `deployments/nvml-mock/scripts/setup.sh` (fabricmanager startup, CDI mounts)
  - `deployments/nvml-mock/Dockerfile` (`nvidia-smi` 580 pin, architecture handling)
  - `pkg/gpu/mocknvml/engine/topology.go` (NVSwitch link counting logic)
  - `deployments/nvml-mock/helm/nvml-mock/templates/daemonset.yaml` (volume mounts for fabric state)

- Test strategy:
  - Review `.github/workflows/nvml-mock-e2e.yaml` to ensure it covers the fabricmanager readiness gate and NVLink topology validation for profiles that use them (e.g., gb200).
  - Check if the fake fabricmanager daemon (`cmd/fake-fabricmanager/daemon/main.go`) handles signals and cleans up the marker correctly.
  - Verify that the NVLink counters accrue deterministically and don't reset unexpectedly across `nvidia-smi` invocations.

- Evidence that would change this plan:
  - If `fabricReadinessCache` is only called infrequently (e.g., once per initialization), the mutex/stat performance risk is moot.
  - If E2E tests already run in environments without `/proc/stat`, the epoch resolution fallback is already proven.
