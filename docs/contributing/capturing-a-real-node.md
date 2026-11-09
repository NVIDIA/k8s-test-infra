# Capturing a real node

A profile is only as good as what it was checked against. This guide takes you
from a Kubernetes cluster with real GPU nodes to a scrubbed capture you can
commit, and then to a check of how closely a Mokka profile reproduces it. Use it
when adding a GPU type, when a new driver branch ships, and to find out how much
of a platform Mokka can already simulate.

Every step uses one tool, [`mokka-capture`](../tools/mokka-capture.md), run on
your machine. The procedure produces three things:

- a directory of raw outputs per node and for the cluster (`mokka-capture collect`);
- an NVML recording of every getter the driver answers, taken in the driver
  container as part of the same run (`mokka-capture dump`);
- a scrubbed copy of both (`mokka-capture scrub`) that is safe to commit under
  `tests/hardware-captures/`.

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
  (`-host-pod-image`) is pinned by digest and the pod is deleted on every
  way out, but the pin does not cover the tools it installs: the pod runs
  `apt-get install` for `pciutils`, `rdma-core` and others from the image's
  distribution mirrors. On a cluster where that is not acceptable, set
  `-host-pod-image` to an image that already has those tools;
- the nodes running the GPU Operator's driver container. The driver and
  `nvidia-smi` live in that container, not on the host. If the driver is
  installed on the host instead, run the `nvidia-smi` and `/proc` commands in
  [`internal/capture/collect`](https://github.com/NVIDIA/k8s-test-infra/blob/main/internal/capture/collect/surfaces.go)
  there directly;
- `kubectl`, Go to build `mokka-capture`, and Docker to build its Linux copy for
  the driver container.

Capture at least two nodes of the same kind. Two nodes show which fields vary per
unit and which describe the model. The values that differ between them (tray
index, slot number, minors) belong in the profile as per-node fields.

Take the capture with the node idle. Readings that move with load, such as
utilisation, temperature and power draw, are excluded from comparisons.

## Step 1: build `mokka-capture`

Build the tool for your machine, then a Linux copy for the driver container. The
NVML dump loads the driver's own `libnvidia-ml.so.1`, so it must run inside the
driver container, built for the node's architecture. go-nvml loads the library
at run time, so the binary is dynamically linked: build it on a host of the
node's architecture.

```bash
export OUT=$PWD/capture
go build -o bin/mokka-capture ./cmd/mokka-capture
bin/mokka-capture collect -out "$OUT" build-dump
```

This writes `$OUT/bin/mokka-capture-linux-<arch>`. If Docker hangs while pulling
the image, point it at an anonymous configuration first with
`export DOCKER_CONFIG=$(mktemp -d)`.

## Step 2: capture

List the nodes, then run the capture for each GPU node. `all` takes the driver
container captures, the host captures and, once, the cluster captures:

```bash
kubectl get nodes
bin/mokka-capture collect -out "$OUT" -dump-binary "$OUT/bin/mokka-capture-linux-<arch>" \
  all <node-a> <node-b>
```

Every command runs in the cluster and every output is written on your machine, so
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
the sysfs captures still work and the rest fail; set `-host-pod-namespace` if the
default namespace forbids privileged pods. The other flags are listed by
`mokka-capture collect -h`.

## Step 3: check the capture

`collect` ends by listing empty files. An empty file nearly always means the
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
`mokka-capture scrub` writes a copy with a placeholder for each, one per distinct value
across the whole run, so a GPU keeps the same placeholder in every file:

```bash
bin/mokka-capture scrub -out scrubbed \
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
a PDI as an integer, which no text rule matches, so the dump redacts them by
field name as it writes, and `scrub` does the same to a recording it is given
(plain or `.json.gz`).

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

The scrubbed `smi-q.xml` is also the hardware fixture for
`tests/e2e/go/assertions/nvidiasmi/testdata/hardware/`.
`mokka-capture scrub -check <file>` verifies it holds only the placeholders the
fixtures require; `TestHardwareCapturesAreScrubbed` enforces them in CI.

Finally run `go test ./internal/capture/scrub -run TestCommittedCapturesAreScrubbed`
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

- **Gzip the NVML dump** (`gzip -9 -n nvml-dump.json`); `mokka-capture diff nvml` reads
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
Each comparison is a command that takes the real capture and the same surface
taken from the mock. In the commands, `REAL` is the real node's directory (a raw
capture, or a committed one) and `MOCK` is a directory holding the mock's files.

Take the mock's files with the same command, pointed at a Mokka cluster running
the profile under test. `-mock` runs the driver commands in a stand-in pod and
opts the pods into the mock ([A Mokka node](../tools/mokka-capture.md#a-mokka-node)
says why), and `-dump-binary` is built for that cluster's architecture:

```bash
bin/mokka-capture collect -mock -dump-binary <linux binary> -out mock all <node>
MOCK=mock/<node>
```

1. **Symbols.** Which of the real library's functions does the bridge answer?
   `generate-bridge -symbols` reads the real library's exported symbols and says
   how many the bridge implements, how many are generated stubs that answer
   `NOT_SUPPORTED` where a real GPU answers something else, and which are
   missing altogether. Give it the built mock library's symbols
   (`nm -D --defined-only libnvidia-ml.so.1 | awk '{print $3}' | sort`) as
   `-mock-symbols`; without them the report reads the bridge sources, which do not
   show the legacy unversioned names the C shim exports, and counts them as
   missing. `collect -mock` leaves `$MOCK/nvml-symbols.txt` empty, so take them
   from the library you built:

   ```bash
   go run ./cmd/generate-bridge -symbols "$REAL/nvml-symbols.txt" \
     -mock-symbols mock-nvml-symbols.txt
   ```
2. **Values.** Compare the NVML dumps, which gives return-code and board-value
   match rates and lists every difference:

   ```bash
   bin/mokka-capture diff nvml "$REAL/nvml-dump.json.gz" "$MOCK/nvml-dump.json"
   ```

   Field IDs differ between driver branches; the dump probes each ID and records
   the per-field return code, so a changed layout shows up directly.
3. **`nvidia-smi`.** Compare the two `-q -x` documents element by element:

   ```bash
   bin/mokka-capture diff smi "$REAL/smi-q.xml" "$MOCK/smi-q.xml"
   ```

   The report says how many of the real document's elements the mock has, how
   many carry the same value, and lists the missing elements and the differing
   values. Values that are not a property of the board are not compared; the
   report lists each of them, and [`diff smi`](../tools/mokka-capture.md#diff-smi)
   says which they are.
4. **Surfaces.** There is no tool for the `host-*`, `proc-*`, `rdma-*` and `net-*`
   files. Read each against what the node agent renders for the profile, and
   decide whether a difference matters. A real node and a mock differ in
   numbering and counters, so this step has no pass or fail.

Report coverage per surface, for example "NVML return codes: 472 of 588 match".
That is the number to quote for how much of the node a profile reproduces. Each
diff ends by listing the values it did not compare; state those next to the
score, since the score says nothing about them.

## From capture to profile

1. Generate the profile from the committed capture, with an existing profile of
   the same GPU as `-base` for what a capture cannot give (identifiers,
   mock-only blocks); for a GPU with no profile yet, leave `-base` out:

   ```bash
   bin/mokka-capture profile \
     -base deployments/nvml-mock/helm/nvml-mock/profiles/<name>.yaml \
     -o pkg/gpu/mocknvml/configs/mock-nvml-config-<name>.yaml \
     tests/hardware-captures/<platform>/<node>
   ```

   Every value's comment names its source in the capture, and `-explain` lists
   each GPU's answer for every key; see
   [`profile`](../tools/mokka-capture.md#profile). Fix a wrong value in the
   rules, not in the generated file. The dump records the per-device NVLink
   bandwidth-mode getters only, so a board whose mode is system-wide (Hopper)
   takes `nvlink.bw_mode` from `-base`.
2. Copy the result, and the `.mig.yaml` beside it, to
   `deployments/nvml-mock/helm/nvml-mock/profiles/<name>.yaml` and
   `profiles/mig/<name>.yaml`; the chart's copy is the source of truth, and the
   one under `pkg/gpu/mocknvml/configs/` runs the library outside Kubernetes.
   [Configuration](../configuration.md) documents every field.
3. Add the capture to `goldens` in `internal/capture/profile/golden_test.go` and
   run `go test ./internal/capture/profile -update`, so a later change to a rule
   shows up as a diff of this profile.
4. Deploy it and look: `tilt up -- --gpu-profile <name>`, then check that
   `nvidia-smi` on the mock reports what the capture shows.
5. Run the drift guard: `go test -tags=e2e ./tests/e2e/go/profile/...
   ./tests/e2e/go/assertions/...` asserts derived expectations across all
   profiles, so an inconsistent profile fails here before it fails on a cluster.
6. Add the `nvidia-smi -q -x` fixture to
   `tests/e2e/go/assertions/nvidiasmi/testdata/hardware/` with a row in that
   folder's README. `hardware_test.go` asserts on readings, not on the parse
   succeeding, because a renamed element decodes as absent rather than as an
   error.

A rented or shared machine needs one more check before its capture is kept: confirm
it has no MIG partitioning, no vGPU mode and no leftover processes.

## Troubleshooting

| Symptom | Cause and fix |
| --- | --- |
| `TLS handshake timeout` or `connection refused` in `errors.log` | The API connection dropped. Restore it and rerun the part that failed; `collect` checks the connection before each node. |
| `nvidia-smi: ... not recognized` for an option | The driver's `nvidia-smi` lacks it. The failure is the answer; keep it. |
| `-q -d <section>` reports `Failed to parse` | The section name is not valid on this driver. Sections are captured one per file so one invalid name costs one file. |
| `GLIBC` error running the dump | The driver container's libc is older than the build image's. Build the Linux `mokka-capture` on an image with an older base. |
| `nvidia-ctk` not found | It is not in the driver container. The capture reads the CDI specs from the host and runs `nvidia-ctk` from the toolkit container. |
| The host pod stays `Pending` | Pod security forbids privileged pods in the namespace. Set `-host-pod-namespace` to one that allows them. |
| `/var/run/cdi` appears empty from the pod | `/var/run` is a symlink to `/run`, which resolves inside the pod. Read `/host/run/cdi`. |
