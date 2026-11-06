# smi-xml-diff

Compares two `nvidia-smi -q -x` documents element by element: a real node's
capture against the output of the mock for the same profile.

```bash
smi-xml-diff [-ignore regexp] [-max n] real.xml mock.xml
```

Each document is flattened to path=value pairs for its leaf elements. Every
element is indexed among its same-named siblings in document order, so the first
GPU is `nvidia_smi_log[0]/gpu[0]`. The report says:

- how many of the real document's paths exist in the mock;
- how many of those carry the same value;
- the paths only one side has, and the values that differ.

Readings that move with load (temperatures, utilisation, power draw, memory
used) and identifiers are matched on presence only, since a real node and a mock
cannot agree on them. `-ignore` replaces that set; the default is in
`hack/tools/smi-xml-diff/main.go`.

## Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-ignore` | load and identity paths | Regular expression of paths whose value is not compared |
| `-max` | `40` | Entries listed per kind |

## Exit codes

| Code | Meaning |
|------|---------|
| `0` | The comparison completed, whatever it found |
| `1` | A file could not be read or parsed |
| `2` | Bad usage |

See [Capturing a real node](../capturing-a-real-node.md), step 6.
