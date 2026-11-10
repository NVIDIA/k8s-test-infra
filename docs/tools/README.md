# Command-Line Tools

Besides the mock libraries, Mokka ships a set of small binaries under `cmd/`.
Three of the four below are workloads the chart runs for you; only
`nvml-mock-ctl` is meant to be invoked by hand.

| Component | Purpose |
|------|---------|
| [`node-agent`](node-agent.md) | Compiles a mock NVML profile into on-host state and keeps it reconciled. The process the nvml-mock DaemonSet runs. |
| [`nri-plugin`](nri-plugin.md) | containerd NRI plugin that injects the mock driver overlay, LD_PRELOAD shims and device nodes into containers at creation time. |
| [`control-plane`](control-plane.md) | Runs the [Mokka control plane](../control-plane.md) and serves liveness and controller-readiness probes. |
| [`nvml-mock-ctl`](../nvml-mock-ctl.md) | Mutate the simulated GPU state of a running node without a restart. |

Three more are documented under [Contributing](../contributing/index.md):
[`check-fabric`](check-fabric.md), which prints the NVLink fabric identity
(cluster UUID, clique ID, state) of every visible GPU,
[`generate-bridge`](generate-bridge.md), which generates the mock NVML bridge
stubs, and [`mokka-capture`](mokka-capture.md), which captures a real GPU node
and scrubs the capture for the repository.

## Building

`make build` compiles every `cmd/*/main.go` into `dist/`, then does the same for
the Go shims under `shims/`.

The nvml-mock image installs four of these under `/usr/local/bin`: `node-agent`,
`nri-plugin`, `nvml-mock-ctl` and `check-fabric`. `control-plane` has an image
of its own, built from `deployments/control-plane/Dockerfile`, and
`generate-bridge` and `mokka-capture` are developer tools that ship in neither.
