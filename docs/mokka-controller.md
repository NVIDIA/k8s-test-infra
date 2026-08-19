# Mokka controller

The Stage 1 controller turns `SGPUProfile` and `SGPUInventory` resources into
controller-owned `SGPURack` resources, assigns eligible Nodes to rack slots,
and projects the assignment onto those Nodes. It models static capacity and
topology; it does not provide GPUs to workloads by itself.

## Install

Install the CRDs, then enable the control plane in the existing chart:

```bash
helm upgrade --install mokka-crds deployments/mokka-crds/helm/mokka-crds
helm upgrade --install nvml-mock deployments/nvml-mock/helm/nvml-mock \
  --namespace mokka --create-namespace \
  --set controlPlane.enabled=true \
  --set controlPlane.image.repository=REGISTRY/mokka-control-plane \
  --set controlPlane.image.tag=TAG
```

Only one Helm release may enable the cluster-wide control plane in a cluster.
Its fixed `mokka-control-plane.mokka.nvidia.com` ClusterRoleBinding is owned by
that release and acts as the singleton guard: another release must wait until
the owner is uninstalled. Upgrades and rollbacks of the owning release keep
using the same guard.

Build the image locally with:

```bash
docker build -f deployments/mokka-control-plane/Dockerfile \
  -t REGISTRY/mokka-control-plane:TAG .
```

For local Tilt development, pass `--control-plane`; the controller and its
CRDs are otherwise disabled and existing Tilt defaults are unchanged.

## Declare capacity

Apply the [example profile](../examples/mokka-controller/sgpu-profile.yaml)
and [inventory](../examples/mokka-controller/sgpu-inventory.yaml), then make a
Node eligible and match the example group selector:

```bash
kubectl apply -f examples/mokka-controller/sgpu-profile.yaml
kubectl apply -f examples/mokka-controller/sgpu-inventory.yaml
kubectl label node NODE \
  mokka.nvidia.com/sgpu-node=true \
  mokka.nvidia.com/pool=example
```

Only Nodes with `mokka.nvidia.com/sgpu-node=true` enter the controller cache.
An empty group selector matches every eligible Node; otherwise both eligibility
and the selector must match. Placement selectors cannot reference
`mokka.nvidia.com/sgpu-assigned` or `nvidia.com/gpu.clique` because those labels
are derived from placement. Such an inventory is rejected without changing its
last materialized racks or bindings.

The durable assignment is `SGPURack.spec.slots[].nodeRef`. Existing valid
bindings do not move when Nodes, racks, or profiles are added or edited. New
Nodes are ordered by creation time, name, then UID and fill rack/slot
coordinates in order. GPU, serial, fabric, and rack identities derive from the
inventory UID and coordinate, so retries, restarts, and leader changes do not
change an unchanged coordinate.

For each successfully projected binding the controller owns only:

- `mokka.nvidia.com/sgpu-assigned=true`;
- `nvidia.com/gpu.clique=<fabric UUID>.<clique ID>` when the profile has a GPU fabric;
- `mokka.nvidia.com/sgpu-assignment`, compact JSON containing exact inventory,
  rack, profile revision, coordinate, and Node UID data.

## Observe and troubleshoot

```bash
kubectl get sgpuinventories,sgpuracks
kubectl get sgpuinventory example -o yaml
kubectl get node NODE -o jsonpath='{.metadata.annotations.mokka\.nvidia\.com/sgpu-assignment}'
kubectl -n mokka logs -l app.kubernetes.io/component=control-plane
kubectl -n mokka get lease mokka-control-plane.mokka.nvidia.com -o yaml
```

Inventory conditions distinguish invalid input or profile references,
materialization failures, pending or conflicting placements, and projection
failures. A missing or invalid profile preserves the last materialized racks
but blocks new allocations for that group. Selector overlaps leave an
unassigned Node in conflict. Foreign rack ownership and incompatible Node
metadata are reported and never overwritten.

Shrinking capacity, deleting an inventory or rack, losing eligibility, and
replacing a Node UID remove the exact old projection before clearing a live
binding. Cleanup removes only controller keys whose assignment annotation still
names that binding; incompatible values are preserved and retried. If the exact
Node UID no longer exists, cleanup may proceed without touching a same-name
replacement. The singleton ClusterRoleBinding ensures only the owning release
has cluster permissions. Within that release, its namespace-local Lease
prevents rolling-update replicas from reconciling concurrently; cluster-wide
Lease permissions are unnecessary under the single-installation invariant.

## Stage 1 exclusions

Stage 1 has no runtime policy evaluation, agent or driver, workload GPU
injection, REST API, Redis, heartbeat-based reclamation, Node lease, or
cross-rack switch graph. The only Lease is Kubernetes leader election.
