# Mokka controller Stage 1 implementation plan

## Scope

Stage 1 materializes static simulated GPU capacity in Kubernetes:

```text
SGPUProfile + SGPUInventory -> SGPURack -> Node metadata
                                      \-> aggregate status
```

It delivers cluster-scoped `SGPUProfile`, `SGPUInventory`, and controller-owned
`SGPURack` resources; stable placement of eligible Kubernetes Nodes into rack
slots; deterministic GPU and rack-fabric identities; rack-local GPU, PCIe,
NUMA, fabric, and network topology; Node labels and annotations; and inventory
and rack status.

Redis, an agent REST API, heartbeats or leases for Nodes, runtime samples,
`SGPURuntimePolicy` evaluation, cross-rack switch graph generation, and any
Mokka node-agent or mock-driver change are explicitly out of scope. The only
Lease in this stage is the standard Kubernetes leader-election Lease.

## API contract

Add `mokka.nvidia.com/v1alpha1` under `pkg/apis/mokka/v1alpha1`. All three
kinds are cluster scoped.

### SGPUProfile

`SGPUProfile.spec` contains the static portions of the MEP profile:

- `rack.nodesPerRack`;
- `node.gpus` hardware template and count, optional host characteristics, and
  `topology.gpuSlots`, `gpuFabric`, and `network`;
- the driver, NVML, and CUDA versions in `software`.

`defaults.runtime` is not added in Stage 1. `gpuSlots` is an associative list
keyed by `index`; indexes must be contiguous from zero, its length must equal
`node.gpus.count`, PCI addresses must be canonical and unique within a Node,
and all counts and quantities must be positive. OpenAPI/CEL enforces local
bounds; the controller rejects cross-field failures or a rendered rack spec
larger than 1 MiB through the referencing inventory's conditions.
Profiles are mutable. A spec revision is the SHA-256 of its canonical JSON.

### SGPUInventory

`SGPUInventory.spec.rackGroups` is an associative list keyed by `id`, with at
most 64 entries. Each entry has a DNS-label `id`, non-negative `count`, a
same-cluster `profileRef.name`, and an optional Kubernetes `LabelSelector` at
`placement.nodeSelector`. Zero is valid so a group can be drained without
removing its declaration.

`status` has `observedGeneration`, aggregate `capacity`, `usage`, per-group
copies of both, and standard map-list conditions. Capacity contains rack,
Node-slot, and GPU totals. Usage contains `requestedNodes`, `allocatedNodes`,
`availableNodes`, `pendingNodes`, `conflictingNodes`, and `projectedNodes`.
At inventory level, requested Nodes are the distinct live eligible Nodes that
match at least one of its groups; per-group requested counts may overlap.
Pending Nodes have one global placement candidate but no free slot. Conflicting
Nodes have more than one candidate and are not assigned. Available Nodes are
desired slots minus live allocations.

Conditions are `Accepted`, `ResolvedRefs`, `Materialized`,
`RequestsSatisfied`, and `NodesProjected`. Every condition carries the current
observed generation and changes `lastTransitionTime` only when status or reason
changes. Missing or invalid profiles make `ResolvedRefs=False`; capacity only
includes resolved groups. Status writes occur only when the semantic value
changes.

### SGPURack

`SGPURack` is controller generated. Its deterministic name is a readable,
length-limited inventory/group/index prefix followed by the first 12 hex
characters of SHA-256(`inventory UID`, `group id`, `rack index`); the hash is
always retained when truncating. It has a controller owner reference to the
exact inventory UID and labels/annotations for indexed lookup.

Its spec contains:

- `inventoryRef{name,uid}` and
  `profileRef{name,uid,generation,revision}`;
- `identity{rackGroup,rackIndex,fabricUUID,cliqueID}`;
- the rendered rack-local fabric and network topology; and
- an associative `slots` list keyed by index. Every slot exists even when
  free, has an optional `nodeRef{name,uid}`, and has an associative `gpus`
  list containing `index`, UUID, serial, minor number, PCI address, root
  complex, and NUMA/host-processor indexes.

