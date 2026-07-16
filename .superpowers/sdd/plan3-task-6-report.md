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
- Mutation order is deterministic: assignee deltas, hold display state, newly
  needed reviewer requests, LGTM/approval/needs-approval display state, policy
  state/comment checkpoint, then retest reruns. Mutations are either reconciled
  from live state or use an idempotent endpoint. Retest state is checkpointed
  before reruns, and each planned run is re-fetched immediately before its
  non-retried rerun call; a changed/non-failed run is skipped.
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

## Delivery

- Commit: pending.
