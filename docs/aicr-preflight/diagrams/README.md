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
| `preflight-workflow` | The operational sequence, including how to triage a failing check. |

## Keeping the numbers honest

`coverage-map` and `preflight-timeline` carry counts from a real run. **If the catalog changes, these
must be regenerated.** The source of truth is
[`tests/aicr-preflight/catalog.yaml`](../../../tests/aicr-preflight/catalog.yaml) and the generated
[report](../../../tests/aicr-preflight/results/2026-07-25-gb200-kind/coverage.md).

Current values, from the 2026-07-25 run (provenance `sim`):

- 21 checks total: A 14, B 0, C 4, G 3
- Today 66.7% (A / total), Mokka-specific 42.9% (9 / 21), reachable 81.0% ((A + G) / total)
- Executed 9 of 21: 5 pass, 4 fail, 12 not run
- Proxy back-test: 2 of 4 caught

Verify with:

```bash
grep -c 'bucket: A' ../../../tests/aicr-preflight/catalog.yaml
```
