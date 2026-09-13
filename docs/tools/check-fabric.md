# check-fabric

Prints the NVLink fabric identity of every visible GPU and encodes the result in
its exit code. It is the consumer that proves the ComputeDomain simulation
works: with the mock NVML library resolvable by the dynamic loader, it reports
the cluster UUID, clique ID and registration state the topology overlay assigned
to this node.

Output per GPU is a four-line block with fixed-width labels. Callers grep for
those literal strings, so the alignment is part of the contract:

```text
Discovered 8 GPU(s)
GPU 0 (GPU-...)
  clusterUuid : 00000000-0000-0000-0000-0000000000ab
  cliqueId    : 1
  state       : completed (3)
```

`state` maps the raw NVML value: `not_supported` (0), `not_started` (1),
`in_progress` (2), `completed` (3), and `unknown` for anything else.

A GPU with no fabric prints `GPU N (uuid): fabric NOT SUPPORTED` and does not
abort the run — the count is folded into the exit code at the end.

## Exit codes

| Code | Meaning |
|------|---------|
| `0` | Every visible GPU reported fabric info |
| `1` | An NVML call failed: init, device count, handle, or the fabric query |
| `2` | At least one GPU returned `ERROR_NOT_SUPPORTED` |

## Usage

It takes no flags or arguments.

```bash
kubectl exec <nvml-mock-pod> -- check-fabric
```

Asserting against the output, which is how the scenarios use it:

```bash
out=$(kubectl exec "${pod}" -- check-fabric 2>&1 || true)
grep -q  "cliqueId    : ${expected_clique}" <<<"$out"
grep -qi "clusterUuid : ${expected_uuid}"   <<<"$out"
grep -q  'state       : completed'          <<<"$out"
```

Gating on an IMEX channel device, so it runs only where a fabric exists:

```bash
sh -c 'test -c /dev/nvidia-caps-imex-channels/channel0 && check-fabric | head -6'
```

!!! note "How it finds the mock library"

    The program does not locate the library itself — go-nvml `dlopen`s
    `libnvidia-ml.so.1`, which resolves either through the standard
    `/usr/local/lib` install or through the `LD_LIBRARY_PATH` the NRI plugin
    injects. Where neither applies, the run stops at `nvmlInit` with exit `1`.

## Where it comes from

It is built into the nvml-mock image at `/usr/local/bin/check-fabric`. The
[node daemon](../components/node-daemon.md) then copies it into the driver-root
overlay at `$DRIVER_ROOT/usr/bin/check-fabric`, which is what lets NRI-injected
workload pods that never mount the image run it. An image without the binary
skips that copy rather than failing.

Three callers run it, never as a long-running process: the Go e2e suite, which
execs it inside an NRI-injected pod on every worker; the ComputeDomain and
node-wide injection scripts, which fail on a non-zero status or a clique
mismatch; and a developer, through the manually triggered `check-fabric` Tilt
resource.

## See also

- [Command-line tools](README.md)
- [ComputeDomain guide](../guides/compute-domain/README.md)
- [NRI Plugin](../components/nri-plugin.md) — what injects the library into pods
