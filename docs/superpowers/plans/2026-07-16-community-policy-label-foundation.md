# Community, Policy, and Label Foundation Implementation Plan

> **For Codex:** REQUIRED SUB-SKILL: Use `superpowers:subagent-driven-development` to implement this plan task-by-task.

**Goal:** Reconcile the repository's public contribution policies, establish one declarative automation policy, and deliver an additive, dry-run-first label synchronizer.

**Architecture:** This plan creates the smallest usable slice of the local Node 24 action: a strict configuration loader, a GitHub API adapter, a pure label reconciliation engine, and the `label-sync` mode. Later plans extend the same dispatcher and fake API. Repository labels remain declarative in YAML; synchronization can create or update declared labels but cannot delete unmanaged labels.

**Tech stack:** GitHub Actions, CommonJS JavaScript on Node 24, `node:test`, `@actions/core`, `@actions/github`, `yaml`, `@vercel/ncc`, ESLint 9.

**Depends on:** Approved design at `docs/superpowers/specs/2026-07-16-github-actions-repository-automation-design.md`.

**Produces for later plans:** Stable `mode`, `pr-number`, and `dry-run` action inputs; `loadConfig()`; a mocked GitHub boundary; `labels.yml`, `areas.yml`, and `policy.yml` schemas.

---

## Task 1: Add policy fixtures and make configuration validation fail first

**Files:**

- Create: `.github/actions/repo-automation/package.json`
- Create: `.github/actions/repo-automation/package-lock.json`
- Create: `.github/actions/repo-automation/eslint.config.js`
- Create: `.github/actions/repo-automation/test/config.test.js`
- Create: `.github/actions/repo-automation/test/fixtures/config/invalid-labels.yml`
- Create: `.github/actions/repo-automation/test/fixtures/config/invalid-policy.yml`
- Create: `.github/repo-automation/policy.yml`
- Create: `.github/repo-automation/labels.yml`
- Create: `.github/repo-automation/areas.yml`

### Step 1: Define the local action toolchain

Create a private CommonJS package with these scripts:

```json
{
  "name": "k8s-test-infra-repo-automation",
  "private": true,
  "type": "commonjs",
  "engines": { "node": ">=24" },
  "scripts": {
    "test": "node --test --test-reporter=spec",
    "lint": "eslint src test",
    "package": "ncc build src/index.js -o dist --minify"
  },
  "dependencies": {
    "@actions/core": "1.11.1",
    "@actions/github": "6.0.1",
    "yaml": "2.8.1"
  },
  "devDependencies": {
    "@vercel/ncc": "0.38.4",
    "eslint": "9.39.1"
  }
}
```

Run `npm install --package-lock-only` from the action directory to generate `package-lock.json`; do not hand-edit it.

### Step 2: Write failing configuration tests

The tests must require `src/config.js` and assert:

- all three repository YAML files load under `schemaVersion: 1`;
- every label has a unique name, six-digit hexadecimal color without `#`, and non-empty description;
- every `areas.yml` label exists in `labels.yml`;
- size thresholds are exactly `S: 0`, `M: 50`, `L: 250`, `XL: 1000`;
- `activeOwnerFiles` initially contains only `/OWNERS`, so `vendor/**/OWNERS` cannot grant authority;
- protected branch patterns are `main` and `release-*`;
- reviewer target is `2`, `/retest` cooldown is `600` seconds, and bot exemptions are exact GitHub login plus email pairs;
- invalid fixtures produce path-specific errors and never silently default.

Run:

```bash
cd .github/actions/repo-automation
npm test -- test/config.test.js
```

Expected: FAIL because `src/config.js` does not exist.

### Step 3: Declare the exact policy schemas

Use these top-level keys:

```yaml
# policy.yml
schemaVersion: 1
protectedBranches: [main, "release-*"]
activeOwnerFiles: [/OWNERS]
review:
  reviewerTarget: 2
commands:
  retestCooldownSeconds: 600
merge:
  method: SQUASH
bots:
  - login: dependabot[bot]
    emails: [49699333+dependabot[bot]@users.noreply.github.com]
  - login: github-actions[bot]
    emails: [41898282+github-actions[bot]@users.noreply.github.com]
sizeThresholds:
  S: 0
  M: 50
  L: 250
  XL: 1000
```

`labels.yml` must use `schemaVersion` plus a `labels` array of `{name, color, description}` and declare every approved `kind/*`, `size/*`, `area/*`, review, lifecycle, blocker, community, and priority label. Include `needs-triage` only if the issue forms retain it; do not retain `priority/unprioritized`, which conflicts with the approved taxonomy.

Use consistent category colors: `0e8a16` for kind and positive review state,
`1d76db` for areas, `c2e0c6`/`fef2c0`/`f9d0c4`/`d93f0b` for S/M/L/XL,
`fbca04` for lifecycle, `b60205` for blockers, `7057ff` for community,
and `b60205`/`d93f0b`/`fbca04`/`c5def5` for critical/soon/long-term/backlog.
Descriptions must say who or what manages the label; do not leave any empty.

