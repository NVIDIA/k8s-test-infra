# Workflow Governance and Hardening Implementation Plan

> **For Codex:** REQUIRED SUB-SKILL: Use `superpowers:subagent-driven-development` to implement this plan task-by-task.

**Goal:** Bring all repository workflows under consistent least-privilege, immutable-action, validation, dependency, stale-lifecycle, and maintainer-setting controls.

**Architecture:** Add an independently checksum-verified actionlint installer, make workflow linting a first-class Make/CI check, introduce GitHub's official dependency review, and harden existing jobs in place. Preserve the existing Scorecard and publication security work. Stale policy is corrected but remains dry-run-only until maintainers inspect the exact candidate population and explicitly authorize those remote closures.

**Tech stack:** GitHub Actions, actionlint v1.7.12, GitHub dependency-review-action v5.0.0, actions/stale v10, Dependabot, existing Go/Helm/CodeQL workflows.

**Depends on:** Plan 1 action directory exists; Plan 2 `automation-ci.yml` exists. Plan 3 may be implemented before or after this plan, but re-run the all-workflow audit after both land.

**Immutable pins introduced here:**

- `actions/stale@1e223db275d687790206a7acac4d1a11bd6fe629 # v10`
- `actions/dependency-review-action@a1d282b36b6f3519aa1f3fc636f609c47dddb294 # v5.0.0`

---

## Task 1: Make workflow validation reproducible

**Files:**

- Create: `hack/actionlint.sh`
- Create: `hack/tests/actionlint_test.sh`
- Modify: `Makefile`

### Step 1: Write the failing installer contract

The shell test must use a temporary directory and a fake `curl` to prove:

- Linux amd64 selects `actionlint_1.7.12_linux_amd64.tar.gz`;
- Linux arm64 selects `actionlint_1.7.12_linux_arm64.tar.gz`;
- Darwin arm64 selects `actionlint_1.7.12_darwin_arm64.tar.gz`;
- archives match these SHA-256 values:
  - Linux amd64: `8aca8db96f1b94770f1b0d72b6dddcb1ebb8123cb3712530b08cc387b349a3d8`;
  - Linux arm64: `325e971b6ba9bfa504672e29be93c24981eeb1c07576d730e9f7c8805afff0c6`;
  - Darwin arm64: `aba9ced2dee8d27fecca3dc7feb1a7f9a52caefa1eb46f3271ea66b6e0e6953f`;
- a mismatched digest fails before extraction or execution;
- an existing binary reporting exactly `1.7.12` is reused;
- an unsupported OS/architecture fails clearly;
- checksum verification uses `sha256sum` when available and otherwise
  `shasum -a 256`, so the current Darwin/ARM64 workspace and Linux runners both work;
- downloads use `curl --fail --silent --show-error --location` and a fixed HTTPS GitHub release URL.

Run `bash hack/tests/actionlint_test.sh` and confirm RED because the installer is absent.

### Step 2: Implement the verified installer

`hack/actionlint.sh` installs only into `${ACTIONLINT_CACHE_DIR:-.cache/actionlint/1.7.12}` and executes that binary against all `.github/workflows/*.yml` and `*.yaml`. Never pipe a download directly to a shell or tar. Download to a temporary file, verify with the available `sha256sum` or `shasum -a 256`, then extract only the `actionlint` member.

Implement verification by computing the first output field and comparing it to
the selected hard-coded digest: use `sha256sum "$archive"` when present,
otherwise `shasum -a 256 "$archive"`. Do not use a GNU-only `--check` path on
Darwin.

Add `.cache/actionlint/` to `.gitignore` if it is not already ignored.

### Step 3: Add Make targets and repair the stale build target

Add `actionlint` to `.PHONY` and:

```make
actionlint:
	./hack/actionlint.sh
```

The current `build` target references missing `cmd/nv-ci-bot/main.go`. Replace its recipe with `$(GO_CMD) build ./...`; do not resurrect the deleted bot or invent `BINARY_NAME` semantics.

### Step 4: Verify and commit

```bash
bash hack/tests/actionlint_test.sh
make actionlint
go build ./...
git add hack/actionlint.sh hack/tests/actionlint_test.sh Makefile .gitignore
git commit -s -S -m "ci: add reproducible workflow validation"
```