The materializer derives identities with UUIDv5 from a fixed, versioned Mokka
namespace and logical coordinates, never from Node name, event order, or wall
clock. Fabric UUID uses (`inventory UID`, group, rack); its clique ID is zero,
so all Nodes in one rack share `<fabricUUID>.0`. GPU UUID and serial use
(`inventory UID`, group, rack, slot, GPU); minor number equals GPU index and
PCI/NUMA placement comes from the profile. Consequently a retry, controller
restart, rack recreation, profile edit, or Node replacement produces the same
identity for an unchanged coordinate. Recreating the inventory produces a new
identity domain.

Rack status has `observedGeneration`, assigned and projected slot counts, and
`Ready` and `NodesProjected` conditions. Conditions and spec lists use
Kubernetes map-list semantics. Controller-owned racks carry a finalizer until
their Node projections are removed.

## Placement and projection invariants

A Node is eligible only while it has
`mokka.nvidia.com/sgpu-node=true`. An empty group selector matches every
eligible Node; a non-empty selector must also match. A binding in
`SGPURack.spec.slots[].nodeRef` is the durable source of truth. Node metadata is
derived and never used to reconstruct a missing binding.

Existing bindings remain fixed across Node additions, informer relists,
controller restarts, capacity growth, and profile changes. A binding is
released only when its exact Node UID no longer exists, that Node stops being
eligible or matching its group, its slot is removed by shrink/deletion, or an
operator explicitly deletes its generated rack. The allocator never compacts
holes or rebalances a valid binding. It sorts only newly pending Nodes by
creation timestamp, name, and UID and fills free coordinates by rack index then
slot index.

An unassigned Node matching multiple rack groups, including groups in different
inventories, remains unassigned and is reported as a placement conflict. An
existing valid binding wins over a newly introduced overlapping selector. Two
rack slots referencing the same Node UID are a data conflict: neither is
projected or automatically discarded, and both inventories report it.

The projection uses server-side apply with field manager `mokka-controller`
and never forces ownership. It owns only:

- label `mokka.nvidia.com/sgpu-assigned=true`;
- label `nvidia.com/gpu.clique=<fabricUUID>.<cliqueID>` when the profile has a
  GPU fabric; and
- annotation `mokka.nvidia.com/sgpu-assignment`, a versioned compact JSON value
  containing inventory/rack/profile names and UIDs, profile revision, rack
  group/index, slot index, and Node UID.

Removing a projection deletes only those keys and only when the assignment
annotation still names the binding being removed. A field-ownership conflict
or a pre-existing incompatible value is preserved, surfaced as
`NodeMetadataConflict`, and retried with rate limiting; it is never overwritten
with force.

## Reconciliation behavior

The controller first validates an inventory and resolves every profile. It
does not mutate racks for an invalid inventory. If a profile disappears or is
invalid, it retains the last materialized racks and bindings for that group,
stops new allocations there, and reports `ResolvedRefs=False`. Recreating a
profile with the same name refreshes its UID and revision while preserving
valid slot bindings and coordinate-derived identities.

For each resolved group, rack reconciliation creates or applies exactly indexes
`[0,count)`, preserving `nodeRef` for unchanged slots and updating rendered
profile data. Profile GPU-count changes retain identities for surviving GPU
indexes. A node-per-rack shrink retires high slots; a rack-count shrink retires
high rack indexes. Retiring slots first remove their Node projection, then clear
the exact-UID binding. Released, still-eligible Nodes re-enter normal allocation
and may fill an already-free surviving slot; bindings in surviving slots never
move. A rack is deleted only after it is empty and its finalizer can be removed.

Inventory deletion is guarded by an inventory finalizer. Reconciliation stops
allocation, cleans projections, empties and deletes owned racks, then removes
the finalizer. Owner references remain a safety net, not the cleanup mechanism.
Manual rack deletion follows the rack finalizer path and the desired rack is
recreated while its inventory still requests it.