`areas.yml` must use `schemaVersion` plus an ordered `areas` array. Map:

- `deployments/nvml-mock/**`, `cmd/nvml-mock/**` to `area/nvml-mock`;
- `pkg/gpu/mocknvml/**` to `area/nvml-mock`;
- `pkg/gpu/mockcuda/**` to `area/mockcuda`;
- `deployments/nvml-mock/helm/**` to `area/helm`;
- `tests/e2e/**` to `area/kubernetes`;
- `.github/**`, `hack/**`, `Makefile` to `area/ci`;
- `docs/**`, `*.md` to `area/docs`.

Overlaps are intentional and additive.

### Step 4: Preserve RED and continue directly to Task 2

Do not commit the failing test. Keep these files in the worktree and implement
the minimum loader in Task 2 so the first commit remains green.

## Task 2: Implement strict configuration loading

**Files:**

- Create: `.github/actions/repo-automation/src/config.js`
- Modify: `.github/actions/repo-automation/test/config.test.js`

### Step 1: Implement the public interface

Export:

```js
function loadConfig(rootDir) {
  return { policy, labels, areas };
}

function validateConfig(config) {
  return config;
}
```

Resolve only the three fixed paths beneath `rootDir`; never accept an event-provided config path. Parse with `YAML.parse`, reject aliases/merge keys, reject unknown top-level keys, and accumulate deterministic validation errors before throwing one `ConfigError`.

### Step 2: Make the focused tests green

```bash
cd .github/actions/repo-automation
npm test -- test/config.test.js
npm run lint
```

Expected: PASS.

### Step 3: Commit

```bash
git add .github/actions/repo-automation/package.json \
  .github/actions/repo-automation/package-lock.json \
  .github/actions/repo-automation/eslint.config.js \
  .github/actions/repo-automation/src/config.js \
  .github/actions/repo-automation/test/config.test.js \
  .github/actions/repo-automation/test/fixtures/config \
  .github/repo-automation
git commit -s -S -m "feat: validate repository automation policy"
```

## Task 3: Implement additive label reconciliation test-first

**Files:**

- Create: `.github/actions/repo-automation/src/label-reconciliation.js`
- Create: `.github/actions/repo-automation/test/label-reconciliation.test.js`

### Step 1: Write the reconciliation truth table

Test `planLabelChanges(declared, existing)` for:

- a missing label produces one `create` operation;
- changed color or description produces one `update` operation;
- names compare case-insensitively but preserve the declared spelling;
- an exact match produces no operation;
- unmanaged existing labels never produce delete operations;
- duplicate existing labels or invalid declared labels fail closed.

Run the focused test and confirm it fails because the module is missing.

### Step 2: Implement the pure planner

Export:

```js
function planLabelChanges(declaredLabels, existingLabels) {
  return { creates, updates, unchanged, unmanaged };
}
```

Sort operations by lowercase label name for deterministic logs and tests. The module must have no GitHub or filesystem dependency.

### Step 3: Verify and commit

```bash
cd .github/actions/repo-automation
npm test -- test/label-reconciliation.test.js
npm run lint
cd ../../..
git add .github/actions/repo-automation/src/label-reconciliation.js \
  .github/actions/repo-automation/test/label-reconciliation.test.js
git commit -s -S -m "feat: plan additive label reconciliation"
```

## Task 4: Add the GitHub boundary, action dispatcher, and dry-run mode

**Files:**

- Create: `.github/actions/repo-automation/action.yml`
- Create: `.github/actions/repo-automation/src/github-client.js`
- Create: `.github/actions/repo-automation/src/modes/label-sync.js`
- Create: `.github/actions/repo-automation/src/index.js`
- Create: `.github/actions/repo-automation/test/helpers/fake-github.js`
- Create: `.github/actions/repo-automation/test/label-sync.test.js`
- Create: `.github/actions/repo-automation/dist/index.js`
- Modify: `.gitignore`

### Step 1: Write failing mode tests

The fake client records calls to `listLabels`, `createLabel`, and `updateLabel`. Cover dry-run, apply, retry-safe rerun, pagination, and an API failure. Assert dry-run returns the complete plan without mutation.

### Step 2: Define the stable action contract

`action.yml` must use `runs.using: node24`, `runs.main: dist/index.js`, and inputs:

```yaml
inputs:
  mode:
    required: true
  pr-number:
    required: false
  dry-run:
    required: false
    default: "true"
outputs:
  summary:
    description: JSON summary of the idempotent operation
```

`src/index.js` exports `run(dependencies)` for tests and calls it only under the real action entry point. Dispatch unknown modes as a hard error. Plan 1 supports only `label-sync`.

`createGitHubClient(octokit, owner, repo)` is the only module that calls Octokit. It owns pagination and normalizes errors; policy modules receive plain objects.

### Step 3: Implement and package

Run:

```bash
cd .github/actions/repo-automation
npm test
npm run lint
npm run package
git diff --exit-code -- dist/index.js
```

