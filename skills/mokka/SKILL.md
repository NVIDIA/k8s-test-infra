---
name: mokka
description: >-
  Use when testing GPU-dependent Kubernetes software without GPUs by
  simulating NVIDIA GPUs, drivers and InfiniBand with Mokka (the nvml-mock
  Helm chart from NVIDIA/k8s-test-infra) on Kind or another CPU-only cluster.
  Covers deciding whether a mock is enough, installing and pinning the chart,
  choosing a GPU profile, running the NVIDIA device plugin, DRA driver, GPU
  Operator, HAMi, NFD, DCGM exporter, NVSentinel, Slurm/Slinky or Topograph
  on top, injecting GPU failures and Xid, ECC, temperature or NVLink faults
  with nvml-mock-ctl, CI lanes on CPU runners, and troubleshooting "no GPUs",
  "0 allocatable", missing nvidia-smi and NRI injection. Triggers on "Mokka",
  "nvml-mock", "mock GPU", "fake GPU", "simulated GPU", "GPU-free kind
  cluster", "test without GPUs". Not for CUDA execution, NCCL, RDMA or
  performance testing, which need real hardware.
license: Apache-2.0
---

# Mokka

Mokka simulates the software contracts around NVIDIA devices, not the devices.
The node daemon stages a mock driver root on each node: a mock
`libnvidia-ml.so`, device nodes, sysfs and procfs, CDI specs. The real,
unmodified `nvidia-smi`, device plugin, DRA driver, GPU Operator operands and
`dcgm-exporter` then read it as if the hardware were there.

Mokka releases every two weeks and is pre-1.0, so any minor release can carry a
breaking change. What you remember about its chart values may be stale. The
repository docs are the source of truth, and this skill points into them.

## Ground facts

- **Chart:** `oci://ghcr.io/nvidia/k8s-test-infra/chart/nvml-mock`, which needs
  Helm 3.8 or newer. The image is `ghcr.io/nvidia/nvml-mock`. By convention the
  release is `nvml-mock` in namespace `mokka`, and the DaemonSet is
  `ds/nvml-mock` with label `app.kubernetes.io/name=nvml-mock`.
- **Profiles:** `a100`, `b200`, `gb200`, `gb300` (the default), `h100`, `l40s`,
  `t4` and `vrnvl72`, set with `--set gpu.profile=<name>`. `t4` and the
  superchip trays (`gb200`, `gb300`, `vrnvl72`) present 4 GPUs; the 8-GPU
  baseboards present 8. `gpu.count` overrides the count.
- **Driver root on the node:** `/var/lib/nvml-mock/driver`. Every consumer that
  takes a driver root points there. The GPU Operator's validator reads
  `/run/nvidia/driver`, which Mokka also serves.
- **Placement:** the chart ships `nodeSelector: {}` and tolerates every taint,
  so by default it lands on **every** node, control plane included. The guides
  label nodes `mokka.nvidia.com/type=sgpu` so that their consumer DaemonSets
  can select the simulated-GPU pool.
- **One release per node.** All releases share the same hostPaths:
  `/var/lib/nvml-mock`, `/var/run/cdi`, `/run/nvidia` and the NFD features
  directory. For different GPU models on different nodes, install one release
  per pool, each with its own `nodeSelector`, and never let two releases
  overlap on a node.
- **How a pod gets the mock driver:** two paths, both on by default. The node
  daemon makes containerd's default handler the `nvidia` runtime (CDI mode), and
  the NRI plugin injects the driver into containers that hold a GPU allocation
  or carry `nvml-mock.nvidia.com/devices: "true"`. The injected `nvidia-smi` is
  glibc, so use a Debian or Ubuntu image to check it, not `busybox` or `alpine`.
- **What it does to nodes:** privileged pods with `hostPID`. The node daemon
  writes `/etc/containerd/conf.d/50-mokka.toml`, enables CDI and NRI, makes
  `nvidia` the default runtime handler, and restarts containerd through
  systemd. It needs containerd 1.7 or later under systemd; CRI-O and k3s nodes
  are left alone.
- **Multi-node NVLink profiles download IMEX**, among them the default
  `gb300`. The node agent fetches NVIDIA's IMEX archive from
  `developer.download.nvidia.com` for DRA ComputeDomains. A node that cannot
  reach it keeps working without IMEX after about a minute of retries;
  `--set featureGates.IMEXDownload=false` skips the download.
- **Driver version comes from the profile**, and `driverVersion` overrides it.
  Consumers that gate on a minimum driver read this value. The image bundles
  one `nvidia-smi` per driver branch (580 and 615) and stages the one that
  matches the driver.
