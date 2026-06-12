## MAGI Report: CASPER / gemini-3.1-pro

### Findings
- [High] [Must-fix]: Hardcoded `nvidia-utils` apt package pin will cause build failures
  - Evidence: `deployments/nvml-mock/Dockerfile` runs `apt-get download nvidia-utils-580=580.65.06-0ubuntu1`. The comment notes this exact version is required because later 580.x packages are dummy packages.
  - Impact: NVIDIA frequently prunes older minor versions of packages from the CUDA Ubuntu apt repositories. Once `580.65.06-0ubuntu1` is pruned, the `apt-get download` command will return a 404 and permanently break the `nvml-mock` Docker image build.
  - Suggested fix: Do not rely on the upstream rolling apt repo for an exact, pinned, non-latest version. Mirror the `.deb` file in a stable location (e.g., a GitHub release asset or internal artifact registry) or fetch the `nvidia-smi` binary from a stable CUDA base image instead.
  - Confidence: High

- [Medium] [Should-fix]: High lock contention via `os.Getenv` in mock NVML hot path
  - Evidence: In `pkg/gpu/mocknvml/engine/fabric_readiness.go`, `state()` calls `os.Getenv(EnvFabricStateDir)` on every invocation. In Go, `os.Getenv` acquires a global `syscall.envs` `RWMutex` read lock.
  - Impact: The mock NVML engine is frequently polled by observability tools (like DCGM or Prometheus exporters). Under high concurrency, calling `os.Getenv` in a hot path causes `RWMutex` contention across goroutines, degrading performance of the mock and potentially affecting the timing characteristics of the test infrastructure.
  - Suggested fix: Cache the result of `os.Getenv(EnvFabricStateDir)` at package `init()` time or inside the `fabricReadinessCache` struct upon its initialization, as the environment variable does not change during the daemon's lifetime.
  - Confidence: High

- [Low] [Should-fix]: Unsupervised background daemons in `entrypoint.sh`
  - Evidence: `deployments/nvml-mock/scripts/setup.sh` launches `/usr/bin/nv-fabricmanager &` (and mock-ib) in the background. `entrypoint.sh` then runs `exec sleep infinity`.
  - Impact: If the mock fabricmanager daemon crashes, it will not be restarted. While it is a mock daemon, an unhandled crash would silently fail to maintain the mock fabric state (e.g. failing to maintain the readiness marker, leaving GPUs in `IN_PROGRESS`), causing downstream scheduling or topology issues in E2E tests without failing the Pod.
  - Suggested fix: Use a lightweight process supervisor (like `s6`, `tini`, or a simple bash `wait` loop) in `entrypoint.sh` to monitor the background daemons and fail/restart the container if they exit unexpectedly.
  - Confidence: Medium

- [Info] [No-action]: Clean dynamic Helm gating for `fabricmanager`
  - Evidence: `deployments/nvml-mock/helm/nvml-mock/values.yaml` uses `enabled: ""` for `fabricmanager`, deriving the boolean dynamically from the selected GPU profile's defaults via the `nvml-mock.fabricmanagerEnabled` helper in `daemonset.yaml`. E2E tests (`.github/workflows/nvml-mock-e2e.yaml`) dynamically check this injected `MOCK_FABRICMANAGER` env var.
  - Impact: Prevents duplicate state management between CI, Helm values, and profile YAMLs. Profile definitions remain the single source of truth.
  - Suggested fix: N/A - Good infrastructure design pattern.
  - Confidence: High