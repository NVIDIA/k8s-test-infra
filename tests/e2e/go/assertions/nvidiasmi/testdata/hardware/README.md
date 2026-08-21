# Real-hardware nvidia-smi captures

`nvidia-smi -q -x` taken from physical nodes, kept whole — every GPU, every
element. The fixtures one directory up come from the mock and are trimmed to two
GPUs; these are the counterpart the mock is supposed to look like, on drivers
spread either side of the one the mock targets, so they are where an element
rename lands first.

| File | Node | Driver / CUDA |
| --- | --- | --- |
| `qx-b200.xml` | 8x NVIDIA B200 | 580.105.08 / 13.0 |
| `qx-h100.xml` | 8x NVIDIA H100 80GB HBM3 | 580.105.08 / 13.0 |
| `qx-gb200.xml` | 4x NVIDIA GB200 (`p6e-gb200.36xlarge`, arm64) | 580.173.02 / 13.0 |
| `qx-gb300.xml` | 4x NVIDIA GB300 | 580.173.02 / 13.0 |
| `qx-l40s.xml` | 1x NVIDIA L40S (AWS `g6e.2xlarge`) | 595.91.07 / 13.2 |
| `qx-t4.xml` | 1x Tesla T4 (AWS `g4dn.xlarge`) | 595.91.07 / 13.2 |
| `qx-a100.xml` | 1x NVIDIA A100-SXM4-40GB (Lambda `gpu_1x_a100_sxm4`) | 570.148.08 / 12.8 |

The single-GPU captures are the no-NVLink, no-fabric end of the range — useful
for seeing which elements a small board omits. The drivers deliberately spread
from 570 to 595, since a spread is what exposes an element rename: the A100 is
the oldest and the T4 and L40S the newest.

The GB200 and GB300 are the only ones in a fabric cluster, so they are the only
place `clusterUuid` and `cliqueId` carry values rather than zeros or `N/A`. The
GB200 is also the only arm64 host, taken from a running GPU-operator driver pod
rather than a node we control directly.

The A100 matches the board `mock-nvml-config-a100.yaml` models
(`NVIDIA A100-SXM4-40GB`). It came from a rented single-GPU VM rather than a
node we own, so it was checked for MIG partitioning, vGPU mode and leftover
processes before being kept; it has none, and reports `Pass-Through` like the
rest.

Use them to author and check `pkg/gpu/mocknvml/configs/mock-nvml-config-*.yaml`
against what the real board reports, and to check `schema.go` still finds its
elements — `hardware_test.go` asserts on the readings rather than on the parse
succeeding, because a renamed element decodes as absent, not as an error.

## Identifiers are scrubbed

These files are public, so anything naming a specific piece of hardware is
replaced with a placeholder before it lands here. Six elements need it:

| Element | Placeholder |
| --- | --- |
| `serial` | `16500000000NN` |
| `chassis_serial_number` | `18200000000NN` |
| `uuid` | `GPU-000000NN-0000-0000-0000-0000000000NN` |
| `clusterUuid` | `000000NN-0000-0000-0000-0000000000NN` |
| `pdi` | `0x00000000000000NN` |
| `gpu_fabric_guid` | `0x00000000000000NN` |

`TestHardwareCapturesAreScrubbed` asserts these shapes over every file, so a
capture added without scrubbing fails CI rather than merging.

Everything else is verbatim. Board and part numbers, VBIOS, PCI addresses,
clocks and thresholds describe the model rather than the unit, and are the
reason to keep the capture at all.

Two rules matter when scrubbing by hand, both of which are easy to get wrong:

- Leave bodies that report an absent value — `N/A`, empty, an all-zero
  `clusterUuid` — alone, so a scrubbed file does not claim hardware the node
  lacks. Number `clusterUuid` placeholders from 1 for the same reason: zero is
  how nvidia-smi says "not in a fabric cluster", and a node that was in one
  must not come out looking like a node that was not.
- Use one placeholder per distinct value, so GPUs that shared an identifier
  upstream still share one here. The GB200 and GB300 modules each cover two
  GPUs and so report a serial twice, and every GPU on a node repeats the one
  `chassis_serial_number` and `clusterUuid`. `pdi` and `gpu_fabric_guid` share
  one numbering for the same reason: on Grace-Blackwell they are the same value,
  and scrubbing them apart would invent a difference the node did not report.

To add a capture, take it and replace the six elements above:

    nvidia-smi -q -x > qx-<node>.xml

Then grep the result for each original identifier to confirm none survived, and
read the diff for anything else that traces back to the machine.
