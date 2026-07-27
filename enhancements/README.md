# Mokka Enhancement Proposals (MEPs)

A MEP is a design document that captures a substantial change to Mokka -
new components, major cross-cutting refactors, or breaking API changes.

Small bug fixes and routine features do **not** need a
MEP; open a PR instead.

Inspired by the [Kubernetes KEP][kep] process, adapted for this repo.

[kep]: https://github.com/kubernetes/enhancements/tree/master/keps

## Layout

| Path          | Purpose                                          |
| ------------- | ------------------------------------------------ |
| `template.md` | Canonical MEP template — copy this to start one. |
| `meps/`       | Accepted and in-flight proposals, one per file.  |

## When to write a MEP

Write one if the change:

- Reworks how existing components fit together.
- Adds or removes a top-level component (chart, CLI, etc.).
- Alters a user-visible contract (configuration, Helm values, etc.).
- Introduces a new dependency, platform target, or release artifact.

When in doubt, open a discussion first — a maintainer will tell you
whether a MEP is needed.

## Workflow

1. **Copy the template** into `meps/` as `NNNN-short-title.md`, where
   `NNNN` is the next unused four-digit number (starts from `0001`).
2. **Fill in Summary, Motivation, and Goals** first — enough for
   reviewers to weigh in on direction before you invest in Design Details.
3. **Open a PR** tagged `kind/mep`. Discussion happens on the PR.
4. **Iterate** through review; expand Design Details, Risks, and
   Alternatives as consensus forms.
5. **Merge** once maintainers approve. The MEP is now the record of
   intent; implementation PRs reference it by number (`MEP-NNNN`).

Superseded or withdrawn MEPs stay in `meps/` with their status updated —
history is preserved, not rewritten.
