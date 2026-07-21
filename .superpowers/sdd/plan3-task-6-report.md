# Plan 3 Task 6 report

## Resolved contracts before GREEN

- `runCommand({ event, github, config, dryRun, now })` is the privileged mode
  boundary. `now` is an injected function returning an ISO UTC timestamp.
- The webhook supplies only a case-consistent repository identity, positive PR
  number, and positive comment ID. The live comment supplies the body and actor;
  webhook actor/body/association fields are never authority.
- Logical commands and diagnostics are merged by source line. Commands on
  different lines have sequential last-command-wins semantics for LGTM and
  hold state. Assignment changes are accumulated against the live assignee set.
  A final cancelled LGTM does not request approvers.
- The mode plans from one live snapshot, then re-fetches and compares repository,
  number, open state, draft state, author, base repository, and head OID directly
  before any write. Dry-run stops before this write fence and performs no writes.
- Mutation order is deterministic: assignee deltas; all policy-label deltas in
  fixed LGTM, approved, hold, and needs-approval order; newly needed reviewer
  requests; the policy state/comment checkpoint; then retest reruns. Mutations
  are either reconciled from live state or use an idempotent endpoint. Retest
  state is checkpointed before reruns, and each planned run is re-fetched
  immediately before its non-retried rerun call; a changed/non-failed run is
  skipped.
- A failure stops later operations. The returned/thrown summary contains only
  fixed operation and reason codes plus validated identifiers. Successfully
  applied operations remain listed, enabling a safe live-state retry.
- A missing action-owned policy comment initializes exact-head null authority.
  A duplicate action-owned comment or mismatched action-owned comment plan is
  ambiguous and aborts before writes. A malformed marker/state is repaired to
  null authority while rejecting non-help commands and deriving
  `do-not-merge/needs-approval`. Foreign marker lookalikes are never authority.
- `/help` produces a fixed grammar/authorization description in the bounded
  policy summary. `/approve` remains a parser diagnostic. Raw comment text is
  never included in output, API paths/refs, workflow identifiers, or executable
  input.
- The command renderer replaces only its own marked subsection and preserves
  the trusted metadata section in the one action-owned comment. Task 8 still
  must preserve same-head state when metadata reruns and reset it only on a new
  head before the command workflow is enabled.
- Task 9 must give metadata, commands, and the evaluator one shared per-PR
  concurrency group. Separate workflow-specific group names would permit a
  synchronize run to overwrite current command state despite the repeated
  in-process head fences.

## RED evidence

- Initial contract run: 59/59 cases failed because `src/modes/command.js` and
  the command client surface did not exist. `run()` also rejected `command`.
- Added follow-up RED cases before their fixes for the global command limit,
  metadata-section preservation, stable help content, invalid configuration,
  action-owned state provenance, same-comment convergence, and retest partial
  delivery.

## GREEN and verification evidence

- Focused command contract: 78/78 pass.
- Full suite in the worktree with loopback permission: 596/596 pass.
- Full suite in a correctly rooted dependency-clean snapshot after `npm ci`:
  596/596 pass. The ordinary sandbox run is 595/596 solely because the existing
  packaged-action smoke server cannot bind `127.0.0.1` there (`EPERM`).
- Worktree and dependency-clean snapshot: `npm run lint` pass; `npm run package`
  pass (`864kB dist/index.js`); generated bundle byte comparison pass.
- `git diff --check` pass. Security scans found no child-process, dynamic
  executable/ref/workflow identifier, artifact bridge, PR-head checkout, or
  event comment actor/body authority use. The only comment body reads are from
  the live issue-comment client result and the action-owned policy comment.
- Five targeted mutants were killed by the focused contract: disabled final PR
  fence; manual label as LGTM authority; same-comment LGTM timestamp overwrite;
  automatic transient rerun retry; and invalid-policy command execution.
- No workflow file changed, so `actionlint` was not applicable to this task.

## Independent-review corrections