- **Memory used is always 0 MiB**, unless `allocationWatcher.enabled=true`
  makes it track Kubernetes GPU allocations.
- **No CUDA.** A kernel launch, NCCL, RDMA, GPUDirect or a throughput number is
  out of scope. Unimplemented NVML calls return `NVML_ERROR_NOT_SUPPORTED`.

## Rules

- **Check the cluster before installing.** Run `kubectl config current-context`
  and confirm that it is a throwaway or test cluster. Never install Mokka on a
  cluster with real NVIDIA GPUs, a real driver, or production workloads: it
  takes over containerd's default runtime and injects a fake driver into GPU
  pods ([FAQ](https://github.com/NVIDIA/k8s-test-infra/blob/main/docs/faq.md#is-this-safe-for-production)).
  Ask the user when you are unsure.
- **Read the docs that match the installed version.** `helm list -n mokka`
  shows the chart version. The links below point at `main`, which can describe
  unreleased behavior. Replace `blob/main` with `blob/v<version>` to read the
  release you are using.
- **Pin `--version` once tests pass.** An unpinned chart changes under you when
  Mokka releases, and the failure will not look like a version change.
- **Prefer published artifacts.** Install the OCI chart. Building from a
  clone is the contributor workflow, unless the user is changing Mokka itself.
- **Do not present proposals as features.** Mokka Enhancement Proposals under
  `enhancements/` describe planned work. Only what the docs for the installed
  version describe is available.
- **Before a `helm upgrade`, confirm with the user if overrides are active.**
  An upgrade that rolls the DaemonSet wipes every `nvml-mock-ctl` override on
  the node, and so does a pod restart.

## Workflow

### 1. Decide whether Mokka fits

Mokka fits when the system under test **reads** hardware state and reacts to
it. That covers discovery, scheduling, device plugin and DRA allocation, node
labels, NVML, `nvidia-smi`, DCGM and dcgm-exporter consumers, dashboards and
alerts, remediation logic, declared topology (PCI, NUMA, NVLink, cliques,
fabric) and IMEX peer readiness. Real hardware is needed when the system
**executes** work, moves data or measures performance. Read
[Simulation depth by area](https://github.com/NVIDIA/k8s-test-infra/blob/main/docs/index.md)
and the [FAQ](https://github.com/NVIDIA/k8s-test-infra/blob/main/docs/faq.md)
before you promise coverage. The FAQ's "What is not simulated yet?" lists the
surfaces that behave like a machine with no GPU.

### 2. Install and verify

```bash
kind create cluster --name mokka          # skip for an existing test cluster

helm install nvml-mock oci://ghcr.io/nvidia/k8s-test-infra/chart/nvml-mock \
  --namespace mokka --create-namespace \
  --set gpu.profile=h100 \
  --wait --timeout 5m
kubectl -n mokka rollout status ds/nvml-mock --timeout=5m

kubectl -n mokka exec ds/nvml-mock -- nvidia-smi -L
```

Keep the `rollout status` line. Except with Helm 4's default wait, `--wait`
can return before every node pod is ready, and `exec` then fails with
`container not found ("node-agent")`.
[Use in CI/CD](https://github.com/NVIDIA/k8s-test-infra/blob/main/docs/guides/ci-cd.md)
explains why. Before tests run, also wait for the consumer's own signal, such
as the node's allocatable `nvidia.com/gpu`. Use
[Installation](https://github.com/NVIDIA/k8s-test-infra/blob/main/docs/helm-chart.md)
for every chart value and
[Configuration](https://github.com/NVIDIA/k8s-test-infra/blob/main/docs/configuration.md)
for what a profile defines and how to override single fields or devices.

### 3. Put the consumer on top

Follow the guide for the consumer. Most guides are self-contained and run from
published artifacts; the ComputeDomain and NVSentinel guides run from a clone
of this repository. The table
lists the settings that most often break when skipped; the guide has the full,
version-pinned steps.

| Consumer | Guide | Settings that matter |
|---|---|---|
| NVIDIA device plugin | [device-plugin.md](https://github.com/NVIDIA/k8s-test-infra/blob/main/docs/guides/device-plugin.md) | `--nvidia-driver-root` and `--driver-root-ctr-path` set to `/var/lib/nvml-mock/driver`, `--device-discovery-strategy=nvml`, and `--pass-device-specs=true`, without which the NRI plugin cannot recognise the allocation |
| NVIDIA DRA driver | [dra.md](https://github.com/NVIDIA/k8s-test-infra/blob/main/docs/guides/dra.md) | `nvidiaDriverRoot=/var/lib/nvml-mock/driver`, `gpuResourcesEnabledOverride=true`, the `nvidia.com/gpu.present=true` node label (or NFD), and a Kind cluster with DRA enabled |
| NVIDIA GPU Operator | [gpu-operator.md](https://github.com/NVIDIA/k8s-test-infra/blob/main/docs/guides/gpu-operator.md) | `driver.enabled=false`, `toolkit.enabled=false`, CDI on, the validator env overrides, and the operator version the guide pins. Pass the settings in a **values file**, because nested `env` lists break under `--set` |
| HAMi | [hami.md](https://github.com/NVIDIA/k8s-test-infra/blob/main/docs/guides/hami.md) | `devicePlugin.nvidiaDriverRoot=/var/lib/nvml-mock/driver`, the `gpu=on` node label, and HAMi as the only NVIDIA device plugin on the node |
| Node Feature Discovery | [nfd.md](https://github.com/NVIDIA/k8s-test-infra/blob/main/docs/guides/nfd.md) | NFD's feature directory must match `nodeLabels.featuresDir` |
| DCGM / dcgm-exporter | [gpu-operator.md](https://github.com/NVIDIA/k8s-test-infra/blob/main/docs/guides/gpu-operator.md) | `dcgm-exporter` embeds the host engine, so the standalone DCGM DaemonSet is optional |
| NVSentinel | [nv-sentinel](https://github.com/NVIDIA/k8s-test-infra/blob/main/docs/guides/nv-sentinel/README.md) | GPU Operator with the **standalone** DCGM on, NVSentinel's `metadataCollector` on, `labeler.assumeDriverInstalled=true`, and the mock artifacts `DCGM_FR_NVLINK_EFFECTIVE_BER_THRESHOLD`, `DCGM_FR_NVLINK_SYMBOL_BER_THRESHOLD` and `DCGM_FR_IMEX_UNHEALTHY` suppressed, or nodes stay cordoned. The guide's `run.sh`, run from a clone, installs the chart from the checkout and runs a cordon, drain and uncordon cycle |
| ComputeDomain / IMEX | [compute-domain](https://github.com/NVIDIA/k8s-test-infra/blob/main/docs/guides/compute-domain/README.md) | A multi-node NVLink profile, four workers, and the DRA driver's `altProcDevices=/var/lib/nvml-mock/imex/proc-devices`. Needs a clone of this repository, plus `go` and `make`, to build the IMEX binaries image |
| Others | [Guides index](https://github.com/NVIDIA/k8s-test-infra/blob/main/docs/guides/README.md) | Amazon EKS, Dynamo, Slinky, Run:ai fake-gpu-operator, Topograph, MIG, vGPU. DSX Air: [dsx-air.md](https://github.com/NVIDIA/k8s-test-infra/blob/main/docs/guides/dsx-air.md) |

Mokka writes no node labels itself. As on real hardware, NFD derives
`feature.node.kubernetes.io/pci-10de.present` from the feature file Mokka
writes, and GPU Feature Discovery derives the `nvidia.com/gpu.*` labels from
NVML. Point GFD at Mokka's machine-type file, as the GPU Operator guide does.

### 4. Change GPU state

- **During a test, without a redeploy:** use
  [`nvml-mock-ctl`](https://github.com/NVIDIA/k8s-test-infra/blob/main/docs/nvml-mock-ctl.md)
  in the DaemonSet pod on the target node. Overrides are per node and apply
  within about a second.

  ```bash
  POD=$(kubectl -n mokka get pod -l app.kubernetes.io/name=nvml-mock \
    --field-selector spec.nodeName=<node> -o jsonpath='{.items[0].metadata.name}')
  kubectl -n mokka exec "$POD" -- nvml-mock-ctl fail --gpu 0 --mode ecc_uncorrectable --xid 79
  kubectl -n mokka exec "$POD" -- nvml-mock-ctl temp --gpu all 90
  kubectl -n mokka exec "$POD" -- nvml-mock-ctl status
  kubectl -n mokka exec "$POD" -- nvml-mock-ctl reset
  ```

  Run `nvml-mock-ctl <command> --help` before using a flag you have not
  confirmed in the docs. `--gpu` takes an index, `all`, or a UUID; a UUID
  works only for devices whose profile declares a `uuid:`.
  DCGM latches Xid and ECC faults until its host engine restarts, while
  temperature, power and other gauges clear on the next poll. A test that
  expects recovery after `fail --mode healthy` or `reset` must restart the
  DCGM host engine, or use a gauge fault instead.
- **From the start of a run:** set chart values such as
  `gpu.failureInjection.enabled=true` and `gpu.failureInjection.mode=<mode>`, or
  a custom profile. See the
  [failure injection guide](https://github.com/NVIDIA/k8s-test-infra/blob/main/docs/guides/failure-injection/README.md).
- **MIG:** use `nvidia-smi mig` on every profile except `l40s` and `t4`. A
  runtime repartition changes only the NVML view. What Kubernetes can allocate
  is the layout the node booted with. See
  [MIG partitioning](https://github.com/NVIDIA/k8s-test-infra/blob/main/docs/guides/mig/README.md).
- **To match a real node:** `mokka-capture` records a real GPU node, scrubs the
  capture, and writes a mock profile from it. Install that profile as a custom
  profile. It is a contributor tool, built from a clone with
  `go build ./cmd/mokka-capture`, and it needs access to a cluster with the
  real GPU node. See
  [Capturing a real node](https://github.com/NVIDIA/k8s-test-infra/blob/main/docs/contributing/capturing-a-real-node.md).

### 5. Run it in CI

Follow [Use in CI/CD](https://github.com/NVIDIA/k8s-test-infra/blob/main/docs/guides/ci-cd.md):
create a Kind cluster, install the pinned chart with `--wait`, wait with
`kubectl -n mokka rollout status ds/nvml-mock`, then run the tests. To cover
several GPU models, run a job matrix over profiles rather than several releases
in one cluster. `t4` is the smallest profile.

## Troubleshooting

Collect evidence first:

```bash
helm list -n mokka
kubectl -n mokka get pods -o wide
kubectl -n mokka logs -l app.kubernetes.io/name=nvml-mock -c node-agent --tail=100
kubectl -n mokka exec ds/nvml-mock -- env MOCK_NVML_DEBUG=1 nvidia-smi 2>&1 | head -40
```

`MOCK_NVML_DEBUG=1` traces every NVML call, plus the config the library loaded
and how many devices it built.

Check
[Troubleshooting](https://github.com/NVIDIA/k8s-test-infra/blob/main/docs/troubleshooting.md)
first: it covers NRI crash-loops, pods that get no mock GPUs, the GPU Operator
validator, `N/A` fields and runtime changes that do not apply. The rows below
are causes that page does not list.

| Symptom | Usual cause |
|---|---|
| A pod requesting a GPU has no `nvidia-smi` | The image is musl-based (Alpine); the injected tools are glibc binaries. NRI also injects only at container creation, so recreate the pod after fixing the node |
| The device plugin reports 0 GPUs | Its driver root does not point at `/var/lib/nvml-mock/driver` |
| A pod that requested one GPU sees every GPU on the node | It carries the `nvml-mock.nvidia.com/devices` annotation, and the device plugin runs without `--pass-device-specs=true` or the `cdi-cri` strategy, so NRI sees no allocation and gives the pod the whole node |
| No ResourceSlices | The DRA kubelet plugin did not schedule, usually because the node lacks `nvidia.com/gpu.present=true` |
| Two releases overwrite each other's state on a node | The releases overlap on that node; give each release its own `nodeSelector` |

## Reporting problems

Only the latest release receives fixes, and there are no backports. Before
reporting a bug, reproduce it on the latest release. Then draft an issue with
the output of the evidence commands above, and show the draft to the user
before filing it. Use the bug report
template at <https://github.com/NVIDIA/k8s-test-infra/issues/new/choose>. Report
security issues privately through
[SECURITY.md](https://github.com/NVIDIA/k8s-test-infra/blob/main/SECURITY.md),
never in a public issue.

## Further reading

- [Architecture](https://github.com/NVIDIA/k8s-test-infra/blob/main/docs/architecture.md):
  the node daemon, libraries and shims, the NRI plugin, and the control plane.
- [InfiniBand mocking](https://github.com/NVIDIA/k8s-test-infra/blob/main/docs/helm-chart.md#infiniband-mocking):
  the mock InfiniBand devices and tools, and a cross-node `ibping`.
- [Set up NRI injection](https://github.com/NVIDIA/k8s-test-infra/blob/main/docs/guides/nri-injection.md):
  the annotations, and how to opt pods and namespaces out.
- [Feature gates](https://github.com/NVIDIA/k8s-test-infra/blob/main/docs/feature-gates.md)
  and the [support policy](https://github.com/NVIDIA/k8s-test-infra/blob/main/docs/support-policy.md).
- [references/downstream-patterns.md](references/downstream-patterns.md): how
  projects that already test against Mokka set up their lanes.
