# Pull Request Metadata and Reviewer Routing Implementation Plan

> **For Codex:** REQUIRED SUB-SKILL: Use `superpowers:subagent-driven-development` to implement this plan task-by-task.

**Goal:** Classify every pull request, enforce title and DCO policy, resolve auditable ownership, and request two deterministic eligible reviewers without executing fork code.

**Architecture:** Extend the local action with pure parsers for title, size, areas, DCO, and `OWNERS`, then compose them in `metadata` mode behind the Plan 1 GitHub API boundary. `pull_request_target` supplies write access, but the workflow checks out only the trusted default branch and treats all pull request content as data. The root `/OWNERS` is initially the only active authority; vendored `OWNERS` files are ignored unless a maintainer later adds an exact path to policy.

**Tech stack:** Plan 1 Node 24 action, GitHub REST/GraphQL APIs through the existing adapter, `node:test`, `minimatch` 10, ncc, actionlint.

**Depends on:** `2026-07-16-community-policy-label-foundation.md` completed and its label configuration synchronized in dry-run.

**Produces for Plan 3:** `resolveOwners()`, approval coverage inputs, marker-delimited policy comments, current head OID, and an idempotent `metadata` mode.

**Bundle gate:** After the action entry point exists, every task that changes
`src/` or runtime dependencies must run `npm test && npm run lint && npm run
package`, stage `dist/index.js` in the same commit, and verify `npm run package
&& git diff --exit-code -- dist` after the commit. The task-specific `git add`
lists below are additive to this gate; no intermediate commit may leave source
and the checked-in runtime bundle divergent.

---

## Task 1: Implement Conventional Commit title classification

**Files:**

- Create: `.github/actions/repo-automation/src/title.js`
- Create: `.github/actions/repo-automation/test/title.test.js`

### Step 1: Write failing table tests

Cover these exact mappings:

```text
feat -> kind/feature
fix -> kind/bug
docs -> kind/documentation
test -> kind/test
refactor -> kind/refactor
perf -> kind/performance
build, ci -> kind/ci
chore -> kind/cleanup
chore(deps) -> kind/dependencies
revert -> kind/revert
```

Accept optional scopes, optional `!`, and a non-empty description after `: `. Reject leading spaces, missing descriptions, bracket-style issue titles, unsupported types, newline injection, and `chore(deps-extra)`. Assert exactly one `kind/*` label.

Run the focused test and confirm RED because `src/title.js` is missing.

### Step 2: Implement and verify

Export:

```js
function classifyTitle(title) {
  return { valid, type, scope, breaking, description, label, error };
}
```

Keep the parser anchored to the full first line. Then run:

```bash
cd .github/actions/repo-automation
npm test -- test/title.test.js
npm run lint
```

### Step 3: Commit green behavior

```bash
git add .github/actions/repo-automation/src/title.js \
  .github/actions/repo-automation/test/title.test.js
git commit -s -S -m "feat: classify pull request titles"
```

## Task 2: Implement exact PR size and area derivation

**Files:**

- Create: `.github/actions/repo-automation/src/size.js`
- Create: `.github/actions/repo-automation/src/areas.js`
- Create: `.github/actions/repo-automation/test/size.test.js`
- Create: `.github/actions/repo-automation/test/areas.test.js`
- Modify: `.github/actions/repo-automation/package.json`
- Modify: `.github/actions/repo-automation/package-lock.json`

### Step 1: Add and lock the glob dependency

Add exact dependency `minimatch: 10.0.3` and regenerate the lockfile with `npm install --package-lock-only`.

### Step 2: Write failing boundaries

For `classifySize(additions, deletions, thresholds)`, test totals 0, 49, 50, 249, 250, 999, and 1000. Assert additions plus deletions are used, all file categories count, negative/non-integer input is rejected, and issues are never accepted as an input type.

For `deriveAreaLabels(paths, areas)`, test overlapping rules, root Markdown, dotfiles, glob order independence, duplicate suppression, Windows separator rejection, and no-match behavior.

### Step 3: Implement pure functions

Export:

```js
function classifySize(additions, deletions, thresholds) {
  return { changedLines, label };
}

function deriveAreaLabels(paths, areas) {
  return [...labels].sort();
}
```

Use minimatch with `dot: true`, `nonegate: true`, and POSIX paths only.

### Step 4: Verify and commit

```bash
cd .github/actions/repo-automation
npm test -- test/size.test.js test/areas.test.js
npm run lint
cd ../../..
git add .github/actions/repo-automation/src/size.js \
  .github/actions/repo-automation/src/areas.js \
  .github/actions/repo-automation/test/size.test.js \
  .github/actions/repo-automation/test/areas.test.js \
  .github/actions/repo-automation/package.json \
  .github/actions/repo-automation/package-lock.json
git commit -s -S -m "feat: derive pull request size and areas"
```

## Task 3: Validate every human commit against DCO

**Files:**

- Create: `.github/actions/repo-automation/src/dco.js`
- Create: `.github/actions/repo-automation/test/dco.test.js`

