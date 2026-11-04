# scrub-smi-xml

Replaces the identifiers in an `nvidia-smi -q -x` capture with the placeholders
the [hardware captures](https://github.com/NVIDIA/k8s-test-infra/blob/main/tests/e2e/go/assertions/nvidiasmi/testdata/hardware/README.md)
document, so the capture can be committed.

```bash
scrub-smi-xml raw.xml > qx-<node>.xml
scrub-smi-xml -check qx-<node>.xml
```

Six elements are rewritten:

| Element | Placeholder |
|---------|-------------|
| `serial` | `1650000000NNN` |
| `chassis_serial_number` | `1820000000NNN` |
| `uuid` | `GPU-000000NN-0000-0000-0000-0000000000NN` |
| `clusterUuid` | `000000NN-0000-0000-0000-0000000000NN` |
| `pdi` | `0x00000000000000NN` |
| `gpu_fabric_guid` | `0x00000000000000NN` |

It follows the rules the README sets down:

- **One placeholder per distinct value.** GPUs that shared an identifier on the
  node still share one here.
- **Absent values stay.** `N/A`, empty and all-zero values are not rewritten, so
  the file does not claim hardware the node lacks.
- **`clusterUuid` counts from 1.** Zero is how `nvidia-smi` says "not in a fabric
  cluster".
- **`pdi` and `gpu_fabric_guid` share one numbering.** On Grace-Blackwell they
  are the same value.

The scrub writes the result to standard output and, to standard error, how many
distinct values each element had, which is a quick check that the count matches
the number of GPUs. It is idempotent.

`-check` reads a file and exits `1` if any of the six elements still holds a
value that is neither absent nor a placeholder. It applies the same patterns as
`TestHardwareCapturesAreScrubbed`.

The tool rewrites those six elements only. Read the diff for anything else that
traces back to the machine before committing.
