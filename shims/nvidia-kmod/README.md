# nvidia-kmod

Loadable kernel modules that impersonate the NVIDIA driver's module family:
`nvidia`, `nvidia_uvm`, `nvidia_modeset`, and `nvidia_peermem`. They drive no
hardware and move no data. Loading them is the entire behaviour.

**Status: prototype.** Nothing in Mokka loads these — not the node daemon, not
the Helm chart. They are built and exercised by their own test.

## Why a kernel module and not a shim

Mokka's other simulation is userspace: rendered file trees and `LD_PRELOAD`
interposition. The consumers that decide whether a GPU driver is installed sit
below that line. `k8s-driver-manager` reads `/sys/module/nvidia/refcnt` and
calls `delete_module(2)`; it is a Go binary issuing raw syscalls, so there is no
libc symbol for `dlsym(RTLD_NEXT, …)` to interpose on. The GPU Operator's
driver container and operator-validator gate on the same file.

`/sys/module/nvidia/refcnt` alone governs five code paths across four binaries —
see [`specs/nvidia-driver-run-state.md`](../../specs/nvidia-driver-run-state.md)
§4.4 and [MEP-0004](../../enhancements/meps/0004-gpu-driver-upgrade-simulation/README.md).

A loaded module makes the kernel answer all of it directly:

| Surface | Consumer |
|---|---|
| `/sys/module/nvidia/{refcnt,holders,initstate,version}` | `k8s-driver-manager`'s `isDriverLoaded`, the driver container's `_should_skip_kernel_module_reload`, its startup probe |
| `/proc/modules` refcount and "Used by" columns | `lsmod`, unload diagnostics |
| `delete_module(2)` failing with `EWOULDBLOCK` while a dependent is loaded | `k8s-driver-manager`'s teardown, which unloads in dependency order because of it |

None of that is implemented here. The dependents reference an exported symbol
from `nvidia.ko`, and the kernel derives the rest.

## What the modules add on top

- **Character device majors.** `nvidia.ko` registers major 195 and
  `nvidia_uvm.ko` registers 510 — the majors `charDevsForDevices` in
  [`internal/agent/gpudriver/stage.go`](../../internal/agent/gpudriver/stage.go)
  already `mknod`s. Until something registers them, those nodes cannot be opened
  at all: `open()` returns `ENXIO`. Registration also puts `nvidia` and
  `nvidia-uvm` in `/proc/devices`, where `nvidia-container-cli` looks up the uvm
  major.
- **`/proc/driver/nvidia/`.** `version`, `params`, `registry`, and
  `gpus/<bdf>/information`. The text matches what `gpudriver.writeProcFS` stages
  into the node overlay, so the two agree while both exist. The module's copy
  appears at the real path, needs no mount, and disappears when the driver is
  unloaded.
- **`NVreg_*` module parameters**, taken from the list `writeProcFS` renders.

## Building

Needs a kernel build tree at `/lib/modules/$(uname -r)/build` — the
`linux-headers-$(uname -r)` or `kernel-devel` package for the running kernel.

```sh
make -C shims/nvidia-kmod            # or: make nvidia-kmod
sudo make -C shims/nvidia-kmod test  # or: make test-nvidia-kmod
```

Everything the build produces goes to `dist/`. Kbuild writes its artifacts into
whatever directory `M` names and has no output-directory flag portable across
kernel versions, so the Makefile points `M` at `dist/` and links the sources in.

The driver version is a build-time constant, as it is upstream — NVIDIA selects
a version by choosing an image tag, and the version rides inside the build:

```sh
make -C shims/nvidia-kmod NVRM_VERSION=580.65.06
```

It reaches `MODULE_VERSION` and the `/proc/driver/nvidia/version` banner from
that one place, so simulating an upgrade means loading a differently-built
module — which is what a driver upgrade is.

### On macOS

Neither OrbStack nor Docker Desktop can host these. Their kernels ship no
`/lib/modules/<version>/build`, so no out-of-tree module compiles against them.
Use a VM with a stock distro kernel:

```sh
brew install colima
colima start --vm-type vz --mount-type virtiofs --cpu 4 --memory 6
colima ssh -- sudo apt-get install -y build-essential "linux-headers-$(uname -r)"
```

Colima mounts `$HOME` read-write at the same path, so the repository is already
visible inside the VM. The VM has no Go toolchain, so cross-compile the test
binary on the workstation and run it there:

```sh
make -C shims/nvidia-kmod test-binary
colima ssh -- sudo sh -c 'cd /path/to/mokka2/shims/nvidia-kmod && make && ./dist/kmod.test -test.v'
```

Note that `colima start` switches the active Docker context. Run
`docker context use orbstack` afterwards if the rest of your workflow expects it.

## Constraints

**One kernel, one driver.** Modules are not namespaced. Every kind node on a
host shares one `/sys/module/nvidia`, one `/proc/driver/nvidia`, and one
registered major, so per-node driver lifecycle cannot diverge — a simulated
upgrade is all-nodes-at-once. `local/kind/default.kind.yaml` records the same
property for `/dev/kmsg`.

**The build is tied to one kernel.** A `.ko` loads only into the kernel it was
built against, matching `vermagic` and symbol CRCs. This is the real driver's
own problem, solved either by building on the node against
`/lib/modules/$(uname -r)/build` or by shipping precompiled per-kernel objects.
Neither is implemented here.

**The uvm major is fixed at 510**, where the real driver takes a dynamically
allocated one. `charDevsForDevices` already `mknod`s 510, and a dynamic major
would leave the registration and the device nodes pointing at different numbers.

**Some `gpus/<bdf>/information` fields are fixed.** Model, UUID, bus location
and device minor come from the `mokka_gpus` parameter. IRQ, VBIOS version, bus
type and DMA sizing are plausible constants, because nothing on the node knows a
simulated GPU's interrupt line. The field set is the real file's, since a
consumer looks fields up by name.

**GPU records cannot contain spaces.** `mokka_gpus` is a module parameter and
`finit_module` takes a space-separated parameter string, so a model name like
`Tesla T4` needs a different transport. A control interface the node daemon
writes would lift this; module parameters were chosen because driver identity
arriving at load time is what the real driver does.

**`writeProcFS` hardcodes `x86_64`** in its NVRM banner. The module reads the
architecture from the running kernel, so the two differ on arm64 nodes. The Go
side is the one that is wrong.

**`lsmod` shows these as `(OE)`, where a real driver shows `(POE)`.** Out-of-tree
and unsigned are inherent to any module built this way; the missing `P` is the
proprietary taint, which the real `nvidia.ko` carries and this one does not.
Anything keying off the taint flags can tell the two apart.

## Licensing

The sources carry `SPDX-License-Identifier: MIT OR GPL-2.0-only` and declare
`MODULE_LICENSE("Dual MIT/GPL")`, which is what NVIDIA's open kernel modules
declare. `MODULE_LICENSE` has to name something the kernel recognises as
GPLv2-compatible, or the module is refused access to `EXPORT_SYMBOL_GPL` symbols
and taints the kernel as proprietary. This repository's Apache-2.0 licence is
neither recognised nor GPLv2-compatible.

Adding `LICENSES/MIT.txt`, `LICENSES/GPL-2.0-only.txt` and the matching
`REUSE.toml` annotation is left for sign-off, since it changes the repository's
licence inventory.
