# Diagrams

Each diagram exists twice: a `.mmd` mermaid source that renders inline on GitHub, and a committed
`.svg` for reuse in email, slides, and docs. Mermaid does not render in mail clients, so use the SVG
there.

The SVGs are hand-authored. Text is real text, not paths, so it stays selectable and searchable. Each
carries a `prefers-color-scheme: dark` block so it reads in both GitHub themes, and none relies on
colour alone to carry meaning.

| Diagram | What it is for |
|---|---|
| `stack-layering` | Kills the common misreading that simulation lives inside AICR. AICR is the workload, Mokka is the substrate. |
| `vertical-vs-horizontal` | KWOK is node-count breadth, Mokka is GPU-stack depth. Orthogonal, and they compose. |
| `preflight-timeline` | The customer value: where integration failures get found, with and without preflight. |
| `coverage-map` | The A/B/C/G split with the final-gate boundary drawn. |
| `break-matrix` | Where the base cell actually broke, and who owns each failure. Attribution, not the pass/fail split. |
| `preflight-workflow` | The operational sequence, including how to triage a failing check. |

## Keeping the numbers honest

`coverage-map` and `preflight-timeline` carry counts from a real run. **If the catalog changes, these
must be regenerated.** The source of truth is
[`tests/aicr-sweep/catalog.yaml`](https://github.com/NVIDIA/k8s-test-infra/blob/main/tests/aicr-sweep/catalog.yaml), and the reports are generated
from it by the harness in the same directory.

Bucket split, from the catalog (21 checks):

- A meaningful 16, B trivial 1, C hardware-dependent 4, G closable Mokka gap 0
- **76% is the analytical ceiling** (A / total) if the full 14-component recipe stack deploys. It is
  not a measured result, and the diagrams say so on their face.
- **Measured on 2026-08-03: 14%**, three of 21 reaching a meaningful pass.

A second run against Mokka `57ef01659` on 2026-08-26 did not change the bucket split, because the
catalog did not change. It moved the measured side: 53 of 210 check-results reached a verdict against
13 in the first run. See `tests/aicr-sweep/FINDINGS-57ef016.md`.

Verify the bucket counts with:

```bash
grep -c 'bucket: A' tests/aicr-sweep/catalog.yaml
```
