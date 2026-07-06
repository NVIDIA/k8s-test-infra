# Node-Wide Mock GPU Injection (Every Pod) Design

**Date:** 2026-07-01
**Status:** Superseded by [`2026-07-06-node-wide-mock-injection-nri-design.md`](2026-07-06-node-wide-mock-injection-nri-design.md)
**Authors:** Design session (brainstorming)
**Supersedes:** [`2026-06-30-node-wide-mock-injection-design.md`](2026-06-30-node-wide-mock-injection-design.md)
and its plan [`plans/2026-06-30-node-wide-mock-injection.md`](plans/2026-06-30-node-wide-mock-injection.md)

> **Superseded (2026-07-06):** Injecting at the API-server admission layer works
> but rewrites every pod spec and carries a TLS webhook + `caBundle` + a
> cluster-wide `failurePolicy`. The successor design keeps this exact overlay
> contract (mount + `PATH`/`LD_*`/`MOCK_*` env) but moves the injection point
> down into containerd via an NRI plugin, so the pod spec is untouched and the
> failure blast radius is node-local. See the successor design.

## Summary

Make the mock GPU environment **ambient on the node**: any pod scheduled on a
Kind node running the nvml-mock DaemonSet should be able to run `nvidia-smi`,
`ibnetdiscover`, `ibstat`, `iblinkinfo`, etc. and believe a GPU is present —
**without** requesting `nvidia.com/gpu`, without special annotations, and
without any containerd/CDI or `runtimeClassName` configuration.

A new mutating admission webhook (`nvml-mock-injector`) rewrites **every** pod
spec to mount the DaemonSet-materialized host tree at a side path
(`/opt/nvml-mock`) and inject environment variables. The DaemonSet keeps its
existing responsibility of laying down host artifacts.

This design **replaces** the 2026-06-30 tiered, opt-in, CDI-based design. That
approach injected `cdi.k8s.io/*` annotations resolved by containerd (a node
change) and explicitly listed "automatic injection into every pod" as a
non-goal. The requirement has since changed: inject into every pod, using a
pure-Kubernetes webhook that mutates the pod spec directly.

## Goals

- Any pod on a mock node — no annotations, no GPU resource request — can run
  `nvidia-smi` and the IB discovery tools (`ibnetdiscover`, `ibstat`,
  `iblinkinfo`, `ibstatus`, `sminfo`) and see a mock GPU/IB fabric.
- Pure Kubernetes: no containerd `enable_cdi`, no `runtimeClassName`, no node
  image customization. Works on a stock Kind cluster.
- Non-destructive: never overwrite an image's `/usr/bin` or `/usr/lib64`; a pod
  must never fail to start because of injection.
- Fail-open: a webhook outage must never block cluster-wide scheduling.
- Single source of truth for host artifacts remains the DaemonSet / `setup.sh`.

## Non-Goals

- DCGM hostengine / dcgm-exporter enablement (follow-up).
- CUDA kernel execution fidelity (mock `libcuda.so` remains early-stage).
- Injecting real `/dev/nvidia*` device nodes into *every* pod (opt-in only —
  requires `privileged`).
- MIG or SR-IOV VF simulation.
- Guaranteeing correctness inside musl/Alpine, distroless, or scratch images
  (handled via opt-out, not universal compatibility).

## Requirements (from design session)

| Decision | Choice |
|----------|--------|
| Which pods | Literally every pod, all namespaces |
| Mechanism | Mutating webhook that rewrites the pod spec directly (webhook-only, no node/containerd changes) |
| Path strategy | Additive overlay via `PATH`/`LD_LIBRARY_PATH` at a side path (non-destructive) |
| `/dev/nvidia*` device nodes | Opt-in per pod via annotation (adds `privileged`) |
| `LD_PRELOAD` hazard guard | Inject all namespaces by default; honor an opt-out annotation + configurable namespace exclusion list |
| Capabilities | Full parity: NVML + `nvidia-smi`, CUDA libs, IB tooling, PCI sysfs redirect |

