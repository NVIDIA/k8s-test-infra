# Mokka controller implementation plan

## Scope

Reconcile `SGPUProfile` and `SGPUInventory` into controller-owned `SGPURack`
objects, durable exact-UID Node bindings, compact Node assignment metadata, and
the aggregate status fields defined by the current `v1alpha1` API. Kubernetes
objects are authoritative; runtime-policy evaluation, agent state, Redis, and
driver changes are out of scope.

## Steps

1. Generate typed clients, listers, and zero-resync informers from
   `internal/controlplane/api/v1alpha1` and add pure deterministic materializer
   and allocator packages with red-green unit tests.
2. Add one informer-derived Node catalog with immutable shared records,
   by-name/by-UID/by-label indexes, cached sorted snapshots, exact-UID deletes,
   indexed selector candidates, and 100k-Node scale contracts.
3. Reconcile bounded inventory/rack group work: validate profile references,
   materialize stable identities, preserve valid bindings, and stage safe
   shrink, recreation, and deletion cleanup without adopting foreign objects.
4. Project only controller-owned labels and the compact assignment annotation
   with non-forced server-side apply. Coalesce idempotent rack and inventory
   status writes from informer snapshots.
5. Wire shared informers, typed rate-limiting queues, cache readiness, graceful
   shutdown, and Lease leader election into `mokka-control-plane`; add focused
   lifecycle, restart, UID-replacement, cleanup, and race tests.
6. Add the minimum RBAC/deployment, Tilt, examples, and concise operator docs;
   then run generation, formatting/lint, race tests, the full suite, Helm tests,
   vendor checks, and scale benchmarks.

## Scale and safety contracts

- Cache only eligible Nodes and rebuild all local state after restart.
- Keep unchanged bindings stable and allocate pending Nodes in deterministic
  creation-time/name/UID and rack/slot order.
- Use indexes for event fan-out and selector candidates; avoid Node or rack
  API lists during reconciliation.
- Treat UIDs as object identity for release and cleanup, and never force field
  ownership or overwrite incompatible Node metadata.
- Bound status payloads to API aggregate fields and suppress semantic no-op
  writes.
