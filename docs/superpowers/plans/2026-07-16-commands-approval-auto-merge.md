# Slash Commands, Approval, and Auto-Merge Implementation Plan

> **For Codex:** REQUIRED SUB-SKILL: Use `superpowers:subagent-driven-development` to implement this plan task-by-task.

**Goal:** Provide the approved Kubernetes-style command subset and enable native squash auto-merge only when current-head LGTM and hierarchical GitHub approval policy are satisfied.

**Architecture:** Extend the local action with exact command parsing, authorization, current-head LGTM provenance, approval set coverage, and a pure merge state machine. An unprivileged review observer emits only workflow completion. A privileged evaluator consumes no observer data or artifacts; it re-queries all live state from the trusted default branch, checks the head OID twice, and performs idempotent mutations. Manual labels are display state, never authorization evidence.

**Tech stack:** Existing Node 24 action and API boundary, GitHub review and Actions APIs, GraphQL native auto-merge mutations, `node:test`, ncc, actionlint.

**Depends on:** `2026-07-16-pr-metadata-reviewer-routing.md` completed.

**Security invariant:** `lgtm` and `approved` labels alone never permit merge. LGTM comes from bot-owned policy state bound to a head OID and an authorized command comment; approval comes from current-head `APPROVED` reviews by applicable approvers.

**Bundle gate:** Every task that changes `src/` or runtime dependencies must
run `npm test && npm run lint && npm run package`, stage `dist/index.js` in the
same commit, and verify `npm run package && git diff --exit-code -- dist` after
the commit. The task-specific `git add` lists are additive to this gate.

---

## Task 1: Parse commands exactly and ignore quoted examples

**Files:**

- Create: `.github/actions/repo-automation/src/commands/parser.js`
- Create: `.github/actions/repo-automation/test/command-parser.test.js`

### Step 1: Write failing parser cases

Cover only:

```text
/lgtm
/lgtm cancel
/assign @user [@user...]
/unassign @user [@user...]
/hold
/hold cancel
/retest
/help
```

Test logical-line anchoring, leading horizontal whitespace, case-insensitive command names, exact arguments, multiple commands in one comment, CRLF, fenced code blocks with backticks or tildes, Markdown blockquotes, inline prose, malformed mentions, duplicate users, unsupported `/approve`, and command-like prefixes such as `/lgtmplease`.

Confirm RED because the parser module is absent.

### Step 2: Implement the pure parser

Export:

```js
function parseCommands(body) {
  return [{ name, operation, users, line, raw }];
}
```

Ignore fenced and quoted lines before applying a fully anchored command grammar. Reject the whole logical command, not just its bad arguments, and return parse diagnostics separately from valid commands.

### Step 3: Verify and commit

```bash
cd .github/actions/repo-automation
npm test -- test/command-parser.test.js
npm run lint
cd ../../..
git add .github/actions/repo-automation/src/commands/parser.js \
  .github/actions/repo-automation/test/command-parser.test.js
git commit -s -S -m "feat: parse repository slash commands"
```

## Task 2: Authorize actors and assignment targets from live state

**Files:**

- Create: `.github/actions/repo-automation/src/commands/authorization.js`
- Create: `.github/actions/repo-automation/test/authorization.test.js`

### Step 1: Write the authorization matrix

Test exact rules:

- `/lgtm`: applicable reviewer or approver, never PR author;
- `/lgtm cancel`: LGTM giver, PR author, or collaborator;
- `/assign`: PR author, applicable owner, or collaborator;
- `/unassign`: named assignee, PR author, or collaborator;
- `/hold` and `/hold cancel`: PR author or collaborator;
- `/retest`: PR author or collaborator;
- `/help`: anyone.

An assignment target must be an applicable owner, current participant, or collaborator. Test `read`, `triage`, `write`, `maintain`, and `admin` permission normalization; only repository permissions that GitHub documents as collaborator capabilities count. Test deleted users, bots, case normalization, arbitrary mentions, stale payload author association, and API failure.

### Step 2: Implement pure decisions

Export:

```js
function authorizeCommand(command, context) {
  return { allowed, reason };
}

function eligibleAssignmentTargets(users, context) {
  return { eligible, rejected };
}
```

The orchestration layer must fetch collaborator permission live; never authorize from `author_association` alone.

### Step 3: Verify and commit

```bash
cd .github/actions/repo-automation
npm test -- test/authorization.test.js
npm run lint
cd ../../..
git add .github/actions/repo-automation/src/commands/authorization.js \
  .github/actions/repo-automation/test/authorization.test.js
git commit -s -S -m "feat: authorize repository slash commands"
```

