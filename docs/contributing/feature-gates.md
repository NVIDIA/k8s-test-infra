---
title: Gating New Behavior
---

# Gating New Behavior

Put an incomplete or transitional behavior behind a feature gate when it should
ship before it becomes part of Mokka's default contract. See
[Feature Gates](../feature-gates.md) for how operators configure gates and what
each lifecycle stage allows.

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
code so package tests do not mutate the process-wide registry.

In the same pull request, list the gate under
[Available gates](../feature-gates.md#available-gates) with its identifier,
component, stage, default, and tracking issue, and add an `Added`
[changelog fragment](pull-requests.md#changelog-entries) naming the gate.

## Change a gate's stage

1. Change its `Stage`, test the new default, and update its row under
   Available gates.
2. For `stable` or `deprecated`, set `ToVersion` to the release that removes
   the gate.
3. Add a changelog fragment: `Changed` for a promotion or a return to alpha,
   `Deprecated` when the gate becomes deprecated.

## Remove a gate

At `ToVersion`, delete the definition, the branch that is no longer reachable,
the tests for overrides, and the gate's row under Available gates. Add a
`Removed` changelog fragment, because the identifier stops being accepted and
any deployment still setting it fails to start.

Gate metadata is validated at registration. Invalid IDs, missing descriptions,
versions or reference URLs, duplicate registrations, and missing removal
versions panic during process initialization so they cannot ship unnoticed.

## See also

- [Feature Gates](../feature-gates.md) — configuration and lifecycle
- [Testing](testing.md) — repository test commands
- [Pull requests](pull-requests.md) — changelog fragments