## Background

### Current state

The nvml-mock DaemonSet (`deployments/nvml-mock/scripts/setup.sh`) writes to
the host:

| Path | Purpose |
|------|---------|
| `/var/lib/nvml-mock/driver/` | Mock NVML/CUDA libs, `nvidia-smi`, `/dev/nvidia*` nodes, `/proc/driver/nvidia` stubs |
| `/var/lib/nvml-mock/ib/` | Fake InfiniBand sysfs tree |
| `/var/lib/nvml-mock/sys/` | Fake PCI sysfs tree (`render-pci-sysfs`) |
| `/var/run/cdi/nvidia.yaml` | CDI spec for GPU device injection |
| `/run/nvidia/driver` | GPU Operator compatibility symlink |

Inside the DaemonSet pod only, `LD_PRELOAD` activates the IB shims
(`libibmocksys`, `libibmockumad`, `libibmockverbs`). CDI-injected GPU pods get
NVML libs and device nodes but not the IB/PCI redirect. Arbitrary pods see
nothing.

### Why the overlay works with so few mounts

- **NVML / `nvidia-smi`:** the only piece that must physically exist on disk in
  the container. The mock `nvidia-smi` ELF has `RPATH=$ORIGIN/../lib64`, so once
  the driver tree is mounted it finds `libnvidia-ml.so.1` relative to itself.
- **InfiniBand tools:** work entirely through the `LD_PRELOAD` shims plus env —
  `libibmocksys` redirects `/sys/class/infiniband*` reads to `$MOCK_IB_ROOT`,
  and `libibmockumad` routes UMAD ioctls to the DaemonSet's `mock-ib` daemon via
  the shared socket. No bind-mount over `/sys` is required.
- **PCI sysfs:** the new `libpcimocksys` shim redirects `/sys/bus/pci/*` reads to
  `$MOCK_PCI_ROOT`, same pattern as the IB sysfs shim.

## Architecture

```
┌─────────────────────────────────────────────────────────────────────────┐
│ Kind Node (host filesystem)                                              │
│  nvml-mock DaemonSet (setup.sh)                                          │
│    → /var/lib/nvml-mock/driver/{usr/lib64,usr/bin,usr/local/lib,config}  │
│    → /var/lib/nvml-mock/{ib,sys,run/mock-ib.sock}                        │
└─────────────────────────────────────────────────────────────────────────┘
         ▲ hostPath /var/lib/nvml-mock  (DirectoryOrCreate, RO tree)
         │
┌─────────────────────────────────────────────────────────────────────────┐
│ Kubernetes                                                               │
│  nvml-mock-injector (MutatingWebhookConfiguration, failurePolicy=Ignore) │
│    CREATE pods, all namespaces (minus exclusion list)                    │
│    → adds pod volume + per-container volumeMount at /opt/nvml-mock        │
│    → prepends PATH/LD_LIBRARY_PATH, appends LD_PRELOAD, sets MOCK_* env   │
│    → (opt-in) privileged + /dev/nvidia* device nodes                     │
│                                                                          │
│  Every pod → sees nvidia-smi, ibnetdiscover, ibstat, ... ambiently       │
└─────────────────────────────────────────────────────────────────────────┘
```

**Principle:** The DaemonSet owns host-side artifacts. The webhook wires each
pod to them via a non-destructive overlay. Nothing depends on the container
runtime honoring CDI.

## Components

### 1. The overlay contract

For each pod not excluded/opted-out, and for each container **and**
initContainer, the webhook applies:

**Pod-level volume (added once):**

```yaml
volumes:
  - name: nvml-mock-overlay      # also the idempotency marker
    hostPath:
      path: /var/lib/nvml-mock
      type: DirectoryOrCreate
```

**Per-container volumeMount:**

```yaml
volumeMounts:
  - name: nvml-mock-overlay
    mountPath: /opt/nvml-mock
    readOnly: true
```

