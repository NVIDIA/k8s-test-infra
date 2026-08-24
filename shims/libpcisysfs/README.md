# libpcisysfs

`libpcisysfs.so` is an `LD_PRELOAD` shim that redirects PCI sysfs accesses to a fake filesystem tree.

It is useful for testing software that discovers or inspects PCI devices without exposing or depending on the host's real PCI topology.

## How it works

When `MOCK_PCI_ROOT` is set, accesses under:

```text
/sys/bus/pci
/sys/bus/pci/devices
/sys/devices/pci*
```

are rewritten by prepending `MOCK_PCI_ROOT`.

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
LD_PRELOAD=/path/to/libpcisysfs.so \
lspci
```

If `MOCK_PCI_ROOT` is unset, the library is a no-op.

## Scope

This is userspace libc interposition. It does not modify, mount, or virtualize the real `/sys` filesystem and only affects processes started with the library preloaded.
