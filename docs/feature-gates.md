---
title: Feature Gates
---

# Feature Gates

Feature gates let an incomplete or transitional behavior ship without making it
part of Mokka's default contract. They are process-wide startup settings: Mokka
reads them before starting any workers, and changing them requires a restart.

## Available gates

The current release defines the mechanism but registers no gates. Supplying any
gate name therefore fails startup.

## Configure gates

Every long-running Mokka binary accepts the same comma-separated setting:

```bash
node-agent start --feature-gates=mokka.example,-mokka.enabledByDefault
```

An unprefixed name or a `+` prefix enables a gate. A `-` prefix disables it.
The equivalent environment variable is `MOKKA_FEATURE_GATES`; the flag wins
when both are set:

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

Unknown identifiers, empty entries, a gate listed more than once, and
lifecycle-invalid overrides stop the affected process instead of silently
choosing a state. Helm also rejects malformed and duplicate entries before
rendering workloads.

## Lifecycle

| Stage | Default | Allowed override | Exit condition |
|-------|---------|------------------|----------------|
| `alpha` | Disabled | Enable or disable | Promote to beta or deprecate |
| `beta` | Enabled | Enable or disable | Promote to stable, or return to alpha before deprecating |
| `stable` | Enabled | May not be disabled | Gate removed at its removal version; the behavior stays |
| `deprecated` | Disabled | May not be enabled | Gate and behavior removed at its removal version |

Stable and deprecated gates are temporary compatibility handles, not permanent
configuration. Each one names the release in which it will be removed, and
every stage change is recorded in the [changelog](https://github.com/NVIDIA/k8s-test-infra/blob/main/CHANGELOG.md).

## See also

- [Gating new behavior](contributing/feature-gates.md) — putting code behind a gate
- [Installation](helm-chart.md) — chart configuration
- [Command-line tools](tools/README.md) — service command references
