# GPU Operator CDI Validation — Systematic Debugging Plan

## Context

PR #239 adds GPU Operator E2E testing with CDI mode. After 5 CI runs, each fix
reveals a new layer. This plan applies systematic debugging to identify root
causes and fix them methodically.

## Complete Failure History

| Run | Commit | device-plugin | dra | gpu-operator | What Changed |
|-----|--------|:---:|:---:|:---:|-------------|
| 1 | 8b5261b0 | ✅ | ✅ | ❌ | Initial: Helm --wait timeout, validator stuck |
| 2 | 3da6cb01 | ✅ | ✅ | ❌ | Added symlink + updated paths — still stuck |
| 3 | fe335323 | ✅ | ✅ | ❌ | Added diagnostics — confirmed files exist on node |
| 4 | c0777723 | ❌ | ❌ | ❌ | Switched nvidia-smi to ELF binary — broke DRA/device-plugin |
| 5 | 27e2d986 | ✅ | ❌ | ❌ | LD_LIBRARY_PATH fix + DISABLE_DEV_CHAR_SYMLINK |
| 6 | 53912921 | ✅ | ✅ | ❌ | Reverted to bash shim — DRA/device-plugin pass, but validator has no /bin/bash |
| 7 | fb104cba | ? | ? | ? | patchelf RPATH=$ORIGIN/../lib64 — ELF binary self-locating |

## Root Cause Analysis

### What run 4 revealed (key evidence)

The GPU Operator validator logs from run 4 showed:

```
Attempting to validate a pre-installed driver on the host
Attempting to validate a driver container installation
[nvidia-smi runs SUCCESSFULLY — full GPU table output]
Error: error creating symlink creator: failed to create devices info: no NVIDIA devices found
```

**Finding**: nvidia-smi works fine. The validator crashes on a POST-nvidia-smi step:
creating `/dev/char` symlinks for NVIDIA devices. In our mock environment, there
are no real `/dev/nvidia*` devices, so this always fails.

### The bash shim vs ELF binary conflict

| Question | Answer |
|----------|--------|
| Does the GPU Operator validator have /bin/bash? | **NO** — gpu-operator:v25.10.1 (distroless/cc) does NOT have /bin/bash |
| Does the DRA distroless container have /bin/bash? | YES — nvcr.io/nvidia/distroless/cc has /bin/bash |
| Did the bash shim work in the validator? | **NO** — run 6 confirmed driver-validation loops "failed to validate the driver" |
| Was the ELF binary correct for the validator? | YES — but broke DRA/device-plugin (no LD_LIBRARY_PATH) |
| Was the bash shim correct for DRA? | YES — but broke GPU Operator validator (no /bin/bash) |

**Root conflict**: GPU Operator validator needs ELF binary (no shell interpreter).
DRA/device-plugin need lib resolution (LD_LIBRARY_PATH or equivalent). The bash
shim solved DRA but broke the validator. The ELF binary solved the validator but
broke DRA.

**Solution**: `patchelf --set-rpath '$ORIGIN/../lib64'` on the nvidia-smi binary.
This bakes library resolution into the ELF header itself, eliminating the need
for wrapper scripts or environment variables. Works in all contexts because
`$ORIGIN` resolves relative to the binary's actual location at runtime.

### DRA regression analysis (run 5)

In run 5, DRA fails at "Install DRA driver" (Helm install timeout), not at
validate-nvidia-smi.sh. The DRA kubelet-plugin init container likely fails
because:

1. The init container runs nvidia-smi from the driver root
2. With the ELF binary, nvidia-smi needs `LD_LIBRARY_PATH` to find libnvidia-ml.so.1
3. The bash shim previously handled this internally
4. The DRA chart doesn't know to set `LD_LIBRARY_PATH`

### GPU Operator remaining issues after driver-validation

Even if driver-validation passes, the validator chain has 3 more steps:

1. **toolkit-validation**: Runs `nvidia-smi` with NO env manipulation — relies on CDI
   injection via nvidia-container-runtime
2. **cuda-validation**: `WITH_WORKLOAD=false` → skips, creates status file immediately
3. **plugin-validation**: Polls node for `nvidia.com/gpu` allocatable resources

The device-plugin and GFD DaemonSets ALSO have toolkit-validation init
containers. If CDI injection doesn't work, nothing in the GPU Operator stack
starts.

## Plan

### Task 1: Revert nvidia-smi to bash shim (fix DRA regression)

**Rationale**: The ELF binary change was unnecessary (bash shim works in
distroless) and broke DRA. Revert to the bash shim approach from before
commit `c0777723`.

**Files:**
- Revert: `deployments/gpu-mock/scripts/setup.sh` — restore bash shim as
  `nvidia-smi`, ELF binary as `nvidia-smi.real`
- Revert: `tests/e2e/validate-nvidia-smi.sh` — remove LD_LIBRARY_PATH
  (shim handles it)

