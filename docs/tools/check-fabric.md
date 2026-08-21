# check-fabric

Prints the NVLink fabric identity of every visible GPU by calling
`nvmlDeviceGetGpuFabricInfo` through go-nvml, and encodes the result in its exit
code. It is the demo-time and e2e-time consumer for the ComputeDomain
simulation: with the mock NVML library resolvable by the dynamic loader, it
reports the cluster UUID, clique ID and registration state the topology overlay
assigned to this node.

Output per GPU is a four-line block with fixed-width labels. Consumers grep for
those literal strings, so the alignment is part of the contract:

```text
Discovered 8 GPU(s)
GPU 0 (GPU-...)
  clusterUuid : 00000000-0000-0000-0000-0000000000ab
  cliqueId    : 1
  state       : completed (3)
```

`state` maps the raw NVML value: `not_supported` (0), `not_started` (1),
`in_progress` (2), `completed` (3), `unknown` for anything else.

## Exit codes

| Code | Meaning |
|------|---------|
| `0` | every visible GPU reported fabric info |
| `2` | at least one GPU returned `ERROR_NOT_SUPPORTED` |
| `1` | any other NVML failure (init, device count, handle, or fabric query) |

A GPU with no fabric prints `GPU N (uuid): fabric NOT SUPPORTED` and does not
abort the run; the count is folded into the exit code at the end.

## Who runs it

Never a long-running process. Three callers:

- the Go e2e suite, which runs it inside an NRI-injected pod on every worker and
  asserts the `cliqueId` and `clusterUuid` lines;
- the compute-domain and node-wide-injection demo scripts, which exec it per
  node and fail on a non-zero status or a clique mismatch;
- a developer, through the manually triggered `check-fabric` Tilt resource.

It is built into the nvml-mock image at `/usr/local/bin/check-fabric`, and
`setup.sh` also copies it into the driver-root overlay at
`$DRIVER_ROOT/usr/bin/check-fabric` so NRI-injected workload pods that never
mount the image can still run it.

## Flags

`check-fabric` defines no flags and reads no environment variables of its own.
Arguments are ignored, and there is no help output.

The mock library is found by the dynamic loader, not by the program: go-nvml
`dlopen`s `libnvidia-ml.so.1`, which resolves either through the standard
`/usr/local/lib` install or through the `LD_LIBRARY_PATH` the NRI plugin injects.

## Usage

```bash
kubectl exec <nvml-mock-pod> -- check-fabric
```

As the Tilt scenario asserts it:

```bash
out=$(kubectl exec "${pod}" -- check-fabric 2>&1 || true)
grep -q  "cliqueId    : ${expected_clique}" <<<"$out"
grep -qi "clusterUuid : ${expected_uuid}"   <<<"$out"
grep -q  'state       : completed'          <<<"$out"
```

Gated on an IMEX channel device, as the compute-domain demo does it:

```bash
sh -c 'test -c /dev/nvidia-caps-imex-channels/channel0 && check-fabric | head -6'
```

## See also

- [Tools index](README.md)
- [Compute Domain demo](../demo/compute-domain/README.md)
- [fake-fabricmanager](fake-fabricmanager.md)
