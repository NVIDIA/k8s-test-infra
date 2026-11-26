# Downstream patterns

These projects already run Mokka lanes in CI. Read them for working,
end-to-end examples. They pin their own Mokka versions, and some lag behind the
current release, so where one of them disagrees with the Mokka docs for your
version, the Mokka docs win.

| Project | Lane | Stack on top of Mokka |
|---|---|---|
| [NVIDIA/aicr](https://github.com/NVIDIA/aicr) | `tests/uat/kind/setup-gpu-sim.sh` (Slurm UAT), `tools/component-test/setup-gpu-mock.sh`, `.github/workflows/gpu-operator-mokka-test.yaml` | Standalone device plugin, DCGM host engine, NVSentinel; or NFD, GPU Operator and DRA driver |
| [NVIDIA/cluster-readiness-engine](https://github.com/NVIDIA/cluster-readiness-engine) | `test/uat/mokka/setup.sh`, `make mokka-up PROFILE=<profile>`, `docs/how-to-guides/run-locally-without-gpus.md` | NFD, device plugin, GFD; on `gb200`/`gb300`, the DRA driver with ComputeDomains |
| [kubernetes-sigs/dra-driver-nvidia-gpu](https://github.com/kubernetes-sigs/dra-driver-nvidia-gpu) | `hack/ci/mock-nvml/` | DRA driver end-to-end tests against the mock NVML library, built from source without the chart |

For HAMi, follow the
[HAMi guide](https://github.com/NVIDIA/k8s-test-infra/blob/main/docs/guides/hami.md).

## Lessons that carry over

**Pin the chart and the image as one pair.** The chart installs the image
tagged with its own `appVersion`, so pinning `--version` alone pins both.
Override `image.tag` only with a version that was released together with the
chart. A chart from one release with a newer image has left the node agent
failing its bind mount and never Ready. Pinning by digest also protects
against a tag that is pushed again.

**The chart and the image live at different OCI paths.** The chart is
`oci://ghcr.io/nvidia/k8s-test-infra/chart/nvml-mock`. `ghcr.io/nvidia/nvml-mock`
is the image. Passing the image path to `helm install` fails with a media-type
error.

**Use the stock Kind node image.** Since 0.5.0 the node daemon installs the
NVIDIA container runtime, enables CDI and NRI, and restarts containerd itself.
Older lanes built on `ghcr.io/nvidia/mokka-kind-node` or patched containerd
through `containerdConfigPatches`. That image is no longer rebuilt, and the
patch is no longer needed.

**Keep consumers off the control plane.** The chart runs on every node. Label
the workers that should advertise GPUs, for example
`mokka.nvidia.com/type=sgpu`, and select them from the device plugin, GFD, DCGM
and DRA kubelet plugin. To keep the mock itself off a node, also set the
chart's `nodeSelector` to that label. On Kind, the first worker is
`<cluster>-worker`, with no `1` suffix.

**A green rollout is not proof.** After the install, poll each GPU node until
`.status.allocatable["nvidia.com/gpu"]` equals the profile's GPU count. Add
negative controls:

- An unlabeled node advertises no GPUs.
- A pod that requests one GPU more than the node has stays `Pending` with
  `Insufficient nvidia.com/gpu`.

**Wait for objects that exist.** `kubectl wait --all` on a namespace whose
operands have not been created yet passes at once. Wait for named DaemonSets
or poll for the node state instead.

**Mokka writes no `nvidia.com/` labels.** NFD derives
`feature.node.kubernetes.io/pci-10de.present` from the feature file Mokka
writes, and GFD derives the `nvidia.com/gpu.*` labels from NVML. Without the
GPU Operator, nothing writes `nvidia.com/gpu.present=true`, so label the node
yourself when the DRA driver's kubelet plugin needs it, or give that plugin an
affinity on your own pool label.

**Point GFD at Mokka's machine-type file.** GFD otherwise reads
`/sys/class/dmi/id/product_name`, which says `kind` on Kind. The file is
`/var/lib/nvml-mock/driver/config/machine-type` on the node, and Mokka's CDI
spec serves it at `/etc/nvml-mock/machine-type`. See
[Machine type](https://github.com/NVIDIA/k8s-test-infra/blob/main/docs/helm-chart.md#machine-type-nvidiacomgpumachine).

**Install in dependency order.** NFD goes before the GPU Operator, or the
operator schedules no operands. Prometheus Operator CRDs go before an NFD
chart that creates a `PodMonitor`.

**ComputeDomains need the IMEX surface.** On a multi-node NVLink profile such
as `gb200`, the node agent renders a substitute `/proc/devices` at
`/var/lib/nvml-mock/imex/proc-devices`. Set the DRA driver's
`altProcDevices` to that path, or its compute-domain kubelet plugin aborts with
`error parsing '/proc/devices'`. Older Mokka releases gated this behind an
`imex.*` chart value, so check the values for your version.

**Every node on a profile reports the same GPU UUIDs.** That is fine for
per-node consumers. A consumer that compares UUIDs or fabric GUIDs across
nodes needs a distinct config per node.

**Things a mocked lane cannot cover.** CUDA workloads, NCCL, RDMA, and
throughput all need real hardware. Downstream projects exclude those tests or
replace them with stubs: for example, a stub NCCL image that replays recorded
output.

**Budget memory.** A Kind cluster with Mokka and a consumer stack uses about
2.5 GiB while idle.
