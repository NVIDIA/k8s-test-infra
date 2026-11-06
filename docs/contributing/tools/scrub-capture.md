# scrub-capture

Copies a directory of real-node captures, such as the one
[`hack/capture-node.sh`](https://github.com/NVIDIA/k8s-test-infra/blob/main/hack/capture-node.sh)
writes, replacing everything that names a particular machine with a placeholder,
so the copy can be committed. The procedure is in
[Capturing a real node](../capturing-a-real-node.md).

```bash
scrub-capture -out scrubbed \
  -replace worker-7=node-a -replace worker-9=node-b \
  -seed raw/worker-7/smi-L.txt -seed raw/worker-9/smi-L.txt \
  raw/worker-7 raw/worker-9 raw/cluster
```

The output directory must be empty and outside every source directory.

## What it replaces

| Value | Placeholder |
| --- | --- |
| GPU UUID, MIG UUID | `GPU-0000000N-0000-0000-0000-00000000000N`, `MIG-…` |
| Other UUIDs (fabric cluster, Kubernetes object UIDs) | `0000000N-0000-0000-0000-00000000000N` |
| 32-digit machine IDs, and the unit fields of PCI vital product data | 32 hex digits ending in `N` |
| Serial numbers: `<serial>`, `<chassis_serial_number>`, `Serial Number` lines, VPD `[SN]` | `1650000000NNN` and `1820000000NNN` |
| PDIs, fabric GUIDs and other 16-digit hex values (except the masks below) | `0x000000000000000N` |
| Colon-separated GUIDs (`cc30:8903:00cb:be0e`) | `0000:0000:0000:000N`, the same `N` as the `0x` form of that GUID |
| IPv6 addresses in any notation (`fd00::12`, `::ffff:10.0.0.7`), such as node addresses and RDMA GIDs | `fe80:0000:…:000N` |
| MAC addresses, and `enx<mac>` interface names | `02:00:00:00:00:0N` |
| IPv4 addresses | `192.0.2.N` |
| Anything given with `-replace old=new` | `new` |

Rules that follow from keeping the data useful:

- **One placeholder per distinct value, across every file and directory in the
  run.** A GPU keeps the same placeholder in `nvidia-smi`, the NVML dump and the
  CDI specs. An NVML dump numbers its own GPUs from 0 when it is recorded; the
  scrubber pairs its devices with the GPUs in the same directory's `smi-q.xml` by
  PCI bus ID and renames the dump's UUID, serial, chassis serial, PDI and fabric
  GUID to match.
- **`-seed` numbers GPUs in index order.** Give it the `nvidia-smi -L` output of
  each node, in node order.
- **A serial number is scrubbed everywhere once it is found in a context that
  names it.** The same value in an NVML dump or a kernel log line carries nothing
  around it that says "serial".
- **Absent values stay.** Empty, `N/A` and all-zero values are not rewritten.
- **Things that look like identifiers but are not stay.** Version numbers
  (`99.00.16.55`), netmasks, the unspecified, loopback and broadcast addresses,
  16-digit hex values below 2^32 or all ones in their low bits, such as clock
  event and timestamp masks (no ID is either), kernel virtual addresses (`0xffff…`) and the link-local prefix `fe80::` of a
  GID; IPv6 loopback, multicast and the unspecified address; and colon-separated
  text that is not an address on its own: times, `std::vector`, or `a::b`, which
  has no digit.
- **File and directory names are scrubbed too**, so `-replace worker-7=node-a`
  renames a `worker-7/` directory.
- **Running it over its own output changes nothing.**

Binary files, `errors.log` and shared objects (`*.so`, `*.so.*`) are skipped and
named on standard error; override the list with `-exclude`. The output directory
must be empty or absent.

## Reviewing the result

The scrub is a best effort, not a guarantee. `-report <file>` writes a table of
every substitution, with the original and its placeholder, for an audit:

```bash
cut -f2 report.tsv | sort -u | while read -r v; do
  [ ${#v} -ge 8 ] && grep -rqF -- "$v" scrubbed && echo "LEAK: $v"
done
```

The report contains the originals, so never commit it. Names a pattern cannot
recognise (tenant, site and user names, internal domains) are for you to find
with `grep` and to add as `-replace` values.

## Exit codes

| Code | Meaning |
| --- | --- |
| `0` | The copy was written |
| `1` | A file could not be read or written, or the output directory is not empty |
| `2` | Bad usage |
