# Command-Line Tools

Besides the mock libraries, Mokka ships a set of small binaries under `cmd/`.
Of the five below, only `nvml-mock-ctl` is meant to be invoked by hand; the
others run for you, from the chart or inside a KubeVirt guest.

| Component | Purpose |
|------|---------|
| [`node-agent`](node-agent.md) | Compiles a mock NVML profile into on-host state and keeps it reconciled. The process the nvml-mock DaemonSet runs. |
| [`nri-plugin`](nri-plugin.md) | containerd NRI plugin that injects the mock driver overlay, LD_PRELOAD shims and device nodes into containers at creation time. |
| [`control-plane`](control-plane.md) | Runs the [Mokka control plane](../control-plane.md) and serves liveness and controller-readiness probes. |
| [`nvml-mock-ctl`](../nvml-mock-ctl.md) | Mutate the simulated GPU state of a running node without a restart. |
| [`nvml-mock-guest`](nvml-mock-guest.md) | Runs inside a KubeVirt guest and mirrors the node's runtime overrides into it. |

Two more are documented under [Contributing](../contributing/index.md):
[`check-fabric`](check-fabric.md), which prints the NVLink fabric identity
(cluster UUID, clique ID, state) of every visible GPU, and
[`generate-bridge`](generate-bridge.md), which generates the mock NVML bridge
stubs.

## Building

`make build` compiles every `cmd/*/main.go` into `dist/`, then does the same for
the Go shims under `shims/`.

The nvml-mock image installs five of these under `/usr/local/bin`: `node-agent`,
`nri-plugin`, `nvml-mock-ctl`, `check-fabric` and `nvml-mock-guest`, the only
one built without cgo so that it runs in any guest. `control-plane` has an image
of its own, built from `deployments/control-plane/Dockerfile`, and
`generate-bridge` is a build-time tool that ships in neither.