A Node delete clears only slots with its old UID. A same-name Node with a new
UID is a new candidate and cannot inherit the old binding or identities; stale
metadata is removed only if its annotation still records the old assignment.
Loss of the eligibility label is delivered as a filtered-watch delete and uses
the same release path.

All rack and status updates use resource-version preconditions and retry normal
API conflicts. Server-side apply does not adopt a deterministic rack name owned
by another UID and does not force a user-owned spec field; the inventory reports
`RackOwnershipConflict`. Deletion, shrink, and finalizer progress wait for Node
projection cleanup unless the exact Node UID is gone. NotFound is success only
for the exact object being removed.

## Controller architecture for 100k Nodes

Use client-go, generated typed clients/listers/informers, and typed
rate-limiting workqueues; do not add controller-runtime. Start workers only
after all caches sync, use zero periodic resync, and seed queues from informer
add events on every restart.

The Node ListWatch is server-side filtered to
`mokka.nvidia.com/sgpu-node=true`, so unrelated Nodes are not cached. Shared
informers index inventories by referenced profile, racks by inventory UID and
group, and rack slots by Node UID and name. Inventory changes compile selectors
into an in-memory placement registry. A Node event evaluates cached selectors
without an API list. A selector change reevaluates the already-cached eligible
Nodes once and queues only affected group keys.

Use separate queues for inventory/rack materialization, allocation-group keys,
Node projection, and aggregate status. Coalescing group keys lets one worker
batch pending Nodes into rack updates instead of producing one rack read/write
cycle per Node. Work is keyed, idempotent, and may run concurrently across
groups; resource versions protect the remaining cross-queue races. Events
enqueue dependent keys through indexes rather than scanning all racks or
listing all Nodes. Status is debounced per inventory.

Only the elected replica starts informers and workers. Use
`client-go/tools/leaderelection` with the namespaced Lease
`mokka-controller.mokka.nvidia.com`, release on cancellation, and defaults of
15-second lease duration, 10-second renew deadline, and 2-second retry period.
SIGTERM cancels workers and waits for them before releasing leadership.

## Concrete files and packages

- `pkg/apis/mokka/v1alpha1/{doc.go,register.go,types_profile.go,types_inventory.go,types_rack.go}`:
  public types and validation markers.
- `pkg/generated/{clientset,informers,listers}` and
  `pkg/apis/mokka/v1alpha1/zz_generated.deepcopy.go`: generated code; add a
  pinned `hack/update-mokka-codegen.sh` and Makefile generate/verify targets.
- `deployments/mokka-crds/helm/mokka-crds/{Chart.yaml,crds/*.yaml}`: generated,
  separately installable CRDs as required by the MEP.
- `pkg/mokka/materialize/{materialize.go,identity.go,naming.go}`: pure profile
  validation, revision, rack rendering, and identity functions.
- `pkg/mokka/allocate/{allocator.go,selectors.go}`: pure binding validation,
  candidate/conflict classification, stable retention, and free-slot filling.
- `internal/mokkacontroller/rack/controller.go`: inventory/profile/rack
  reconciliation, ownership, shrink, and finalizers.
- `internal/mokkacontroller/projection/controller.go`: exact-UID Node metadata
  apply and cleanup.
- `internal/mokkacontroller/status/controller.go`: cached rack/Node aggregation
  and conditions.
- `internal/mokkacontroller/controller.go`: informer indexes, queues, event
  routing, cache synchronization, and worker lifecycle.
- `cmd/mokka-controller/{main.go,options.go}`: kubeconfig/in-cluster setup,
  flags, probes, signals, and leader election.
- `deployments/mokka-controller/{Dockerfile,helm/mokka-controller/**}`:
  controller image and chart; `local/mokka-controller/mokka-controller.tiltfile`
  adds opt-in Tilt installation.
