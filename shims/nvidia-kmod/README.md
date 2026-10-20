# nvidia-kmod

Loadable kernel modules that impersonate the NVIDIA driver's module family:
`nvidia`, `nvidia_uvm`, `nvidia_modeset`, and `nvidia_peermem`. They drive no
hardware and move no data. Beyond being loaded, they answer the handful of
ioctls a client enumerates GPUs with.

A loader swaps them in wherever the driver is loaded with `modprobe`. That is
how the GPU Operator's own driver container, from the original NVIDIA image,
runs on a CPU node.

**Status: alpha**, behind the `GPUOperatorDriver` feature gate. With it on, the
node agent stages the modules and the NRI plugin hands them to the GPU
Operator's driver container, as described in
[Delivery to the GPU Operator](#delivery-to-the-gpu-operator).

## Why a kernel module and not a shim

Mokka's other simulation is userspace: rendered file trees and `LD_PRELOAD`
interposition. The consumers that decide whether a GPU driver is installed sit
below that line. `k8s-driver-manager` reads `/sys/module/nvidia/refcnt` and
calls `delete_module(2)`; it is a Go binary issuing raw syscalls, so there is no
libc symbol for `dlsym(RTLD_NEXT, …)` to interpose on. The same file gates the
GPU Operator's driver container (its fast path, its unload, and its startup
probe) and the operator-validator's device-node step.

A loaded module makes the kernel answer all of it directly:

| Surface | Consumer |
|---|---|
| `/sys/module/nvidia/{refcnt,holders,initstate,version}` | `k8s-driver-manager`'s `isDriverLoaded`, the driver container's `_should_skip_kernel_module_reload` and `_unload_driver`, its startup probe |
| `/proc/modules` refcount and "Used by" columns | `lsmod`, unload diagnostics |
| `delete_module(2)` failing with `EWOULDBLOCK` while a dependent is loaded | `k8s-driver-manager`'s teardown, which unloads in dependency order because of it |

None of that is implemented here. The dependents reference an exported symbol
from `nvidia.ko`, and the kernel derives the rest.

## What the modules add on top

- **Character device majors.** `nvidia.ko` registers major 195 and
  `nvidia_uvm.ko` registers 510 — the majors `charDevsForDevices` in
  [`internal/agent/nvml/stage.go`](../../internal/agent/nvml/stage.go)
  already `mknod`s. Until something registers them, those nodes cannot be opened
  at all: `open()` returns `ENXIO`. Registration also puts `nvidia` and
  `nvidia-uvm` in `/proc/devices`, where `nvidia-container-cli` and the driver
  libraries look up the uvm major. Opening a GPU minor that no record declares
  fails with `ENODEV`, as on a real node.
- **`/proc/driver/nvidia/`.** `version`, `params`, `registry`, and
  `gpus/<bdf>/information`. `params` and `information` are byte-for-byte what
  `gpudriver.writeProcFS` stages into the node overlay, so the two agree while
  both exist. The module's copy appears at the real path, needs no mount, and
  disappears when the driver is unloaded.
- **`NVreg_*` module parameters**, taken from the list `writeProcFS` renders,
  plus `NVreg_CoherentGPUMemoryMode`, which the GPU Operator's driver container
  passes on every load.
- **The interface-layer ioctls**, described next.

GPU identity comes from the `mokka_gpus` parameter: comma-separated
`<bdf>|<uuid>|<model>|<minor>[|<pci-device-id>]` records, the device ID in hex.
A record with an unparseable PCI location or minor fails the load.

## ioctls

The real driver answers a few ioctls in its kernel interface layer, before a
request reaches the Resource Manager (RM). This module answers the same ones,
on `/dev/nvidiactl` (minor 255) or a GPU node:

