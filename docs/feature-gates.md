# Feature Gates

Feature gates switch individual Mokka behaviors on or off, so a behavior can
ship before it becomes part of Mokka's default contract. They work like
[Kubernetes feature gates](https://kubernetes.io/docs/reference/command-line-tools-reference/feature-gates/):
same syntax, same stages. Mokka reads them once at startup, so a change takes
effect when the service restarts.

## Available gates

| Gate | Stage | Default | Since | Read by | Issue |
|------|-------|---------|-------|---------|-------|
| `IMEXDownload` | Beta | On | 0.6 | node agent | [#821](https://github.com/NVIDIA/k8s-test-infra/issues/821) |

`IMEXDownload` lets the node agent on a multi-node NVLink profile, such as
gb200, download the IMEX archive it pins from NVIDIA into a node cache under
`/var/lib/nvml-mock/cache/imex`, when neither `imex.binaries.image` nor that
cache provides it. Each attempt is limited by `imex.binaries.download.timeout`, a minute
by default. It retries for about a minute per node-agent start, then leaves
the node without IMEX, which fails ComputeDomain daemons there as on a
real node without the IMEX package. The download needs HTTPS access to
`developer.download.nvidia.com`; turn the gate off where nodes must not reach
the internet.
It is to be deprecated once Mokka publishes the IMEX binaries image.

## Set gates

Set gates through the chart, which passes the same list to the node agent, the
NRI plugin and the control plane:

```yaml
featureGates:
  AllAlpha: true
```

Each of these services accepts every Mokka gate and ignores the ones it does
not read, so one list serves all three. Changing the list restarts all of
their pods, including the node agent on every node, even when only one service
reads the gate that changed.

Outside the chart, each service takes the same value as a flag,
`--feature-gates=AllAlpha=true,AllBeta=false`, or as the `MOKKA_FEATURE_GATES`
environment variable. The value is a comma-separated list of `Name=true` or
`Name=false`; if a name appears more than once, the last value wins. Unlike in
Kubernetes components, values do not merge: only the last `--feature-gates`
counts, and it replaces `MOKKA_FEATURE_GATES` entirely, so put every gate in one
value.

`AllAlpha=true` and `AllBeta=false` change the default of every
[alpha or beta](#stages) gate at once; an explicit entry for a single gate
overrides them.

The chart's values schema lists every gate this chart version accepts. Helm
therefore rejects an unknown gate, a value that is not a boolean, or an attempt
to change a gate that is [locked](#stages) to its default, and the install or
upgrade fails before any pod changes. Outside the chart, a service given such a
value refuses to start and logs the reason. When a service starts with gates
registered, it logs the state of every gate.

## Stages

| Stage | Default | Can be changed | What it means |
|-------|---------|----------------|---------------|
| Alpha | Off | Yes | Incomplete or experimental. May change or be removed in any release |
| Beta | On | Yes | Complete and tested. Disable it if it causes problems, and report them |
| GA (generally available) | On | No | Permanent behavior. The gate stays for compatibility; setting it logs a warning |
| Deprecated | Off | Until locked | Being removed. Setting it logs a warning |

A GA or deprecated gate is removed no earlier than three minor (Y) releases
after it is deprecated or reaches GA, the floor the
[support policy](support-policy.md#deprecation) sets. Once a gate is removed,
an upgrade that still sets it fails with an error naming the gate, so drop it
from `featureGates`, including values carried over by `--reuse-values`, before
upgrading. The
[changelog](https://github.com/NVIDIA/k8s-test-infra/blob/main/CHANGELOG.md)
records when each gate is added, changes stage, or is removed.

## See also

- [Installation](helm-chart.md) — every chart value
- [Command-line tools](tools/README.md) — service flags
- [Gating new behavior](contributing/feature-gates.md) — putting code behind a gate
