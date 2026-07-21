# PR #474 Review Fixes Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Resolve all 10 findings from the 2026-07-19 multi-agent review of PR #474 (6 confidence-gated findings + 4 sub-threshold ones), including the rebase onto main that re-applies #465, on the PR branch `docs/github-actions-automation-design`.

**Architecture:** The PR adds a JS GitHub Action (`.github/actions/repo-automation`, CommonJS, node:test, ncc-bundled `dist/`) plus release automation (`release.yml`, `.github/scripts/*.mjs`, release-please) and a hardened Helm chart. Fixes are surgical: one rebase task, two workflow-shell/CI-wiring tasks, one helm-unittest hermeticity task, three behavioral src fixes (TDD), and three small refactors, closed by a dist repackage and a verified push.

**Tech Stack:** Node 24 (CommonJS action + ESM `.mjs` scripts, `node --test`), GitHub Actions, Helm 3/4 + helm-unittest (pinned plugin commit `6f82a998e0b5461762ca959f87f5dd344af5e4eb`), actionlint, release-please, git (signed commits).

## Global Constraints

- All work happens in the existing worktree: `/Users/eduardoa/src/github/nvidia/k8s-test-infra/.worktrees/github-actions-automation-design` (branch `docs/github-actions-automation-design`, pushes to fork remote `origin` = ArangoGutierrez/k8s-test-infra). Call it `$WT` below: `WT=/Users/eduardoa/src/github/nvidia/k8s-test-infra/.worktrees/github-actions-automation-design`.
- Every commit: conventional format, DCO sign-off AND GPG signature: `git commit -s -S`.
- Never edit `dist/index.js` by hand; regenerate with `npm run package` (Task 11). CI enforces `git diff --exit-code -- dist`.
- `.github/actions/repo-automation` requires Node >= 24 (local node is v26.5.0 — fine).
- Do not edit `docs/superpowers/plans/2026-07-16-*.md` — they are historical records.
- Sandbox note: run `git rebase`, `git worktree`, and `gh` commands sandbox-disabled; pipe-free exit-code capture per shell-conventions (`cmd > /tmp/out 2>&1; rc=$?`).
- Rebase workflow only — no merge commits on the feature branch.

---

### Task 1: Rebase onto upstream/main (re-applies #465, resolves CONFLICTING)

**Files:**
- Modify: entire branch (rebase of 63 commits onto `upstream/main` @ `b0a55b78`)
- Conflict expected ONLY in: `.gitignore`

**Interfaces:**
- Consumes: nothing (first task).
- Produces: a branch where `.github/workflows/nvml-mock-e2e-go.yaml` contains main's `build-image`/ttl.sh job (#465) plus the PR's hardening. All later tasks assume the post-rebase tree.

- [ ] **Step 1: Capture the expected post-merge e2e workflow before rebasing**

```bash
R=/Users/eduardoa/src/github/nvidia/k8s-test-infra
git -C "$R" fetch upstream main
MT=$(git -C "$R" merge-tree --write-tree upstream/main docs/github-actions-automation-design | head -1)
git -C "$R" show "$MT:.github/workflows/nvml-mock-e2e-go.yaml" > /tmp/claude/e2e-expected.yaml
git -C "$R" show "$MT:.github/workflows/golang.yaml" > /tmp/claude/golang-expected.yaml
wc -l /tmp/claude/e2e-expected.yaml /tmp/claude/golang-expected.yaml
```
Expected: both files non-empty; `e2e-expected.yaml` contains a `build-image:` job and `ttl.sh` references.

- [ ] **Step 2: Rebase with re-signing**

```bash
git -C "$WT" rebase -S upstream/main > /tmp/claude/rebase.out 2>&1; rc=$?; echo "rc=$rc"; tail -5 /tmp/claude/rebase.out
```
Expected: `rc=1` with a conflict in `.gitignore` at the commit that introduced the automation ignore rules (possibly more than one stop; apply Step 3 each time). If a conflict appears in `nvml-mock-e2e-go.yaml` or `golang.yaml`, resolve by taking the corresponding `/tmp/claude/*-expected.yaml` content verbatim.

- [ ] **Step 3: Resolve the .gitignore conflict as the union of both sides**

The final `.gitignore` (post-rebase tip) must contain main's addition AND the PR's additions, i.e. the tail of the file reads:

```gitignore
# Agentic working artifacts
.agents/
.superpowers/
AGENTS.md
docs/plans/
docs/superpowers/
docs/magi/
.worktrees/
.spike-dcgm/

# Repository automation action dependencies and checked-in bundle.
/.github/actions/repo-automation/node_modules/
!/.github/actions/repo-automation/dist/
!/.github/actions/repo-automation/dist/index.js

# Checksum-verified workflow linter cache.
.cache/actionlint/
```

At each conflicted stop: edit `.gitignore` to the union, then
```bash
git -C "$WT" add .gitignore && git -C "$WT" rebase --continue
```
(If the same conflict repeats on later commits, repeat. `git rebase --abort` restores the pre-rebase branch if anything looks wrong.)

- [ ] **Step 4: Verify the e2e workflow kept #465 plus the PR hardening**

```bash
diff /tmp/claude/e2e-expected.yaml "$WT/.github/workflows/nvml-mock-e2e-go.yaml" && echo E2E-OK
diff /tmp/claude/golang-expected.yaml "$WT/.github/workflows/golang.yaml" && echo GOLANG-OK
grep -c "build-image\|ttl.sh" "$WT/.github/workflows/nvml-mock-e2e-go.yaml"
```
Expected: `E2E-OK`, `GOLANG-OK`, grep count >= 3. If diff fails, STOP and inspect — do not hand-edit past a mismatch.