| Escape | Node | Answer |
|---|---|---|
| `CHECK_VERSION_STR` | nvidiactl | Recognized when the client's version equals the module's; otherwise `EINVAL` with the module's version in the reply, as upstream |
| `CARD_INFO` | nvidiactl | One entry per record: PCI location, vendor `0x10de`, device ID, minor, and RM's GPU ID |
| `ATTACH_GPUS_TO_FD` | nvidiactl | Accepts the GPU IDs card info reported, refuses any other |
| `SYS_PARAMS` | nvidiactl | Keeps the NUMA memory block size the first caller sets |
| `REGISTER_FD`, `WAIT_OPEN_COMPLETE` | any | Succeed: there is no RM client to link, and every open completes synchronously |
| `NUMA_INFO` | GPU | NUMA disabled: there is no GPU memory to online |
| Anything else, every RM escape included | — | `ENOTTY`, logged with the calling process |

That covers the version handshake and card enumeration, but not yet the
clients the GPU Operator's driver container runs on top of the modules.
`nvidia-smi` and every other NVML client allocate an RM client
(`NV_ESC_RM_ALLOC`, escape `0x2b`) before anything else, and so does
`libnvidia-cfg`, through which `nvidia-persistenced` lists GPUs. Mokka serves
NVML clients elsewhere from its userspace shims. An escape the module does not
answer shows up in the kernel log as unsimulated, with the calling process,
which is how these were found.

Argument sizes are validated the way upstream validates them, so a layout
mismatch fails with `EINVAL` instead of corrupting a client's memory. The
layouts live in [`src/nv_ioctl_abi.h`](src/nv_ioctl_abi.h), asserted against
sizes measured from NVIDIA's open-gpu-kernel-modules headers.

## The loader

The node agent stages one `install` directive per module, rendered by
`ModprobeConf` in [`sources.go`](sources.go). `modprobe` runs an install
command instead of inserting the module, passing the options from
configuration and the command line as `$CMDLINE_OPTS`. Each command runs
[`load`](load), which:

1. Takes the driver version from the real module this load replaces
   (`modinfo -F version nvidia`), or from `DRIVER_VERSION` when no real module
   is installed. Every client's version handshake depends on this match.
2. Compiles the modules once per kernel and version against
   `/lib/modules/$(uname -r)/build`, into `MOKKA_KMOD_BUILD_DIR` (default
   `/var/cache/mokka/nvidia-kmod`) rather than beside the sources, which are
   mounted read-only. The driver container installs exactly those headers
   before it loads anything.
3. Inserts the module with the options `modprobe` passed, adding `mokka_gpus`
   when loading `nvidia`: from `MOKKA_GPUS` if set, otherwise from the `gpus`
   file the node agent stages beside the loader.

`rmmod` needs nothing: it removes modules by name.

## Delivery to the GPU Operator

The driver image stays NVIDIA's own. Mokka adapts the container at creation
instead, and only while the `GPUOperatorDriver` feature gate is on:

1. The node agent's `nvidiakmod` simulator stages the sources, the loader and a
   stand-in for `nvidia-persistenced` (all embedded in its binary), the install
   directives, a dynamic linker directive, and a `gpus` file rendered from the
   node's simulated GPUs, under `<overlay>/nvidia-kmod`.
2. The NRI plugin recognizes the installer by its pod's
   `app.kubernetes.io/component=nvidia-driver` label and its `nvidia-driver-ctr`
   container name, which the operator uses for both `ClusterPolicy` and
   `NVIDIADriver`. It binds the staged tree read-only at
   `/opt/mokka/nvidia-kmod`, puts the tree's `stand-ins` first on the
   container's `PATH`, and binds the directives at
   `/etc/modprobe.d/mokka-nvidia.conf`.
   When nothing is staged it mounts nothing, so the container fails its real
   module load in its own log rather than failing to start.
3. The same adjustment puts the mock NVML in the container's root: the library
   at `/usr/lib64/libnvidia-ml.so.1`, the linker directive at
   `/etc/ld.so.conf.d/000-mokka-nvml.conf`, the node's profile at `/config`,
   and the GPU device nodes.