- `/retest` now accepts only the fixed source-controlled workflow allowlist
  `.github/workflows/automation-ci.yml`,
  `.github/workflows/basic-checks.yaml`, and
  `.github/workflows/helm.yaml`. The GitHub boundary safely parses GitHub's
  documented optional `@<ref>` suffix, retains that exact safe source ref, and
  filters by workflow path, `pull_request` event, exact head, current PR
  association, and base-repository identity at list time. The final non-retried
  rerun read validates and compares the same complete identity. Publisher,
  manual, push, wrong-PR, wrong-repository, malformed/traversal, stale-head,
  path mutation, and source-ref mutation cases are covered.
- An untrustworthy command-policy state now forces LGTM and approval false,
  requests no approvers, removes either display-authority label, and
  preserves/adds `do-not-merge/needs-approval`. Malformed and duplicated state
  with otherwise complete current-head review coverage are covered in both
  apply and dry-run modes; dry-run exposes the complete label delta without
  writing.
- State authority and rendering now share one exact policy/state/command marker
  structure. The policy marker must be first and the single canonical state
  marker second; an optional single command marker may only follow the metadata
  prefix. Reordered, missing, duplicated, or malformed structure resets to null
  authority. Rendering validates its input prefix, replaces exactly one state
  marker, emits exactly one canonical marker, and validates the final body.
  Valid metadata-prefix round trips remain byte-stable.

### Correction verification evidence

- RED was observed for all three findings before production changes: expanded
  workflow identity was rejected by the old planner; reordered markers were
  trusted; and complete approval coverage survived malformed/duplicate state.
- Focused retest, policy-comment, and command integration suite: 115/115 pass.
- Full suite with loopback permission: 610/610 pass. The ordinary sandbox run
  is 609/610 solely because the existing packaged-action smoke server cannot
  bind `127.0.0.1` there (`EPERM`).
- ESLint and `git diff --check` pass. Two consecutive `npm run package` builds
  were byte-identical; the generated `867kB dist/index.js` SHA-256 is
  `de168570b3aac4a99ee05d4dd0932244448af6e9a0c38b01c0f0447c182dfc52`.
- Six targeted mutants were killed: broadening the workflow allowlist; removing
  the event gate; removing final workflow-path identity comparison; restoring
  approval from invalid state; trusting marker presence without marker order;
  and omitting the renderer's exact state replacement.
- No workflow file changed, so `actionlint` remains not applicable.

### Production workflow-path shape correction

- RED client evidence reproduced the official
  `.github/workflows/automation-ci.yml@main` shape being filtered by the prior
  `@refs/...`-only parser. RED planner and orchestration evidence also showed
  that the accepted suffix was absent from normalized identity and could not be
  compared after the final live read.
- The normalized run now carries both the exact allowlisted `workflowPath` and
  a canonical `workflowSourceRef` containing the exact validated suffix, or
  `null` for the previously supported suffix-less shape. Documented short refs
  such as `main` and `feature/ci`, plus explicit legacy `refs/...` forms, are
  accepted. Empty, control-bearing, traversal, dot-component, repeated-`@`, and
  otherwise ambiguous suffixes are rejected without weakening the base-path,
  event, PR, head, or repository gates.
- Focused retest, policy-comment, client, and command integration suite:
  116/116 pass. Full suite with loopback permission: 611/611 pass. ESLint and
  `git diff --check` pass.
- Two consecutive package builds are byte-identical. The generated `867kB`
  bundle SHA-256 is
  `d606513512f6cc06239fb51023d084bcce496d25a8e3f1dc60234f4c308d6299`.
- Four targeted mutants were killed: requiring the obsolete `refs/` prefix;
  discarding the parsed source ref; omitting the final source-ref comparison;
  and accepting an ambiguous repeated-`@` suffix.

## Delivery

- Source, tests, fixture, and generated bundle commit:
  `d85b1685b9554068ea033af8c060c13818eb74fb` (`feat: execute authorized
  repository commands`), with a valid GPG signature and DCO signoff.
- Post-commit `npm run package` regenerated `864kB dist/index.js`; `git diff
  --exit-code -- dist` passed.
