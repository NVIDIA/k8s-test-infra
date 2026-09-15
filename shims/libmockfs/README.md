# libmockfs

`libmockfs.so` is an `LD_PRELOAD` shim that redirects PCI sysfs and kernel-module accesses to a fake filesystem tree.

Use it to test software that finds PCI devices or checks whether a kernel module is loaded. That software then needs neither the real PCI topology nor the real module set.

One shim serves both surfaces. Both render under the same root, and the redirect is a single prefix table. A second shim would repeat this libc interposition to add two entries.

The root variable keeps the name `MOCK_PCI_ROOT`. The NRI plugin sets it in every workload container it serves, and the chart documentation names it, so it is a container-visible contract. Renaming it is a separate change.

## How it works

When `MOCK_PCI_ROOT` is set, accesses under:

```text
/sys/bus/pci
/sys/bus/pci/devices
/sys/devices/pci*
/sys/module
/proc/modules
```

are rewritten by prepending `MOCK_PCI_ROOT`.

The shim redirects `/proc/modules` instead of a mount. runc refuses any bind mount inside `/proc` that is not on its allowlist. Therefore `lsmod` depends on this shim.

For example:

```text
/sys/bus/pci/devices/0000:07:00.0/config
```

with:

```bash
MOCK_PCI_ROOT=/tmp/mock-pci
```

becomes:

```text
/tmp/mock-pci/sys/bus/pci/devices/0000:07:00.0/config
```

All unrelated paths pass through unchanged.

The library intercepts common libc filesystem APIs including:

* `open`, `open64`, `openat`, `openat64`
* fortified `__open*_2` variants
* `fopen`, `fopen64`
* `opendir`
* `stat`, `lstat`, `fstatat`, `statx`
* `access`, `faccessat`
* `readlink`, `readlinkat`

The real libc implementations are called through `dlsym(RTLD_NEXT, ...)`.

## Usage

Create a fake tree that mirrors the expected PCI sysfs layout:

```text
/tmp/mock-pci/
└── sys/
    └── bus/
        └── pci/
            └── devices/
                └── 0000:07:00.0/
                    ├── config
                    ├── resource
                    └── vendor
```

Then run the target process with the shim preloaded:

```bash
MOCK_PCI_ROOT=/tmp/mock-pci \
LD_PRELOAD=/path/to/libmockfs.so \
lspci
```

If `MOCK_PCI_ROOT` is unset, the library is a no-op.

## Scope

This is userspace libc interposition. It does not modify, mount, or virtualize the real `/sys` filesystem and only affects processes started with the library preloaded.