The container then runs its usual installation: it installs headers for the
node's kernel, compiles and installs the real driver, and runs `modprobe
nvidia`, which now builds and loads these modules. The `ldconfig` the installer
ends with reads the linker directive and ranks the mock ahead of the NVML it
has just installed. The container exposes its root to the other operands at
`/run/nvidia/driver`, and each of them reaches the mock there: the validator's
`chroot /run/nvidia/driver nvidia-smi` through the root's loader cache, and the
device plugin through `/usr/lib64`, the first directory it searches.

Two behaviours of the mock keep the operator's own bookkeeping right:

- It reports the loaded module's version, which is that of the installed
  driver, rather than the profile's. The device plugin's CDI spec names that
  version's libraries, and a driver upgrade changes it.
- Like real NVML, it holds `/dev/nvidiactl` open while initialized, so a running
  client keeps the driver in use. The old driver container then cannot unload
  the driver when it stops, and `k8s-driver-manager`, the pod's init container,
  evicts the operands and unmounts the old root before it reschedules them,
  unloading the driver first unless the new pod can keep it. A driver container
  cannot fully unmount its own root, so without that order an operand could
  start against a root that is already gone.

The [Local Development](../../docs/contributing/local-development.md) guide runs
the whole scenario with `--gpu-operator-driver`.

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

It reaches `MODULE_VERSION`, the `/proc/driver/nvidia/version` banner and the
version handshake from that one place, so simulating an upgrade means loading a
differently-built module — which is what a driver upgrade is.

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
built against, matching `vermagic` and symbol CRCs. The loader builds on the
node for that reason. Precompiled driver images (`driver.usePrecompiled`) install
no headers, so the loader cannot build in them.

**RM is not simulated.** Clients that only enumerate GPUs work. Anything that
allocates RM objects or issues control calls — `nvidia-smi`, NVML, CUDA, the
fabric manager — fails against the module. Under the GPU Operator, NVML clients
load the mock instead, as [Delivery](#delivery-to-the-gpu-operator) describes,
and the driver container starts a stand-in for `nvidia-persistenced`
([`stand-ins/nvidia-persistenced`](stand-ins/nvidia-persistenced)), first on
its `PATH`.
The real daemon reaches RM through `libnvidia-cfg`, and its failure would abort
the userspace-only install the container runs over a driver it keeps loaded.

**A running driver container keeps the mock NVML it started with.** The
library, the linker directive and the install directives are single-file binds,
which hold on to the file staged when the container was created. The node agent
replaces files rather than rewriting them, so an agent upgrade reaches a driver
container only once that container is recreated.

**Only Ubuntu driver images are exercised.** The mock's bind sits at
`/usr/lib64/libnvidia-ml.so.1`, where RHEL-family images install NVML
themselves.

**OpenShift's Driver Toolkit path is not covered.** It copies the files that
`modprobe --show-depends` names, and for an install directive that is a command
line, not a module.

**The uvm major is fixed at 510**, where the real driver takes a dynamically
allocated one. `charDevsForDevices` already `mknod`s 510, and a dynamic major
would leave the registration and the device nodes pointing at different numbers.

**`writeProcFS` hardcodes `x86_64`** in its NVRM banner. The module reads the
architecture from the running kernel, so the two differ on arm64 nodes. The Go
side is the one that is wrong.

**`lsmod` shows these as `(OE)`, where a real driver shows `(POE)`.** Out-of-tree
and unsigned are inherent to any module built this way; the missing `P` is the
proprietary taint, which the real `nvidia.ko` carries and this one does not.
Anything keying off the taint flags can tell the two apart. Unsigned also means
a host enforcing module signatures, such as one booted with Secure Boot, refuses
to load them.

## Licensing

The C sources carry `SPDX-License-Identifier: MIT OR GPL-2.0-only` and declare
`MODULE_LICENSE("Dual MIT/GPL")`, which is what NVIDIA's open kernel modules
declare. `MODULE_LICENSE` has to name something the kernel recognises as
GPLv2-compatible, or the module is refused access to `EXPORT_SYMBOL_GPL` symbols
and taints the kernel as proprietary. This repository's Apache-2.0 licence is
neither recognised nor GPLv2-compatible.

Adding `LICENSES/MIT.txt`, `LICENSES/GPL-2.0-only.txt` and the matching
`REUSE.toml` annotation is left for sign-off, since it changes the repository's
licence inventory.
