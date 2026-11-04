# vr-nvl72

One compute tray of a rack-scale NVLink system, captured on 4 November 2026.

| | |
| --- | --- |
| Platform | DMI product `VR NVL72`; each tray has 4 GPUs and 352 logical CPUs in 2 NUMA nodes (GPUs 0-1 on node 0, GPUs 2-3 on node 1) |
| GPUs | 4 per tray, `NVIDIA Graphics Device`, architecture `Rubin`, PCI `10de:3041`, compute capability 10.7, 286,524 MiB of memory each |
| NICs | 12 RDMA devices per tray: 8 ConnectX-9 and 4 BlueField-4 ports, all Ethernet (RoCE) |
| Host | Ubuntu 26.04.1, kernel `7.0.0-2022-nvidia-bos-64k`, aarch64, 64 KiB pages |
| Driver | 615.71.09 (open kernel module), CUDA 13.4, installed by the GPU Operator in a driver container |
| Kubernetes | v1.36.3, GPU Operator, device plugin, GPU Feature Discovery, DCGM, MIG manager, DRA driver (compute domains only) |

`node-a` is that tray. A second tray of the same kind was captured and is not
committed, because it adds almost nothing: 6,192 of its 6,196 NVML calls returned
the same code as `node-a`'s, and what differed is per unit (tray index, slot
number, device minors, and the identifiers, which are placeholders here). Treat
those as per-node fields in a profile. The node was idle when captured.

## Layout

- `node-a/`: the tray. File names say what they hold: `smi-*` is `nvidia-smi`,
  `nvml-dump.json.gz` is the NVML recording (gzipped JSON, one entry per getter and
  device; `nvml-dump diff` reads it as it is; identifiers in it are redacted by
  field name), `proc-*` is `/proc/driver/nvidia`, `host-*` and `pci-*` are host
  topology and devices, `rdma-*` and `net-*` are NICs, `cdi-*` are CDI specs.
- `cluster/`: the Kubernetes view. `nodes-labels-capacity.yaml` holds each node's
  labels, capacity and `nodeInfo` only; the full node objects, operator pod and
  ConfigMap listings and validator logs are left out.

## What is not in this capture

- **Bulk the committed files already summarise.** The captures were taken with
  `lspci -vv` listings for the GPUs and NICs, the `nvidia-smi nvlink -e` error
  counters and the `-q -d SUPPORTED_CLOCKS` table (about 0.7 MB). They are
  left out: `smi-q.xml` holds the supported clocks and the PCI summary files hold the
  topology. Take them with `hack/capture-node.sh` if a profile needs them.

- **Fabric topology.** Which NIC sits on which rail or leaf, and the number of
  planes, are switch-side facts a node cannot report.
- **A DRA GPU driver and a network DRA driver.** Neither was installed, so the
  attributes they publish are missing; the only ResourceSlices are for compute
  domains.
- **The Network Operator** and SR-IOV operator objects, for the same reason.
- **A healthy ComputeDomain.** The one in the capture is `NotReady`, and no IMEX
  channel devices existed.
- **MIG enabled.** MIG was capable but disabled, so the profile tables are
  present and instance listings are empty.
- **Load.** Readings are idle values.
- **One driver branch.** Only 615.71.09; return codes and field IDs differ between
  branches.
- **`nvidia-smi -gBwMode`**, which this driver does not recognise; the NVML
  bandwidth-mode getters are in `nvml-dump.json.gz`.
- **`nvidia-ctk cdi generate`**, which is not in the driver container; the CDI
  specs are read from the host.
- **The driver's `libnvidia-ml.so`**, which is NVIDIA's; `node-a/nvml-symbols.txt`
  lists its exported symbols.

## Notable facts

These are the observations that shape a profile for this platform. They come from
the files above, not from documentation.

- NVLink: 36 links per GPU, `NV36` between every pair of GPUs in the tray, fabric
  state Completed and healthy.
- Power: a 2300 W limit, minimum 200 W.
- The NVML field-ID layout differs from the existing profiles' (135 of 400 probed
  IDs answer, 82 in common with the `gb300` profile).
- NVML NVLink error and utilisation counters answer `NOT_SUPPORTED`; telemetry has
  moved to field values.
- The NICs are RoCE, not InfiniBand: no `smi*` devices, no Data Direct functions,
  no `umad` or `issm` device nodes, and `/sys/module/mlx5_core/version` does not
  exist because the driver is in-kernel.
- GPU Feature Discovery labels the family `blackwell` while NVML reports `Rubin`.