- [ ] **Step 5: Post-rebase gate**

```bash
cd "$WT/.github/actions/repo-automation" && npm ci > /tmp/claude/npmci.out 2>&1; echo "npm ci rc=$?"
npm test > /tmp/claude/npmtest.out 2>&1; echo "npm test rc=$?"; tail -3 /tmp/claude/npmtest.out
cd "$WT" && make actionlint > /tmp/claude/lint.out 2>&1; echo "actionlint rc=$?"
helm unittest "$WT/deployments/nvml-mock/helm/nvml-mock" > /tmp/claude/helmut.out 2>&1; echo "helm unittest rc=$?"; tail -3 /tmp/claude/helmut.out
```
Expected: all rc=0 (129 helm tests pass). No commit — the rebase already rewrote history.

---

### Task 2: `release.yml` — stop gating on `tee`'s exit code (finding: dead #390 retry)

**Files:**
- Modify: `.github/workflows/release.yml` (two `run:` blocks: the release-plan step containing `node .github/scripts/release-state.mjs plan ... | tee -a "$GITHUB_STEP_SUMMARY"`, and the `chart-push` step at the `for attempt in 1 2 3` loop)

**Interfaces:**
- Consumes: post-rebase tree from Task 1.
- Produces: nothing consumed later (workflow-only change).

- [ ] **Step 1: Demonstrate the bug with a local shell simulation (RED)**