## Task 3: Bind LGTM and retest cooldown to auditable bot-owned state

**Files:**

- Create: `.github/actions/repo-automation/src/commands/state.js`
- Create: `.github/actions/repo-automation/src/retest.js`
- Create: `.github/actions/repo-automation/test/command-state.test.js`
- Create: `.github/actions/repo-automation/test/retest.test.js`
- Modify: `.github/actions/repo-automation/src/policy-comment.js`

### Step 1: Write failing state tests

Extend the single bot policy comment with one hidden, versioned JSON record:

```text
<!-- repo-automation-state:v1 {"headOid":"...","lgtm":null,"lastRetest":null} -->
```

Test strict parse/serialize round trips, malformed JSON fail-closed, mismatched head invalidation, authorized LGTM provenance `{actor, commentId, headOid, createdAt}`, cancel, and one state marker only. A manually added `lgtm` label without matching state must evaluate false.

For retest, test failed completed workflow runs for the exact current head, no failed runs, in-progress runs, a ten-minute cooldown, duplicate webhook delivery, stale head runs, and bounded API failure. The cooldown source is the bot-owned state record, not process memory.

### Step 2: Implement interfaces

Export:

```js
function parsePolicyState(commentBody) { /* fail closed */ }
function serializePolicyState(state) { /* stable key order */ }
function currentLgtm(state, headOid) { /* provenance or null */ }

function planRetest({ runs, headOid, now, lastRetest, cooldownSeconds }) {
  return { rerunRunIds, nextAllowedAt, reason };
}
```

Rerun only failed jobs using GitHub's rerun-failed-jobs API; never dispatch arbitrary workflows or accept a run ID from comment text.

### Step 3: Verify and commit

```bash
cd .github/actions/repo-automation
npm test -- test/command-state.test.js test/retest.test.js
npm run lint
cd ../../..
git add .github/actions/repo-automation/src/commands/state.js \
  .github/actions/repo-automation/src/retest.js \
  .github/actions/repo-automation/src/policy-comment.js \
  .github/actions/repo-automation/test/command-state.test.js \
  .github/actions/repo-automation/test/retest.test.js
git commit -s -S -m "feat: persist current-head command state"
```

## Task 4: Compute current-head approval coverage and approver requests

**Files:**

- Create: `.github/actions/repo-automation/src/approval-coverage.js`
- Create: `.github/actions/repo-automation/test/approval-coverage.test.js`

### Step 1: Write the review truth table

Reduce reviews to each reviewer's latest effective state and test:

- `APPROVED` for the current head counts;
- approval for an old commit does not count;
- later `CHANGES_REQUESTED` or `DISMISSED` cancels an earlier approval;
- comments without `APPROVED` never count;
- author approval never counts;
- root and nested approver coverage follows `resolveOwners()` output;
- every changed file needs at least one effective applicable approver;
- manually applied `approved` labels are ignored;
- greedy approver requests cover the most uncovered files first, then stable login order;
- existing valid approvals and pending eligible requests reduce new requests.

### Step 2: Implement coverage and set cover

Export:

```js
function evaluateApprovalCoverage({ files, reviews, headOid, author }) {
  return { approved, effectiveReviews, coveredPaths, uncoveredPaths };
}

function selectApprovers({ files, effectiveReviews, requested, author }) {
  return { selected, uncoveredPaths };
}
```

Do not assume one repository-wide approval satisfies all files.

### Step 3: Verify and commit

```bash
cd .github/actions/repo-automation
npm test -- test/approval-coverage.test.js
npm run lint
cd ../../..
git add .github/actions/repo-automation/src/approval-coverage.js \
  .github/actions/repo-automation/test/approval-coverage.test.js
git commit -s -S -m "feat: evaluate hierarchical approval coverage"
```

## Task 5: Define the fail-closed merge state machine

**Files:**

- Create: `.github/actions/repo-automation/src/merge-state.js`
- Create: `.github/actions/repo-automation/test/merge-state.test.js`

### Step 1: Write the complete truth table

Test open versus closed, draft, protected target, current LGTM, complete approval coverage, each `do-not-merge/*` label, conflict/unknown mergeability, uncovered ownership, stale metadata head, API error, CI pending, CI failed, and a head change immediately before mutation.

The expected decision has three values:

- `ENABLE`: policy is satisfied; ask GitHub to enable native squash auto-merge. Required checks may still be pending, and GitHub waits for them.
- `DISABLE`: native auto-merge is currently enabled but policy is no longer satisfied.
- `NOOP`: desired and live state already agree.