The `run/` subtree that carries the mock-ib socket must be connectable. A
read-only *mount* does not prevent `connect(2)` to a unix socket (connect is not
a filesystem write), and the socket file mode is world-connectable, so a single
read-only mount suffices. If a runtime is found to reject socket connect on a RO
mount, the fallback is a second `rw` mount of `/var/lib/nvml-mock/run` only.

**Per-container env** (merge semantics — see below):

| Var | Value | Merge rule |
|-----|-------|------------|
| `PATH` | `/opt/nvml-mock/driver/usr/bin:<existing-or-default>` | prepend |
| `LD_LIBRARY_PATH` | `/opt/nvml-mock/driver/usr/lib64:<existing>` | prepend |
| `LD_PRELOAD` | shims (see below) appended to `<existing>` | append |
| `MOCK_NVML_CONFIG` | `/opt/nvml-mock/driver/config/config.yaml` | set if unset |
| `MOCK_IB` | `full` | set if unset |
| `MOCK_IB_ROOT` | `/opt/nvml-mock/ib` | set if unset |
| `MOCK_IB_PING_SOCKET` | `/opt/nvml-mock/run/mock-ib.sock` | set if unset |
| `MOCK_PCI_ROOT` | `/opt/nvml-mock` | set if unset |

`LD_PRELOAD` value (order matters — UMAD before sysfs, as in the DaemonSet):

```
/opt/nvml-mock/driver/usr/local/lib/libibmockumad.so.1:\
/opt/nvml-mock/driver/usr/local/lib/libibmockverbs.so.1:\
/opt/nvml-mock/driver/usr/local/lib/libibmocksys.so.1:\
/opt/nvml-mock/driver/usr/local/lib/libpcimocksys.so.1
```

**Env merge semantics.** A container's env list is authoritative in the pod
spec, but `PATH`/`LD_LIBRARY_PATH`/`LD_PRELOAD` inherited from the *image* are
not visible at admission time. The webhook therefore:

- If the container already declares the var in `env`, prepend/append to that
  literal value.
- If it does not, set the var to our prefix plus a conservative default
  (`PATH` default: `/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin`;
  `LD_LIBRARY_PATH`/`LD_PRELOAD` default: empty).

This is the one known limitation: an image relying on a non-standard `PATH` set
in its Dockerfile (not in the pod spec) will have that `PATH` replaced by the
default-plus-prefix. This is acceptable for the Kind test-infra use case and is
covered by the opt-out annotation.

### 2. `nvml-mock-injector` webhook (`cmd/nvml-mock-injector`)

Go binary, built into the existing nvml-mock image (or a slim image). Responsibilities:

- Serve `/mutate` over TLS; decode `AdmissionReview`, mutate, respond with an
  RFC 6902 JSON patch.
- **Idempotency:** if a `nvml-mock-overlay` volume is already present, return an
  empty patch (handles re-admission and controllers that resubmit).
- **Opt-out:** if the pod carries `nvml-mock.nvidia.com/inject: "false"`, return
  an empty patch.
- **Device opt-in:** if the pod carries `nvml-mock.nvidia.com/devices: "true"`,
  additionally set `securityContext.privileged: true` on each container and add
  hostPath device-node mounts (`/dev/nvidiactl`, `/dev/nvidia-uvm`,
  `/dev/nvidia0..N`) sourced from `/var/lib/nvml-mock/driver/dev`.
- Record an audit annotation `nvml-mock.nvidia.com/injected: "true"` on mutated
  pods for debugging.

Configuration via env / flags (populated from Helm values): TLS cert paths,
overlay host path + mount path, `LD_PRELOAD` shim list, whether IB/PCI overlay
is enabled, and the opt-out annotation key.

### 3. `MutatingWebhookConfiguration`

- `rules`: `CREATE` on `pods`.
- `failurePolicy: Ignore` — **mandatory**. A cluster-wide `Fail` policy on all
  pods would make the API server unable to schedule anything (including the
  injector itself) whenever the webhook is unavailable.
