## MAGI Final Summary

PR #387 — `feat(nvml-mock): simulate NVSwitch topology and nvidia-fabricmanager`
Branch: `feat/nvswitch-fabricmanager` | 55 files | +4842/−458 | Draft

### Consensus Must-Fix
- (none) — No finding reached must-fix consensus across two or more MAGI agents. MELCHIOR and CASPER each raised one independent must-fix (Helm `fabricmanager.enabled` typo coercion; Dockerfile `nvidia-utils-580` apt pin fragility).

### Disputed Must-Fix
- [High] Helm `fabricmanager.enabled` typo silently disables readiness gate — positions: MELCHIOR → must-fix; BALTHASAR/CASPER → not raised (CASPER noted clean dynamic gating as no-action)
- [High] Dockerfile hardcoded `nvidia-utils-580=580.65.06-0ubuntu1` apt pin — positions: CASPER → must-fix; MELCHIOR/BALTHASAR → not raised (PR author documents intentional pin; mitigated until NVIDIA prunes the package)

### Should-Fix (implemented in this review)
- [High] `fabricmanager.enabled` validation — MELCHIOR; aligns with existing `infiniband.mockTier` fail-fast pattern
- [Medium] `values.yaml` Fabric Manager comments contradict A100 NVSwitch behavior — MELCHIOR
- [Medium] `computeNVCounts` fans switch links to non-switch GPUs — BALTHASAR
- [Medium] NUMA default-zero for unmatched BDFs — BALTHASAR
- [Medium] `fabricReadiness` calls `os.Getenv` on hot path; global cache not reset in `ResetForTesting` — CASPER + BALTHASAR
- [Low] Standalone demo `helm install` breaks cluster reuse — MELCHIOR

### Should-Fix (deferred — out of scope for safe consensus fixes)
- CDI/DRA consumer-pod NVLink/FM assertions in e2e CI — MELCHIOR only; larger workflow change
- Topology getters ignore CDI visible-device subset — BALTHASAR; behavior change needs maintainer scoping
- NVLink `LINK_COUNT` vs link-index enumeration mismatch — BALTHASAR; no shipped profile hits it
- Counter uint64 overflow on multi-year uptime — BALTHASAR; low likelihood
- Fabricmanager SIGKILL stale marker / unsupervised background daemon — BALTHASAR + CASPER; mock-by-design trade-off
- Mirror `nvidia-smi` .deb to stable artifact store — CASPER; infrastructure decision

### No-Action
- Clean dynamic Helm `fabricmanager` gating from profile — CASPER
- Intentional directional GPU↔GPU NV# for one-sided link configs — BALTHASAR (document)
- Packet vs byte counter cosmetic inconsistency — BALTHASAR
- `SPEED_MBPS_Lx` only covers links 0–11 — BALTHASAR (580 nvidia-smi uses scopeId path)
- Immutable `NodeFabric` architecture — BALTHASAR (keep)

### Implementation Plan
- Implementer: BALTHASAR (correctness-heavy fixes)
- Fix order: Helm validation → engine topology/readiness → demo UX → docs comments → helm unittest
- Validation: `go test ./pkg/gpu/mocknvml/engine/... ./pkg/fmcoord/...`, `helm unittest` on nvml-mock chart
- Remaining risks: DCO check ACTION_REQUIRED on PR; e2e CI not yet run on real NVIDIA runners; Dockerfile apt pin still fragile
