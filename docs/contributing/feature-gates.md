---
title: Feature Gates
---

# Feature Gates

Feature gates let an incomplete or transitional behavior ship without making it
part of Mokka's default contract. They are process-wide startup settings: Mokka
reads them before starting any workers, and changing them requires a restart.

The current release defines the mechanism but registers no gates. Supplying any
gate name therefore fails startup. Once a gate is registered, its tracking issue
and release metadata are the authoritative record of what it controls.

## Configure gates

Every long-running Mokka binary accepts the same comma-separated setting:

```bash
node-agent start --feature-gates=mokka.example,-mokka.enabledByDefault
```

An unprefixed name or a `+` prefix enables a gate. A `-` prefix disables it.
The equivalent environment variable is `MOKKA_FEATURE_GATES`:

```bash
MOKKA_FEATURE_GATES=mokka.example,-mokka.enabledByDefault node-agent start
```

The Helm chart keeps separate lists because each service registers only the
gates for behavior it contains:

```yaml
featureGates:
  nodeAgent:
    - mokka.example
    - "-mokka.enabledByDefault"
  nri: []
  controlPlane: []
```

Unknown identifiers, empty entries, and lifecycle-invalid overrides stop the
affected process instead of silently choosing a state. Helm also rejects
malformed and duplicate entries before rendering workloads.

## Lifecycle

| Stage | Default | Allowed override | Exit condition |
|-------|---------|------------------|----------------|
| `alpha` | Disabled | Enable or disable | Promote to beta or deprecate |
| `beta` | Enabled | Enable or disable | Promote to stable, or return to alpha before deprecating |
| `stable` | Enabled | May not be disabled | Remove the gate at `ToVersion`; keep the behavior |
| `deprecated` | Disabled | May not be enabled | Remove the gate and behavior at `ToVersion` |

Stable and deprecated gates are temporary compatibility handles, not permanent
configuration. Their definitions must name the version in which the gate will
be removed.

## Put behavior behind a gate

Declare the gate in the package that owns the behavior. Use a namespaced,
positive identifier that describes the behavior rather than its implementation:

```go
var alternatePlanner = featuregate.MustRegister(featuregate.Definition{
    ID:           "mokka.alternatePlanner",
    Description:  "selects the alternate placement planner",
    Stage:        featuregate.StageAlpha,
    FromVersion:  "v0.9.0",
    ReferenceURL: "https://github.com/NVIDIA/k8s-test-infra/issues/1234",
})
```

Query the returned handle at the narrow boundary where the old and new behavior
diverge:

```go
if alternatePlanner.IsEnabled() {
    return planWithAlternateStrategy(input)
}
return planWithCurrentStrategy(input)
```

Do not read CLI arguments, environment variables, or Helm values in feature
code. The shared registry owns those inputs, which keeps the behavior testable
and makes all binaries reject invalid settings consistently.

Add tests for both states. Prefer passing the selected behavior into lower-level
code so package tests do not mutate the process-wide registry. Also add the new
identifier, stage, default, and tracking issue to release notes while the gate
exists.

When advancing a gate:

1. Change its `Stage` and test the new default.
2. For `stable` or `deprecated`, set `ToVersion` and remove any now-unreachable
   branch at that version.
3. Remove the definition, tests for overrides, and release-note entry when the
   gate expires.

Gate metadata is validated at registration. Invalid IDs, missing descriptions,
versions or reference URLs, duplicate registrations, and missing removal
versions panic during process initialization so they cannot ship unnoticed.

## See also

- [Testing](testing.md) — repository test commands
- [Installation](../helm-chart.md) — chart configuration
- [Command-line tools](../tools/README.md) — service command references
