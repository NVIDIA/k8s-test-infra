# Mokka on DSX Air

Use the hosted **Kubernetes Intro – Mokka + KWOK** demos on DSX Air to explore
the Kubernetes GPU stack without physical GPUs or a local repository checkout.
Choose a configured environment to explore the stack, or an unconfigured
environment to work through its setup.

## What DSX Air provides

[NVIDIA DSX Air](https://docs.nvidia.com/dsx-air) is a cloud-hosted data center
simulation platform for validating infrastructure configurations, features, and
automation. It lets you build a digital twin of an infrastructure environment
and experiment with network topologies and operating systems.

Its [Demo Marketplace](https://docs.nvidia.com/dsx-air/pre-built-demos) provides
ready-made simulations with accompanying instructions. Launching a demo creates
a copy in your account that you can modify and rebuild. The Mokka + KWOK demos
use this delivery model to provide a hosted environment for exploring the
Kubernetes GPU stack.

## How the integration fits together

The demos bring together three layers:

| Layer | Role |
|---|---|
| DSX Air | Hosts the demo environment and its lab instructions. |
| Mokka | Supplies simulated GPU driver interfaces on CPU nodes that run containers, so real tools such as `nvidia-smi` and GPU management components can discover simulated devices. See the [Mokka architecture](../architecture.md). |
| Kubernetes WithOut Kubelet (KWOK) | Represents virtual Kubernetes nodes without running a kubelet or workload containers on each one, allowing control-plane exercises with a larger node population. |

These layers exercise different parts of the stack. A KWOK node's Kubernetes
status does not demonstrate container execution. Use a Mokka-backed node that
runs containers when inspecting driver interfaces with `nvidia-smi`.

## Access DSX Air

DSX Air uses NVIDIA NGC for authentication and organization management. You need
an NGC account and organization; sign in with your business email and select the
organization you want to use. Each user needs an organization-level **Air User**
or **Air Org Admin** role. See
[Account Setup](https://docs.nvidia.com/dsx-air/account-setup) for onboarding and
access troubleshooting.

## Choose a demo

| Demo | Use it to |
|---|---|
| [Kubernetes Intro – Mokka + KWOK Configured](https://dsx-air.nvidia.com/demos/07891765-a16f-4fae-8f36-21c2babd739f) | Explore the configured environment. |
| [Kubernetes Intro – Mokka + KWOK Unconfigured](https://dsx-air.nvidia.com/demos/23ba81c5-8180-4075-90bb-7c502fb559a8) | Work through configuration using the demo's lab instructions. |

1. Open the selected demo and click **Launch** to create your copy. Deployment
   can take several minutes; find the copy in your **Simulations** list.
2. Open the demo's **Documentation** for credentials, cluster access, component
   versions, and setup steps. See
   [Pre-built Demos](https://docs.nvidia.com/dsx-air/pre-built-demos) for the
   launch workflow.
3. In the simulation's **Topology** tab, wait until its status is **Active**,
   then double-click a node to open its console. Select the node identified by
   the lab instructions for Kubernetes access. The
   [DSX Air Quick Start](https://docs.nvidia.com/dsx-air/quick-start) also explains
   node properties and external connections through the **Services** tab.

For the **configured** demo, inspect the existing deployment before making
changes. For the **unconfigured** demo, complete the lab's setup before checking
for simulated GPUs. The [installation reference](../helm-chart.md) explains
Mokka's deployment options if you need more context for those steps.

## Inspect the environment

Run these commands in the lab terminal with `kubectl` configured for the demo
cluster:

```bash
kubectl config current-context
kubectl get nodes -o wide
kubectl get pods -A -o wide
kubectl get nodes \
  -o custom-columns='NODE:.metadata.name,GPUS:.status.allocatable.nvidia\.com/gpu'
```

The node and pod listings help identify where components run. The last command
shows advertised `nvidia.com/gpu` resources, if present. That resource count is
an allocation view; it does not establish whether a node runs containers or
provides Mokka's driver interfaces.

After Mokka is configured, find one of its node-daemon pods in the pod listing.
Replace `<namespace>` and `<mokka-pod>` below with its namespace and pod name:

```bash
kubectl -n <namespace> exec <mokka-pod> -c node-agent -- nvidia-smi -L
```

This checks the driver view on that pod's node. The reported devices come from
the active [GPU configuration](../configuration.md); use the demo's instructions
for its expected model and count. If the command fails, consult
[Troubleshooting](../troubleshooting.md).

For the scope of what these checks prove, see Mokka's
[simulation boundaries](../index.md#simulation-depth-by-area). Use the
[device-plugin guide](device-plugin.md) for GPU resource allocation and
[runtime control](../nvml-mock-ctl.md) for changing simulated device state.

## Save and resume the lab

When finished, select **Stop Simulation and Store Checkpoint** to save the
nodes' state and release compute resources. Starting the simulation again
resumes from its latest checkpoint by default. This lets you continue a setup
exercise later. See
[Simulation Management](https://docs.nvidia.com/dsx-air/simulation-management)
for checkpoint selection, automatic sleep, and lifecycle details.
