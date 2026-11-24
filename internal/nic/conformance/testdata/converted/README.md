# Converted NIC captures

Hardware captures as `mokka-capture nic-surfaces` lists them, converted by
hand from evidence that predates `collect network`. Each file holds only what
its source shows; a record the source lacks is absent, and the fixture's
causes file counts it as a capture blind spot. Identifiers are placeholders.

| File | Source | What it lacks |
| --- | --- | --- |
| `aws-c5n-efa.nic-surfaces.tsv` | One AWS `c5n.9xlarge` instance with one EFA adapter (`1d0f:efa0`), captured in October 2026 for a DRA proof of concept: a copy of the adapter's sysfs, `ibv_devinfo -v`, `ls -l /dev/infiniband` and `rdma system show`. The capture is not in this repository | Counters, `files` lists, modules. The GID is replaced by a placeholder; the original was the ENI MAC's EUI-64 link-local address |
| `aws-gb300-vf.nic-surfaces.tsv` | One AWS `p6e-gb300r.36xlarge` instance, captured in October 2026 for the network simulation design: listings of its PCI functions, RDMA devices, `/dev/infiniband`, netdevs, modules and VPD, with outputs trimmed. The listing is not in this repository | Everything the listing trims: the attributes of seven of the eight RDMA devices beyond name and link state; VPD beyond the part number and the Data Direct marker on the first six functions; the IDs above each function; `/dev/infiniband` beyond `rdma_cm`; `ibv_devinfo`. The VFs' netdevs are in pods, so the host shows none |

`collect network` records only Mellanox (`0x15b3`) functions today, so it
can't capture an EFA node yet. The EFA file uses `nic-surfaces`' objects and
attributes as if that filter were lifted. The first line of each file names
the capture files its source has no counterpart of, which `diff nic` counts
as capture blind spots.