```bash
cat > /tmp/claude/retry-sim.sh <<'SH'
#!/bin/bash
# Emulates the GitHub Actions implicit shell: bash -e {0}
set -e
FAKE_LOG=/tmp/claude/helm-sim-calls.log
: > "$FAKE_LOG"
helm() {
  echo "call" >> "$FAKE_LOG"
  if [ "$(wc -l < "$FAKE_LOG")" -lt 3 ]; then return 1; fi
  echo "Digest: sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
}
[ "$1" = "on" ] && set -o pipefail
for attempt in 1 2 3; do
  if helm push archive target 2>&1 | tee /tmp/claude/chart-push.log; then
    break
  fi
  if [[ "$attempt" -eq 3 ]]; then echo "helm push failed after bounded retries" >&2; exit 1; fi
  sleep 0
done
echo "attempts=$(wc -l < "$FAKE_LOG")"
SH
bash /tmp/claude/retry-sim.sh off
bash /tmp/claude/retry-sim.sh on
```
Expected: `off` prints `attempts=1` (bug: the failing push "succeeds" via tee's rc and the loop breaks); `on` prints `attempts=3` (retry works, third attempt emits the Digest line).

- [ ] **Step 2: Add `set -o pipefail` to both piped run blocks**

In `.github/workflows/release.yml`, make these two edits (line numbers from the pre-rebase file; release.yml is PR-added so they are stable — locate with `grep -n "release-state.mjs plan\|for attempt in 1 2 3" "$WT/.github/workflows/release.yml"`):

Edit A — the plan step (around line 58-61). Change:
```yaml
        run: |
          SOURCE_SHA="$HEAD_SHA" node .github/scripts/release-state.mjs plan "$CHART_DIR/Chart.yaml" | tee -a "$GITHUB_STEP_SUMMARY"
```
to:
```yaml
        run: |
          set -o pipefail
          SOURCE_SHA="$HEAD_SHA" node .github/scripts/release-state.mjs plan "$CHART_DIR/Chart.yaml" | tee -a "$GITHUB_STEP_SUMMARY"
```

Edit B — the chart-push step (around line 858). Change:
```yaml
        run: |
          for attempt in 1 2 3; do
```
to:
```yaml
        run: |
          set -o pipefail
          for attempt in 1 2 3; do
```

- [ ] **Step 3: Verify**

```bash
grep -n -B1 -A1 "set -o pipefail" "$WT/.github/workflows/release.yml"
cd "$WT" && make actionlint > /tmp/claude/lint2.out 2>&1; echo "rc=$?"
```
Expected: exactly 2 occurrences, both as the first line of their run blocks; actionlint rc=0.

- [ ] **Step 4: Commit**

```bash
git -C "$WT" add .github/workflows/release.yml
git -C "$WT" commit -s -S -m "fix(ci): gate helm push retry on the push exit status, not tee

The chart-push retry loop and the release-plan step piped a gating
command into tee under the implicit 'bash -e {0}' shell, so the if
read tee's exit code (always 0): the 3-attempt retry added for the
GHCR tag-consistency flake never retried, and a plan failure could
pass unnoticed. set -o pipefail restores the intended gating."
```

---

### Task 3: Run `.github/scripts` tests in CI (finding: tests exist, nothing runs them)

**Files:**
- Modify: `.github/workflows/automation-ci.yml`

**Interfaces:**
- Consumes: post-rebase tree.
- Produces: CI job name `Release scripts CI` (referenced in nothing else; informational).

- [ ] **Step 1: Prove the tests pass locally before wiring (baseline)**

```bash
cd "$WT" && node --test --test-reporter=spec .github/scripts/ > /tmp/claude/scripts-test.out 2>&1; echo "rc=$?"; tail -5 /tmp/claude/scripts-test.out
```
Expected: rc=0; suites from `release-state.test.mjs`, `release-reader.test.mjs`, `spdx.test.mjs` all pass.

- [ ] **Step 2: Extend the paths filter and add the job**

In `.github/workflows/automation-ci.yml`, change the trigger block:
```yaml
on:
  pull_request:
    paths:
      - .github/actions/repo-automation/**
      - .github/repo-automation/**
      - .github/workflows/**
      - OWNERS
      - OWNERS_ALIASES
```
to:
```yaml
on:
  pull_request:
    paths:
      - .github/actions/repo-automation/**
      - .github/repo-automation/**
      - .github/schemas/**
      - .github/scripts/**
      - .github/workflows/**
      - OWNERS
      - OWNERS_ALIASES
```

Append this job at the end of the `jobs:` map (sibling of `automation-ci`):
```yaml
  scripts-ci:
    name: Release scripts CI
    runs-on: ubuntu-latest
    timeout-minutes: 15
    permissions:
      contents: read
    steps:
      - name: Check out repository
        uses: actions/checkout@9c091bb21b7c1c1d1991bb908d89e4e9dddfe3e0 # v7.0.0
        with:
          persist-credentials: false
          submodules: false

      - name: Set up Node.js
        uses: actions/setup-node@249970729cb0ef3589644e2896645e5dc5ba9c38 # v6
        with:
          node-version: 24

      - name: Run release script tests
        run: node --test --test-reporter=spec .github/scripts/
```

- [ ] **Step 3: Verify and commit**

```bash
cd "$WT" && make actionlint > /tmp/claude/lint3.out 2>&1; echo "rc=$?"
git -C "$WT" add .github/workflows/automation-ci.yml
git -C "$WT" commit -s -S -m "ci(automation): run release script tests in automation CI

.github/scripts/*.test.mjs existed but no workflow or make target
ever executed them, and automation-ci's paths filter excluded
.github/scripts entirely - a release-logic regression would have
merged with zero test signal."
```

---

### Task 4: Version-independent helm-unittest suites (finding: every release-please bump goes red)

**Files:**
- Modify: all 7 suites in `deployments/nvml-mock/helm/nvml-mock/tests/`: `configmap_test.yaml`, `daemonset_test.yaml`, `network_policy_ibping_test.yaml`, `notes_test.yaml`, `nri_daemonset_test.yaml`, `rbac_test.yaml`, `service_ibping_test.yaml`
- Regenerate: `deployments/nvml-mock/helm/nvml-mock/tests/__snapshot__/*.snap`

**Interfaces:**
- Consumes: post-rebase tree (Task 1 already re-ran the suite green).
- Produces: version-hermetic suites — Chart.yaml bumps no longer touch tests. Task 10 relies on the regenerated snapshots as its no-drift baseline.

- [ ] **Step 1: Align the local plugin with the CI pin**

```bash
helm plugin uninstall unittest 2>/dev/null; helm plugin install https://github.com/helm-unittest/helm-unittest.git --version 6f82a998e0b5461762ca959f87f5dd344af5e4eb
helm plugin list | grep -i unittest
```
Expected: plugin installed at the pinned commit (same as `.github/workflows/helm.yaml`).

- [ ] **Step 2: Pin the rendered chart version in every suite**

helm-unittest supports a suite-level `chart:` override (`chart.version`, `chart.appVersion`) that replaces Chart.yaml values at render time. In EACH of the 7 test files, insert this block immediately after the `templates:` list (same indentation level as `suite:`):

```yaml
chart:
  version: 0.0.0-tests
  appVersion: 0.0.0-tests
```

- [ ] **Step 3: Update the 8 hardcoded version assertions**

| File:line (pre-edit) | Old value | New value |
|---|---|---|
| `tests/configmap_test.yaml:76` | `nvml-mock-0.2.1` | `nvml-mock-0.0.0-tests` |
| `tests/daemonset_test.yaml:63` | `nvml-mock-0.2.1` | `nvml-mock-0.0.0-tests` |
| `tests/daemonset_test.yaml:72` | `"0.2.1"` | `"0.0.0-tests"` |
| `tests/daemonset_test.yaml:105` | `"ghcr.io/nvidia/nvml-mock:0.2.1"` | `"ghcr.io/nvidia/nvml-mock:0.0.0-tests"` |
| `tests/nri_daemonset_test.yaml:39` | `ghcr.io/nvidia/nvml-mock:0.2.1` | `ghcr.io/nvidia/nvml-mock:0.0.0-tests` |
| `tests/rbac_test.yaml:38` | `nvml-mock-0.2.1` | `nvml-mock-0.0.0-tests` |
| `tests/rbac_test.yaml:74` | `nvml-mock-0.2.1` | `nvml-mock-0.0.0-tests` |
| `tests/rbac_test.yaml:117` | `nvml-mock-0.2.1` | `nvml-mock-0.0.0-tests` |

- [ ] **Step 4: Regenerate snapshots and run the suite**

```bash
CH="$WT/deployments/nvml-mock/helm/nvml-mock"
helm unittest -u "$CH" > /tmp/claude/helm-regen.out 2>&1; echo "rc=$?"; tail -3 /tmp/claude/helm-regen.out
helm unittest "$CH" > /tmp/claude/helm-green.out 2>&1; echo "rc=$?"; tail -3 /tmp/claude/helm-green.out
grep -rc "0\.2\.1" "$CH/tests/" || echo "no-stale-versions"
```
Expected: both rc=0 (129 tests, 15 snapshots); final grep prints `no-stale-versions` (zero remaining `0.2.1` literals under `tests/`).

- [ ] **Step 5: Prove hermeticity — simulate the next release-please bump (the finding's acceptance test)**

```bash
sed -i '' -e 's/^version: .*/version: 9.9.9/' -e 's/^appVersion: .*/appVersion: "9.9.9"/' "$CH/Chart.yaml"
helm unittest "$CH" > /tmp/claude/bump-sim.out 2>&1; rc=$?
git -C "$WT" checkout -- deployments/nvml-mock/helm/nvml-mock/Chart.yaml
echo "bump-sim rc=$rc"; tail -3 /tmp/claude/bump-sim.out
```
Expected: `bump-sim rc=0` — the suite no longer depends on Chart.yaml's version. (Before this task, the same simulation failed 20/129 tests and 13/15 snapshots.)

- [ ] **Step 6: Commit**

```bash
git -C "$WT" add deployments/nvml-mock/helm/nvml-mock/tests
git -C "$WT" commit -s -S -m "test(helm): pin chart version in unittest suites to survive release bumps

release-please's extra-files block rewrites Chart.yaml version and
appVersion on every release PR, but the suites asserted the live chart
version in labels, image tags, and snapshots - so every release PR
arrived with helm CI red (simulated: 20/129 tests, 13/15 snapshots).
Suite-level chart.version/appVersion pins make renders hermetic."
```

---

### Task 5: `/retest` reaches the primary Go CI (TDD)

**Files:**
- Modify: `.github/actions/repo-automation/src/retest.js:25-29` (constants) and the `rerunRunIds` filter (~line 198-209)
- Test: `.github/actions/repo-automation/test/retest.test.js` (the test at ~line 77, "plans only fixed source-controlled pull-request CI...")

**Interfaces:**
- Consumes: nothing new.
- Produces: `TRUSTED_WORKFLOW_EVENTS` (Map path → Set of allowed events) inside retest.js; exports unchanged (`planRetest`, `trustedWorkflowIdentity`).

Background: `ci.yaml` (which carries the Go build/test and e2e suites via `workflow_call`) triggers on `push` to `pull-request/[0-9]+` branches, never on `pull_request` — so the old `run.event === "pull_request"` filter made the primary CI unreachable regardless of the allow-list. `listWorkflowRunsForHead` queries by `head_sha`, so push runs for the PR's mirror branch share the PR's head OID and are already in the candidate list.

- [ ] **Step 1: Extend the failing test**

In `test/retest.test.js`, in the test `"plans only fixed source-controlled pull-request CI for this repository and PR"`, extend the `runs:` array (after `run(10, ...)`):

```js
      run(11, { workflowPath: ".github/workflows/ci.yaml", event: "push" }),
      run(12, { workflowPath: ".github/workflows/ci.yaml", event: "workflow_dispatch" }),
      run(13, { workflowPath: ".github/workflows/automation-ci.yml", event: "push" }),
```

and update the expectation:

```js
  assert.deepEqual(result, {
    rerunRunIds: [1, 2, 3, 11],
    nextAllowedAt: null,
    reason: "rerun-failed",
  });
```

- [ ] **Step 2: Run to verify it fails**

```bash
cd "$WT/.github/actions/repo-automation" && node --test --test-reporter=spec test/retest.test.js > /tmp/claude/retest-red.out 2>&1; echo "rc=$?"; grep -c "fail" /tmp/claude/retest-red.out
```
Expected: rc=1 — run 11 is excluded by the current `pull_request`-only filter (`rerunRunIds` comes back `[1, 2, 3]`).

- [ ] **Step 3: Implement — per-workflow allowed events**

In `src/retest.js`, replace lines 25-29:

```js
const TRUSTED_WORKFLOW_PATHS = new Set([
  ".github/workflows/automation-ci.yml",
  ".github/workflows/basic-checks.yaml",
  ".github/workflows/helm.yaml",
]);
```

with:

```js
const TRUSTED_WORKFLOW_EVENTS = new Map([
  [".github/workflows/automation-ci.yml", new Set(["pull_request"])],
  [".github/workflows/basic-checks.yaml", new Set(["pull_request"])],
  [".github/workflows/ci.yaml", new Set(["push"])],
  [".github/workflows/helm.yaml", new Set(["pull_request"])],
]);
const TRUSTED_WORKFLOW_PATHS = new Set(TRUSTED_WORKFLOW_EVENTS.keys());
```

In the `rerunRunIds` filter (~line 199-207), replace the event line:

```js
      && normalizedWorkflowIdentity(run)
      && run.event === "pull_request"
```

with:

```js
      && normalizedWorkflowIdentity(run)
      && TRUSTED_WORKFLOW_EVENTS.get(run.workflowPath).has(run.event)
```

(`normalizedWorkflowIdentity` already guarantees `run.workflowPath` is a key of the map, so `.get()` cannot return undefined.)

- [ ] **Step 4: Run tests to verify they pass**

```bash
cd "$WT/.github/actions/repo-automation" && node --test --test-reporter=spec test/retest.test.js > /tmp/claude/retest-green.out 2>&1; echo "rc=$?"
npm test > /tmp/claude/full1.out 2>&1; echo "full rc=$?"; npm run lint > /tmp/claude/eslint1.out 2>&1; echo "lint rc=$?"
```
Expected: all rc=0.

- [ ] **Step 5: Commit**

```bash
git -C "$WT" add .github/actions/repo-automation/src/retest.js .github/actions/repo-automation/test/retest.test.js
git -C "$WT" commit -s -S -m "fix(automation): let /retest rerun push-triggered ci.yaml runs

The trusted-workflow allow-list omitted ci.yaml (the caller of the Go
build/test and e2e suites) and the rerun filter required the
pull_request event, while ci.yaml only ever runs on push to
pull-request/N mirror branches - so /retest could never rerun the
primary CI. Trust is now a per-workflow event map: ci.yaml on push,
the direct pull_request workflows unchanged."
```

---

### Task 6: Apply/remove the `needs-rebase` label (TDD; spec'd but unimplemented)

**Files:**
- Modify: `.github/actions/repo-automation/src/github-client.js` (insert after `getPullRequest`, ~line 521)
- Modify: `.github/actions/repo-automation/src/managed-labels.js:3`
- Modify: `.github/actions/repo-automation/src/modes/metadata.js` (~line 224 fetch; ~line 310 desiredLabels)
- Modify: `.github/actions/repo-automation/test/helpers/fake-github.js` (next to its `getPullRequest`)
- Test: `.github/actions/repo-automation/test/metadata-contract.test.js` (3 new tests)

**Interfaces:**
- Consumes: `labelPlan` (metadata.js:74) removal semantics — it removes labels for which `isManagedMetadataLabel()` is true and that are absent from `desiredLabels`.
- Produces: client method `getPullRequestMergeable(prNumber) -> "MERGEABLE" | "CONFLICTING" | "UNKNOWN"` (string enum mirrors merge-state.js's `MERGEABILITY_STATES`). Task 9 folds this call into its `Promise.all`.

Design: a separate client method (a second `pulls.get` call) rather than widening the `getPullRequest` snapshot — the snapshot is consumed by exact-key validators in several modes, and REST `mergeable` is tri-state (`true`/`false`/`null`, null while GitHub computes asynchronously). Semantics: CONFLICTING → desired; MERGEABLE → not desired (managed removal); UNKNOWN → preserve the current state (no add, no remove).

- [ ] **Step 1: Write the failing tests**

Append to `test/metadata-contract.test.js` (after the existing metadata tests; `metadataState`, `createFakeGitHub`, `loadConfig`, `repositoryRoot`, and `event` are already in scope in this file):

```js
test("adds needs-rebase when the live pull request is conflicting", async () => {
  const { runMetadata } = require("../src/modes/metadata.js");
  const github = createFakeGitHub(metadataState({ mergeable: "CONFLICTING" }));

  const result = await runMetadata({ event, github, config: loadConfig(repositoryRoot), dryRun: false });

  assert.equal(result.labels.add.includes("needs-rebase"), true);
  assert.equal(github.metadataSnapshot().labels.includes("needs-rebase"), true);
});

test("removes needs-rebase when the live pull request is mergeable again", async () => {
  const { runMetadata } = require("../src/modes/metadata.js");
  const github = createFakeGitHub(metadataState({
    mergeable: "MERGEABLE",
    labels: ["kind/bug", "size/S", "area/ci", "lgtm", "approved", "do-not-merge/hold", "maintainer/custom", "needs-rebase"],
  }));

  const result = await runMetadata({ event, github, config: loadConfig(repositoryRoot), dryRun: false });

  assert.equal(result.labels.remove.includes("needs-rebase"), true);
  assert.equal(github.metadataSnapshot().labels.includes("needs-rebase"), false);
});

test("preserves needs-rebase while mergeability is unknown", async () => {
  const { runMetadata } = require("../src/modes/metadata.js");
  const github = createFakeGitHub(metadataState({
    mergeable: "UNKNOWN",
    labels: ["kind/bug", "size/S", "area/ci", "lgtm", "approved", "do-not-merge/hold", "maintainer/custom", "needs-rebase"],
  }));

  const result = await runMetadata({ event, github, config: loadConfig(repositoryRoot), dryRun: false });

  assert.equal(result.labels.add.includes("needs-rebase"), false);
  assert.equal(result.labels.remove.includes("needs-rebase"), false);
  assert.equal(github.metadataSnapshot().labels.includes("needs-rebase"), true);
});
```

- [ ] **Step 2: Run to verify they fail**

```bash
cd "$WT/.github/actions/repo-automation" && node --test --test-reporter=spec test/metadata-contract.test.js > /tmp/claude/nr-red.out 2>&1; echo "rc=$?"
```
Expected: rc=1 — `github.getPullRequestMergeable is not a function` (or the label assertions fail).

- [ ] **Step 3: Implement**

3a. `src/github-client.js` — insert after the `getPullRequest` method (after its closing `},` ~line 521):

```js
    async getPullRequestMergeable(prNumber) {
      positiveInteger(prNumber, "PR number");
      const { data } = await call("getPullRequestMergeable", () => octokit.rest.pulls.get({
        owner, repo, pull_number: prNumber,
      }), true);
      if (data.mergeable === true) return "MERGEABLE";
      if (data.mergeable === false) return "CONFLICTING";
      return "UNKNOWN";
    },
```

3b. `test/helpers/fake-github.js` — insert after its `getPullRequest` method:

```js
    async getPullRequestMergeable(prNumber) {
      record("getPullRequestMergeable", { prNumber });
      return clone(options.mergeable ?? "UNKNOWN");
    },
```

3c. `src/managed-labels.js:3` — change:

```js
const MANAGED_STATE_LABELS = new Set(["do-not-merge/work-in-progress"]);
```
to:
```js
const MANAGED_STATE_LABELS = new Set(["do-not-merge/work-in-progress", "needs-rebase"]);
```

3d. `src/modes/metadata.js` — after line 224 (`const issueLabels = await github.listIssueLabels(identity.prNumber);`) add:

```js
  const mergeable = await github.getPullRequestMergeable(identity.prNumber);
```

and replace the `desiredLabels` block (~line 310-315):

```js
  const desiredLabels = [
    ...(title.valid ? [title.label] : []),
    ...(size === null ? [] : [size.label]),
    ...areas,
    ...(pullRequest.draft ? ["do-not-merge/work-in-progress"] : []),
  ];
```

with:

```js
  const currentNeedsRebase = issueLabels.some(
    (label) => typeof label === "string" && label.toLowerCase() === "needs-rebase",
  );
  const needsRebase = mergeable === "CONFLICTING"
    || (mergeable === "UNKNOWN" && currentNeedsRebase);
  const desiredLabels = [
    ...(title.valid ? [title.label] : []),
    ...(size === null ? [] : [size.label]),
    ...areas,
    ...(pullRequest.draft ? ["do-not-merge/work-in-progress"] : []),
    ...(needsRebase ? ["needs-rebase"] : []),
  ];
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
cd "$WT/.github/actions/repo-automation" && npm test > /tmp/claude/nr-green.out 2>&1; echo "rc=$?"; npm run lint > /tmp/claude/eslint2.out 2>&1; echo "lint rc=$?"
```
Expected: rc=0 for both — the 3 new tests pass and every pre-existing test stays green (the fake defaults `mergeable` to `"UNKNOWN"` and no default fixture carries `needs-rebase`, so old expectations are untouched).

- [ ] **Step 5: Commit**

```bash
git -C "$WT" add .github/actions/repo-automation/src/github-client.js .github/actions/repo-automation/src/managed-labels.js .github/actions/repo-automation/src/modes/metadata.js .github/actions/repo-automation/test/helpers/fake-github.js .github/actions/repo-automation/test/metadata-contract.test.js
git -C "$WT" commit -s -S -m "feat(automation): manage needs-rebase label from live mergeability

labels.yml declared needs-rebase as automation-managed and the design
spec promised it on CONFLICTING PRs, but no code path ever applied or
removed it. Metadata mode now fetches tri-state mergeability
(REST mergeable true/false/null -> MERGEABLE/CONFLICTING/UNKNOWN),
adds the label on CONFLICTING, removes it on MERGEABLE via the
managed-label plan, and preserves it while GitHub is still computing."
```

---

### Task 7: Thread frozen policy values from config (dead-key finding)

**Files:**
- Modify: `.github/actions/repo-automation/src/modes/command.js` (~line 293)
- Modify: `.github/actions/repo-automation/src/modes/merge-evaluate.js` (~line 371)

**Interfaces:**
- Consumes: `validConfiguration` (command.js:224); `validateConfig(config)` already runs at merge-evaluate.js:318 before line 371, so `config.policy.merge.method` is safe there.
- Produces: nothing new.

Honest scope note: `config.js` freezes both values (`retestCooldownSeconds` must be 600, `merge.method` must be SQUASH), so no black-box test can distinguish threaded from hardcoded — this is a wiring refactor so a future policy change needs only the validator + yml, not a hunt for scattered literals. Verification is suite-green + lint. `retest.js`'s own `input.cooldownSeconds !== 600` guard and `github-client.js:607`'s SQUASH assertion stay as defense-in-depth.

- [ ] **Step 1: command.js — use the config value when configuration is valid**

Replace line ~293:
```js
    cooldownSeconds: 600,
```
with:
```js
    cooldownSeconds: validConfiguration ? config.policy.commands.retestCooldownSeconds : 600,
```

- [ ] **Step 2: merge-evaluate.js — use the config merge method**

Replace line ~371:
```js
      await github.enableAutoMerge(finalGraph.nodeId, "SQUASH");
```
with:
```js
      await github.enableAutoMerge(finalGraph.nodeId, config.policy.merge.method);
```

- [ ] **Step 3: Verify and commit**

```bash
cd "$WT/.github/actions/repo-automation" && npm test > /tmp/claude/cfg.out 2>&1; echo "rc=$?"; npm run lint > /tmp/claude/eslint3.out 2>&1; echo "lint rc=$?"
git -C "$WT" add .github/actions/repo-automation/src/modes/command.js .github/actions/repo-automation/src/modes/merge-evaluate.js
git -C "$WT" commit -s -S -m "refactor(automation): thread policy config into retest cooldown and merge method

policy.yml's retestCooldownSeconds and merge.method were schema-
validated but never consumed - the call sites hardcoded 600 and
SQUASH independently. Thread the config values through so a future
policy change edits the validator and yml only. Behavior unchanged
(the validator freezes both values)."
```

---

### Task 8: Entrypoint fails via `core.setFailed` even when bootstrap imports reject

**Files:**
- Modify: `.github/actions/repo-automation/src/index.js:78-102`

**Interfaces:**
- Consumes: nothing new. Produces: nothing new (`module.exports = { run }` unchanged).

Scope note: the failure path is the dynamic import of `@actions/core`/`@actions/github` itself — a framework boundary not reachable from `node --test` without module-loader mocking, and the repo's eslint set (`no-undef`, `no-unused-vars`) has no floating-promise rule. No new unit test; verification is suite + lint green and the structural property that `executeAction` can no longer reject.

- [ ] **Step 1: Replace `executeAction` (lines 78-98) and its invocation**

Replace:
```js
async function executeAction() {
  const [core, github] = await Promise.all([
    import(/* webpackMode: "eager" */ "@actions/core"),
    import(/* webpackMode: "eager" */ "@actions/github"),
  ]);

  try {
    const { owner, repo } = github.context.repo;
    const octokit = github.getOctokit(process.env.GITHUB_TOKEN);
    await run({
      core,
      octokit,
      owner,
      repo,
      event: github.context.payload,
      eventName: github.context.eventName,
    });
  } catch (error) {
    core.setFailed(error instanceof Error ? error.message : String(error));
  }
}
```
with:
```js
async function executeAction() {
  let core = null;
  try {
    const [coreModule, github] = await Promise.all([
      import(/* webpackMode: "eager" */ "@actions/core"),
      import(/* webpackMode: "eager" */ "@actions/github"),
    ]);
    core = coreModule;
    const { owner, repo } = github.context.repo;
    const octokit = github.getOctokit(process.env.GITHUB_TOKEN);
    await run({
      core,
      octokit,
      owner,
      repo,
      event: github.context.payload,
      eventName: github.context.eventName,
    });
  } catch (error) {
    const message = error instanceof Error ? error.message : String(error);
    if (core === null) {
      console.error(message);
      process.exitCode = 1;
    } else {
      core.setFailed(message);
    }
  }
}
```
(The `if (require.main === module) { executeAction(); }` invocation at line 100-102 stays as-is — the function now catches everything, so the un-awaited call can no longer produce an unhandled rejection.)

- [ ] **Step 2: Verify and commit**

```bash
cd "$WT/.github/actions/repo-automation" && npm test > /tmp/claude/idx.out 2>&1; echo "rc=$?"; npm run lint > /tmp/claude/eslint4.out 2>&1; echo "lint rc=$?"
git -C "$WT" add .github/actions/repo-automation/src/index.js
git -C "$WT" commit -s -S -m "fix(automation): fail cleanly when action bootstrap imports reject

The @actions/core and @actions/github dynamic imports sat outside the
try/catch and the top-level executeAction() call was un-awaited, so an
import rejection crashed the runner with an unhandled rejection instead
of a core.setFailed annotation. Imports now live inside the try; if
core itself failed to load, fall back to console.error + exitCode 1."
```

---

### Task 9: Batch independent GitHub reads (perf refactor)

**Files:**
- Modify: `.github/actions/repo-automation/src/modes/command.js` (~lines 206-211)
- Modify: `.github/actions/repo-automation/src/modes/metadata.js` (~lines 220-224 plus the Task 6 `mergeable` line)

**Interfaces:**
- Consumes: `getPullRequestMergeable` from Task 6 (its call joins the metadata batch). MUST run after Task 6.
- Produces: nothing new.

Note: `Promise.all` argument expressions evaluate in listed order, so the fake's `record()` call order is unchanged and the contract-test callOrder assertions stay valid.

- [ ] **Step 1: command.js — replace the sequential block (lines 206-211)**

Replace:
```js
  const files = await github.listPullRequestFiles(identity.prNumber);
  const reviews = await github.listPullRequestReviews(identity.prNumber);
  const requestedReviewers = await github.listRequestedReviewers(identity.prNumber);
  const currentAssignees = await github.listIssueAssignees(identity.prNumber);
  const currentLabels = await github.listIssueLabels(identity.prNumber);
  const policyComment = await github.getPolicyComment(identity.prNumber, POLICY_COMMENT_MARKER);
```
with:
```js
  const [files, reviews, requestedReviewers, currentAssignees, currentLabels, policyComment] = await Promise.all([
    github.listPullRequestFiles(identity.prNumber),
    github.listPullRequestReviews(identity.prNumber),
    github.listRequestedReviewers(identity.prNumber),
    github.listIssueAssignees(identity.prNumber),
    github.listIssueLabels(identity.prNumber),
    github.getPolicyComment(identity.prNumber, POLICY_COMMENT_MARKER),
  ]);
```

- [ ] **Step 2: metadata.js — replace the sequential block (lines 220-224 plus Task 6's mergeable line)**

Replace:
```js
  const files = await github.listPullRequestFiles(identity.prNumber);
  const commits = await github.listPullRequestCommits(identity.prNumber);
  const reviews = await github.listPullRequestReviews(identity.prNumber);
  const requested = await github.listRequestedReviewers(identity.prNumber);
  const issueLabels = await github.listIssueLabels(identity.prNumber);
  const mergeable = await github.getPullRequestMergeable(identity.prNumber);
```
with:
```js
  const [files, commits, reviews, requested, issueLabels, mergeable] = await Promise.all([
    github.listPullRequestFiles(identity.prNumber),
    github.listPullRequestCommits(identity.prNumber),
    github.listPullRequestReviews(identity.prNumber),
    github.listRequestedReviewers(identity.prNumber),
    github.listIssueLabels(identity.prNumber),
    github.getPullRequestMergeable(identity.prNumber),
  ]);
```

- [ ] **Step 3: Verify and commit**

```bash
cd "$WT/.github/actions/repo-automation" && npm test > /tmp/claude/batch.out 2>&1; echo "rc=$?"; npm run lint > /tmp/claude/eslint5.out 2>&1; echo "lint rc=$?"
git -C "$WT" add .github/actions/repo-automation/src/modes/command.js .github/actions/repo-automation/src/modes/metadata.js
git -C "$WT" commit -s -S -m "perf(automation): batch independent GitHub reads in command and metadata modes

Six (command) and six (metadata) independent read calls were awaited
sequentially on hot event paths; both functions already used
Promise.all for their other independent pairs. Argument evaluation
order preserves the recorded call order, so contracts are unchanged."
```

---

### Task 10: Drop the dead `tag` field from the merged NRI image dict

**Files:**
- Modify: `deployments/nvml-mock/helm/nvml-mock/templates/nri-daemonset.yaml:4`

**Interfaces:**
- Consumes: Task 4's regenerated snapshots as the no-drift baseline.
- Produces: nothing new.

- [ ] **Step 1: Edit line 4**

Replace:
```
{{- $image := mergeOverwrite (deepCopy $rootImage) $nriImage }}
```
with:
```
{{- /* tag is intentionally omitted: the container image tag comes from $imageTag below */ -}}
{{- $image := mergeOverwrite (deepCopy (omit $rootImage "tag")) (omit $nriImage "tag") }}
```

- [ ] **Step 2: Verify rendered output is bit-identical (snapshots must NOT change)**

```bash
CH="$WT/deployments/nvml-mock/helm/nvml-mock"
helm unittest "$CH" > /tmp/claude/nri.out 2>&1; echo "rc=$?"
git -C "$WT" diff --exit-code -- deployments/nvml-mock/helm/nvml-mock/tests/__snapshot__ && echo SNAPSHOTS-UNCHANGED
```
Expected: rc=0 and `SNAPSHOTS-UNCHANGED` — `$image.tag` was dead, so removing it from the merge cannot alter any manifest.

- [ ] **Step 3: Commit**

```bash
git -C "$WT" add deployments/nvml-mock/helm/nvml-mock/templates/nri-daemonset.yaml
git -C "$WT" commit -s -S -m "refactor(helm): drop dead tag field from merged NRI image dict

\$image.tag survived the mergeOverwrite with different empty-string
semantics than the \$imageTag actually used by the container spec; a
future edit reading \$image.tag would silently resurrect the
empty-tag-overwrite bug. Omit tag from both merge inputs."
```

---

### Task 11: Repackage dist, commit the plan doc, run the full local CI mirror

**Files:**
- Regenerate: `.github/actions/repo-automation/dist/index.js`
- Create (commit): `docs/superpowers/plans/2026-07-19-pr474-review-fixes.md` (this file)

**Interfaces:**
- Consumes: all src changes from Tasks 5-9. MUST run after them.
- Produces: a tree that passes every gate `automation-ci.yml` will run.

- [ ] **Step 1: Repackage and commit dist + plan doc**

```bash
cd "$WT/.github/actions/repo-automation" && npm run package > /tmp/claude/pack.out 2>&1; echo "rc=$?"
git -C "$WT" add .github/actions/repo-automation/dist docs/superpowers/plans/2026-07-19-pr474-review-fixes.md
git -C "$WT" commit -s -S -m "chore(automation): repackage dist for review fixes

Regenerated with npm run package (ncc) after the retest, needs-rebase,
config-threading, entrypoint, and batching changes. Includes the
2026-07-19 review-fix plan document."
```

- [ ] **Step 2: Full local CI mirror (every gate automation-ci + helm + scripts CI will run)**

```bash
cd "$WT/.github/actions/repo-automation"
npm test > /tmp/claude/g1.out 2>&1; echo "npm test rc=$?"
npm run lint > /tmp/claude/g2.out 2>&1; echo "lint rc=$?"
npm run package > /tmp/claude/g3.out 2>&1; echo "package rc=$?"
git -C "$WT" diff --exit-code -- .github/actions/repo-automation/dist && echo DIST-CURRENT
cd "$WT" && make actionlint > /tmp/claude/g4.out 2>&1; echo "actionlint rc=$?"
node --test --test-reporter=spec .github/scripts/ > /tmp/claude/g5.out 2>&1; echo "scripts rc=$?"
helm unittest "$WT/deployments/nvml-mock/helm/nvml-mock" > /tmp/claude/g6.out 2>&1; echo "helm rc=$?"
git -C "$WT" status --porcelain | head -3
```
Expected: every rc=0, `DIST-CURRENT`, and a clean `git status` (no uncommitted changes). If `npm run package` in this step dirties dist again, ncc is non-deterministic across runs — investigate before proceeding; do NOT commit a second dist churn blindly.

---

### Task 12: Push and verify the PR (operator-confirmed)

**Files:** none (remote operation).

**Interfaces:**
- Consumes: everything above, green.

- [ ] **Step 1: Pre-push review**

```bash
git -C "$WT" log --oneline upstream/main..HEAD | head -15
git -C "$WT" diff --stat 79e45eacda7ba272eb67acb578dbea54ac7af908..HEAD -- ':(exclude).github/actions/repo-automation/dist' | tail -5
```
Expected: the rebased 63 commits plus the 9 fix commits from Tasks 2-11, all signed.

- [ ] **Step 2: Push (rebase requires force-with-lease) — CHECKPOINT: confirm with Carlos before executing**

```bash
git -C "$WT" push --force-with-lease origin docs/github-actions-automation-design
```

- [ ] **Step 3: Verify the PR converges**

```bash
gh pr view 474 -R NVIDIA/k8s-test-infra --json mergeable,headRefOid
gh pr checks 474 -R NVIDIA/k8s-test-infra --watch
```
Expected: `mergeable` becomes `MERGEABLE` (GitHub may briefly report UNKNOWN while recomputing), new head SHA, all checks green — including the new `Release scripts CI` job once a workflow-touching event triggers `automation-ci.yml`.

---

## Findings → task coverage

| # | Finding (2026-07-19 review) | Task |
|---|---|---|
| 1 | release-please bump breaks helm-unittest assertions/snapshots (must-fix) | 4 |
| 2 | `helm push \| tee` defeats the #390 retry, plus the sibling plan-step pipe (must-fix) | 2 |
| 3 | `.github/scripts/*.test.mjs` never run in CI (must-fix) | 3 |
| 4 | `/retest` can't reach push-triggered primary CI (should-fix) | 5 |
| 5 | e2e rewrite predates #465 build-once/ttl.sh; PR CONFLICTING (should-fix) | 1 |
| 6 | `needs-rebase` declared + spec'd, never applied (should-fix) | 6 |
| 7 | `retestCooldownSeconds`/`merge.method` validated but never consumed (sub-threshold) | 7 |
| 8 | Unhandled rejection on bootstrap import failure in index.js (sub-threshold) | 8 |
| 9 | Sequential awaits of independent reads in command/metadata (sub-threshold) | 9 |
| 10 | Dead `$image.tag` with divergent empty-string semantics (sub-threshold) | 10 |
