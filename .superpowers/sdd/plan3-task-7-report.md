# Plan 3 Task 7 report

## Result

- Added the privileged `merge-evaluate` mode with strict explicit, trusted
  workflow-run, scheduled, and manual all-open candidate selection.
- The live evaluator ignores workflow artifacts, outputs, refs, conclusions,
  and PR-head content. It accepts only the fixed `Review observer`, `PR
  metadata`, and `Commands` workflow name/path/source-event triples after a
  live run read. `Commands` always performs a bounded all-open scan.
- Each candidate re-loads the open PR, immutable default-branch OWNERS and
  aliases, files, reviews, labels, the exact action-owned policy comment, live
  branch protection, and GraphQL merge state. Candidate failures are isolated.
- Metadata now emits a separate canonical metadata-head marker. Command
  rendering preserves it, while missing, duplicated, malformed, misplaced, or
  stale evidence cannot authorize auto-merge.
- Display `lgtm`, `approved`, and `do-not-merge/needs-approval` labels are
  reconciled from current-head bot state and hierarchical approval coverage;
  display labels themselves never provide authority.
- The evaluator re-fetches REST identity before writes and REST plus GraphQL
  head state after label writes. One head race restarts from a fresh snapshot;
  a second race performs no native merge mutation.
- Native auto-merge uses fixed GraphQL documents and variables, enables only
  `SQUASH`, disables an ineligible or wrong-method request, never retries merge
  mutations, and validates mutation response identity. Dry-run performs the
  complete read and decision path with zero writes.

## RED -> GREEN evidence

- RED: 28/28 initial evaluator cases failed because the evaluator module and
  fixed GraphQL documents did not exist.
- GREEN: the expanded focused contract is 40/40 passing, covering explicit and
  all-open dry-run, strict workflow identity, Commands all-open behavior,
  metadata evidence, current-head approval, protected branches, forks, closed,
  draft, conflicting and unknown state, wrong native method, both race fences,
  GraphQL variables, candidate isolation, and index dispatch.

## Verification

- Full suite with loopback permission: 651/651 pass.
- ESLint: pass.
- `npm run package`: pass; two consecutive builds were byte-identical.
- Generated `dist/index.js`: 889kB, SHA-256
  `2d94cdabda56bfce0f30fd2e454788b9d0d63a45719415d6817777129aa455ac`.
- `git diff --check`: pass.
- Five targeted mutants were killed by the focused contract: trusting a
  Commands run's PR mapping, accepting false branch protection, treating
  missing metadata evidence as current, omitting the final GraphQL head race
  restart, and omitting native auto-merge disable.
- No workflow file changed in Task 7, so actionlint is not applicable until
  Task 9 adds the evaluator workflows.
