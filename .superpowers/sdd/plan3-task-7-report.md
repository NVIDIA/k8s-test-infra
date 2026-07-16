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

## Independent-review corrections

- The runtime now passes GitHub's fixed `context.eventName` into the evaluator.
  `workflow_run` requires completed payload and live identities and rejects an
  explicit PR; `schedule` rejects explicit PRs and scans open; only
  `workflow_dispatch` accepts a numeric PR or an all-open scan. Cross-trigger
  fields and every unsupported event name fail before PR reads. Trusted
  Commands completion still always performs the repository-wide open scan.
- Live evaluator workflow paths require an exact source-controlled allowlisted
  path plus one structurally safe `@<source-ref>` suffix. The suffix is retained
  in normalized identity; empty, repeated-at, traversal, double-slash, control,
  and otherwise ambiguous suffixes fail closed.
- Every candidate now proves REST/GraphQL identity, head, and the native method
  before fallible custom-authority reads. If later configuration, files,
  reviews, comments, immutable policy, branch protection, or bound validation
  fails while SQUASH, MERGE, or REBASE is armed, a fresh REST/GraphQL fence
  disables it at most once only when the same head, node, and method remain.
  Changed heads, changed methods, ambiguous final reads, dry-run, and unarmed
  state perform no fail-safe mutation. Incomplete authority never changes
  display labels or enables merge.
- Open PR, file, review, and action-owned comment collection now uses a limited
  paginator. It retains at most `limit + 1`, calls the pagination stop callback
  immediately on overflow, and requests no subsequent page. Read retries remain
  restricted to the pre-existing safe transient policy; merge mutations remain
  non-retried.
- Correction RED reproduced 36 failures across the three findings. The expanded
  focused contract passes 83/83 after GREEN, including all three armed methods,
  file/review/comment/policy/protection/bound failures, race/ambiguity no-write
  cases, exact trigger classes, safe source suffixes, and multi-page call bounds.
- Corrected full suite with loopback permission: 694/694 pass. ESLint and
  `git diff --check` pass. Two package builds were byte-identical; corrected
  `dist/index.js` is 891kB with SHA-256
  `0a29ea555604de24f3133c965c39f4e299d0e232e1562c4f278b3b6acba58e1d`.
- Five correction mutants were killed: explicit-input trigger bypass, omission
  of fail-safe disable, omission of its fresh REST head fence, omission of the
  limited paginator stop callback, and lossy workflow-source normalization.
