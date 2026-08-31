<!-- SPDX-License-Identifier: Apache-2.0 -->
<!-- SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION -->

# Mokka controller

The Stage 1 controller turns `SGPURackProfile` and `SGPUInventory` resources into
controller-owned `SGPURack` resources, assigns eligible Kubernetes Nodes to
logical rack Nodes,
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
  --set-string controlPlane.image.digest=sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
```

Replace the example digest with the immutable `sha256:...` digest published
for the controller image.

Uninstalling the `mokka-crds` release retains the CRDs and existing Mokka
resources. Removing the Mokka API and its resources requires deleting the CRDs
explicitly.

Only one Helm release may enable the cluster-wide control plane in a cluster.
Its fixed `mokka-control-plane.mokka.nvidia.com` ClusterRoleBinding is owned by
that release and acts as the singleton guard: another release must wait until
the owner is uninstalled. Upgrades and rollbacks of the owning release keep
using the same guard.

## Disable or uninstall safely

Do not disable `controlPlane.enabled`, uninstall its `nvml-mock` release, or
delete the Mokka CRDs while an `SGPUInventory` exists. Disabling or uninstalling
removes the controller Deployment and RBAC; deleting the CRDs removes its API
objects. Either order prevents the controller from releasing the
`mokka.nvidia.com/inventory-cleanup` and `mokka.nvidia.com/rack-cleanup`
finalizers or removing its Node projection metadata. The CRD chart retains
every CRD on uninstall, so removing either Helm release is not a cleanup
mechanism.

Drain the controller before removing it. All Mokka custom resources and Nodes
are cluster-scoped; the commands intentionally omit `--namespace` for them and
discover the namespace of the one owning Helm release from its singleton
ClusterRoleBinding.

```bash
MOKKA_RELEASE="$(kubectl get clusterrolebinding \
  mokka-control-plane.mokka.nvidia.com \
  -o jsonpath='{.metadata.annotations.meta\.helm\.sh/release-name}')"
MOKKA_NAMESPACE="$(kubectl get clusterrolebinding \
  mokka-control-plane.mokka.nvidia.com \
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
those finalizers wait for every controller-owned rack and exact Node projection
to drain. If it times out, keep the controller and RBAC running, inspect the
remaining finalizers and controller logs, and resolve the reported cleanup
conflict. Do not force-remove finalizers.

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
controller installation. To remove the API completely, uninstall the
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

For local Tilt development, pass `--control-plane`; the controller and its
CRDs are otherwise disabled and existing Tilt defaults are unchanged.

## Declare capacity

Apply the [example profile](https://github.com/NVIDIA/k8s-test-infra/blob/main/examples/mokka-controller/sgpu-rack-profile.yaml)
and [inventory](https://github.com/NVIDIA/k8s-test-infra/blob/main/examples/mokka-controller/sgpu-inventory.yaml), then make a
Node eligible and match the example group selector:

```bash
kubectl apply -f examples/mokka-controller/sgpu-rack-profile.yaml
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

For each successfully projected binding the controller owns only:

- `mokka.nvidia.com/sgpu-assigned=true`;
- `nvidia.com/gpu.clique=<fabric UUID>.<clique ID>`;
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
prevents replicas from reconciling concurrently; cluster-wide Lease permissions
are unnecessary under the single-installation invariant. Standbys report ready
after observing the elected leader. The leader reports ready after its informer
caches synchronize, so multi-replica rollouts converge without allowing standby
mutation. Upgrades retain a `Recreate` Deployment strategy for deterministic
whole-generation replacement.

## Stage 1 exclusions

Stage 1 has no runtime policy evaluation, agent or driver, workload GPU
injection, REST API, Redis, heartbeat-based reclamation, Node lease, or
cross-rack switch graph. The only Lease is Kubernetes leader election.
