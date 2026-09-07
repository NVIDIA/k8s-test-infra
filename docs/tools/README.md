# Components

Besides the mock libraries, Mokka ships a set of small binaries under `cmd/`.
Two of them run inside the chart's DaemonSets and are never invoked by hand;
the rest are for operators, demos and developers.

| Component | Purpose |
|------|---------|
| [`control-plane`](control-plane.md) | HTTP service for the Mokka Control Plane (MEP-0001); the init slice serves only `/healthz` and `/readyz`. |
| [`node-agent`](node-agent.md) | Compiles a mock NVML profile into on-host state and keeps it reconciled. The process the nvml-mock DaemonSet runs. |
| [`nri-plugin`](nri-plugin.md) | containerd NRI plugin that injects the mock driver overlay, LD_PRELOAD shims and device nodes into containers at creation time. |
| [`nvml-mock-ctl`](../nvml-mock-ctl.md) | Mutate the simulated GPU state of a running node without a restart. |
| [`check-fabric`](check-fabric.md) | Print the NVLink fabric identity (cluster UUID, clique ID, state) of every visible GPU. |
| [`generate-bridge`](generate-bridge.md) | Code generator for the mock NVML bridge stubs. |

## Building

`make build` compiles every `cmd/*/main.go` into `dist/`, then does the same
for the Go shims under `shims/`. Nested commands take their parent directory as
a prefix, so a `cmd/foo/bar/main.go` would land at `dist/foo-bar`; nothing
under `cmd/` is nested today.

The nvml-mock image installs four of these under `/usr/local/bin`:
`node-agent`, `nri-plugin`, `nvml-mock-ctl` and `check-fabric`.
`control-plane` has an image of its own, built from
`deployments/control-plane/Dockerfile`. `generate-bridge` is a build-time tool
and is in neither image.

`hack/check-docs-tools-sync.sh`, which `make lint` runs, fails when these
pages and `cmd/` disagree: a page with no binary behind it, or a binary
documented nowhere. `nvml-mock-ctl` is documented one level up, so the script
carries it in an explicit `top_level_pages` list and checks that pairing in
both directions too.
