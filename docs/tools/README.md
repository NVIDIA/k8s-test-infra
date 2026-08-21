# Tools

Besides the mock libraries, Mokka ships a set of small binaries under `cmd/`.
Most of them run inside the nvml-mock DaemonSet and are never invoked by hand;
a few are meant for operators, demos and developers.

| Tool | Purpose |
|------|---------|
| [`nvml-mock-ctl`](../nvml-mock-ctl.md) | Mutate the simulated GPU state of a running node without a restart. |
| [`nvml-mock-nri`](nvml-mock-nri.md) | containerd NRI plugin that injects the mock driver overlay, LD_PRELOAD shims and device nodes into containers at creation time. |
| [`mock-ib`](mock-ib.md) | Render the fake InfiniBand sysfs tree from a profile and serve the UMAD/verbs backend over a Unix socket. |
| [`render-pci-sysfs`](render-pci-sysfs.md) | Render a fake PCI sysfs tree from the profile's `pcie_topology:` block. |
| [`render-imex-procdevices`](render-imex-procdevices.md) | Write a substitute `/proc/devices` carrying the NVIDIA caps device majors. |
| [`check-fabric`](check-fabric.md) | Print the NVLink fabric identity (cluster UUID, clique ID, state) of every visible GPU. |
| [`fake-fabricmanager`](fake-fabricmanager.md) | Stand-ins for `nv-fabricmanager` and its readiness query on NVSwitch profiles. |
| [`fake-imex`](fake-imex.md) | Deprecated stand-ins for `nvidia-imex` and `nvidia-imex-ctl`. |
| [`imex-nogpu-shim`](imex-nogpu-shim.md) | argv wrapper that runs the real `nvidia-imex` with `--nogpu` appended. |
| [`mokka-control-plane`](mokka-control-plane.md) | HTTP service for the Mokka Control Plane (MEP-0001); the init slice serves only `/healthz` and `/readyz`. |
| [`generate-bridge`](generate-bridge.md) | Code generator for the mock NVML bridge stubs. |

## Building

`make build` compiles every `cmd/*/main.go` into `dist/`. Nested commands take
their parent directory as a prefix, so `cmd/fake-imex/daemon` lands at
`dist/fake-imex-daemon`.

The nvml-mock image builds its own subset and installs each binary under the
name its caller expects: the IMEX and fabricmanager fakes are installed as
`/usr/bin/nvidia-imex`, `/usr/bin/nvidia-imex-ctl`, `/usr/bin/nv-fabricmanager`
and `/usr/bin/nv-fabricmanager-ctl`, everything else under `/usr/local/bin`.
`generate-bridge` is a build-time tool and is not part of the image.
