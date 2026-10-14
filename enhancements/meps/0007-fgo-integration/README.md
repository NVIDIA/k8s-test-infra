# MEP-0007: Run:ai Fake GPU Operator incorporation

Author: [Roman Hlushko](https://github.com/roma-glushko)

<!-- toc -->
- [Summary](#summary)
- [Motivation](#motivation)
  - [Background](#background)
    - [The Run:ai Fake GPU Operator](#the-runai-fake-gpu-operator)
    - [What Mokka does today](#what-mokka-does-today)
    - [Limits of the current integration](#limits-of-the-current-integration)
  - [Goals](#goals)
  - [Non-Goals](#non-goals)
- [Proposal](#proposal)
  - [User Stories (Optional)](#user-stories-optional)
    - [Story 1 (Optional)](#story-1-optional)
    - [Story 2 (Optional)](#story-2-optional)
  - [Notes/Constraints/Caveats (Optional)](#notesconstraintscaveats-optional)
  - [Risks and Mitigations](#risks-and-mitigations)
- [Design Details](#design-details)
- [Drawbacks](#drawbacks)
- [Alternatives](#alternatives)
<!-- /toc -->

## Summary

The proposal explores how to incorporate the relevant portion of 
[Run:AI Fake GPU Operator (FGO)](https://github.com/run-ai/fake-gpu-operator) functionality directly into Mokka.

## Motivation

The integration will enable Mokka to:
- consolidate Mokka and Run:AI team's effort in one single project
- replace FGO in Run:AI and KAI Scheduler CI/CD pipelines. 
- extend Mokka's functionality with optional KWOK (no kubelet, virtual nodes) support for massive, cheap load testing where accessing nvidia-smi and other on-host tools and libs are not needed.

### Background

[Fake GPU Operator (FGO)](https://github.com/run-ai/fake-gpu-operator) is a Kubernetes operator for testing Run:AI's GPU scheduling capabilities 
without having real, expensive GPU wasted on that activity.

FGO supports two backends:

- `fake` - the primary FGO mode. It uses a custom, fake instances of nvidia-smi, device driver, DRA plugin, etc. in order to simulate presence of GPU devices on the node.
- `mock` - the prior integration with Mokka during the early days of the Mokka project. 

Here is a list of FGO main features:

- TBU

#### FGO Node Lifecycle (Fake Backend)

TBU

#### The Run:ai Fake GPU Operator

The [Fake GPU Operator](https://github.com/run-ai/fake-gpu-operator) (FGO) is
Run:ai's tool for testing GPU scheduling on clusters without GPUs. It simulates
at the Kubernetes API level. 

A node opts in with the label
`run.ai/simulated-gpu-node-pool=<pool>`, and the chart's
`topology.nodePools.<pool>` declares the pool's GPUs. FGO advertises
`nvidia.com/gpu` through its own device plugin, publishes GPU node labels such
as `nvidia.com/gpu.product` and Prometheus metrics, and supports MIG, NUMA
topology (`NodeResourceTopology`), Dynamic Resource Allocation (DRA) and
ComputeDomains.

FGO reaches fleet scale with [KWOK](https://kwok.sigs.k8s.io/) (Kubernetes
WithOut Kubelet), which creates Node objects and drives the lifecycle of the
pods bound to them, with no kubelet and no containers behind either. On a KWOK
node, FGO's central `kwok-gpu-device-plugin` publishes the `nvidia.com/gpu`
capacity and its `kwok-dra-plugin` creates the ResourceSlice, so a scheduler
sees GPU nodes that have no machine behind them.

#### Run:AI x Mokka Integration (Mock Backend)

When the node pool is defined to be `mock` like this:

```yaml
cluster:
  nodePoolLabelKey: run.ai/simulated-gpu-node-pool
  migStrategy: none
  nodePools:
    a100-pool:
      gpu:
        backend: mock
        profile: a100
        overrides:
          device_count: 2
```

TBU

With [integrations.fakeGpuOperator.enabled](../../../deployments/nvml-mock/helm/nvml-mock/templates/integrations-fgo.yaml), 
the Mokka chart creates publishes one ConfigMap per Mokka GPU profile like `gb300`, `h100`, `a100`, etc. in a way that FGO expects 
(e.g. named `gpu-profile-<profile>`, with the profile under the `profile.yaml` key, in `integrations.fakeGpuOperator.targetNamespace`).
That's the only place Mokka knows about FGO.

TBU

### Goals

<!--
List the specific goals of the MEP. What is it trying to achieve? How will we
know that this has succeeded?
-->

### Non-Goals

<!--
What is out of scope for this MEP? Listing non-goals helps to focus discussion
and make progress.
-->

## Proposal

<!--
This is where we get down to the specifics of what the proposal actually is.
This should have enough detail that reviewers can understand exactly what
you're proposing, but should not include things like API designs or
implementation. What is the desired outcome and how do we measure success?
The "Design Details" section below is for the real nitty-gritty.
-->

### User Stories (Optional)

<!--
Detail the things that people will be able to do if this MEP is implemented.
Include as much detail as possible so that people can understand the "how" of
the system. The goal here is to make this feel real for users without getting
bogged down.
-->

#### Story 1 (Optional)

#### Story 2 (Optional)

### Notes/Constraints/Caveats (Optional)

<!--
What are the caveats to the proposal?
What are some important details that didn't come across above?
Go in to as much detail as necessary here.
This might be a good place to talk about core concepts and how they relate.
-->

### Risks and Mitigations

<!--
What are the risks of this proposal, and how do we mitigate? Think broadly.
For example, consider operational overhead, resource consumption, and how
this will impact our users.
-->

## Design Details

<!--
This section should contain enough information that the specifics of your
change are understandable. This may include API specs (though not always
required) or even code snippets. If there's any ambiguity about HOW your
proposal will be implemented, this is the place to discuss them.
-->

## Drawbacks

<!--
Why should this MEP _not_ be implemented?
-->

## Alternatives

<!--
What other approaches did you consider, and why did you rule them out? These do
not need to be as detailed as the proposal, but should include enough
information to express the idea and why it was not acceptable.
-->