### Step 2: Implement a pure decision

Export:

```js
function decideMergeAction(state) {
  return { action: 'ENABLE' | 'DISABLE' | 'NOOP', blockers };
}
```

Never return `ENABLE` for unknown or incomplete input. Required checks are still enforced by the protected branch/ruleset; this action does not imitate or bypass them.

### Step 3: Verify and commit

```bash
cd .github/actions/repo-automation
npm test -- test/merge-state.test.js
npm run lint
cd ../../..
git add .github/actions/repo-automation/src/merge-state.js \
  .github/actions/repo-automation/test/merge-state.test.js
git commit -s -S -m "feat: define native auto-merge policy"
```

## Task 6: Implement command execution and immediate reevaluation

**Files:**

- Create: `.github/actions/repo-automation/src/commands/executor.js`
- Create: `.github/actions/repo-automation/src/modes/command.js`
- Create: `.github/actions/repo-automation/test/command-contract.test.js`
- Create: `.github/actions/repo-automation/test/fixtures/events/issue-comment.json`
- Modify: `.github/actions/repo-automation/src/github-client.js`
- Modify: `.github/actions/repo-automation/src/index.js`
- Modify: `.github/actions/repo-automation/test/helpers/fake-github.js`

### Step 1: Write the webhook contract first

Assert the mode ignores issue comments that are not on open PRs, re-fetches PR/actor/owners/state, parses all logical commands, processes each comment ID once, and performs idempotent label, assignment, reviewer request, rerun, and comment updates.

Test all commands, unauthorized explanations, `/lgtm` approver set-cover requests, `/hold` disabling eligibility, `/retest` cooldown, `/help`, duplicate delivery, partial API failure, and `/approve` rejection. No command text may reach a shell, ref, path, GraphQL document, or workflow identifier.

### Step 2: Extend the API only with fixed operations

Add collaborator permission, assign/unassign, list workflow runs for current head, rerun failed jobs, reviews, and reviewer-request methods. GraphQL documents must be source constants with variables; never interpolate event text.

### Step 3: Implement command mode

The mode applies the authorized command plan, updates hidden state and visible
policy summary together, derives display labels from state, and requests
approvers after LGTM. It does not call a service that has not been installed
yet. Completion of the `Commands` workflow is one of the trusted signals for
the evaluator added in Task 7. Because an `issue_comment` workflow run has no
reliable PR association, that evaluator deliberately scans and reconciles all
open PRs for this signal rather than trusting an empty or ambiguous mapping.
On any ambiguous state, leave or add `do-not-merge/needs-approval`.

### Step 4: Verify and commit

```bash
cd .github/actions/repo-automation
npm test -- test/command-contract.test.js
npm run lint
cd ../../..
git add .github/actions/repo-automation/src \
  .github/actions/repo-automation/test
git commit -s -S -m "feat: execute authorized repository commands"
```

## Task 7: Implement live merge evaluation with a head-OID race guard

**Files:**

- Create: `.github/actions/repo-automation/src/modes/merge-evaluate.js`
- Create: `.github/actions/repo-automation/test/merge-contract.test.js`
- Create: `.github/actions/repo-automation/test/fixtures/events/pull-request-review.json`
- Create: `.github/actions/repo-automation/test/fixtures/events/workflow-run.json`
- Modify: `.github/actions/repo-automation/src/github-client.js`
- Modify: `.github/actions/repo-automation/src/index.js`

### Step 1: Write failing evaluator contracts

Cover explicit PR number, observer `workflow_run`, metadata `workflow_run`,
Commands `workflow_run`, and scheduled all-open-PR modes. A Commands completion
must list and reconcile all open PRs because `workflow_run.pull_requests` is not
reliable for an `issue_comment` source workflow. The evaluator must:

1. map the event to candidate PR numbers without trusting artifacts or outputs;
2. re-fetch each open PR, current head, files, owner files, labels, state comment, reviews, and native auto-merge status;
3. recompute LGTM and approval coverage;
4. derive display `approved` and `do-not-merge/needs-approval` labels;
5. fetch the head OID again immediately before `ENABLE` or `DISABLE`;
6. abort mutation if it changed;
7. use GraphQL variables to enable `SQUASH` auto-merge or disable native auto-merge idempotently.

Test empty observer PR mapping, fork PR, stale review, synchronize invalidation, label tampering, head race, closed PR, GraphQL failure, and rerun convergence.

### Step 2: Implement and verify