### Step 1: Write failing DCO contracts

Test:

- one matching `Signed-off-by: Name <email>` trailer passes;
- trailer name and email match author identity case-insensitively;
- missing, malformed, or another person's trailer fails;
- a signoff in the body rather than the trailer block fails;
- merge commits are checked like other human commits;
- exact configured bot login plus configured bot author email is exempt;
- merely using a bot-like email or `[bot]` suffix is not exempt;
- every failing commit SHA and reason is returned, without truncating internally.

### Step 2: Implement

Export:

```js
function evaluateDco(commits, botPolicy) {
  return { valid, failures, exempted };
}
```

Parse the final RFC-822-style trailer block only. Normalize whitespace and case for comparison, but preserve original identities in diagnostics.

### Step 3: Verify and commit

```bash
cd .github/actions/repo-automation
npm test -- test/dco.test.js
npm run lint
cd ../../..
git add .github/actions/repo-automation/src/dco.js \
  .github/actions/repo-automation/test/dco.test.js
git commit -s -S -m "feat: enforce commit DCO signoffs"
```

## Task 4: Resolve hierarchical OWNERS without trusting vendored policy

**Files:**

- Create: `OWNERS_ALIASES`
- Create: `.github/actions/repo-automation/src/owners.js`
- Create: `.github/actions/repo-automation/test/owners.test.js`
- Create: `.github/actions/repo-automation/test/fixtures/owners/root/OWNERS`
- Create: `.github/actions/repo-automation/test/fixtures/owners/nested/OWNERS`
- Create: `.github/actions/repo-automation/test/fixtures/owners/no-parent/OWNERS`
- Create: `.github/actions/repo-automation/test/fixtures/owners/OWNERS_ALIASES`
- Create: `.github/actions/repo-automation/test/fixtures/owners/vendor/OWNERS`

### Step 1: Define alias and ownership parsing tests

Use Kubernetes-compatible YAML fields `reviewers`, `approvers`, `labels`, and `options.no_parent_owners`. Alias references use names defined under `aliases` in `OWNERS_ALIASES`.

Cover root fallback, parent inheritance, `no_parent_owners: true`, nested overrides, alias expansion, unknown alias, duplicate login, PR author exclusion, missing coverage, and a `vendor/**/OWNERS` fixture that is ignored because it is not listed in `policy.activeOwnerFiles`.

### Step 2: Define the interface

Export:

```js
function parseOwnersFile(text, path) { /* normalized declaration */ }
function parseAliases(text) { /* Map<string, string[]> */ }
function resolveOwners(paths, ownerFiles, aliases, policy) {
  return {
    files: [{ path, reviewers, approvers }],
    reviewerCandidates,
    approverCandidates,
    uncoveredPaths
  };
}
```

The production loader may fetch only exact entries in `activeOwnerFiles`; it must not recursively discover every file named `OWNERS`. Keep `/OWNERS` as the only production entry in this plan. Create `OWNERS_ALIASES` as:

```yaml
aliases: {}
```

No new person or team receives authority.

### Step 3: Verify and commit

```bash
cd .github/actions/repo-automation
npm test -- test/owners.test.js
npm run lint
cd ../../..
git add OWNERS_ALIASES \
  .github/actions/repo-automation/src/owners.js \
  .github/actions/repo-automation/test/owners.test.js \
  .github/actions/repo-automation/test/fixtures/owners
git commit -s -S -m "feat: resolve auditable pull request owners"
```

## Task 5: Select two deterministic weighted reviewers

**Files:**

- Create: `.github/actions/repo-automation/src/reviewer-selection.js`
- Create: `.github/actions/repo-automation/test/reviewer-selection.test.js`

### Step 1: Write failing selection tests

For each candidate, weight is the sum of additions plus deletions for files they review. Cover deterministic repeatability, repository/PR seed isolation, weighted selection without replacement, exact target of two, fewer-than-two candidates, author exclusion, preservation of existing eligible requests, and filling only coverage gaps after new paths appear.

### Step 2: Implement deterministic weighted ordering

Export:

```js
function selectReviewers({ candidates, files, seed, target, author, requested }) {
  return { selected, preserved, uncoveredPaths };
}
```

Derive pseudorandom values from SHA-256 of `<owner>/<repo>#<pr>:<login>`. Use a deterministic weighted-race key and stable login tie-breaker; do not use `Math.random()` or persistent state.

### Step 3: Verify and commit

```bash
cd .github/actions/repo-automation
npm test -- test/reviewer-selection.test.js
npm run lint
cd ../../..
git add .github/actions/repo-automation/src/reviewer-selection.js \
  .github/actions/repo-automation/test/reviewer-selection.test.js
git commit -s -S -m "feat: route deterministic pull request reviewers"
```

## Task 6: Compose metadata mode behind the mocked GitHub API

**Files:**

