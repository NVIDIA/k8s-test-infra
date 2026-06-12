## MAGI Report: MELCHIOR / gpt-5.5-extra-high

### Findings
- [High] [Must-fix]: Invalid `fabricmanager.enabled` values silently disable the Fabric Manager readiness gate
  - Evidence: `deployments/nvml-mock/helm/nvml-mock/templates/_helpers.tpl` / `nvml-mock.fabricmanagerEnabled` treats any non-empty override as boolean by comparing `toString $override` only to `"true"`, so typos like `ture` render as disabled. `deployments/nvml-mock/helm/nvml-mock/values.schema.json` has no `fabricmanager` schema to reject the typo. I confirmed with `helm template ... --set gpu.profile=h100 --set fabricmanager.enabled=ture`: it exited successfully and rendered no `MOCK_FABRICMANAGER` env or `host-fabric-state` mount, while the default h100 render included both.
  - Impact: A user typo or string value can bypass the new product behavior without an install-time error. For H100/GB200/GB300 profiles with `fabric.state: auto`, disabling the env/mount makes the engine resolve `auto` to `COMPLETED`, so workloads can appear fabric-ready even though the fake Fabric Manager gate is absent.
  - Suggested fix: Add `fabricmanager` validation to `values.schema.json` (`enabled` as boolean or empty string, `stateDir` as absolute path string) and make the helper fail on unsupported non-empty strings instead of coercing them to false. Add Helm unit coverage for default/profile-derived, explicit true, explicit false, and invalid values.
  - Confidence: High

- [Medium] [Should-fix]: CDI/DRA consumer-path tests do not assert the new NVLink/Fabric Manager behavior inside workload pods
  - Evidence: `.github/workflows/nvml-mock-e2e.yaml` / `Test CDI injection end-to-end` runs `nvidia-smi 2>&1 || echo "nvidia-smi failed exit=$?"`, then always continues to `CDI_TEST_DONE`; it does not fail the job on missing/broken `nvidia-smi` and does not assert `topo -m`, `nvlink -s`, or Fabric Manager state from inside the CDI-injected pod. The DRA job only waits for `gpu-test-pod` to reach `Running`, so it proves `NodePrepareResources` but not that the allocated container sees the CDI-mounted Fabric Manager state directory.
  - Impact: The PR adds CDI env/mount wiring for `MOCK_FABRICMANAGER_STATE_DIR`, but CI can still pass if that consumer-facing wiring regresses. Node-container and DaemonSet checks do not fully represent the user path: applications call NVML from allocated pods.
  - Suggested fix: Make the CDI pod command fail on `nvidia-smi` errors, assert the pod phase is `Succeeded`, and run a consumer-pod NVLink/Fabric check for NVSwitch profiles. For DRA, run a short command in the ResourceClaim pod that validates `nvidia-smi -L` and, for NVSwitch profiles, `nvidia-smi topo -m` / `nvlink -s` or a small `GetGpuFabricInfo` probe.
  - Confidence: High

- [Low] [Should-fix]: Standalone demo reuse mode still uses unconditional `helm install`
  - Evidence: `docs/demo/standalone/demo.sh` now reuses an existing Kind cluster when `FORCE_RECREATE` is not set, but later always runs `helm install nvml-mock ...` for the same release name.
  - Impact: The new "reuse existing cluster" UX fails on the common second run if the previous `nvml-mock` release still exists, with Helm's "cannot re-use a name that is still in use" error. Users trying the demo iteratively will hit a failure after build/load work has already completed.
  - Suggested fix: Use `helm upgrade --install nvml-mock ...` for the demo path, or delete/replace the existing release when reusing the cluster. If reuse is intended to preserve state, also make the output explicit about whether the chart is being upgraded or freshly installed.
  - Confidence: High

- [Low] [Should-fix]: Fabric Manager values comments contradict the chart helper's A100 behavior
  - Evidence: `deployments/nvml-mock/helm/nvml-mock/values.yaml` describes default enablement as derived from `device_defaults.fabric.state: auto` and says "Non-NVSwitch profiles (A100, standalone B200) ... do not start the daemon." In contrast, `nvml-mock.fabricmanagerEnabled` enables the daemon whenever the profile declares `nvlink.switches`, and the A100 profile declares six switches.
  - Impact: Users reading values documentation can reasonably expect A100 to skip fake Fabric Manager, while the rendered DaemonSet starts it. That mismatch makes workflow skips, readiness checks, and custom overrides harder to reason about.
  - Suggested fix: Update the `values.yaml` comments to match the helper: default enablement is `fabric.state:auto` or declared NVSwitches; A100 is NVSwitch-backed and starts the daemon but does not couple `GpuFabricInfo` because the fabric API is unsupported there. Keep README/NOTES wording aligned with the same rule.
  - Confidence: High