```bash
cd .github/actions/repo-automation
npm test -- test/merge-contract.test.js
npm run lint
cd ../../..
git add .github/actions/repo-automation/src/modes/merge-evaluate.js \
  .github/actions/repo-automation/src/github-client.js \
  .github/actions/repo-automation/src/index.js \
  .github/actions/repo-automation/test/merge-contract.test.js \
  .github/actions/repo-automation/test/fixtures/events
git commit -s -S -m "feat: reconcile pull request auto-merge"
```

## Task 8: Invalidate state on every new commit

**Files:**

- Modify: `.github/actions/repo-automation/src/modes/metadata.js`
- Modify: `.github/actions/repo-automation/test/metadata-contract.test.js`

### Step 1: Add a failing synchronize contract

On a new head OID, metadata mode must clear LGTM provenance, remove display labels `lgtm` and `approved`, add `do-not-merge/needs-approval`, preserve a concise explanation, recalculate metadata, and request only newly needed reviewers. Re-delivery for the same head is a no-op.

### Step 2: Implement, verify, and commit

```bash
cd .github/actions/repo-automation
npm test -- test/metadata-contract.test.js
npm run lint
cd ../../..
git add .github/actions/repo-automation/src/modes/metadata.js \
  .github/actions/repo-automation/test/metadata-contract.test.js
git commit -s -S -m "feat: invalidate review state on new commits"
```

## Task 9: Add command and privilege-bridge workflows

**Files:**

- Create: `.github/workflows/commands.yml`
- Create: `.github/workflows/review-observer.yml`
- Create: `.github/workflows/merge-evaluator.yml`
- Modify: `.github/workflows/pr-metadata.yml`
- Modify: `.github/workflows/automation-ci.yml`
- Modify: `.github/actions/repo-automation/dist/index.js`

### Step 1: Add `commands.yml`

Trigger only `issue_comment` type `created`; editing an already processed comment
must not retroactively change an executed command. Use top-level
`permissions: {}` and one PR-only job with `contents: read`, `actions: write`,
`issues: write`, and `pull-requests: write`; 10-minute timeout; per-PR
concurrency; explicit trusted default-branch checkout with persisted credentials
disabled; and local action `mode: command`. Native auto-merge remains isolated
in the evaluator workflow.

### Step 2: Add the read-only observer

`review-observer.yml` triggers on `pull_request_review` types `submitted`, `edited`, and `dismissed`. Set `permissions: {}`. Its job has no checkout, no cache, no artifact, no repository script, and only a fixed `run: ':'` step. Workflow completion is the signal.

### Step 3: Add the privileged evaluator

`merge-evaluator.yml` triggers on:

- `workflow_run` completion for workflows named `Review observer`, `PR metadata`, and `Commands`;
- schedule every 15 minutes;
- `workflow_dispatch` with optional numeric PR input and boolean `dry-run`
  defaulting to `true`.

Use top-level `permissions: {}` and a job with `contents: write`, `issues: write`, and `pull-requests: write`; a 15-minute timeout; non-cancelling per-PR concurrency where a PR is known; and trusted default-branch checkout. For Commands completion, use repository-wide evaluator concurrency because all open PRs are scanned. Pass only fixed mode plus event JSON location supplied by GitHub. Never download observer artifacts or use observer outputs, caches, refs, or repository code from a PR.
Scheduled and workflow-run events apply reconciliation; manual dispatch passes the
input through the action's stable `dry-run` input and requires an explicit
`dry-run: false` selection before mutation.

### Step 4: Package, validate, and commit

```bash
cd .github/actions/repo-automation
npm ci
npm test
npm run lint
npm run package
cd ../../..
actionlint \
  .github/workflows/commands.yml \
  .github/workflows/review-observer.yml \
  .github/workflows/merge-evaluator.yml \
  .github/workflows/pr-metadata.yml \
  .github/workflows/automation-ci.yml
git add .github/actions/repo-automation/dist/index.js .github/workflows
git commit -s -S -m "ci: add commands and guarded auto-merge"
```

## Task 10: Verify the complete state machine

```bash
cd .github/actions/repo-automation
npm ci
npm test
npm run lint
npm run package
cd ../../..
git diff --exit-code -- .github/actions/repo-automation/dist
actionlint .github/workflows/*.yml .github/workflows/*.yaml
rg -n "download-artifact|pull_request\.head|head\.ref|submodules: true" \
  .github/workflows/commands.yml \
  .github/workflows/review-observer.yml \
  .github/workflows/merge-evaluator.yml
git diff --check
```

Expected: tests and validation pass; the observer contains no repository checkout; privileged workflows consume no untrusted artifact or ref. Use workflow dispatch in dry-run mode against one selected PR before enabling repository auto-merge settings.
