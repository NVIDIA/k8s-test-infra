# Mock Driver Container

The mock driver container (`mock-driver`) lets the **real, unmodified GPU
Operator** manage a driver DaemonSet on nodes with no GPUs. It is substituted
for NVIDIA's driver image via standard Helm/ClusterPolicy values
(`driver.repository/image/version`) with `driver.enabled=true` -- the operator
renders the DaemonSet, runs its own k8s-driver-manager init container, gates
readiness with its own startup probe, and validates through its own
operator-managed driver branch. Nothing in the operator is patched or mocked;
only the image is.

**Status:** Early stage -- covers the phase-1 lifecycle (DaemonSet rollout,
startup-probe handshake, managed-branch validation). The driver upgrade
controller (`nvidia.com/gpu-driver-upgrade-state`) is a planned follow-up.

## Why

The `driver.enabled=false` baseline (see
[`tests/e2e/gpu-operator-values.yaml`](../tests/e2e/gpu-operator-values.yaml))
already turns the operator green, but the entire containerized-driver
lifecycle never executes there: DaemonSet rendering and image-tag resolution,
the k8s-driver-manager init flow, the startup-probe → `.driver-ctr-ready`
handshake, and the validator's operator-managed branch (the code path every
real cluster with a containerized driver uses). `mock-driver` exercises all of
that on a plain Kind cluster.

Faking one layer down -- making the real driver image succeed by mocking
`/proc`, `/sys`, or modprobe from outside -- is not possible for the pod that
matters (all verified empirically): runc rejects pod mounts anywhere under
`/proc`, regardless of privilege; mounts at `/sys/module/nvidia/*` fail
because creating mountpoints inside sysfs is impossible (only a
whole-directory shadow of `/sys/module` from a prepopulated volume works
mechanically -- and the operator-rendered driver DaemonSet has a fixed mount
set with no ClusterPolicy field to add volumes, so no such mount can reach
it); and module loading is a syscall into the kernel shared by every
container, so even an intercepted "successful" modprobe leaves the real
`/sys/module/nvidia` missing. Above all, the real entrypoint does not merely
check files -- it installs kernel headers, compiles, and loads the real
module, and its nvidia-smi issues real ioctls; faked preconditions cannot
make those actions succeed. The one genuine way to fake kernel state -- a
stub kernel module -- would also have to implement NVIDIA's proprietary ioctl
ABI for the real userspace stack to function, at the cost of per-kernel
builds and host-wide blast radius. The mock therefore replaces the driver
container at its thinnest stable interface, exactly as nvml-mock replaces
`libnvidia-ml.so`.

## The Contract

The GPU Operator imposes a contract on whatever image it deploys as the driver
DaemonSet. The relevant operator assets are vendored under
[`tests/e2e/contract/`](../tests/e2e/contract/) and drift-checked in CI by
[`tests/e2e/check-driver-contract.sh`](../tests/e2e/check-driver-contract.sh).

| Contract point | Imposed by | How mock-driver satisfies it |
|----------------|-----------|------------------------------|
| Command `nvidia-driver init` | DaemonSet template (fixed) | Entrypoint script named `nvidia-driver`, dispatches on subcommand |
| Startup probe: `/sys/module/nvidia/refcnt` exists | Operator-owned probe ConfigMap | tmpfs mounted over `/sys/module` in the container's own mount namespace |
| Startup probe: `nvidia-smi` exits 0 | Operator-owned probe ConfigMap | Real RPATH-patched nvidia-smi backed by mock NVML (same binaries as nvml-mock) |
| `.driver-ctr-ready` sentinel | Written by the probe, removed by the operator's preStop hook | Never touched by the mock -- the operator's own machinery runs |
| Driver rootfs at `/run/nvidia/driver` | Bidirectional hostPath `/run/nvidia` | `mount --rbind / /run/nvidia/driver`, same as the real entrypoint |
| Image tag `<version>-<osID><osVersionID>` | Operator tag resolution from NFD labels | CI computes the suffix from the node's `/etc/os-release`; registry users can digest-pin (`driver.version: "sha256:..."`) to bypass |
| k8s-driver-manager init container | Real image, deployed by the operator | Runs unmodified; a clean node has no host driver and no refcnt, so it exits 0 |
| Peermem sidecar subcommands | Only rendered when `driver.rdma` is enabled | Handled as no-ops anyway (defense in depth) |

The host's `/proc` and `/sys` are never modified -- all kernel-interface fakes
live in the driver container's own mount namespace, which is exactly where the
operator's probe execs.

## Usage

