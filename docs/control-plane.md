<!-- SPDX-License-Identifier: Apache-2.0 -->
<!-- SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION -->

# Mokka control plane

The Stage 1 control plane turns `SGPURackProfile` and `SGPUInventory` resources
into control-plane-owned `SGPURack` resources, assigns eligible Kubernetes Nodes
to logical rack Nodes, and projects the assignment onto those Nodes. It also
decides which [`SGPURuntimePolicy` overrides](#override-runtime-state) apply to
each simulated GPU. It models capacity, topology, and runtime state; it does not
provide GPUs to workloads by itself.

## Install

Install the CRDs, then enable the control plane in the existing chart:

```bash
helm upgrade --install mokka-crds deployments/mokka-crds/helm/mokka-crds
helm upgrade --install nvml-mock deployments/nvml-mock/helm/nvml-mock \
  --namespace mokka --create-namespace \
  --set controlPlane.enabled=true \
  --set controlPlane.image.tag=latest
```

By default the control plane runs `ghcr.io/nvidia/mokka-control-plane` tagged
with the chart `appVersion`, which in a released chart is the image published
for the same release. On `main` the `appVersion` is the next `-dev` version,
but `main` builds of the control-plane image are published only as `latest`,
so the command above sets `controlPlane.image.tag=latest`; drop it when
installing a released chart. The controller is cluster-privileged, so pin it
by digest in production: set `controlPlane.image.digest` to the `sha256:...`
digest published for the control-plane image. A digest takes precedence over
`controlPlane.image.tag`.

The control-plane pods are scheduled and labelled independently of the node
DaemonSet, through `controlPlane.nodeSelector`, `controlPlane.tolerations`,
`controlPlane.affinity`, `controlPlane.topologySpreadConstraints`,
`controlPlane.priorityClassName`, `controlPlane.podLabels` and
`controlPlane.podAnnotations`. They take the same forms as their
[node DaemonSet counterparts](helm-chart.md#values) and the Kubernetes pod
spec. With `controlPlane.replicas` above 1, pod anti-affinity on
`kubernetes.io/hostname` keeps the standby replica off the leader's node, and a
spread constraint on `topology.kubernetes.io/zone` keeps it in another zone:

```yaml
controlPlane:
  replicas: 2
  affinity:
    podAntiAffinity:
      preferredDuringSchedulingIgnoredDuringExecution:
        - weight: 100
          podAffinityTerm:
            topologyKey: kubernetes.io/hostname
            labelSelector:
              matchLabels:
                app.kubernetes.io/name: nvml-mock-control-plane
  topologySpreadConstraints:
    - maxSkew: 1
      topologyKey: topology.kubernetes.io/zone
      whenUnsatisfiable: ScheduleAnyway
      labelSelector:
        matchLabels:
          app.kubernetes.io/name: nvml-mock-control-plane
```

`controlPlane.pdb.enabled` adds a
[PodDisruptionBudget](https://kubernetes.io/docs/concepts/workloads/pods/disruptions/)
(PDB) that bounds how many replicas a node drain or cluster upgrade may evict
at once. The PDB is rendered only when `controlPlane.replicas` is above 1: over
a single replica it would either block every drain or protect nothing. Set
`minAvailable` or `maxUnavailable` as a count or a percentage;
`maxUnavailable` wins if both are set, and with neither `minAvailable` is 1. A
`minAvailable` equal to `controlPlane.replicas` blocks every drain of a node
that runs a replica.

Uninstalling the `mokka-crds` release retains the CRDs and existing Mokka
resources. Removing the Mokka API and its resources requires deleting the CRDs
explicitly.

Only one Helm release may enable the cluster-wide control plane in a cluster.
Its fixed `control-plane.mokka.nvidia.com` ClusterRoleBinding is owned by
that release and acts as the singleton guard: another release must wait until
the owner is uninstalled. Upgrades and rollbacks of the owning release keep
using the same guard.

## Disable or uninstall safely

Do not disable `controlPlane.enabled`, uninstall its `nvml-mock` release, or
delete the Mokka CRDs while an `SGPUInventory` exists. Disabling or uninstalling
removes the control-plane Deployment and RBAC; deleting the CRDs removes its API
objects. Either order prevents the control plane from releasing the
`mokka.nvidia.com/inventory-cleanup` and `mokka.nvidia.com/rack-cleanup`
finalizers or removing its Node projection metadata. The CRD chart retains
every CRD on uninstall, so removing either Helm release is not a cleanup
mechanism.

Drain the control plane before removing it. All Mokka custom resources and Nodes
are cluster-scoped; the commands intentionally omit `--namespace` for them and
discover the namespace of the one owning Helm release from its singleton
ClusterRoleBinding.

```bash
MOKKA_RELEASE="$(kubectl get clusterrolebinding \
  control-plane.mokka.nvidia.com \
  -o jsonpath='{.metadata.annotations.meta\.helm\.sh/release-name}')"
MOKKA_NAMESPACE="$(kubectl get clusterrolebinding \
  control-plane.mokka.nvidia.com \
  -o jsonpath='{.metadata.annotations.meta\.helm\.sh/release-namespace}')"
test -n "$MOKKA_RELEASE"
test -n "$MOKKA_NAMESPACE"

kubectl --namespace "$MOKKA_NAMESPACE" rollout status deployment \
  --selector="app.kubernetes.io/component=control-plane,app.kubernetes.io/instance=$MOKKA_RELEASE" \
  --timeout=2m

kubectl delete sgpuinventories.mokka.nvidia.com \
  --all --wait=true --timeout=15m
```

The delete must complete successfully. It waits for the inventory finalizers;
those finalizers wait for every control-plane-owned rack and exact Node projection
to drain. If it times out, keep the control plane running with its RBAC permissions,
inspect the remaining finalizers and control-plane logs, and resolve the reported
cleanup conflict. Do not force-remove finalizers.

Verify the drain before changing the Helm release. The first command must
produce no objects. The second must produce no Node names: it checks the two
projection identity markers and any fields still owned by the
`mokka-controller` server-side apply manager. A clique-only label owned by
another manager is deliberately not treated as Mokka projection state.

```bash
kubectl get \
  sgpuinventories.mokka.nvidia.com,sgpuracks.mokka.nvidia.com \
  --no-headers \
  -o 'custom-columns=KIND:.kind,NAME:.metadata.name,FINALIZERS:.metadata.finalizers'

kubectl get nodes --show-managed-fields=true \
  -o go-template='{{range .items}}{{$node := .metadata.name}}{{if or (index .metadata.labels "mokka.nvidia.com/sgpu-assigned") (index .metadata.annotations "mokka.nvidia.com/sgpu-assignment")}}{{printf "%s\n" $node}}{{else}}{{range .metadata.managedFields}}{{if eq .manager "mokka-controller"}}{{printf "%s\n" $node}}{{end}}{{end}}{{end}}{{end}}'
```

Only after both checks are empty may the control plane be disabled or its
owning release uninstalled:

```bash
# Disable only the control plane; run from the repository root.
helm upgrade "$MOKKA_RELEASE" deployments/nvml-mock/helm/nvml-mock \
  --namespace "$MOKKA_NAMESPACE" --reuse-values \
  --set controlPlane.enabled=false

# Or uninstall the complete nvml-mock release instead.
helm uninstall "$MOKKA_RELEASE" --namespace "$MOKKA_NAMESPACE"
```

Profiles, runtime policies, and the retained CRDs may remain for a later
control-plane installation. To remove the API completely, uninstall the
`mokka-crds` Helm release and then explicitly delete all four retained CRDs,
but only after the drain above:

```bash
# Use the namespace where this release was installed.
MOKKA_CRD_NAMESPACE=default
helm uninstall mokka-crds --namespace "$MOKKA_CRD_NAMESPACE"
kubectl delete customresourcedefinitions.apiextensions.k8s.io \
  sgpuinventories.mokka.nvidia.com \
  sgpuracks.mokka.nvidia.com \
  sgpurackprofiles.mokka.nvidia.com \
  sgpuruntimepolicies.mokka.nvidia.com
```

Build the image locally with:

```bash
docker build -f deployments/control-plane/Dockerfile \
  -t REGISTRY/mokka-control-plane:TAG .
```

For local Tilt development, pass `--control-plane`; the control plane and its
CRDs are otherwise disabled and existing Tilt defaults are unchanged.

## Enable the KWOK data plane

Install the Mokka CRDs and provide a running KWOK controller and virtual nodes.
Set both flags in the nvml-mock chart values for a new installation.

```yaml
controlPlane:
  enabled: true
kwok:
  enabled: true
```

Helm starts one [KWOK data-plane](tools/mokka-kwok.md) Pod. The node daemon
excludes nodes labeled `mokka.nvidia.com/node-type=KWOK` from its placement.
The data plane watches rack assignments and bound Pods. GPU capacity
publication and per-Pod GPU allocation are future work.

## Declare capacity

Apply the [example profile](https://github.com/NVIDIA/k8s-test-infra/blob/main/examples/controlplane-crds/sgpu-rack-profile.yaml)
and [inventory](https://github.com/NVIDIA/k8s-test-infra/blob/main/examples/controlplane-crds/sgpu-inventory.yaml), then make a
Node eligible and match the example group selector:

```bash
kubectl apply -f examples/controlplane-crds/sgpu-rack-profile.yaml
kubectl apply -f examples/controlplane-crds/sgpu-inventory.yaml
kubectl label node NODE \
  mokka.nvidia.com/sgpu-node=true \
  mokka.nvidia.com/pool=example
```

Set `spec.rackGroups[].node.type` to `CPU` (the default) or `KWOK`.
Each generated rack records the value in `spec.nodeType`. To change the type,
create a group with a new ID. The API rejects `mokka.nvidia.com/node-type`
in placement selectors. The control plane applies the group selector and node type together.
A node with the `mokka.nvidia.com/node-type=KWOK` label or the
`kwok.x-k8s.io/node` annotation matches only KWOK groups. CPU groups also
accept nodes without a type label. KWOK racks carry the
`mokka.nvidia.com/kwok-shard` label. With KWOK mode enabled, the chart sets this label through the control
plane's shard-name flag. It uses the StatefulSet replica name,
`mokka-kwok-data-plane-0`. With a separately deployed data plane, use
`controlPlane.kwokShardName` to select the shard.

Only Nodes with `mokka.nvidia.com/sgpu-node=true` enter the control plane's cache.
An empty placement selector matches every eligible Node of the group's type.
A nonempty selector also requires the Node to match its labels. Placement selectors cannot reference
`mokka.nvidia.com/sgpu-assigned` or `nvidia.com/gpu.clique` because those labels
are derived from placement. Such an inventory is rejected without changing its
last materialized racks or bindings.

The supported topology envelope is 100,000 eligible Nodes, 100,000 generated
racks, and 64 declared rack groups across all `SGPUInventory` resources. A rack
group is a homogeneous declaration, not an individual rack: use its `count` to
expand one group into many racks. Inventories are admitted whole in creation
order while the aggregate limits fit. An inventory outside the envelope reports
`CapacityExceeded`; deleting or shrinking an older inventory allows the next
declaration to be admitted.

The durable assignment is `SGPURack.spec.nodes[].nodeRef`. Existing valid
bindings do not move when Nodes, racks, or profiles are added or edited. New
Nodes are ordered by creation time, name, then UID and fill logical Node
coordinates in rack/index order. GPU, serial, fabric, and rack identities derive from the
inventory UID and coordinate, so retries, restarts, and leader changes do not
change an unchanged coordinate.

For each successfully projected binding the control plane owns only:

- `mokka.nvidia.com/sgpu-assigned=true`;
- `nvidia.com/gpu.clique=<fabric UUID>.<clique ID>`;
- `mokka.nvidia.com/sgpu-assignment`, compact JSON containing exact inventory,
  rack, profile revision, coordinate, and Node UID data.

## Override runtime state

An `SGPURuntimePolicy` overrides the simulated runtime state of some GPUs in one
inventory: their health, device modes, and telemetry. A profile's
`defaults.runtime` sets the starting state of every GPU the profile renders, and
a policy changes only the fields it sets. An omitted field keeps the value it
would otherwise have, and an explicit zero is a value: `drawMilliWatts: 0` sets
the power draw to zero. Stage 1 evaluates policies and reports whether each is
accepted, but it does not apply them to simulated GPUs yet.

The [example policy](https://github.com/NVIDIA/k8s-test-infra/blob/main/examples/controlplane-crds/sgpu-runtime-policy.yaml)
marks the GPU on logical Node 1 of the example rack as failed and drops its
power draw to zero:

```bash
kubectl apply -f examples/controlplane-crds/sgpu-runtime-policy.yaml
```

`spec.targetRef` names the inventory and can narrow the selection with
`rackGroups`, `rackIndexes`, `nodeIndexes`, and `gpuIndexes`; an omitted list
selects every index on that axis. The deepest list sets the policy's scope:

| Deepest list in `targetRef` | Scope |
| --- | --- |
| none | Inventory |
| `rackGroups` | Rack group |
| `rackIndexes` | Rack |
| `nodeIndexes` | Node |
| `gpuIndexes` | GPU |

A GPU's effective state starts from its profile defaults and applies the
accepted policies that select it from the broadest scope to the narrowest, so a
narrower scope overrides a broader one field by field.

Every listed rack group must be declared by the inventory. When a policy selects
rack groups of different shapes, each listed index must exist in at least one of
them, and each rack group applies only the indexes it has. Node and GPU indexes
come from a rack group's profile, so a rack group whose profile is missing has
none.

Two policies conflict when they have the same scope, select at least one common
GPU, and set a common field. The policy with the older `creationTimestamp` stays
in force, with the UID breaking ties, and the other is rejected as a whole.
Deleting the winner accepts the oldest remaining policy.

The `Accepted` condition reports the outcome:

| Reason | Status | Meaning |
| --- | --- | --- |
| `Accepted` | `True` | The policy applies to its target. |
| `TargetNotFound` | `False` | The target inventory does not exist. |
| `InvalidTarget` | `False` | A listed rack group is not declared, or a listed index selects no GPU. |
| `Conflicted` | `False` | An older policy of the same scope sets one of its fields for some of the same GPUs. |

`kubectl get sgpuruntimepolicies` shows each policy's target and acceptance, and
the condition message explains a rejection:

```bash
kubectl get sgpuruntimepolicies
kubectl get sgpuruntimepolicy example-node-1-failed \
  -o jsonpath='{.status.conditions[?(@.type=="Accepted")].message}'
```

## Observe and troubleshoot

```bash
kubectl get sgpuinventories,sgpuracks,sgpuruntimepolicies
kubectl get sgpuinventory example -o yaml
kubectl get node NODE -o jsonpath='{.metadata.annotations.mokka\.nvidia\.com/sgpu-assignment}'
kubectl -n mokka logs -l app.kubernetes.io/component=control-plane
kubectl -n mokka get lease control-plane.mokka.nvidia.com -o yaml
```

Inventory conditions distinguish invalid input or profile references,
materialization failures, pending or conflicting placements, and projection
failures. A missing or invalid profile preserves the last materialized racks
but blocks new allocations for that group. Selector overlaps leave an
unassigned Node in conflict. Foreign rack ownership and incompatible Node
metadata are reported and never overwritten. A runtime policy's `Accepted`
condition names the reason it does not apply.

Shrinking capacity, deleting an inventory or rack, losing eligibility, and
replacing a Node UID remove the exact old projection before clearing a live
binding. Cleanup removes only control-plane-owned keys whose assignment annotation
still names that binding; incompatible values are preserved and retried. If the exact
Node UID no longer exists, cleanup may proceed without touching a same-name
replacement. The singleton ClusterRoleBinding ensures only the owning release
has cluster permissions. Within that release, its namespace-local Lease
prevents replicas from reconciling concurrently; cluster-wide Lease permissions
are unnecessary under the single-installation invariant. Every replica keeps a
synchronized informer view of profiles, inventories, racks, runtime policies,
eligible Nodes, and durable rack-slot assignments. Only the elected leader attaches reconciliation
handlers and runs workers. A standby reports ready after its caches synchronize
and it observes the elected leader; the leader additionally waits for its
handlers to replay the current caches and its workers to start. Upgrades use a
`RollingUpdate` Deployment strategy with `maxUnavailable: 0` and `maxSurge: 1`
to preserve serving overlap while leader election maintains single-writer mutation.

## Compiled rack state

The control plane can compare a rack's current inputs with its saved delivery
record. It compares the rack spec generation, the inventory and profile versions,
and the UIDs and generations of the accepted policies that select this rack.
For a declared rack group, the policy evaluation must use the same profile UID
and generation as the delivery record. If they differ, the caller must retry.
Deleting an accepted policy changes the input list even when another policy
supplies the same setting. Status updates alone leave the list unchanged.

The rack-state compiler builds one hardware description for the rack. It reads the
profile version pinned in `SGPURack.status.delivery` and checks its UID, generation,
and content revision. A missing delivery record or a mismatch prevents compilation.

The result contains the driver and CUDA versions, the GPU model, and the NVLink
fabric when the profile declares one. An active slot lists the node, its assignment
revision, and each GPU’s identity, PCI and NUMA locations, and runtime settings.
Each GPU inherits the profile defaults. Accepted policies override the fields they
set, from inventory scope down to GPU scope.

During release, the slot lists the node and assignment revision with
`releasing: true` and omits the GPU list. A cleanup applies only to the assignment
revision it captured. If the same node later returns to that slot, the new binding
keeps its GPUs.

A per-node reader can select one slot from the same result. The compiler runs in
memory.

The elected leader saves the input versions in `SGPURack.status.delivery`.
The first delivery has generation 1. Each changed rack gets a new generation
when its binding, profile, inventory version, or accepted policies change. The
leader writes the generation and inputs together with the rack status. A new
leader continues from the saved generation. The writer waits for admission of
the exact inventory UID and generation. A rejected edit leaves the previous
delivery in place. Readers can observe a new delivery after a watch reconnects
and skips intermediate edits, even when the final spec has the same values.
The writer retries if it sees an older generation of an input already in the
saved delivery.

The v2 node assignment format identifies the node UID, assignment revision,
rack and slot, and the minimum rack delivery generation for that binding.
A release record contains the node UID, its revision, and `assigned: false`.
The current projection writer still uses v1. Publication of v2 annotations
and the rack-state HTTP endpoint are separate steps.

## Stage 1 exclusions

Stage 1 has no agent or driver, workload GPU injection, REST API, Redis,
heartbeat-based reclamation, Node lease, or cross-rack switch graph. The only
Lease is Kubernetes leader election.
