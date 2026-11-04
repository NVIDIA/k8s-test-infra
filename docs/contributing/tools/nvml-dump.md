# nvml-dump

Records what an NVML library answers and compares two recordings. It is the
tool for the question "does the mock say what a real GPU says?": run it once
against a real node and once against a mock node, then diff the two.

## Dump

```bash
nvml-dump > real.json
```

`nvml-dump` initialises whichever `libnvidia-ml.so.1` the dynamic loader finds,
then calls the getters (`Get*` and `Is*`) on every device and, at process level,
the driver, NVML and CUDA version getters and the device count. It finds the
device getters by reflection over go-nvml's device handle, so a getter added in a
newer go-nvml is picked up without editing the tool. Nothing that changes state is
called. "Every getter" is bounded by the list below: what the dump could not call
is named in `skipped`.

- A getter with no arguments is recorded as `Name`.
- A getter whose arguments are all integers or enums is called over a fixed range
  of values and recorded as `Name(a,b)`: clock types, NVLink indexes 0 to 39, ECC
  counter kinds, temperature sensors and so on. A value past the end of an enum
  answers `ERROR_INVALID_ARGUMENT`, which is recorded too, so the dump also shows
  where each range ends.
- `GetFieldValues` is probed one field ID at a time (0 to 399), at scope 0 only,
  recorded as `GetFieldValues(id=N)` with the per-field return code. Per-link
  field values (scope IDs for each NVLink) are not probed. The IDs a driver
  defines differ between releases, so this is where a driver-branch change shows.
- A getter with any other kind of argument (a struct, a handle, a string) is
  listed under `skipped` and not called, so the coverage of a dump is visible in
  the dump.

Each call is recorded with its NVML return code, as a fixed name such as
`ERROR_NOT_SUPPORTED`, and its value. A getter the library answers with
`ERROR_NOT_SUPPORTED` is recorded as such: on real hardware that is an answer
the mock has to give too.

A real node's identifiers are redacted as the dump is written: GPU UUIDs, serial
numbers, chassis serials, PDIs, IB and rack GUIDs and the fabric cluster UUID are
replaced by placeholders, one per distinct value. NVML returns most of these as C
byte arrays and integers, which no text scrubber recognises, so the redaction
goes by the name of the field or getter and not by the shape of the value. The
dump is written one device at a time, so a driver crash on a later device keeps
the earlier ones (the file is then truncated JSON; the devices written are
intact). A new field that names an identifier is covered; one that does not is
not, so a reviewer still reads a dump before it is committed.

## Diff

```bash
nvml-dump diff real.json mock.json
```

Example output:

```text
calls compared:        412
return code matches:   398 of 412 (96.6%)
value matches:         301 of 340 (88.5%) (of 412 calls answered SUCCESS on both sides, excluding 72 whose value is load or identity)
only on real device:   3
only on mock device:   0
```

Return codes are compared for every call. A value is compared only where both
sides answered `SUCCESS`; a call both sides answer with the same error counts as
a return-code match and nothing more. Getters that report load, time or identity
(temperatures, power draw, utilisation, clocks in use, UUIDs and serials) are
excluded from the value rate and counted separately. Names are matched whole:
`GetClockInfo` is excluded and `GetMaxClockInfo` is compared. Override the list
with `-ignore <regexp>` over whole call names, and the number of mismatches shown per kind with
`-max <n>`. Devices pair by index, which holds when both sides expose the same
GPUs in the same order.

## Running it on a real node

The binary links dynamically (go-nvml loads the driver's library at run time),
so build it for the node's architecture, on a host of that architecture, and run
it inside the driver container, which holds the real library:

```bash
hack/capture-node.sh build-dump        # writes $OUT/bin/nvml-dump-linux
NVML_DUMP=$OUT/bin/nvml-dump-linux hack/capture-node.sh driver <node>
```

That copies the binary into the driver container, runs it there and writes
`nvml-dump.json` beside the node's other outputs. See
[Capturing a real node](../capturing-a-real-node.md). To dump the
mock, copy the same binary into an nvml-mock pod, where the mock library is on
the loader path; it is not baked into the nvml-mock image. `diff` reads gzipped
dumps (`.json.gz`) directly, which is how a committed capture is stored.

## Exit codes

| Code | Meaning |
|------|---------|
| `0` | The dump or diff completed |
| `1` | NVML failed to initialise, the device count could not be read, or a file could not be read |
| `2` | Bad usage |