```bash
# 1. nvml-mock provides CDI specs, device nodes, and node labels -- but must
#    NOT own /run/nvidia/driver in this mode:
helm install nvml-mock deployments/nvml-mock/helm/nvml-mock \
  --set gpuOperator.driverSymlink.enabled=false

# 2. Build the mock-driver image (or pull ghcr.io/nvidia/mock-driver) and tag
#    it with the OS suffix the operator will compute:
docker build -t mock-driver:local -f deployments/mock-driver/Dockerfile .
OSTAG=$(docker exec <kind-node> sh -c '. /etc/os-release && echo "${ID}${VERSION_ID}"')
docker tag mock-driver:local "docker.io/library/mock-driver:local-$OSTAG"
kind load docker-image "docker.io/library/mock-driver:local-$OSTAG" --name <cluster>

# 3. Install the GPU Operator with a managed (mock) driver. The driver values
#    file is a DELTA layered on the driver-disabled baseline:
helm install gpu-operator nvidia/gpu-operator \
  -n gpu-operator --create-namespace \
  --version v26.3.3 \
  -f tests/e2e/gpu-operator-values.yaml \
  -f tests/e2e/go/assets/gpu-operator-driver-values.yaml \
  --set driver.version=local
```

GPU profile and count are passed to the driver container through ClusterPolicy
`driver.env` (`MOCK_GPU_PROFILE`, `MOCK_GPU_COUNT`, and optionally
`MOCK_KMOD` -- see below); the driver version string
is derived from the profile (overridable via `DRIVER_VERSION`, which is also
pinned into the NVML config) so the library filename, nvidia-smi output,
`/proc/driver/nvidia/version` content, and NVML answers always agree.

**Coupling:** `MOCK_GPU_PROFILE`/`MOCK_GPU_COUNT` must match nvml-mock's
`gpu.profile`/`gpu.count`. The device plugin advertises what the driver
root's NVML reports, but container device injection comes from nvml-mock's
CDI spec -- if the counts diverge, pods scheduled onto the extra GPUs fail at
container create with a missing CDI device.

## When to Use Which Mode

Two simpler alternatives exist when the driver DaemonSet itself is not
needed (both keep `driver.enabled=false`, no mock-driver image at all):

| Mode | Install | What you get |
|------|---------|--------------|
| Symlinked driver root (default) | nvml-mock + baseline overlay | Operator green via the `/run/nvidia/driver` symlink and validator env overrides |
| Host driver masquerade | nvml-mock with `hostDriver.enabled=true` + baseline + hostdriver overlays | nvidia-smi/libs at standard host paths; validator host branch (`IS_HOST_DRIVER=true`); zero env overrides; node-level consumers (slurmd GRES `AutoDetect=nvml`) work unconfigured |
| Managed mock driver (this doc) | nvml-mock (symlink off) + mock-driver image + baseline + driver overlays | Full driver DaemonSet lifecycle |

The host masquerade and the managed mock driver are mutually exclusive per
node: a working `nvidia-smi` at the node's `/usr/bin` flips
k8s-driver-manager's preinstalled-driver detection and unschedules the
driver DaemonSet.

## Division of Labor with nvml-mock

| Concern | Owner |
|---------|-------|
| CDI spec (`/var/run/cdi/nvidia.yaml`), host device nodes, `nvidia.com/gpu.present` + PCI labels | nvml-mock DaemonSet (unchanged) |
| `/run/nvidia/driver` rootfs, `/sys/module/nvidia` + `/proc/driver/nvidia` (container-local), driver pod lifecycle | mock-driver via the operator |
| Readiness sentinels (`/run/nvidia/validations/*`) | GPU Operator's own probe and validator |

The two modes are mutually exclusive per node: `gpuOperator.driverSymlink.enabled`
must be `false` whenever `driver.enabled=true`, because the operator's
`DirectoryOrCreate` hostPath for `/run/nvidia/driver/lib/firmware` and the
driver pod's rbind both conflict with nvml-mock's symlink.

## Non-Goals