- `namespaceSelector`: exclude the injector's own release namespace by default
  using the auto-managed `kubernetes.io/metadata.name` label
  (`NotIn [<release-namespace>]`). The excluded set is configurable so operators
  can add `kube-system`, `kube-node-lease`, etc.
- `reinvocationPolicy: Never` (idempotency guard already covers re-admission).
- `sideEffects: None`, `admissionReviewVersions: [v1]`.
- `clientConfig.caBundle`: injected from the Helm-generated CA.

### 4. PCI sysfs redirect shim (new) — `libpcimocksys.so`

**Location:** `pkg/system/mockpcisysfs/c/shim.c` → `libpcimocksys.so.1`.

Mirrors `pkg/network/mockib/c/shim.c`:

- Hooks libc path functions (`open`, `openat`, `readlink`, `readlinkat`, `stat`,
  `lstat`, `fstatat`, `access`, `faccessat`, `opendir`).
- Rewrites paths under `/sys/bus/pci/` and `/sys/devices/pci*` to
  `$MOCK_PCI_ROOT/sys/...` via `dlsym(RTLD_NEXT, ...)`.
- No-op when `MOCK_PCI_ROOT` is unset.

The rendered tree at `/var/lib/nvml-mock/sys/` already exists via
`render-pci-sysfs`; the shim exposes it inside containers without bind-mounting
over `/sys`.

### 5. Host-side changes (DaemonSet / `setup.sh`)

1. **Expose IB tools + shims on the host driver root** so the overlay mount
   surfaces them: copy `ibnetdiscover`, `ibstat`, `iblinkinfo`, `ibstatus`,
   `sminfo`, `ibping` into `$DRIVER_ROOT/usr/bin`, and `libibmockumad.so.*`,
   `libibmockverbs.so.*`, `libibmocksys.so.*`, `libpcimocksys.so.*` into
   `$DRIVER_ROOT/usr/local/lib`. (Today these live only in the image.)
2. **Move the mock-ib socket to a host path** `/var/lib/nvml-mock/run/mock-ib.sock`
   (create `/var/lib/nvml-mock/run`) so injected pods share the DaemonSet's
   `mock-ib` daemon. Cross-pod IB tools (`ibnetdiscover`) additionally rely on
   the existing `mock-ib -fabric` TCP relay.
3. **Build `libpcimocksys.so`** in the Dockerfile builder stage and copy it into
   the runtime image (and thence to the host driver root in step 1).

### 6. Helm chart additions

```yaml
injector:
  enabled: true
  replicaCount: 1
  port: 8443
  image: {}                     # defaults to the nvml-mock image
  overlay:
    hostPath: /var/lib/nvml-mock
    mountPath: /opt/nvml-mock
    ib: true
    pci: true
  optOutAnnotation: nvml-mock.nvidia.com/inject
  excludedNamespaces: []        # release namespace always excluded
  certManager:
    enabled: false              # else Helm genSignedCert self-signed CA
  resources: {}
```

New templates: `injector-deployment.yaml`, `injector-service.yaml`,
`injector-webhook.yaml`, `injector-rbac.yaml`, `injector-tls-secret.yaml`.

## Data Flow

### Ambient discovery pod (no annotations)

```
1. User creates a plain pod (e.g. image ubuntu:22.04), no GPU request.
2. API server calls the mutating webhook.
3. Webhook: not opted out, not excluded, no overlay volume yet → build patch.
4. Patch adds nvml-mock-overlay volume + per-container mount + env.
5. Scheduler places pod; kubelet mounts /opt/nvml-mock (hostPath).
6. Container starts; PATH/LD_LIBRARY_PATH/LD_PRELOAD/MOCK_* are set.
7. `nvidia-smi -L`, `ibstat`, `ibnetdiscover` all succeed.
```

### Device-opt-in pod