The final command is expected to PASS after the generated bundle is staged once. Add an anchored `.gitignore` exception so `dist/index.js` remains tracked even if a broad `dist/` rule exists.

### Step 4: Commit

```bash
git add .gitignore .github/actions/repo-automation
git commit -s -S -m "feat: add dry-run label synchronization action"
```

## Task 5: Add the least-privileged label workflow

**Files:**

- Create: `.github/workflows/label-sync.yml`
- Modify: `.github/CODEOWNERS`

### Step 1: Write the workflow

Trigger on `workflow_dispatch` with a boolean `apply` input defaulting to `false`, and on pushes to `main` that change `labels.yml` or the action. Set top-level `permissions: {}`. The one job gets only `contents: read` and `issues: write`, uses a 10-minute timeout, concurrency `label-sync`, and checks out the exact default-branch SHA with `actions/checkout@9c091bb21b7c1c1d1991bb908d89e4e9dddfe3e0 # v7.0.0`.

On push, force dry-run. On manual dispatch, set `dry-run` to the inverse of `inputs.apply`. This keeps activation an explicit maintainer action.

Extend CODEOWNERS so `/.github/actions/repo-automation/`, `/.github/repo-automation/`, `/OWNERS`, `/OWNERS_ALIASES`, `/release-please-config.json`, and `/.release-please-manifest.json` retain the existing repository code owner. Do not invent new authority.

### Step 2: Validate and commit

```bash
actionlint .github/workflows/label-sync.yml
git diff --check
git add .github/workflows/label-sync.yml .github/CODEOWNERS
git commit -s -S -m "ci: add additive label synchronization workflow"
```

## Task 6: Reconcile community health without inventing private channels or SLAs

**Files:**

- Create: `SUPPORT.md`
- Create: `.github/ISSUE_TEMPLATE/support.yml`
- Modify: `.github/ISSUE_TEMPLATE/config.yml`
- Modify: `.github/ISSUE_TEMPLATE/bug_report.yml`
- Modify: `.github/ISSUE_TEMPLATE/feature_request.yml`
- Modify: `.github/ISSUE_TEMPLATE/documentation.yml`
- Modify: `.github/PULL_REQUEST_TEMPLATE.md`
- Modify: `CODE_OF_CONDUCT.md`
- Modify: `CONTRIBUTING.md`
- Modify: `GOVERNANCE.md`
- Modify: `SECURITY.md`

### Step 1: Make the documents internally consistent

- Replace broken `.md` issue-template links in `CONTRIBUTING.md` with chooser links.
- Document Conventional Commit PR titles, the approved slash commands, GitHub-review approval, DCO, and auto-merge prerequisites.
- Make `SUPPORT.md` explicitly best-effort with no SLA and route public usage questions to GitHub Discussions.
- Add a support form that applies only `needs-triage`; do not invent `kind/support`.
- Remove the internal NVIDIA Slack contact link from the public chooser.
- Use an absolute link to `SECURITY.md` and retain the GitHub private advisory plus NVIDIA PSIRT reporting routes.
- Remove the hard-coded `0.1.x` support table and 3/10/30-90 day promises from `SECURITY.md`; promise status communication, not deadlines.
- Replace `psirt@nvidia.com` as the conduct-report channel with the reporting instructions on NVIDIA's public company-policies page. PSIRT remains security-only.
- Reconcile governance role names with `OWNERS`: reviewers can LGTM, approvers provide GitHub approvals, and maintainers control releases/settings.
- Expand the PR checklist for linked issue, tests, docs, compatibility, breaking change, and DCO.
- Align issue form labels to `labels.yml`; remove `priority/unprioritized` and the invented seven-day triage SLA.

### Step 2: Verify links and policy terms

```bash
rg -n "bug_report\.md|feature_request\.md|priority/unprioritized|psirt@nvidia\.com" \
  CONTRIBUTING.md CODE_OF_CONDUCT.md .github/ISSUE_TEMPLATE
rg -n "3 business days|10 business days|30-90 days|Unprioritized SLA" \
  SECURITY.md .github/ISSUE_TEMPLATE
```

Expected: the first search finds `psirt@nvidia.com` only in security-reporting content, and neither search finds broken links or invented SLAs.

### Step 3: Commit

```bash
git add SUPPORT.md CODE_OF_CONDUCT.md CONTRIBUTING.md GOVERNANCE.md SECURITY.md \
  .github/ISSUE_TEMPLATE .github/PULL_REQUEST_TEMPLATE.md
git commit -s -S -m "docs: reconcile open source community policies"
```

## Task 7: Verify the complete foundation

Run from the repository root:

```bash
cd .github/actions/repo-automation
npm ci
npm test
npm run lint
npm run package
cd ../../..
git diff --exit-code -- .github/actions/repo-automation/dist
actionlint .github/workflows/label-sync.yml
git diff --check
git status --short
```

Expected: every command passes; the bundle has no regeneration diff; only intentional committed files exist. Manually inspect a `workflow_dispatch` dry-run before setting `apply: true`.
