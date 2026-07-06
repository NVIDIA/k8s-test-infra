# Node-Wide Mock GPU Injection via NRI Design

**Date:** 2026-07-06
**Status:** Proposed
**Authors:** Design session (brainstorming)
**Supersedes:** [`2026-07-01-node-wide-mock-injection-every-pod-design.md`](2026-07-01-node-wide-mock-injection-every-pod-design.md)
and its plan [`plans/2026-07-01-node-wide-mock-injection-every-pod.md`](plans/2026-07-01-node-wide-mock-injection-every-pod.md)

## Summary

Make the mock GPU environment **ambient on the node** — any pod scheduled on a
Kind node running the nvml-mock stack can run `nvidia-smi`, `ibnetdiscover`,
`ibstat`, `iblinkinfo`, etc. and believe a GPU is present — **without**
requesting `nvidia.com/gpu`, without annotations, and **without mutating the pod
spec**.

The injection point moves from the **API-server admission layer** (the mutating
admission webhook of the every-pod design) **down into containerd on the node**,
via an [NRI](https://github.com/containerd/nri) (Node Resource Interface)
plugin. A node-local daemon registers on the containerd NRI socket and, for
**every** container at creation time, appends the mock overlay mount and the
`PATH`/`LD_LIBRARY_PATH`/`LD_PRELOAD`/`MOCK_*` environment to the OCI runtime
spec — before `runc` starts the container. The pod spec is never touched.

This design **replaces** the 2026-07-01 every-pod mutating-webhook design. That
approach worked, but injecting at admission has costs this design removes: it
rewrites every pod spec, needs a TLS-served webhook with a managed `caBundle`,
has cluster-wide fail-open/fail-closed hazards, and is a standing API-server
dependency. Moving the same overlay contract into an NRI plugin makes injection
a pure node/runtime concern that is invisible above the node.

## The hard constraint (why "no injection" is impossible)

A pod's container runs in its own mount namespace over its own image rootfs.
Kind nodes are themselves containers, but pods do **not** inherit the node's
rootfs. Therefore the mock `nvidia-smi` binary and `libnvidia-ml.so` **must** be
placed into each container's mount namespace at creation time by *something*.
"Faking the node" does not remove the mounts — it removes the **pod-spec
mutation**. NRI relocates the identical set of mounts + env from the admission
layer to the runtime layer, where they belong.

| | Every-pod webhook (superseded) | NRI plugin (this design) |
|---|---|---|
| Injection point | API-server admission | containerd, at `CreateContainer` |
| Pod spec | Rewritten (volume + mounts + env + annotation) | Untouched |
| Transport / trust | HTTPS + managed `caBundle` + cert rotation | Local unix socket, no TLS |
| Failure blast radius | Cluster-wide (`failurePolicy`) | Node-local (plugin down → containers run un-injected) |
| API-server dependency | Standing `MutatingWebhookConfiguration` | None |
| musl/distroless handling | Guess at admission (opt-out annotation) | Inspect the resolved OCI spec on the node |
| Node config change | None | One `containerdConfigPatch` (enable NRI) |

## Goals

- Any pod on a mock node — no annotations, no GPU resource request, no pod-spec
  change — can run `nvidia-smi` and the IB discovery tools (`ibnetdiscover`,
  `ibstat`, `iblinkinfo`, `ibstatus`, `sminfo`) and see a mock GPU/IB fabric.
- No mutating admission webhook, no TLS cert lifecycle, no `caBundle`.
- The pod spec as authored is preserved verbatim (no injected volumes/env).
- Node-local failure semantics: a plugin outage never blocks scheduling and
  never blocks container creation.
- Single source of truth for host artifacts remains the DaemonSet / `setup.sh`.

## Non-Goals

- DCGM hostengine / dcgm-exporter enablement (follow-up).
- CUDA kernel execution fidelity (mock `libcuda.so` remains early-stage).
- Injecting real `/dev/nvidia*` device nodes into *every* container (opt-in
  only — requires elevated device access).
- MIG or SR-IOV VF simulation.
- Guaranteeing correctness inside musl/Alpine, distroless, or scratch images
  (handled via opt-out, not universal compatibility).
- Non-containerd runtimes. NRI is also supported by CRI-O, but Kind uses
  containerd; CRI-O parity is out of scope for now.

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

This is unchanged from the every-pod design — the overlay contract is
identical; only its carrier changes.

- **NVML / `nvidia-smi`:** the only piece that must physically exist on disk in
  the container. The mock `nvidia-smi` ELF has `RPATH=$ORIGIN/../lib64`, so once
  the driver tree is mounted it finds `libnvidia-ml.so.1` relative to itself.
- **InfiniBand tools:** work through the `LD_PRELOAD` shims plus env —
  `libibmocksys` redirects `/sys/class/infiniband*` reads to `$MOCK_IB_ROOT`,
  and `libibmockumad` routes UMAD ioctls to the DaemonSet's `mock-ib` daemon via
  a shared socket. No bind-mount over `/sys` is required.
- **PCI sysfs:** the `libpcimocksys` shim redirects `/sys/bus/pci/*` reads to
  `$MOCK_PCI_ROOT`, same pattern as the IB sysfs shim.

### What NRI is

NRI is a containerd/CRI-O extension point: node-local plugins connect to a
runtime-owned unix socket (default `/var/run/nri/nri.sock`) and receive
lifecycle events for pods and containers. On `CreateContainer`, a plugin may
return an **adjustment** to the OCI spec — additional mounts, environment
variables, devices, and resource limits — which the runtime merges before
handing the container to `runc`. NRI is enabled by default in containerd 2.x
(shipped in recent Kind node images) and available behind a config flag in
containerd 1.7+. It is the modern, supported replacement for bespoke OCI hooks
and runtime wrappers.

## Architecture

```
┌─────────────────────────────────────────────────────────────────────────┐
│ Kind Node (host filesystem)                                              │
│  nvml-mock DaemonSet (setup.sh)                                          │
│    → /var/lib/nvml-mock/driver/{usr/lib64,usr/bin,usr/local/lib,config}  │
│    → /var/lib/nvml-mock/{ib,sys,run/mock-ib.sock}                        │
│                                                                          │
│  nvml-mock-nri (DaemonSet, same host tree)                              │
│    → connects /var/run/nri/nri.sock                                     │
│    → CreateContainer: append overlay mount + PATH/LD_*/MOCK_* env        │
└───────────────────────────────┬───────────────────────────────────────┘
        containerd (enable NRI)  │ CreateContainer adjustment
                                 ▼
┌─────────────────────────────────────────────────────────────────────────┐
│ Every container on the node (pod spec unchanged)                         │
│   /opt/nvml-mock overlay mounted by the runtime                          │
│   PATH/LD_LIBRARY_PATH/LD_PRELOAD/MOCK_* set in the OCI spec             │
│   → nvidia-smi, ibnetdiscover, ibstat, ... work ambiently                │
└─────────────────────────────────────────────────────────────────────────┘
```

**Principle:** The DaemonSet owns host-side artifacts. The NRI plugin wires each
container to them at the runtime layer. Nothing above the node participates.

## Components

### 1. The overlay contract (unchanged)

For every container the plugin does not skip, it appends to the OCI spec:

**Mount (from host `/var/lib/nvml-mock` → container `/opt/nvml-mock`):**

```json
{
  "destination": "/opt/nvml-mock",
  "type": "bind",
  "source": "/var/lib/nvml-mock",
  "options": ["rbind", "ro", "nosuid", "nodev"]
}
```

A read-only bind does not prevent `connect(2)` to the `mock-ib` unix socket
under `run/` (connect is not a filesystem write, and the socket mode is
world-connectable). If a runtime rejects socket connect on an RO mount, the
fallback is a second `rw` bind of `/var/lib/nvml-mock/run` only.

**Env (merge onto the container's resolved OCI env):**

| Var | Value | Merge rule |
|-----|-------|------------|
| `PATH` | `/opt/nvml-mock/driver/usr/bin:<existing>` | prepend |
| `LD_LIBRARY_PATH` | `/opt/nvml-mock/driver/usr/lib64:<existing>` | prepend |
| `LD_PRELOAD` | shims (below) appended to `<existing>` | append |
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

**Env merge advantage over the webhook.** At the NRI layer the container's env
has already been **resolved by the runtime** — the plugin sees the effective
`PATH`/`LD_LIBRARY_PATH`/`LD_PRELOAD` (image `ENV` merged with pod-spec `env`),
not just the pod-spec subset. This eliminates the every-pod design's known
limitation where an image's Dockerfile `PATH` was invisible at admission and got
replaced by a conservative default. The NRI plugin prepends/appends to the real
value.

### 2. `nvml-mock-nri` plugin (`cmd/nvml-mock-nri`)

Go binary built on `github.com/containerd/nri/pkg/stub`, shipped in the existing
nvml-mock image (or a slim image). Responsibilities:

- Register with containerd over the NRI socket; implement `CreateContainer` to
  return a `ContainerAdjustment` carrying the overlay mount + env above.
- **Idempotency:** skip when the container already has an `/opt/nvml-mock`
  mount (handles restart/re-sync).
- **Opt-out:** skip when the pod carries `nvml-mock.nvidia.com/inject: "false"`.
  NRI delivers pod labels/annotations to the plugin, so opt-out remains a pod
  annotation with no pod-spec mutation.
- **Namespace exclusion:** skip pods in a configurable namespace set (the
  plugin's own namespace and `kube-system` excluded by default).
- **Device opt-in:** when the pod carries `nvml-mock.nvidia.com/devices:
  "true"`, additionally add `/dev/nvidia*` device entries from
  `/var/lib/nvml-mock/driver/dev` to the OCI spec (no webhook `privileged`
  rewrite needed — NRI adds device nodes to the spec directly).
- **musl/distroless guard:** the plugin can inspect the container's resolved
  root/args to skip images where glibc `LD_PRELOAD` would fail; falls back to
  the opt-out annotation for anything it cannot detect.

Configuration via flags/env (from Helm values): NRI socket path, overlay host
path + mount path, `LD_PRELOAD` shim list, IB/PCI enablement, opt-out annotation
key, excluded namespaces, plugin index/name.

### 3. containerd NRI enablement (Kind)

NRI is turned on with the same `containerdConfigPatches` mechanism the e2e
configs already use for `enable_cdi`:

```toml
[plugins."io.containerd.nri.v1.nri"]
  disable = false
  socket_path = "/var/run/nri/nri.sock"
```

On containerd 2.x (recent Kind images) NRI is on by default and this patch is a
no-op assertion; on 1.7.x it flips the flag. The plugin DaemonSet mounts the NRI
socket path as a hostPath. No `runtimeClassName`, no `enable_cdi`, no default
runtime change is required.

### 4. Helm chart additions

```yaml
nri:
  enabled: true
  socketPath: /var/run/nri/nri.sock
  pluginName: nvml-mock
  pluginIndex: "10"
  image: {}                     # defaults to the nvml-mock image
  overlay:
    hostPath: /var/lib/nvml-mock
    mountPath: /opt/nvml-mock
    ib: true
    pci: true
  optOutAnnotation: nvml-mock.nvidia.com/inject
  excludedNamespaces: []        # release namespace + kube-system always excluded
  resources: {}
```

New templates: `nri-daemonset.yaml`, `nri-rbac.yaml` (read pods for
label/namespace lookups if not carried by NRI directly). **Removed relative to
the every-pod design:** the injector Deployment, Service, `MutatingWebhook`
config, and TLS secret templates — none are needed.

### 5. Host-side changes (DaemonSet / `setup.sh`)

Same host-artifact prerequisites the every-pod overlay already introduced:

1. Expose IB tools + shims on the host driver root so the overlay mount surfaces
   them (`ibnetdiscover`, `ibstat`, `iblinkinfo`, `ibstatus`, `sminfo`, `ibping`
   into `$DRIVER_ROOT/usr/bin`; `libibmock*.so.*` and `libpcimocksys.so.*` into
   `$DRIVER_ROOT/usr/local/lib`).
2. Move the mock-ib socket to `/var/lib/nvml-mock/run/mock-ib.sock` so injected
   containers share the DaemonSet's `mock-ib` daemon.
3. Build `libpcimocksys.so` (PCI sysfs shim, `pkg/system/mockpcisysfs`) into the
   image and stage it on the host driver root.

These are carried over from the every-pod design and are independent of the
injection carrier.

## Data Flow

### Ambient discovery container (no annotations)

```
1. User creates a plain pod (e.g. ubuntu:22.04), no GPU request.
2. Scheduler places it; kubelet asks containerd to create the container.
3. containerd fires NRI CreateContainer to nvml-mock-nri.
4. Plugin: not opted out, not excluded, no /opt/nvml-mock mount yet → adjust.
5. Adjustment adds the overlay bind mount + PATH/LD_*/MOCK_* env to the OCI spec.
6. runc starts the container with the mock stack present.
7. nvidia-smi -L, ibstat, ibnetdiscover all succeed. Pod spec is unchanged.
```

### Device-opt-in container

```
1. Pod annotated nvml-mock.nvidia.com/devices: "true".
2. Plugin adds the overlay AND /dev/nvidia* device nodes to the OCI spec.
3. Container can open /dev/nvidia0 in addition to the user-space mocks.
```

### Opted-out / excluded container

```
1. Pod annotated nvml-mock.nvidia.com/inject: "false" (or in an excluded ns).
2. Plugin returns no adjustment; container runs exactly as authored.
```

## Error Handling

| Condition | Behavior |
|-----------|----------|
| NRI plugin down / not yet connected | containerd creates containers un-injected; scheduling and creation never blocked (node-local, fail-open by construction) |
| NRI disabled in containerd | Plugin cannot register; DaemonSet logs and retries; containers run un-injected |
| Host tree not yet populated | Bind mount of an empty tree succeeds; tools absent; missing `LD_PRELOAD` paths are harmless glibc warnings; container still runs |
| musl/Alpine/distroless image | glibc shims may fail to load → plugin skip heuristic or opt-out annotation |
| mock-ib socket not ready | IB tools fail at runtime; callers retry (existing E2E pattern) |
| Container already has overlay mount | Idempotency guard returns no adjustment |
| Device opt-in on a node without device nodes | Device add fails at create → container fails to start (expected: opt-in is explicit) |

## Testing Plan

| Test | Validates |
|------|-----------|
| Unit: adjust — plain container | Overlay mount + env added |
| Unit: adjust — opt-out annotation | No adjustment |
| Unit: adjust — excluded namespace / idempotency | No adjustment when mount present |
| Unit: adjust — `LD_PRELOAD`/`PATH` merge on resolved OCI env | Prepend/append preserves existing values |
| Unit: adjust — device opt-in | `/dev/nvidia*` device entries added |
| Integration: `libpcimocksys.so` | `/sys/bus/pci/devices/*` readlink rewrites to `$MOCK_PCI_ROOT` |
| Helm unittest | NRI DaemonSet renders; socket hostPath; disabled-by-flag; excluded namespaces |
| E2E: plain `ubuntu` pod, no annotations | `nvidia-smi -L`, `ibstat`, `ibnetdiscover` succeed; pod spec unchanged |
| E2E: opt-out pod | Unmodified (no `/opt/nvml-mock`) |
| E2E: device opt-in pod | `/dev/nvidia0` present |
| E2E: GPU Operator stack | Regression — CDI path still works |
| E2E: NRI enablement | Confirm NRI socket present in target Kind image; plugin registers |

## Implementation Order

1. Confirm NRI is enabled/available in the target Kind node image; add the
   `containerdConfigPatches` NRI block to the demo/e2e Kind configs.
2. Ensure host-side prerequisites exist (IB tools + shims + `libpcimocksys` on
   the host driver root; mock-ib socket on host path). Reuse the every-pod
   design's host changes if that work landed; otherwise implement here.
3. Implement `cmd/nvml-mock-nri` (NRI stub + `CreateContainer` adjustment +
   opt-out/exclusion/idempotency/device-opt-in) + unit tests.
4. Helm: NRI DaemonSet + RBAC + values; unittest. Remove/omit injector webhook
   templates.
5. E2E injection tests (plain pod, opt-out, device opt-in) + GPU Operator
   regression.
6. Docs: Helm README + quickstart "ambient node-wide mock (NRI)" section;
   update the demo walkthrough.
7. Mark the 2026-07-01 every-pod design + plan Superseded.

## Open Questions

| Question | Resolution / status |
|----------|---------------------|
| NRI availability in target Kind image | **To verify** — spike step 1 before committing |
| Does NRI deliver pod annotations to the plugin for opt-out? | Yes (pod metadata is in the NRI pod object); confirm in prototype |
| RO mount vs socket connect | Assume OK (connect is not a write); RW `run/` fallback documented |
| CRI-O parity | Out of scope; NRI API is runtime-agnostic if needed later |
| Interaction with the CDI `nvidia.yaml` path for GPU-requesting pods | Both can coexist; NRI adds the ambient overlay, CDI still handles `nvidia.com/gpu` device pods. Guard against double-injection via the idempotency mount check |

## References

- <https://github.com/containerd/nri> — NRI plugin API and `pkg/stub`
- `deployments/nvml-mock/scripts/setup.sh` — host setup and artifact layout
- `deployments/nvml-mock/helm/nvml-mock/templates/daemonset.yaml` — DaemonSet + current `LD_PRELOAD`
- `pkg/network/mockib/c/shim.c` — IB shim pattern; `pkg/system/mockpcisysfs` — PCI shim
- `tests/e2e/kind-gpu-operator-config.yaml` — existing `containerdConfigPatches` pattern to mirror for NRI
- `docs/designs/2026-07-01-node-wide-mock-injection-every-pod-design.md` — superseded webhook design
