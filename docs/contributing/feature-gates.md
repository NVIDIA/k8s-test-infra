# Gating New Behavior

Put a behavior behind a feature gate when it should ship before it becomes part
of Mokka's default contract. Mokka uses the Kubernetes
[`component-base` feature gate](https://pkg.go.dev/k8s.io/component-base/featuregate)
through `internal/features`, the way Kubernetes components, Cluster API and
Kueue do. See [Feature Gates](../feature-gates.md) for how operators set gates
and what each stage means.

## Gate or chart value

A feature gate is a temporary escape hatch for a behavior inside a running
service, removed once the behavior is stable. A permanent choice, such as
whether the chart deploys a component (`nri.enabled`) or which mode it runs in
(`nri.deviceInjectionMode`), is a chart value instead: the chart decides what
to render, and a gate inside a binary cannot.

## Add a gate

Declare the gate and its first stage in `internal/features/features.go`. The
name is PascalCase and describes the behavior, not its implementation:

```go
const (
    // owner: @github-handle
    // issue: https://github.com/NVIDIA/k8s-test-infra/issues/1234
    //
    // Selects the alternate placement planner.
    AlternatePlanner featuregate.Feature = "AlternatePlanner"
)

var versionedSpecs = map[featuregate.Feature]featuregate.VersionedSpecs{
    AlternatePlanner: {
        {Version: version.MajorMinor(0, 5), Default: false, PreRelease: featuregate.Alpha},
    },
}
```

`Version` is the Mokka minor release that introduces the stage.

In the same pull request:

- Add the gate to `featureGates.properties` in the chart's
  `values.schema.json` as `{ "type": "boolean" }`.
  `TestChartSchemaListsRegisteredGates` fails until the schema matches the
  registered gates.
- List the gate under
  [Available gates](../feature-gates.md#available-gates).
- Add an `Added` [changelog fragment](pull-requests.md#changelog-entries) that
  names the gate.

## Check a gate

Only `node-agent`, `nri-plugin` and `control-plane` apply gates.
`nvml-mock-ctl`, which also runs as the chart's `allocation-watcher` sidecar,
and the mock NVML and CUDA libraries loaded into workloads never do:
`features.Enabled` there always returns the gate's default, without an error.
The packages under `pkg/gpu/`, and the internal packages they import such as
`internal/gpuarch`, run in those processes too, so they must not call
`features.Enabled`.

Read the gate in the service that owns the behavior and pass the result down
as a value, for example as a field of the configuration the service builds at
startup:

```go
cfg.Planner = planner.Current
if features.Enabled(features.AlternatePlanner) {
    cfg.Planner = planner.Alternate
}
```

Behavior inside the mock libraries needs a service to hand over the decision
the same way, for example through the configuration the library reads. A
package that runs only inside one service, such as `internal/agent`,
`internal/nri` or `internal/controlplane`, may instead check the gate at the
point where the old and new behavior diverge. To check a package, make sure
`go list -deps ./cmd/nvml-mock-ctl ./pkg/gpu/mocknvml/bridge` does not list
it.

Do not read flags, environment variables or Helm values for a gate;
`internal/features` owns them, and the three services apply them the same way.

## Test both states

Because most code receives the decision as a value, most tests need no gate at
all. Where a test must flip the process-wide gate, use
`features.SetFeatureGateDuringTest`, which restores the previous value when the
test ends:

```go
func TestAlternatePlanner(t *testing.T) {
    features.SetFeatureGateDuringTest(t, features.AlternatePlanner, true)
    // ...
}
```

A test that sets a gate must not call `t.Parallel()`. Never pass a value the
gate rejects to `features.ConfigureFromCLI` in a test: the process-wide gate
keeps the rejected entry, and every later change to it in the same test binary
fails. Test rejection on a fresh gate, as `internal/features` does.

## Change a gate's stage

Append a spec for the release that changes the stage; keep the earlier ones as
history:

```go
AlternatePlanner: {
    {Version: version.MajorMinor(0, 5), Default: false, PreRelease: featuregate.Alpha},
    {Version: version.MajorMinor(0, 6), Default: true, PreRelease: featuregate.Beta},
    {Version: version.MajorMinor(0, 7), Default: true, PreRelease: featuregate.GA, LockToDefault: true},
},
```

- A GA (generally available) gate sets `LockToDefault: true`, so the behavior
  can no longer be turned off.
- A deprecated gate defaults to `false`. Set `LockToDefault: true` once the
  behavior can no longer be turned back on.
- A gate that was locked stays locked, so a GA gate is removed, not
  deprecated.
- When a gate becomes locked, change its schema entry to
  `{ "const": <default> }`, so Helm rejects an override before rollout.
- Update the gate's entry under Available gates and add a changelog fragment:
  `Changed` for a promotion, `Deprecated` when the gate is deprecated.

A gate that retires behavior which never had a gate starts with an unlocked
`version.MajorMinor(0, 0)` entry recording that behavior:

```go
LegacyPlanner: {
    {Version: version.MajorMinor(0, 0), Default: true, PreRelease: featuregate.GA},
    {Version: version.MajorMinor(0, 6), Default: false, PreRelease: featuregate.Deprecated},
},
```

Without it, component-base rejects a first spec that is deprecated at a
version such as 0.6; its error asks for a "1.0 entry", which for Mokka's 0.x
releases means 0.0.

## Remove a gate

Remove a GA or deprecated gate once the
[removal policy](../feature-gates.md#stages) allows it; an alpha gate can go in
any release. Nothing checks the policy yet; the first gate to reach GA should
add a test that compares its stage version with the chart's `appVersion`.
Delete the constant, its specs, its schema entry, the branch that can no longer
run, the tests of the other state, and its entry under Available gates. Add a
`Removed` changelog fragment, so operators drop the gate from their values
before the upgrade that rejects it.

## Related

| To read about | See |
|---|---|
| Setting gates and what each stage means | [Feature Gates](../feature-gates.md) |
| Running the tests | [Testing](testing.md) |
| Writing the changelog fragment | [Pull Requests](pull-requests.md#changelog-entries) |