## Task 2: Add official dependency review

**Files:**

- Create: `.github/workflows/dependency-review.yml`

### Step 1: Define the workflow

Trigger on pull requests to `main` and `release-*`. Set top-level `permissions: {}`. The job:

- is named `Dependency review`;
- gets only `contents: read`;
- uses `ubuntu-latest`, a 10-minute timeout, and per-PR cancellable concurrency;
- invokes `actions/dependency-review-action@a1d282b36b6f3519aa1f3fc636f609c47dddb294 # v5.0.0`;
- fails on `high` or `critical` vulnerabilities;
- denies GPL-2.0-only and GPL-3.0-only licenses while allowing packages already present through the action's documented `allow-dependencies-licenses`/exception mechanism only when a concrete existing dependency requires it.

Do not add a PAT or write permission. Public repositories can use the dependency review API; document the GitHub Advanced Security requirement for private deployments in the runbook.

### Step 2: Validate and commit

```bash
actionlint .github/workflows/dependency-review.yml
git add .github/workflows/dependency-review.yml
git commit -s -S -m "ci: review pull request dependencies"
```

## Task 3: Correct stale policy and force the first run to be non-mutating

**Files:**

- Modify: `.github/workflows/stale.yaml`

### Step 1: Update the existing workflow in place

Keep the NVIDIA gpu-operator policy reference. Pin the action to `actions/stale@1e223db275d687790206a7acac4d1a11bd6fe629 # v10`. Set a 15-minute timeout and concurrency `stale-lifecycle` with no cancellation.

Use exact policy:

```yaml
days-before-issue-stale: 90
days-before-issue-close: 30
days-before-pr-stale: 30
days-before-pr-close: 14
stale-issue-label: lifecycle/stale
stale-pr-label: lifecycle/stale
exempt-issue-labels: lifecycle/frozen,kind/feature
exempt-pr-labels: lifecycle/frozen
remove-stale-when-updated: true
delete-branch: false
close-issue-reason: not_planned
debug-only: true
operations-per-run: 200
ascending: true
```

Update messages to say 90/30 for issues and 30/14 for PRs. Do not claim branch deletion, locking, or reopening behavior.

### Step 2: Add a static policy assertion

Create a small Node test under the local action tests or a shell assertion under `hack/tests/` that parses the workflow and proves the exact thresholds, exemptions, full SHA, `debug-only: true`, and `delete-branch: false`. Prefer using the already locked `yaml` package from Plan 1 so there is no second parser dependency.

### Step 3: Verify and commit dry-run policy

```bash
make actionlint
cd .github/actions/repo-automation
npm test
cd ../../..
git add .github/workflows/stale.yaml .github/actions/repo-automation/test
git commit -s -S -m "ci: make stale lifecycle policy dry-run safe"
```

### Step 4: Remote activation checkpoint

Dispatch the workflow with `debug-only: true` after it reaches the default branch and capture every issue/PR candidate in a reviewed report. Before changing `debug-only` to `false`, list the exact issue and PR numbers that would be labeled or closed, the effect, and recovery path, then obtain the destructive-action confirmation required by `AGENTS.md`. Activation is a separate signed commit; it is not part of this plan's implementation commit.

## Task 4: Extend Dependabot to all automation dependencies

**Files:**

- Modify: `.github/dependabot.yml`

### Step 1: Add the npm ecosystem

Add a weekly npm entry for directory `/.github/actions/repo-automation`, target branch `main`, day Sunday, and label `kind/dependencies`. Align existing gomod, GitHub Actions, and Docker entries to that label and valid YAML indentation. Keep GitHub Actions daily so full-SHA pins receive updates.

### Step 2: Validate and commit

```bash
cd .github/actions/repo-automation
npm ci
npm audit --audit-level=high
cd ../../..
git diff --check .github/dependabot.yml
git add .github/dependabot.yml
git commit -s -S -m "chore: monitor automation dependencies"
```

## Task 5: Audit every existing workflow for permissions, pins, timeouts, and concurrency

**Files:**