- `docs/mokka-controller.md` and a minimal example profile/inventory: Stage 1
  installation, placement, projected metadata, status, and failure semantics.

Tests live beside each package. Use `testify/require` for assertions. Generated
CRD golden/schema tests cover scope, structural schemas, CEL, map-list markers,
status subresources, and defaulting. Pure unit tests cover canonical revisions,
identity test vectors, name truncation, stable allocation, overlaps, shrink,
and UID replacement. Client-go fake/informer tests cover event-to-key routing,
indexes, retries, finalizers, and semantic status suppression. API-server
integration tests cover status subresources, owner references, SSA conflicts,
and exact metadata cleanup. A `BenchmarkAllocator100kNodes` and informer test
must demonstrate bounded memory, no quadratic reconciliation, no post-sync Node
List calls for a single Node event, and writes limited to affected racks/Nodes.

The controller chart grants only: get/list/watch on profiles; get/list/watch,
patch/update for finalizers, and patch/update on status for inventories;
get/list/watch/create/patch/update/delete plus patch/update on status for racks;
get/list/watch/patch on Nodes; create/get/update on the leader-election Lease in
the release namespace; and create/patch/update on Events.
It includes ServiceAccount, ClusterRole, ClusterRoleBinding, a two-replica
Deployment, probes, resource requests, security context, and leader-election
flags. Helm unit/snapshot tests verify RBAC and rendered arguments.

## Acceptance criteria

- Applying one valid profile and inventory creates the exact deterministic rack
  set, topology, free slots, identities, owner references, and ready status.
- Eligible Nodes fill capacity once; retries, restarts, new lower-sorted Nodes,
  profile edits, and leader failover do not move valid bindings or change their
  identities.
- Capacity exhaustion reports pending Nodes without over-allocation. Selector
  overlap, foreign rack ownership, duplicate bindings, and Node SSA ownership
  conflicts are visible and never resolved by an arbitrary overwrite.
- Node deletion, eligibility loss, same-name/UID replacement, rack and slot
  shrink, manual rack deletion, and inventory deletion follow the cleanup rules
  above without leaving controller-owned Node metadata.
- Inventory and rack status exactly match cached desired capacity, live exact-UID
  allocations, pending/conflicting Nodes, and successful projections.
- Two controller replicas elect one writer and fail over without assignment
  churn. The synthetic 100k-Node benchmark and request-count assertions pass.
- CRDs install before the controller, the least-privilege chart passes Helm
  tests, and a Kind integration test covers create, allocate, project, shrink,
  UID replacement, status, deletion, and leader failover.
- No Stage 1 binary, API, manifest, or test adds Redis, an agent-facing server,
  Node liveness reclamation, runtime-policy logic, or node-agent/mock changes.

## Subsequent implementation steps

Each step starts with failing tests for its contract and leaves generation,
unit tests, race tests, lint, and vendor checks green before the next step.

1. **API/CRDs:** add the three types, generated code, CRD chart, schema/golden
   tests, and generation verification.
2. **Pure materializer/allocator:** add deterministic naming, revision and
   identity test vectors, profile-to-rack rendering, selector classification,
   stable allocation, shrink, conflict, and 100k-Node benchmark tests and code.
3. **Rack reconciliation:** add inventory/profile/rack informers and indexes,
   rack create/update/retirement, optimistic conflict handling, owner references,
   and inventory/rack deletion finalizers with fake-client tests.
4. **Node projection/status:** add exact-UID binding release, non-forced SSA
   projection and cleanup, cached aggregate status/conditions, debouncing, and
   API-server integration tests for conflicts and deletion paths.
5. **Controller wiring/executable:** assemble shared informers and queues, add
   cache-sync and shutdown behavior, flags/probes, leader election, executable
   tests, and Makefile build/test targets.
6. **Deployment/docs/integration:** add the controller image, CRD and controller
   charts with RBAC and Helm tests, opt-in Tilt wiring, concise docs/examples,
   and the Kind lifecycle/leader-failover acceptance scenario.
