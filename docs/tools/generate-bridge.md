# generate-bridge

Keeps the mock NVML library ABI-complete. Every NVML function the hand-written
bridge does not implement gets a generated cgo stub, so the library still
exports the full symbol set and a consumer that calls an unimplemented function
gets a defined answer instead of a link error.

It reads the function list from go-nvml's `nvml.go`, the C prototypes from
`nvml.h`, and the existing `//export` directives from the bridge directory. What
is left over — every function named in `nvml.go` with no hand-written
implementation — is written to `stubs_generated.go`.

A hand-written implementation therefore always displaces its stub. You never
delete one by hand; you add the real function and re-run the generator.

## Running it

```bash
make gen
```

That is the supported path: it resolves go-nvml through the module cache and
runs the `//go:generate` directive in `pkg/gpu/mocknvml/bridge/helpers.go`.

The read-only modes are not reachable through `make gen`, so invoke the binary
directly for those:

```bash
GO_NVML_DIR=$(go list -m -f '{{.Dir}}' github.com/NVIDIA/go-nvml)

go run ./cmd/generate-bridge -stats    -input  "$GO_NVML_DIR/pkg/nvml/nvml.go"
go run ./cmd/generate-bridge -validate -header "$GO_NVML_DIR/pkg/nvml/nvml.h"
```

## Modes

| Mode | Requires | Behaviour |
|---|---|---|
| generate (default) | `-input` and `-header` | Writes `-output`, unconditionally |
| `-stats` | `-input` | Prints an NVML coverage table — total functions, hand-written implementations, generated stubs, and a per-file `//export` count — then exits `0` |
| `-validate` | `-header` | Compares each hand-written export's Go parameter count against its `nvml.h` prototype. Exits `1` after printing one `WARNING: <file>:<line>` per mismatch, or `0` |

`-stats` wins when both read-only flags are set. A required path that is missing
or unreadable exits `1` before any work happens, with a message pointing at
`make gen`.

## Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-input` | none | NVML Go wrapper file (`nvml.go`) |
| `-header` | none | NVML C header, for prototype extraction |
| `-bridge` | `pkg/gpu/mocknvml/bridge` | Directory scanned for existing implementations |
| `-output` | `pkg/gpu/mocknvml/bridge/stubs_generated.go` | Where generated stubs are written |
| `-stats` | `false` | Print the coverage table and exit |
| `-validate` | `false` | Check export parameter counts against `nvml.h` |

## Why the output is committed

`stubs_generated.go` is checked in, and `make gen-check` re-runs generation and
fails when the bridge directory comes back dirty.

The guard earns its place because the trigger is invisible: bumping go-nvml adds
new NVML entry points but does not regenerate anything, so without it the mock
library quietly stops exporting the new symbols and only a consumer calling one
would notice.

## See also

- [Command-line tools](README.md)
- [Libraries and Shims](../components/libraries-and-shims.md) — how the bridge
  fits under the mock NVML library
- [Local Development](../contributing/local-development.md)
