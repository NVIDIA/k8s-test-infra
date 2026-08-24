# libpcisysfs

LD_PRELOAD shim that redirects `/sys/bus/pci/...` and `/sys/devices/pci...` lookups to `$MOCK_PCI_ROOT`, making `lspci` 
and topology-aware schedulers enumerate mock GPU BDFs from a rendered sysfs tree instead of the real one.

## Build

```sh
make
```

Produces `libpcisysfs.so.1.0.0` with `.so.1` and `.so` symlinks.

## Use

```sh
MOCK_PCI_ROOT=/var/lib/nvml-mock LD_PRELOAD=/usr/local/lib/libpcisysfs.so.1 lspci
```

Set `MOCK_PCI_ROOT` to the root passed to `render.Render` (the node agent writes the tree there). Unset or empty disables all redirection.

## Intercepted syscalls

`open`/`open64`/`openat`/`openat64` (including `_FORTIFY_SOURCE` `__open_2` variants), `fopen`/`fopen64`, `opendir`, `stat`/`lstat`/`fstatat`/`statx`, `access`/`faccessat`, `readlink`/`readlinkat`.

## Test

Integration tests require Linux and the shim to be built:

```sh
make
go test -tags integration -v .
```