- Modify: `.github/workflows/basic-checks.yaml`
- Modify: `.github/workflows/ci.yaml`
- Modify: `.github/workflows/code_scanning.yaml`
- Modify: `.github/workflows/golang.yaml`
- Modify: `.github/workflows/helm.yaml`
- Modify: `.github/workflows/nvml-mock-e2e-go.yaml`
- Modify: `.github/workflows/scorecard.yaml`
- Modify: `.github/workflows/trigger-pages-deploy.yaml`
- Modify: `.github/workflows/variables.yaml`
- Modify: `.github/workflows/automation-ci.yml`

### Step 1: Add a failing static audit

Create `.github/actions/repo-automation/test/workflow-policy.test.js` that parses all workflow files and asserts:

- every external `uses:` is a full 40-character SHA with a version comment;
- every workflow declares top-level permissions;
- every runner job has `timeout-minutes`;
- write permissions appear only in an explicit allowlist of workflows/jobs
  loaded from a versioned test constant that Plan 5 must extend for the exact
  `release.yml` jobs and permissions;
- `pull_request_target`, `issue_comment`, and `workflow_run` jobs never use a PR head ref, untrusted cache, artifact download, or submodule checkout;
- local reusable workflow calls and local actions are permitted;
- Scorecard retains its current pinned action and SARIF upload.

Reusable-workflow call jobs in `basic-checks.yaml` and `ci.yaml` cannot use `timeout-minutes`; represent that syntax limitation as a narrow tested exemption, not a blanket skip.

### Step 2: Harden in place

- Move broad workflow permissions to `permissions: {}` and grant per job where GitHub syntax permits.
- Add practical timeouts: variables 5m; Go 30m; CodeQL 360m; Helm lint/unit 15m; Scorecard 30m; Pages trigger 5m; each E2E job 120m.
- Add cancellable branch/PR concurrency to CI, Go/Helm, and E2E work; keep release/security publication non-cancelling.
- Preserve all existing full-SHA pins. Do not replace them merely to make versions visually uniform.
- Pass PR-controlled values through `env:` before shell use; no direct expression interpolation into scripts.
- Keep Scorecard `security-events: write` and `id-token: write` scoped only to its analysis job.
- Keep Pages trigger `actions: write` only on its trigger job and pass `${{ github.repository }}` through an environment variable.

### Step 3: Verify and commit

```bash
cd .github/actions/repo-automation
npm test -- test/workflow-policy.test.js
cd ../../..
make actionlint
git diff --check
git add .github/workflows .github/actions/repo-automation/test/workflow-policy.test.js
git commit -s -S -m "ci: harden repository workflow boundaries"
```

## Task 6: Add the maintainer activation and settings runbook

**Files:**

- Create: `docs/maintainers/repository-automation.md`

### Step 1: Document exact repository settings

Include:

- allow GitHub-owned and specifically approved verified-creator actions only;
- require full-SHA references when organization rules support it;
- allow native auto-merge and squash merge; use PR title as squash commit title;
- require `PR metadata`, `Dependency review`, Go, Helm, E2E, CodeQL/security, and automation CI checks selected from actual workflow job names;
- require reviews, dismiss stale approvals, require conversation resolution, and prevent force pushes/deletion on protected branches;
- keep automatic head-branch deletion disabled;
- install label sync before making metadata required;
- enable commands, review observer, and evaluator before enabling auto-merge;
- inspect scheduled reconciliation and stale dry-run logs;
- emergency rollback: disable the relevant workflow, then manually disable native auto-merge on affected PRs; no labels or branches are deleted;
- private-repository dependency-review licensing caveat;
- first release and artifact verification steps from Plan 5.

Do not claim the implementation changed remote settings.

### Step 2: Commit

```bash
git add docs/maintainers/repository-automation.md
git commit -s -S -m "docs: add repository automation runbook"
```

## Task 7: Full governance verification

```bash
make actionlint
cd .github/actions/repo-automation
npm ci
npm test
npm run lint
npm audit --audit-level=high
cd ../../..
go test ./...
make check-modules
git diff --check
git status --short
```

Expected: all fast checks pass; report any environment-limited Go or Helm/E2E checks explicitly. Review `git diff -- .github/workflows .github/dependabot.yml Makefile hack docs/maintainers` for unplanned publishers, secrets, mutable action refs, or new write permissions.