- Create: `.github/actions/repo-automation/src/policy-comment.js`
- Create: `.github/actions/repo-automation/src/modes/metadata.js`
- Create: `.github/actions/repo-automation/test/policy-comment.test.js`
- Create: `.github/actions/repo-automation/test/metadata-contract.test.js`
- Create: `.github/actions/repo-automation/test/fixtures/events/pull-request-target.json`
- Modify: `.github/actions/repo-automation/src/github-client.js`
- Modify: `.github/actions/repo-automation/src/index.js`
- Modify: `.github/actions/repo-automation/test/helpers/fake-github.js`

### Step 1: Write the contract test first

Use a redacted representative `pull_request_target` payload. The fake API returns live PR metadata, paginated files with per-file changes, commits, reviews, requested reviewers, labels, base-branch `OWNERS`, and `OWNERS_ALIASES`.

Assert one run:

- re-fetches the PR instead of trusting mutable payload fields;
- adds/removes only managed `kind/*`, `size/*`, `area/*`, and draft labels;
- preserves unmanaged labels;
- requests eligible reviewers without requesting the author;
- reports invalid title, DCO failure, and uncovered ownership as a failed mode result;
- writes one comment delimited by `<!-- repo-automation-policy:v1 -->` and updates it on rerun;
- includes the current head OID in the summary;
- performs no mutation in dry-run;
- never accepts checkout ref, config path, executable command, or artifact path from the event.

### Step 2: Extend the API boundary

Add normalized methods for PR, files, commits, labels, requested reviewers,
content-at-default-branch, reviewer requests, per-label add/remove, and
marker-comment upsert. Never replace the complete issue label set: a
maintainer-managed label could be added after the read. Restrict removals to
the action's managed namespaces and exact state-label allowlist. Every list
method paginates. Retry only safe idempotent reads and writes with bounded
backoff.

### Step 3: Implement `metadata` orchestration

The mode receives `{event, github, config, dryRun}`. It must compute a complete plan before mutating. Apply label changes and reviewer requests idempotently, update the policy comment, set the `summary` output, and throw after diagnostics when title, DCO, configuration, or ownership fails so the job becomes the required `PR metadata` check.

### Step 4: Verify and commit

```bash
cd .github/actions/repo-automation
npm test
npm run lint
cd ../../..
git add .github/actions/repo-automation/src \
  .github/actions/repo-automation/test
git commit -s -S -m "feat: orchestrate pull request metadata policy"
```

## Task 7: Add trusted metadata and automation CI workflows

**Files:**

- Create: `.github/workflows/pr-metadata.yml`
- Create: `.github/workflows/automation-ci.yml`
- Modify: `.github/actions/repo-automation/dist/index.js`

### Step 1: Add the privileged metadata adapter

`pr-metadata.yml` triggers on `pull_request_target` types `opened`, `reopened`, `synchronize`, `edited`, `ready_for_review`, and `converted_to_draft`, targeting `main` and `release-*`. Use:

- top-level `permissions: {}`;
- job name `PR metadata`;
- `contents: read`, `issues: write`, `pull-requests: write` only;
- 10-minute timeout;
- concurrency `pr-metadata-${{ github.event.pull_request.number }}` with cancellation;
- `actions/checkout@9c091bb21b7c1c1d1991bb908d89e4e9dddfe3e0 # v7.0.0` with `ref: ${{ github.event.repository.default_branch }}`, `persist-credentials: false`, and `submodules: false`;
- local action `mode: metadata`, explicit PR number, and `dry-run: false`.

Do not interpolate title, body, branch, changed path, or comment into `run:` or `ref:`.

### Step 2: Add unprivileged action CI

`automation-ci.yml` triggers on pull requests that change the action, policy, or workflows. Use `permissions: contents: read`, a 15-minute timeout, Node via `actions/setup-node@249970729cb0ef3589644e2896645e5dc5ba9c38 # v6`, `npm ci`, tests, lint, package, `git diff --exit-code -- dist`, and workflow validation supplied by Plan 4 when available.
Set `node-version: 24` explicitly.

### Step 3: Package, validate, and commit

```bash
cd .github/actions/repo-automation
npm ci
npm test
npm run lint
npm run package
cd ../../..
git add .github/actions/repo-automation/dist/index.js \
  .github/workflows/pr-metadata.yml \
  .github/workflows/automation-ci.yml
actionlint .github/workflows/pr-metadata.yml .github/workflows/automation-ci.yml
git commit -s -S -m "ci: classify and route pull requests"
```

## Task 8: Final verification and security review

```bash
cd .github/actions/repo-automation
npm ci
npm test
npm run lint
npm run package
cd ../../..
git diff --exit-code -- .github/actions/repo-automation/dist
actionlint .github/workflows/pr-metadata.yml .github/workflows/automation-ci.yml
rg -n "pull_request\.head|head\.ref|github\.event\.pull_request\.head|submodules: true" \
  .github/workflows/pr-metadata.yml .github/actions/repo-automation/src
git diff --check
```

Expected: all checks pass and the trust-boundary search returns no checkout or execution path derived from the PR head. After merge, use a fork PR to confirm the workflow checks out the default branch and produces only metadata mutations.
