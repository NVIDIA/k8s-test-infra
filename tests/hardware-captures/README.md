# Real-hardware captures

Raw outputs taken from real GPU nodes, scrubbed of everything that names a
machine. They are the counterpart a Mokka profile is meant to reproduce: where a
profile or a mock surface is in doubt, the capture says what the real node
answered.

The captures cover more than `nvidia-smi`. Each platform directory holds, for
every node, the outputs of `nvidia-smi`, an NVML recording of every getter, the
driver's `/proc` files, device files, PCI and NUMA topology, RDMA devices and
network interfaces, CDI specs, kernel modules and log, plus a `cluster/` directory
with the Kubernetes view (node labels, ResourceSlices, `ClusterPolicy`, DCGM
metrics). The whole-file `nvidia-smi -q -x` fixtures that tests read live in
[`tests/e2e/go/assertions/nvidiasmi/testdata/hardware`](../e2e/go/assertions/nvidiasmi/testdata/hardware/README.md).

| Directory | Platform |
| --- | --- |
| [`vr-nvl72`](vr-nvl72/README.md) | A compute tray of a rack-scale NVLink system, 4 GPUs, arm64 |

## How a capture is taken

The step-by-step guide is `docs/contributing/capturing-a-real-node.md`. In short,
`hack/capture-node.sh` takes the raw outputs, `nvml-dump` records the NVML answers, and `scrub-capture` writes the scrubbed copy
stored here.

## What is scrubbed

GPU, MIG and fabric UUIDs, serial numbers, PDIs and GUIDs, MAC addresses, IP
addresses, machine IDs, and host, cluster and site names are replaced by
placeholders, one per distinct value across the whole capture. Values that report
nothing are left alone. Model-level facts stay as the node reported them: part
numbers, VBIOS and firmware versions, PCI IDs and addresses, clocks and limits.

## Rules for adding a capture

- Scrub with `scrub-capture`, then check the result: no original value may survive,
  and nothing specific to the environment (tenant, site or user names, internal
  domains) may remain. The scrub is a best effort.
- Do not commit proprietary binaries such as the driver's `libnvidia-ml.so`.
- Leave out files that carry environment detail the platform does not need, and
  say so in the platform's `README.md`.
- Record the platform, driver and software versions, the capture date, and what the
  capture does not contain.
