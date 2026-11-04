# Capturing a real node

A profile is only as good as what it was checked against. This guide takes you
from a Kubernetes cluster with real GPU nodes to a scrubbed capture you can
commit, and then to a check of how closely a Mokka profile reproduces it. Use it
when adding a GPU type, when a new driver branch ships, and to find out how much
of a platform Mokka can already simulate.

The procedure produces three things:

- a directory of raw outputs per node and for the cluster, taken by
  [`hack/capture-node.sh`](https://github.com/NVIDIA/k8s-test-infra/blob/main/hack/capture-node.sh);
- an NVML recording of every getter the driver answers, taken by
  [`nvml-dump`](tools/nvml-dump.md);
- a scrubbed copy of both, made by [`scrub-capture`](tools/scrub-capture.md),
  that is safe to commit under `tests/hardware-captures/`.

## What the capture covers

Mokka imitates several layers that real consumers read. Each row is a surface the
capture records and where Mokka produces it today.

| Surface | Captured as | Mock side |
| --- | --- | --- |
| NVML library | `nvml-dump.json`, `nvml-symbols.txt` | `pkg/gpu/mocknvml/bridge`, `engine`, profile YAML |
| `nvidia-smi` | `smi-*.txt`, `smi-q.xml` | the same engine, through the bundled binary |
| Driver `/proc` files | `proc-*.txt` | `internal/agent/gpudriver`, `internal/agent/migcaps` |
| Device files and majors | `host-dev-nvidia.txt`, `host-proc-devices.txt` | node agent, CDI and NRI specs |
| PCI sysfs and DMI | `host-lspci-*.txt`, `host-pci-*.txt`, `pci-*.txt`, `host-dmi.txt` | profile `pcie_topology`, `libmockfs` |
| NUMA and CPU | `host-numa*.txt`, `host-lscpu.txt`, `host-pagesize.txt` | `pcie_topology.numa_node` |
| NVLink and fabric | `smi-nvlink-*.txt`, `smi-q-d-FABRIC.txt`, `proc-capabilities.txt` | profile `nvlink`, IMEX staging |
| RDMA devices and ports | `rdma-*.txt`, `host-ib*.txt`, `host-rdma-link.txt` | profile `infiniband`, `libibmock` |
| Network interfaces | `net-*.txt` | node agent |
| Kernel modules and log | `sys-module-*.txt`, `kernel-log-nic-gpu.txt` | `libmockfs` |
| MIG | `smi-mig-*.txt`, `proc-mig-minors.txt` | `MOCK_MIG_PROFILES_CONFIG` |
| CDI specs | `cdi-*.txt` | node agent |
| Kubernetes view | `cluster/*` | Helm chart, e2e assertions |

Some surfaces cannot be read from a node: the switch-side topology of the
fabric (which NIC is on which rail or leaf), and the attributes a DRA driver
publishes when none is installed. Capture those from the fabric's owner, or
after the driver is installed.

## Before you start

You need:

- a kubeconfig for a cluster whose GPU nodes you may inspect, with permission to
  run `exec` in the GPU Operator namespace and to create a privileged pod. The
  host pod mounts the node's root filesystem and runs as root. Its image
  (`HOST_POD_IMAGE`) is pinned by digest and the script deletes the pod on every
  way out, but the pin does not cover the tools it installs: the pod runs
  `apt-get install` for `pciutils`, `rdma-core` and others from the image's
  distribution mirrors. On a cluster where that is not acceptable, set
  `HOST_POD_IMAGE` to an image that already has those tools;
- the nodes running the GPU Operator's driver container. The driver and
  `nvidia-smi` live in that container, not on the host. If the driver is
  installed on the host instead, run the `nvidia-smi` and `/proc` commands in
  [hack/capture-node.sh](https://github.com/NVIDIA/k8s-test-infra/blob/main/hack/capture-node.sh)
  there directly;
- `kubectl`, and Docker to build `nvml-dump` for the nodes' architecture.

Capture at least two nodes of the same kind. Two nodes show which fields vary per
unit and which describe the model. The values that differ between them (tray
index, slot number, minors) belong in the profile as per-node fields.

Take the capture with the node idle. Readings that move with load, such as
utilisation, temperature and power draw, are excluded from comparisons.

## Step 1: build `nvml-dump`

`nvml-dump` loads the driver's own `libnvidia-ml.so.1`, so it must run inside the
driver container, and it must be built for the node's architecture. go-nvml loads
the library at run time, so the binary is dynamically linked. Build it on a host of
the node's architecture:

```bash
export OUT=$PWD/capture
hack/capture-node.sh build-dump
```

This writes `$OUT/bin/nvml-dump-linux`. If Docker hangs while pulling the image,
point it at an anonymous configuration first with
`export DOCKER_CONFIG=$(mktemp -d)`.

## Step 2: capture

List the nodes, then run the capture for each GPU node. `all` takes the driver
container captures, the host captures and, once, the cluster captures:

```bash
kubectl get nodes
export NVML_DUMP=$OUT/bin/nvml-dump-linux
hack/capture-node.sh all <node-a> <node-b>
```

Every command runs in the cluster and every redirect happens on your machine, so
each output is a file under `$OUT/<node>/` with nothing to copy out of a pod.
The commands are split so you can rerun one part:

| Command | Runs | Takes |
| --- | --- | --- |
| `driver <node>...` | in the driver container | `nvidia-smi`, the NVML dump, `/proc/driver/nvidia` |
| `host <node>...` | in a temporary privileged pod | PCI, NUMA, device files, NICs, CDI specs |
| `network <node>...` | in the same pod | only the NIC, interface and RDMA surfaces, plus the cluster's network objects |
| `cluster` | on your machine | node labels, ResourceSlices, `ClusterPolicy`, DCGM metrics |

The host commands create a pod named `capture-<node>-<pid>` (one per run) in the `default` namespace
and delete it when they finish. Its tools (`pciutils`, `numactl`, the RDMA
utilities) come from `apt`. If the cluster has no egress to the Ubuntu mirrors,
the sysfs captures still work and the rest fail; set `HOST_POD_NAMESPACE` if the
default namespace forbids privileged pods. The other settings are environment
variables listed by `hack/capture-node.sh --help`.

## Step 3: check the capture

The script ends by listing empty files. An empty file nearly always means the
connection dropped, not that the answer was empty; rerun the command that produced
it. A failed command also leaves a line in `errors.log` beside the output, and a
failure is data: an unsupported `nvidia-smi` option or a getter that answers
`NOT_SUPPORTED` is what the mock has to reproduce.

```bash
find "$OUT" -type f -size 0 ! -name errors.log
cat "$OUT"/*/errors.log
```

Check at least:

- `smi-L.txt` lists every GPU you expect;
- `nvml-dump.json` exists for each node and holds about 1,550 calls per GPU on a
  Vera Rubin tray (`grep -o '"ret"' nvml-dump.json | wc -l` counts them; the file
  is one line, so `grep -c` always prints 1);
- `cdi-specs-host.txt` holds the spec files, if the operator generates CDI specs;
- `host-ib*.txt` and `rdma-*.txt` are present on a node with NICs.

## Step 4: scrub

The raw capture names the machines it came from: GPU and fabric UUIDs, serial
numbers, GUIDs, MAC addresses, host names. **Do not commit or post it.**
`scrub-capture` writes a copy with a placeholder for each, one per distinct value
across the whole run, so a GPU keeps the same placeholder in every file:

```bash
scrub-capture -out scrubbed \
  -replace <node-a-name>=node-a -replace <node-b-name>=node-b \
  -replace <cluster-name>=tenant \
  -seed "$OUT/<node-a>/smi-L.txt" -seed "$OUT/<node-b>/smi-L.txt" \
  -report /tmp/substitutions.tsv \
  "$OUT/<node-a>" "$OUT/<node-b>" "$OUT/cluster"
```

`-replace` covers names the tool cannot recognise by shape: node, cluster and
network names. `-seed` numbers GPUs in index order. Binary files and
`errors.log` are skipped. The NVML recording is the exception to scrubbing by
shape: NVML returns serials, GUIDs and the fabric cluster UUID as byte arrays and
a PDI as an integer, which no text rule matches, so `nvml-dump` redacts them by
field name as it writes, and `scrub-capture` does the same to a recording it is
given (plain or `.json.gz`). `go build ./hack/tools/scrub-capture` builds the tool.

Then review the result. The scrub is a best effort, not a guarantee:

1. Check that no original value survives. The `-report` table lists every
   original the tool found; confirm none appears in the output. It cannot list
   a value the tool did not recognise, so also search for identifiers you know
   from elsewhere (the chassis serial on the rack label or in DMI, the node's
   GPU UUIDs from `nvidia-smi -L`), in the forms they take in a capture: as text,
   in hex, as decimal numbers and as lists of byte values (`nvml-dump.json`).

   ```bash
   cut -f2 /tmp/substitutions.tsv | sort -u | while read -r v; do
     [ ${#v} -ge 8 ] && grep -rqF -- "$v" scrubbed && echo "LEAK: $v"
   done
   ```

   The report holds the originals, so never commit it.
2. Search for what a pattern cannot know: project, tenant, site and user names,
   internal domains and registries.

   ```bash
   grep -rniE '<tenant>|<site>|<user>|\.corp|\.internal|token|secret|passw' scrubbed
   ```

3. Read the cluster files. Leave out any that carry environment detail the
   platform does not need, such as full node objects with annotations, operator
   pod and ConfigMap listings, and operator logs.
4. Do not commit proprietary binaries. The driver's `libnvidia-ml.so` is
   NVIDIA's; keep only the symbol list.

Take the `nvidia-smi -q -x` capture separately for the hardware fixtures. The
[`scrub-smi-xml`](tools/scrub-smi-xml.md) tool applies the rules the fixtures
require and `-check` verifies them; `TestHardwareCapturesAreScrubbed` enforces
them in CI.

Finally run `go test ./hack/tools/scrub-capture -run TestCommittedCapturesAreScrubbed`
once the capture is under `tests/hardware-captures/`; CI runs it on every change.
It checks shapes and field names only, so the reading above still matters.

## Step 5: store it

By convention a scrubbed capture goes under `tests/hardware-captures/<platform>/`:
one directory for the node, a `cluster/` directory, and a `README.md` that
records:

- the platform and the nodes captured (model, architecture, operating system,
  kernel and page size);
- driver and CUDA versions, and how the driver was installed;
- the capture date;
- what the capture does not contain, and why.

Keep the committed set small. A repository is cloned far more often than a
capture is read, and git keeps every version of a file for good. Commit what a
profile or a test is built from, and leave out the rest:

- **Gzip the NVML dump** (`gzip -9 -n nvml-dump.json`); `nvml-dump diff` reads
  `.json.gz` directly.
- **Commit one node of each kind in full.** A second node of the same kind is how
  you learn which fields vary per unit (tray index, slot number, device minors),
  but its files are nearly the first node's. Record in the `README.md` what
  differed and how closely the two agreed (for example the share of NVML return
  codes that matched) instead of committing it.
- **Leave out bulk that a smaller file already summarises**: `lspci -vv` listings
  (`host-lspci-*`), the NVLink error counters (`smi-nvlink-e.txt`) and the
  supported-clocks table (`smi-q-d-SUPPORTED_CLOCKS.txt`, which `smi-q.xml` also
  holds). Name what you left out in the `README.md`.

Keep failures out of the committed tree except where they matter: a `README`
note is better than an `errors.log` that names machines.

## Step 6: compare it with the mock

With a capture in hand you can measure how far a profile is from the real node.
Each comparison below says what runs it. Comparisons 1 to 3 are single commands;
comparison 4 still takes reading. In the commands, `REAL` is the real node's
directory (a raw capture, or a committed one such as
`tests/hardware-captures/vr-nvl72/node-a`) and `MOCK` is `./mock/<mock-node>`.

Start a Mokka cluster with the profile under test and its node agent, as in
[Quickstart](../quickstart.md), then capture the mock the same way you captured
the real node. Build `nvml-dump` for the mock cluster's architecture, which is
not the node's if the mock runs in Kind on a laptop:

```bash
DUMP_PLATFORM=linux/amd64 OUT=./mock hack/capture-node.sh build-dump
export KUBECONFIG=<mock cluster kubeconfig>
NVML_DUMP=./mock/bin/nvml-dump-linux OUT=./mock hack/capture-node.sh mock-dump <mock-node>
OUT=./mock hack/capture-node.sh host <mock-node>
```

Both commands write into `./mock/<mock-node>/`. `mock-dump` adds `nvml-dump.json`,
`smi-q.xml`, `smi-q.txt` and the mock library's symbols; `host` adds the same
`host-*`, `pci-*`, `rdma-*` and `net-*` files a real node gave you, so the two
directories line up file for file.

1. **Symbols.** Which of the real library's functions does the bridge answer?
   `generate-bridge -symbols` reads the real library's exported symbols and says
   how many the bridge implements, how many are generated stubs that answer
   `NOT_SUPPORTED` where a real GPU answers something else, and which are
   missing altogether:

   ```bash
   go run ./cmd/generate-bridge -symbols "$REAL/nvml-symbols.txt" \
     -mock-symbols "$MOCK/nvml-symbols.txt"
   ```

   Pass `-mock-symbols`. Without it the report reads the bridge sources, which do
   not show the legacy unversioned names the C shim exports, and counts them as
   missing.
2. **Values.** Compare the NVML dumps, which gives return-code and board-value
   match rates and lists every difference:

   ```bash
   go run ./hack/tools/nvml-dump diff "$REAL/nvml-dump.json.gz" "$MOCK/nvml-dump.json"
   ```

   Field IDs differ between driver branches; the dump probes each ID and records
   the per-field return code, so a changed layout shows up directly.
3. **`nvidia-smi`.** Compare the two `-q -x` documents element by element:

   ```bash
   go run ./hack/tools/smi-xml-diff "$REAL/smi-q.xml" "$MOCK/smi-q.xml"
   ```

   The report says how many of the real document's elements the mock has, how
   many carry the same value, and lists the missing elements and the differing
   values. Readings that move with load and identifiers are matched on presence
   only.
4. **Surfaces.** `compare` lists, for each `host-*`, `proc-*`, `rdma-*` and `net-*`
   file both directories hold, how many of the real file's lines the mock's file
   also has:

   ```bash
   hack/capture-node.sh compare "$REAL" "$MOCK"
   ```

   A line-level match is blunt: numbering and counters differ between a real node
   and a mock, so read the files whose share is low and decide whether the gap
   matters. This step has no pass or fail. `compare` covers only the files both
   directories hold. `host` does not read the mock's `/proc/driver/nvidia`, so the
   `proc-*` files are not compared: read them on the mock node and compare by hand.

Report coverage per surface, for example "NVML return codes: 472 of 588 match".
That is the number to quote for how much of the node a profile reproduces.

## From capture to profile

1. Write the profile in `deployments/nvml-mock/helm/nvml-mock/profiles/<name>.yaml`;
   it is the source of truth. The standalone copy for running the library outside
   Kubernetes is `pkg/gpu/mocknvml/configs/mock-nvml-config-<name>.yaml`.
   [Configuration](../configuration.md) documents every field.
2. Choose the NVLink bandwidth-mode scope from what the node answers, not from the
   manual. A Hopper board takes `system`, a Blackwell board `device`, and an
   earlier board omits the block; the `nvidia-smi` manual marks the mode
   deprecated for Blackwell, and some drivers do not recognise `-gBwMode` at all.
   When `nvidia-smi` does not, read the NVML bandwidth-mode getters in the
   dump.
3. Deploy it and look: `tilt up -- --gpu-profile <name>`, then check that
   `nvidia-smi` on the mock reports what the capture shows.
4. Run the drift guard: `go test -tags=e2e ./tests/e2e/go/profile/...
   ./tests/e2e/go/assertions/...` asserts derived expectations across all
   profiles, so an inconsistent profile fails here before it fails on a cluster.
5. Add the `nvidia-smi -q -x` fixture to
   `tests/e2e/go/assertions/nvidiasmi/testdata/hardware/` with a row in that
   folder's README. `hardware_test.go` asserts on readings, not on the parse
   succeeding, because a renamed element decodes as absent rather than as an
   error.

A rented or shared machine needs one more check before its capture is kept: confirm
it has no MIG partitioning, no vGPU mode and no leftover processes.

## Troubleshooting

| Symptom | Cause and fix |
| --- | --- |
| `TLS handshake timeout` or `connection refused` in `errors.log` | The API connection dropped. Restore it and rerun the part that failed; `capture-node.sh` checks the connection before each node. |
| `nvidia-smi: ... not recognized` for an option | The driver's `nvidia-smi` lacks it. The failure is the answer; keep it. |
| `-q -d <section>` reports `Failed to parse` | The section name is not valid on this driver. Sections are captured one per file so one invalid name costs one file. |
| `GLIBC` error running `nvml-dump` | The driver container's libc is older than the build image's. Build `nvml-dump` on an image with an older base. |
| `nvidia-ctk` not found | It is not in the driver container. The capture reads the CDI specs from the host and runs `nvidia-ctk` from the toolkit container. |
| The host pod stays `Pending` | Pod security forbids privileged pods in the namespace. Set `HOST_POD_NAMESPACE` to one that allows them. |
| `/var/run/cdi` appears empty from the pod | `/var/run` is a symlink to `/run`, which resolves inside the pod. Read `/host/run/cdi`. |