**Keep**: `DISABLE_DEV_CHAR_SYMLINK_CREATION=true` in gpu-operator-values.yaml
(commit 27e2d986).

**Expected outcome**:
- device-plugin: ✅ (bash shim sets LD_LIBRARY_PATH)
- dra: ✅ (bash shim works in distroless, DRA init container succeeds)
- gpu-operator: driver-validation ✅ (bash shim + DISABLE_DEV_CHAR_SYMLINK)
  → toolkit-validation: UNKNOWN (CDI injection needed)

### Task 2: Add CDI injection diagnostics

**Rationale**: toolkit-validation is the next predicted failure. Before waiting
for CI, add diagnostics that will tell us exactly what's happening.

**File**: `.github/workflows/gpu-mock-e2e.yaml`

Add diagnostic step after gpu-mock install, before GPU Operator install:

```yaml
- name: Test CDI injection end-to-end
  run: |
    NODE_CONTAINER=gpu-mock-operator-e2e-control-plane

    echo "=== CDI spec ==="
    docker exec "$NODE_CONTAINER" cat /var/run/cdi/nvidia.yaml | head -30

    echo "=== nvidia-container-runtime config ==="
    docker exec "$NODE_CONTAINER" cat /etc/nvidia-container-runtime/config.toml

    echo "=== containerd config (nvidia runtime) ==="
    docker exec "$NODE_CONTAINER" grep -A10 nvidia /etc/containerd/config.toml || echo "no nvidia runtime in containerd"

    echo "=== nvidia-cdi-hook binary ==="
    docker exec "$NODE_CONTAINER" which nvidia-cdi-hook || echo "NOT FOUND"
    docker exec "$NODE_CONTAINER" nvidia-cdi-hook --version 2>&1 || echo "version check failed"

    echo "=== Test CDI injection with a real pod ==="
    kubectl run cdi-test --image=ubuntu:22.04 --restart=Never \
      --env="NVIDIA_VISIBLE_DEVICES=all" \
      --command -- sh -c 'echo "=== injected files ===" && ls -la /usr/bin/nvidia-smi /usr/lib64/libnvidia-ml* 2>&1 && echo "=== nvidia-smi ===" && nvidia-smi 2>&1 && echo CDI_OK' || true
    sleep 15
    echo "=== CDI test pod status ==="
    kubectl get pod cdi-test -o wide || true
    kubectl describe pod cdi-test || true
    echo "=== CDI test pod logs ==="
    kubectl logs cdi-test || true
    kubectl delete pod cdi-test --ignore-not-found || true
```

This will tell us:
- Whether CDI injection injects our mock libs
- Whether nvidia-smi works inside a CDI-injected container
- If it fails, what the containerd/CDI error is

### Task 3: Fix CDI injection issues (conditional)

Depending on Task 2 diagnostics:

**If nvidia-cdi-hook not found**: Verify nvidia-container-toolkit installation
includes it. May need to install a different package.

**If CDI injection doesn't trigger**: Check containerd config — the nvidia
runtime must be set as default. Verify `nvidia-ctk runtime configure` output.

**If CDI injection triggers but nvidia-smi fails**: Check ldcache update.
May need to add more library mounts to CDI spec (e.g., libcuda.so.1).

**If CDI works for test pod but not for GPU Operator pods**: Check whether
the GPU Operator sets `NVIDIA_VISIBLE_DEVICES` on toolkit-validation init
containers. May need to set it via ClusterPolicy env vars.

### Task 4: Handle plugin-validation (conditional)

plugin-validation polls `nvidia.com/gpu` in node allocatable. This requires
the device-plugin DaemonSet to be running, which requires its own
toolkit-validation init to pass (same CDI dependency).

If CDI injection works (Task 3), device-plugin should start and register GPUs.
If plugin-validation still times out, increase the polling wait in the E2E.

## Architecture Questions (if 3+ fixes don't resolve)

If after Tasks 1-3 we're still chasing new failures:

1. **Should we disable the validator entirely?** GPU Operator supports
   `validator.plugin.env: [{name: WITH_WAIT, value: "false"}]`. But
   device-plugin/GFD still need toolkit-validation.

2. **Can we skip toolkit-validation?** Some GPU Operator versions allow
   disabling specific init containers via ClusterPolicy annotations.

3. **Should we mock CDI differently?** Instead of relying on the
   nvidia-container-runtime CDI hook, we could pre-install mock libs in
   the container images.

4. **Is the GPU Operator E2E testing the right level of abstraction?** If
   the mock can't satisfy the full GPU Operator stack, we might be better
   off testing device-plugin and GFD standalone (which already work).

## Execution Order

1. Task 1 (revert ELF + keep DISABLE_DEV_CHAR) — single commit
2. Task 2 (add CDI diagnostics) — single commit
3. Push, wait for CI
4. Based on results: Task 3 or Task 4 or Architecture Discussion