```
1. Pod annotated nvml-mock.nvidia.com/devices: "true".
2. Webhook adds the overlay AND privileged + /dev/nvidia* hostPath mounts.
3. Container can open /dev/nvidia0 in addition to the user-space mocks.
```

### Opted-out / excluded pod

```
1. Pod annotated nvml-mock.nvidia.com/inject: "false" (or in an excluded ns).
2. Webhook returns an empty patch; pod runs exactly as authored.
```

## Error Handling

| Condition | Behavior |
|-----------|----------|
| Webhook unavailable | `failurePolicy: Ignore` → pods admitted unmodified; scheduling never blocked |
| Host tree not yet populated | `DirectoryOrCreate` mount succeeds but is empty → tools absent; missing `LD_PRELOAD` paths are harmless glibc warnings; pod still runs |
| musl/Alpine/distroless image | glibc shims may fail to load → use opt-out annotation or namespace exclusion |
| mock-ib socket not ready | IB tools fail at runtime; callers retry (existing E2E pattern) |
| Pod already has overlay volume | Idempotency guard returns empty patch |
| Device opt-in on a node without device nodes | Mount source missing → pod fails to start (expected: opt-in is explicit) |

## Testing Plan

| Test | Validates |
|------|-----------|
| Unit: `mutate` — plain pod | Overlay volume + mount + env added to all containers/initContainers |
| Unit: `mutate` — opt-out annotation | Empty patch |
| Unit: `mutate` — excluded namespace / idempotency | Empty patch when marker present |
| Unit: `mutate` — `LD_PRELOAD`/`PATH` merge | Prepend/append preserves existing values |
| Unit: `mutate` — device opt-in | `privileged` + `/dev/nvidia*` mounts added |
| Integration: `libpcimocksys.so` | `/sys/bus/pci/devices/*` readlink rewrites to `$MOCK_PCI_ROOT` |
| Helm unittest | Webhook `failurePolicy: Ignore`, namespace exclusion, disabled-by-flag |
| E2E: plain `ubuntu` pod, no annotations | `nvidia-smi -L`, `ibstat`, `ibnetdiscover` succeed |
| E2E: opt-out pod | Unmodified (no `/opt/nvml-mock`) |
| E2E: device opt-in pod | `/dev/nvidia0` present |
| E2E: GPU Operator stack | Regression — existing paths still work |

## Implementation Order

1. Build `libpcimocksys.so` shim + unit/integration tests; wire into Dockerfile.
2. Host-side: copy IB tools + shims to driver root; move mock-ib socket to host
   path; update DaemonSet + E2E scripts.
3. Implement `cmd/nvml-mock-injector` (mutation logic + HTTP/TLS server) + unit
   tests.
4. Helm: injector Deployment/Service/webhook/RBAC/TLS + unittest.
5. E2E injection tests (plain pod, opt-out, device opt-in).
6. Docs: Helm README + quickstart "ambient node-wide mock" section.
7. Mark the 2026-06-30 design + plan Superseded (done in this commit).

## Open Questions (resolved)

| Question | Resolution |
|----------|------------|
| Injection scope | Every pod, all namespaces (minus configurable exclusions) |
| Mechanism | Webhook rewrites pod spec directly; no containerd/CDI |
| Path strategy | Additive overlay at `/opt/nvml-mock` (non-destructive) |
| Device nodes | Opt-in per pod (adds `privileged`) |
| musl/distroless safety | Opt-out annotation + namespace exclusion list |
| Failure mode | Fail-open (`failurePolicy: Ignore`) + self-namespace exclusion |

## References

- `deployments/nvml-mock/scripts/setup.sh` — host setup and artifact layout
- `deployments/nvml-mock/helm/nvml-mock/templates/daemonset.yaml` — DaemonSet + current `LD_PRELOAD`
- `pkg/network/mockib/c/shim.c` — IB shim pattern to mirror for PCI
- `pkg/system/mockpcisysfs/render/` — existing PCI sysfs renderer
- `docs/designs/2026-06-30-node-wide-mock-injection-design.md` — superseded design
