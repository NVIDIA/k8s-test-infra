# generate-bridge

Emits ABI-compatible cgo stubs for every NVML function the hand-written mock
NVML bridge does not implement yet. It takes three inputs:

1. the vendored go-nvml wrapper (`nvml.go`), AST-parsed for the authoritative
   list of `nvml*` function names;
2. `nvml.h`, scanned for the real C prototypes, including multi-line
   declarations;
3. the bridge directory, walked for existing `//export` directives.

`stubs_generated.go` is skipped during that walk, so the generator does not
count its own previous output. Each remaining function gets a stub with a
C-compatible Go signature derived from the prototype, returning
`stubReturn("<name>")`. When no prototype matches, even after stripping a `_vN`
suffix, it falls back to a zero-argument stub and logs a warning. Output is run
through `go/format`; if formatting fails, the unformatted source is written with
a warning rather than aborting.

Two read-only modes short-circuit before generation. `-stats` is checked first,
so passing both flags runs stats only.

- `-stats` prints an NVML coverage table (total functions, hand-written
  implementations, generated stubs) plus a per-file `//export` count. It counts
  only exports that also appear in `nvml.go`, so bridge-internal exports do not
  inflate the number.
- `-validate` compares the Go parameter count of each hand-written `//export`
  function against the parameter count of its `nvml.h` prototype. It prints one
  `WARNING: <file>:<line>: ...` line per mismatch and exits 1, or prints
  `All hand-written exports match nvml.h parameter counts.` and exits 0.

## Who runs it

A code-generation step and developers, never a cluster workload. It is not
installed into the nvml-mock image.

`make gen` runs `go generate ./pkg/gpu/mocknvml/bridge/...`, and the
`//go:generate` directive in `pkg/gpu/mocknvml/bridge/helpers.go` invokes this
binary with all four path flags. `make gen-check` re-runs generation and fails
if `git diff` over the bridge directory is dirty, so CI catches a stale
`stubs_generated.go`.

That check matters because `stubs_generated.go` is checked-in generator output.
A go-nvml bump that adds new NVML entry points does not refresh it, so the mock
library silently stops exporting the new symbols until the generator is re-run.

`make build` also produces `dist/generate-bridge`, because it globs every
`cmd/*/main.go`.

## Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-input` | `vendor/github.com/NVIDIA/go-nvml/pkg/nvml/nvml.go` | NVML Go wrapper file |
| `-header` | `vendor/github.com/NVIDIA/go-nvml/pkg/nvml/nvml.h` | NVML C header file for prototype extraction |
| `-bridge` | `pkg/gpu/mocknvml/bridge` | bridge directory to scan for existing implementations |
| `-output` | `pkg/gpu/mocknvml/bridge/stubs_generated.go` | output file for generated stubs, written with mode 0644 |
| `-stats` | `false` | print coverage statistics and exit |
| `-validate` | `false` | validate hand-written export parameter counts against `nvml.h` prototypes |

There is no check or diff mode: `-output` is written unconditionally, and drift
detection is done externally by `make gen-check`.

`generate-bridge` reads no environment variables.

## Usage

```bash
make gen
```

The equivalent explicit invocation from the repo root:

```bash
go run ./cmd/generate-bridge \
    -input vendor/github.com/NVIDIA/go-nvml/pkg/nvml/nvml.go \
    -header vendor/github.com/NVIDIA/go-nvml/pkg/nvml/nvml.h \
    -bridge pkg/gpu/mocknvml/bridge \
    -output pkg/gpu/mocknvml/bridge/stubs_generated.go
```

The read-only modes:

```bash
go run ./cmd/generate-bridge -stats      # coverage table
go run ./cmd/generate-bridge -validate   # exit 1 on signature drift
```

## See also

- [Tools index](README.md)
- [Development Guide](../development.md)
- [Architecture](../architecture.md)
