# Guides

Runnable walkthroughs, each one a specific thing you can do with Mokka.

Every guide needs [Helm 3.8 or newer](https://helm.sh/docs/intro/install/) and
[kubectl](https://kubernetes.io/docs/reference/kubectl/). Most use
[Docker](https://docs.docker.com/get-started/get-docker/) and
[Kind](https://kind.sigs.k8s.io/) for a local cluster; the Amazon EKS guide uses an
existing compatible cluster. Each guide states its own target and prerequisites
before it installs anything.

## Scenarios

Standing Mokka up alongside a real consumer. Roughly in order of how much they
ask of you — start at the top if you are new.

| Guide | What it shows | Time |
|---|---|---|
| [NVIDIA Device Plugin](device-plugin.md) | Mock GPUs advertised as `nvidia.com/gpu`, and a workload scheduled against them | ~5 min |
| [Node Feature Discovery](nfd.md) | NFD turns Mokka's feature file into a GPU-presence node label | ~10 min |
| [Amazon EKS](install/aws/eks/README.md) | The device-plugin path on private, CPU-only managed workers running the standard AL2023 image | 20–30 min |
| [NVIDIA GPU Operator](gpu-operator.md) | The real operator stack — device plugin, GFD, DCGM and the validator — against mock GPUs | ~15 min |
| [NVIDIA Dynamo](dynamo/README.md) | An OpenAI-compatible endpoint served by Dynamo, its worker scheduled onto a mock GPU through the GPU Operator | ~15 min |
| [Slinky (Slurm)](slinky/README.md) | A Slurm cluster whose `slurmd` discovers mock GPUs as GRES, and `srun --gres=gpu:N` jobs scheduled against them | ~20 min |
| [NVIDIA DRA Driver](dra.md) | Mock GPUs published as ResourceSlices, and a pod scheduled through a ResourceClaim | ~10 min |
| [Run:ai fake-gpu-operator](runai-fgo/README.md) | Two node pools — Mokka serving one with a real NVML shim, FGO serving the other | ~10 min |
| [Topograph](topograph/README.md) | Topograph groups four simulated GB200 nodes into two NVLink domains and labels each node for topology-aware schedulers | ~10 min |

## Tasks

Things you do *with* Mokka, whichever consumer you are running.

| Guide | What it covers |
|---|---|
| [Set up NRI injection](nri-injection.md) | Check that pods holding a GPU allocation get the mock driver, and keep workloads out |
| [MIG partitioning](mig/README.md) | Carve a board into MIG slices, advertise each one, and schedule a pod onto a single slice |
| [Use in CI/CD](ci-cd.md) | Run GPU-dependent tests on CPU runners |
| [Runtime control](../nvml-mock-ctl.md) | Change temperature, power, utilisation or health on a running node, with no redeploy |