- **Default mode never loads a kernel module** -- with `MOCK_KMOD=off` (the
  default) nothing is loaded, built, or modprobe'd. See the opt-in
  [MOCK_KMOD](#optional-real-kernel-global-proc-and-sys-mock_kmod) section
  below for the disposable-Kind-only path that loads a prebuilt stub.
- **No DCGM / MIG / GPUDirect (GDS, GDRCopy, peermem)** -- these require the
  real driver stack and stay disabled in the values overlay.
- **No toolkit testing** -- the container toolkit remains disabled; mock libs
  reach consumers via CDI.
- **No real compute** -- CUDA kernels are no-ops (see [CUDA Mock](cuda-mock.md)).
- **No real compute on any architecture** -- images are published for amd64
  and arm64 (the nvidia-smi ELF comes from NVIDIA's x86_64/sbsa repos), but
  CUDA kernels remain no-ops everywhere.

A green `e2e-gpu-operator-driver` CI run means the operator's driver
*lifecycle* works -- it does not mean driver *functionality* (DCGM, MIG,
upgrades) is covered. See the vendored contract for the exact surface tested.

## Optional Real Kernel-Global /proc and /sys (MOCK_KMOD)

**Disposable Kind nodes only.** The stub module is node-global state that
outlives the pod, and k8s-driver-manager (v0.11.0+) will `rmmod nvidia`
whenever it detects a module without a matching `/run/nvidia/nvidia-driver.state`
digest; do not enable this on any node you cannot recreate.

By default the kernel interfaces are faked only inside the driver container's
mount namespace -- enough for the operator's own gating, but checks that read
the NODE's real `/proc/driver/nvidia` or `/sys/module/nvidia` (or a plain
pod's own `/proc`/`/sys`) fail, because on real clusters those entries exist
only as a side effect of a genuine module load.

`MOCK_KMOD=on` closes that gap the one legitimate way: a ~70-line stub kernel
module named `nvidia` (`deployments/mock-driver/kmod/`). Loading any module
with that name makes the kernel itself create `/sys/module/nvidia` (refcnt,
version); the stub additionally serves `/proc/driver/nvidia/{version,params}`
in NVRM format. It registers no devices and touches no hardware. Because the
kernel is shared, the entries appear on the node and in every pod -- exactly
like a real driver.

| Value | Behavior |
|-------|----------|
| `off` (default) | Namespace fakes only; no module ever loaded |
| `on` | Load the PREBUILT stub from `/run/nvidia/mock-kmod/nvidia.ko`; fail the pod if it is missing, mis-named, or version-mismatched |

What each mode makes pass, by where the check reads `/proc/driver/nvidia`
or `/sys/module/nvidia`:

| Where the check runs | `off` (namespace fakes) | `on` (stub module) |
|----------------------|:-----------------------:|:------------------:|
| Inside the driver container (operator probe, `kubectl exec`) | pass | pass |
| Through the driver root (`/run/nvidia/driver/...`) | pass (`/proc`) | pass |
| Node's real `/proc`//`/sys` (host, node-problem-detector) | fail | pass |
| An ordinary pod's own `/proc`//`/sys` | fail | pass |

**Prebuilt only.** The entrypoint never installs kernel headers or a
compiler. Build the module host-side (`make -C /lib/modules/$(uname -r)/build
M=$PWD/deployments/mock-driver/kmod modules` after generating
`stub_version.h` with the profile driver version), stage it into the Kind
node under `/run/nvidia/mock-kmod/nvidia.ko`, and then start the mock-driver
pod. The Go E2E harness's `gpu-operator-driver` + `mock-kmod` label does
exactly this -- see `tests/e2e/go/` for the reproducible recipe.

**Lifecycle:** the module is node-global and persists across a *graceful*
driver-pod restart (the entrypoint never `rmmod`s it, matching real-driver
semantics; the namespace fakes below self-skip when the real entries exist).
However, k8s-driver-manager runs an `uninstall_driver` init container on
EVERY new driver pod; when it sees a resident `nvidia` module without a
matching state file, it unloads it before the mock's main container starts.
As a result, MOCK_KMOD is not suitable for any node where a real driver may
be reinstalled, or where the operator manages driver upgrades. Use it for a
single cluster lifecycle on a disposable node.

Changing `DRIVER_VERSION` or profile requires recreating the node (the same
constraint a real driver upgrade imposes). A prebuilt module bakes its
version at compile time from the profile's `driver_version`; the load helper
rejects a module whose recorded version disagrees with the pod's
`DRIVER_VERSION`.

**Scope:** kmod mode provides the real `/sys/module/nvidia` (the entry the
operator probe actually checks) plus `/proc/driver/nvidia`. The cosmetic
`/sys/module/nvidia_uvm` and `/sys/module/nvidia_modeset` entries that the
namespace-fake path fabricates are not created in kmod mode (nothing in the
stack reads them); `/sys/module/nvidia` is what matters.

## Teardown Behavior

Deleting the driver pod removes `.driver-ctr-ready` (operator preStop) and
lazily unmounts the driver root, but a detached mount tree can linger at
`/run/nvidia/driver` on the node until the next mount consumer -- the same
residue the real driver container leaves, and the reason k8s-driver-manager
recursively unmounts stale driver roots on startup. The mock entrypoint does
the same: on (re)start it detects a stale mountpoint, unmounts it, and
remounts fresh (verified: restart-over-stale-mount recovers cleanly).

## Versioning

The image satisfies the driver-container contract of the GPU Operator version
pinned in the Go E2E harness's managed-driver scenario
(`tests/e2e/go/`) and vendored under `tests/e2e/contract/<version>/`. The
contract has changed between operator releases before (v25.x used an inline
`nvidia-smi && touch` probe; v26.x requires `/sys/module/nvidia/refcnt`), so
bump the pin, the vendored assets, and the entrypoint together.

GPU profiles are baked into the image at build time (from the nvml-mock
chart's `profiles/` directory); published images are rebuilt whenever the
profiles change, but a *pinned* older image can carry profiles that differ
from a newer chart's. When profile agreement matters, build the image from
the same commit as the chart (what CI does) or pin matching versions.
