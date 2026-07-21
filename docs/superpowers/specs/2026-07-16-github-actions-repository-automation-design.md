# GitHub Actions Repository Automation Design

**Date:** 2026-07-16
**Status:** Approved for implementation planning
**Scope:** Repository governance, pull request automation, community health, and releases

## Summary

This design makes `k8s-test-infra` self-organizing with GitHub Actions while
preserving a Kubernetes-familiar contribution model. A tested local JavaScript
action implements repository-specific policy. Thin workflows provide trusted
event adapters. GitHub-native reviews, required checks, and auto-merge remain
the final enforcement mechanisms.

The initial command subset is `/lgtm`, `/lgtm cancel`, `/assign`, `/unassign`,
`/hold`, `/hold cancel`, `/retest`, and `/help`. Approval is intentionally not a
slash command: it is an `APPROVED` GitHub review from an applicable approver in
the hierarchical `OWNERS` model.

Release Please prepares releases from Conventional Commit-style PR titles. A
release publishes a multi-platform container image, an OCI Helm chart, a GitHub
Release, SBOMs, provenance, and signatures. GoReleaser is deferred until the
repository has an end-user binary to distribute.

## Current state

The repository already has:

- Go, Helm, E2E, CodeQL, publishing, and Pages workflows.
- Dependabot for Go modules, GitHub Actions, and Docker.
- A Kubernetes-format root `OWNERS` file.
- Issue forms, a pull request template, `CODEOWNERS`, DCO instructions, and
  initial community-health documents.
- A multi-platform `nvml-mock` image and an OCI Helm chart published to GHCR.
- Keyless Cosign signing and image SBOM generation in the existing publishers.
- Scheduled stale handling and OpenSSF Scorecard reporting.

The repository does not currently have:

- A `SUPPORT.md`; the existing contribution, security, conduct, governance,
  issue, and pull request documents also need consistency and public-channel
  corrections.
- A repository label taxonomy or synchronization mechanism.
- Automated PR sizing, type labeling, area labeling, or reviewer routing.
- Slash-command processing or ownership-aware merge policy.
- Release Please or one coordinated version source for the existing image,
  chart, changelog, and GitHub Release publishers.
- Full-SHA pinning and the approved issue/PR timing in the existing stale
  workflow.

The Makefile references `cmd/nv-ci-bot/main.go`, but that command no longer
exists. This design does not restore that stale bot. Repository automation is a
local JavaScript action because it integrates directly with GitHub events and
requires no hosted service.

## Goals

- Make pull requests consistently classified, routed, reviewed, and merged.
- Preserve auditable Kubernetes-style `OWNERS` semantics.
- Support contributors from forks without executing their code in privileged
  workflows.
- Use only GitHub or verified first-party vendor Marketplace actions, pinned to
  immutable full commit SHAs.
- Keep repository policy declarative, reviewable, and testable.
- Add the community-health files expected of a mature open source repository.
- Publish a coherent image, chart, and GitHub Release from one version.
- Make every mutation idempotent and every enforcement decision fail closed.

## Non-goals

- Full Prow compatibility or deployment of Prow services.
- A general-purpose GitHub App or externally hosted webhook service.
- AI-based classification, estimation, reviewer selection, or issue sizing.
- Issue size labels. Size labels apply only to pull requests.
- A merge train or Tide-compatible submit queue in the initial implementation.
- Automatic branch deletion, issue locking, or destructive label cleanup.
- Distribution of Go binaries with GoReleaser.
- Automatic mutation of remote repository settings.

## Architecture

### Repository layout

The implementation adds or refines these logical components:

```text
.github/
  actions/repo-automation/
    action.yml
    package.json
    package-lock.json
    src/
    test/
    dist/index.js
  repo-automation/
    policy.yml
    labels.yml
    areas.yml
  workflows/
    pr-metadata.yml
    commands.yml
    review-observer.yml
    merge-evaluator.yml
    label-sync.yml
    stale.yaml
    release.yml
    automation-ci.yml
  ISSUE_TEMPLATE/
  PULL_REQUEST_TEMPLATE.md
  CODEOWNERS
OWNERS_ALIASES
```

The action exposes four modes through one stable interface:

- `metadata`: validate and classify a PR, resolve owners, and request reviewers.
- `command`: parse and authorize one issue comment, then apply its operation.
- `merge-evaluate`: derive approval and merge eligibility from current GitHub
  state.
- `label-sync`: create or update the labels declared in `labels.yml`.

The source is modular JavaScript targeting the current GitHub-hosted runner
JavaScript runtime. Runtime dependencies are bundled into `dist/index.js`.
Tests import source modules, never the bundle. CI verifies that regenerating the
bundle produces no diff.

### Workflows and permissions

| Workflow | Trigger | Write scope | Responsibility |
| --- | --- | --- | --- |
| `pr-metadata.yml` | `pull_request_target` | PRs, issues | Metadata, labels, reviewer routing, synchronize invalidation |
| `commands.yml` | `issue_comment` | PRs, issues, actions | Authorized commands and immediate reevaluation |
| `review-observer.yml` | `pull_request_review` | None | Emit a safe completion signal when review state changes |
| `merge-evaluator.yml` | `workflow_run`, schedule, dispatch | PRs, issues, contents | Re-query live state and enable or disable native auto-merge |
| `label-sync.yml` | Policy push, dispatch | Issues | Additively synchronize labels |
| `stale.yml` | Schedule, dispatch | Issues, PRs | Apply the approved inactivity policy |
| `release.yml` | Push to `main`, dispatch | Contents, packages, attestations, OIDC | Prepare and publish releases |
| `automation-ci.yml` | Pull request | None | Test, lint, package, and validate workflows |

Every job declares explicit minimum permissions, a timeout, and appropriate
concurrency. External actions are pinned to full SHAs with a nearby version
comment for readability. Dependabot updates those references.

### Trust boundaries

`pull_request_target` is used only because labeling and reviewer requests must
work for fork pull requests. These jobs:

- Check out the base repository's default branch explicitly.
- Never check out a PR head, merge ref, or PR-controlled submodule.
- Never execute files, scripts, build output, caches, or artifacts from a PR.
- Treat titles, bodies, paths, labels, and comments strictly as data passed to
  GitHub APIs or pure parsers.

`pull_request_review` has a read-only token for fork PRs. Therefore
`review-observer.yml` performs no mutation and consumes no repository code or
artifacts. Its completion triggers `merge-evaluator.yml` with a write-capable
default-branch context. The evaluator maps the completed run to its PR, checks
the current head SHA, and queries all policy inputs again. It never consumes an
artifact, cache, or output from the observer.

A scheduled reconciliation provides a recovery path for missed or delayed
events. It scans open PRs and applies the same idempotent evaluator.

## Pull request metadata

### Title and type

The required title form is:

```text
<type>[optional scope][optional !]: <description>
```

Accepted types and mappings are:

| Prefix | Label |
| --- | --- |
| `feat` | `kind/feature` |
| `fix` | `kind/bug` |
| `docs` | `kind/documentation` |
| `test` | `kind/test` |
| `refactor` | `kind/refactor` |
| `perf` | `kind/performance` |
| `build`, `ci` | `kind/ci` |
| `chore` | `kind/cleanup` |
| `chore(deps)` | `kind/dependencies` |
| `revert` | `kind/revert` |

An unrecognized title fails the required `PR metadata` check. The mapping is
mutually exclusive. A breaking-change marker remains compatible with Release
Please. Repository settings must use the PR title as the squash-merge commit
title so merged history remains Conventional Commit compatible.

### Size

Size is calculated from GitHub's total PR additions plus deletions. All files
count, including vendor, generated, lock, and snapshot files.

| Changed lines | Label |
| --- | --- |
| 0-49 | `size/S` |
| 50-249 | `size/M` |
| 250-999 | `size/L` |
| 1000 or more | `size/XL` |

Exactly one `size/*` label is present. Issues never receive size labels.

### Areas and other derived state

`areas.yml` maps repository globs to one or more `area/*` labels. Initial areas
are `nvml-mock`, `mockcuda`, `helm`, `kubernetes`, `ci`, and `docs`. Area labels
are independent of the one-type and one-size constraints.

Draft PRs receive `do-not-merge/work-in-progress`. The label is removed when a
PR becomes ready for review. A conflicting PR receives `needs-rebase` when the
GitHub API reports that state.

### DCO

The metadata check validates a `Signed-off-by` trailer for every human-authored
commit in the PR. The trailer identity must match the commit author identity
case-insensitively. Exact bot identities declared in `policy.yml`, initially
`dependabot[bot]` and `github-actions[bot]`, are exempt because their provenance
is controlled by their workflow or GitHub App identity.

## Ownership and review routing

### Authority

`OWNERS` files are authoritative. Each independent directory may declare
`reviewers` and `approvers`. Parent owners are inherited unless the child file
uses the supported no-parent option. `OWNERS_ALIASES` defines auditable groups
of GitHub usernames; mutable GitHub teams are not an ownership source.

The existing root `OWNERS` is the fallback. Implementation must not grant new
reviewer or approver authority based only on commit history. Nested owner
rosters are activated only after maintainers explicitly confirm them.

Production ownership discovery is allowlist-based, initially containing only
`/OWNERS`. The repository contains vendored upstream `OWNERS` files; recursive
filename discovery would incorrectly turn those files into project authority.
Each project-maintained nested file must therefore be added to the allowlist in
the same reviewed change that confirms its roster.

### Reviewer selection

The default target is two reviewers. For each candidate, weight equals the sum
of changed lines in files that candidate owns. Selection is weighted without
replacement and seeded from repository identity plus PR number, producing
balanced but repeatable routing.

Existing eligible requested reviewers are preserved. A synchronize event fills
only coverage gaps introduced by newly changed areas rather than churning the
whole reviewer set. The PR author is never selected. If no eligible candidate
exists after root fallback, the PR is blocked and the policy comment identifies
the uncovered paths.

### Approver selection and approval coverage

After LGTM, the action chooses a deterministic greedy set cover of approvers for
all changed files and requests the smallest covering set it finds. Existing
current-head approvals and pending eligible requests count when choosing whom
to request.

The evaluator reduces reviews to each reviewer's latest effective state. An
`APPROVED` review counts only when:

- The reviewer is not the PR author.
- The review applies to the current head commit.
- The reviewer is an inherited approver for at least one changed file.

Every changed file must be covered by at least one effective approver. When all
files are covered, the evaluator adds `approved` and removes
`do-not-merge/needs-approval`; otherwise it removes `approved` and applies the
blocker.

## Slash commands

Commands are recognized only at the beginning of a logical comment line.
Quoted text and fenced code blocks are ignored. Parsing is exact,
case-insensitive for the command name, and idempotent.

| Command | Authorization | Effect |
| --- | --- | --- |
| `/lgtm` | Applicable reviewer or approver; never the author | Add `lgtm`, request approvers, evaluate merge state |
| `/lgtm cancel` | LGTM giver, author, or collaborator | Remove `lgtm`, disable auto-merge |
| `/assign @user...` | Author, applicable owner, or collaborator | Assign eligible named users |
| `/unassign @user...` | Assignee, author, or collaborator | Remove eligible assignments |
| `/hold` | Author or collaborator | Add `do-not-merge/hold`, disable auto-merge |
| `/hold cancel` | Author or collaborator | Remove the hold and reevaluate |
| `/retest` | Author or collaborator | Rerun failed CI for the current head |
| `/help` | Anyone | Show supported syntax and authorization |

`/retest` has a ten-minute per-PR cooldown, reruns only failed jobs for the
current head, and is a no-op with an explanation when nothing is rerunnable. It
does not bypass GitHub's first-time-contributor approval controls.

For assignment commands, an eligible named user is an applicable owner, an
existing PR participant, or a repository collaborator. The action never assigns
an arbitrary mentioned account.

`/approve` is not supported. Approval is expressed only with an `APPROVED`
GitHub review.

Invalid or unauthorized commands receive a concise explanation. The bot keeps
one marker-delimited policy comment per PR and updates it in place rather than
posting repetitive status comments.

## Merge state machine

Native squash auto-merge is enabled only when all of these are true:

- The PR is open, not draft, and targets a protected release branch.
- `lgtm` is present.
- `approved` is present and was derived for the current head.
- No `do-not-merge/*` label is present.
- The PR is mergeable and has no unresolved ownership coverage.

Required CI remains a branch or ruleset requirement. The action can enable
auto-merge before CI finishes; GitHub performs the merge only after all required
checks and review requirements pass.

On `synchronize`, the metadata workflow removes `lgtm` and `approved`, adds the
needs-approval blocker, disables auto-merge, recalculates metadata, and fills
reviewer coverage gaps. Current-head comparison makes older reviews ineffective
even if repository settings fail to dismiss them.

The evaluator re-fetches the head OID immediately before its final mutation. A
changed OID aborts the attempt without enabling auto-merge.

## Label taxonomy

`labels.yml` declares names, colors, and descriptions. Synchronization is
additive: it creates missing labels and updates declared labels, but never
deletes unmanaged labels.

- Type: `kind/feature`, `kind/bug`, `kind/documentation`, `kind/test`,
  `kind/refactor`, `kind/performance`, `kind/ci`, `kind/cleanup`,
  `kind/dependencies`, `kind/revert`.
- Size: `size/S`, `size/M`, `size/L`, `size/XL`.
- Area: `area/nvml-mock`, `area/mockcuda`, `area/helm`, `area/kubernetes`,
  `area/ci`, `area/docs`.
- Review: `lgtm`, `approved`, `needs-rebase`.
- Lifecycle: `lifecycle/stale`, `lifecycle/frozen`.
- Merge blockers: `do-not-merge/hold`, `do-not-merge/work-in-progress`,
  `do-not-merge/needs-approval`.
- Community: `good first issue`, `help wanted`.
- Priority: `priority/critical-urgent`, `priority/important-soon`,
  `priority/important-longterm`, `priority/backlog`.

Priority and community labels are maintainer-managed in the initial scope.

## Community health

The repository refines its existing community files and adds the missing
support policy:

- Issue forms for bugs, features, documentation, and support questions.
- A template chooser that links security reporters to NVIDIA PSIRT and prevents
  blank security reports.
- A PR template for linked issues, testing, documentation, compatibility,
  breaking changes, and DCO.
- `GOVERNANCE.md` defining contributors, reviewers, approvers, maintainers,
  decision making, escalation, and release authority.
- `SUPPORT.md` declaring best-effort public support with no service-level
  agreement.
- `SECURITY.md` directing private reports to NVIDIA PSIRT and prohibiting public
  disclosure through issues.
- `CODE_OF_CONDUCT.md` applying NVIDIA's published conduct expectations and
  confidential reporting channel to project spaces.
- Expanded `CONTRIBUTING.md` describing the automated contribution lifecycle.

Existing issue forms and the pull request template are retained as the
starting point, then aligned to the declared label taxonomy and corrected to
use valid public links. Existing governance, conduct, and security text is
edited narrowly: unsupported service-level promises and misuse of the security
response channel for conduct complaints are removed.

`.github/CODEOWNERS` protects workflows, local actions, `OWNERS`,
`OWNERS_ALIASES`, label policy, and release configuration. It supplements but
does not replace the custom hierarchical approval evaluator.

## Stale policy

The existing workflow is updated to use the verified first-party
`actions/stale` action at an immutable full SHA.

- Issues become stale after 90 inactive days and close 30 days later.
- Pull requests become stale after 30 inactive days and close 14 days later.
- Activity removes the stale label and restarts the clock.
- `lifecycle/frozen` exempts issues and PRs.
- `kind/feature` exempts issues.
- Branch deletion is disabled.
- Close reason is `not_planned` for inactivity.

The initial scheduled configuration is dry-run. Maintainers inspect the complete
candidate list before a small reviewed change activates mutation. The approved
timings and exemptions are already fixed; the activation review validates only
the actual candidate population.

## Release process

### Version authority

Release Please is the only version authority. Its manifest configuration treats
`nvml-mock` as one release component, keeps the root changelog and Helm chart
version aligned, and creates an unprefixed repository tag of `vX.Y.Z`.

Release Please runs on pushes to `main` and maintains a release PR from
Conventional Commit history. Merging that PR creates the Git tag and GitHub
Release. Release creation and artifact publication stay in the same workflow
because events created by the default `GITHUB_TOKEN` do not trigger another
workflow.

The no-PAT choice also means a Release Please pull request created by the
default `GITHUB_TOKEN` does not itself trigger new pull request workflow runs.
Maintainers must explicitly run the required checks against that PR head, or
make a signed maintainer update that emits a synchronization event, before
merge. Branch protection is not weakened to hide this GitHub token behavior.

The existing `nvml-mock-publish.yaml` and `helm-publish.yaml` capabilities are
consolidated into that release workflow. Their working multi-architecture
build, GHCR retry, digest signing, and SBOM patterns are preserved while their
independent triggers and conflicting tag policy are removed.

### Artifacts

For a stable release, the workflow publishes:

- `ghcr.io/nvidia/nvml-mock:X.Y.Z`
- `ghcr.io/nvidia/nvml-mock:X.Y`
- `ghcr.io/nvidia/nvml-mock:X`
- `ghcr.io/nvidia/nvml-mock:latest`
- `oci://ghcr.io/nvidia/charts/nvml-mock:X.Y.Z`

The chart `version` and `appVersion` are `X.Y.Z`, and its default image tag is
the same immutable version. Main-branch development builds use only `edge` and
`sha-<commit>`; they never move `latest`.

The multi-platform image is built once. Publication records its digest, creates
SBOM and provenance attestations, and signs the image and chart digests with
keyless OIDC credentials. No long-lived PAT or signing key is introduced.

Publication steps are idempotent. If a later chart or attestation step fails
after an image is available, rerunning the same release resumes the missing
operations without deleting or renumbering existing artifacts.

GoReleaser remains out of scope until the repository has a supported end-user
binary. The current `cmd/generate-bridge` command is a development generator,
not a release artifact.

## Additional security and quality automation

- Retain existing Go, Helm, E2E, and CodeQL jobs.
- Add GitHub dependency review for pull requests.
- Retain the scheduled OpenSSF Scorecard workflow and its SARIF publication.
- Extend Dependabot to the local JavaScript action's npm lockfile.
- Validate all workflows with a checksum-pinned `actionlint` binary.
- Protect workflow and automation changes with `CODEOWNERS` review.
- Keep release credentials short-lived and scoped through `GITHUB_TOKEN` and
  OIDC.

## Failure handling

The system fails closed:

- Malformed configuration, unresolved ownership, missing permissions, stale
  head state, or API failure never enables auto-merge.
- If possible, the action applies `do-not-merge/needs-approval` and updates the
  policy comment with the exact missing condition.
- Authorization failures are not retried with a different identity.
- Transient network and rate-limit errors use bounded exponential retries.
- Label, assignment, comment, and auto-merge operations are idempotent.
- Per-PR concurrency prevents an older event from committing state after a
  newer synchronize event.
- A scheduled reconciliation repairs missed events without keeping external
  state.

Disabling the relevant workflow or policy feature stops new mutations. No
database or cleanup job is required for rollback.

## Testing

Implementation follows RED-GREEN-REFACTOR. The action includes:

- Unit tests for command parsing, code fences, title mapping, size boundaries,
  label reconciliation, authorization, and cooldown behavior.
- Fixture tests for nested owners, inheritance, aliases, root fallback,
  weighted selection, and approver set cover.
- A merge truth table covering current and stale approvals, LGTM, CI waiting,
  drafts, holds, conflicts, ownership gaps, and head races.
- Contract tests using representative GitHub webhook payloads and a mocked API
  boundary.
- Tests proving privileged modes never derive a checkout ref from PR-controlled
  input.
- Bundle freshness checks.
- Dry-run workflow dispatch for any selected PR number.

Repository-level verification includes:

```text
npm ci
npm test
npm run lint
npm run package
git diff --exit-code -- .github/actions/repo-automation/dist
actionlint .github/workflows/*.yml .github/workflows/*.yaml
go test ./...
make check-modules
```

The implementation plan may split expensive E2E verification from fast local
checks, but completion requires the checks relevant to changed workflows and
release paths.

## Rollout

1. Reconcile community files, then add policy, labels, action tests, and
   non-blocking metadata reporting.
2. Confirm nested `OWNERS` rosters and run additive label synchronization.
3. Enable command processing and reviewer requests.
4. Configure repository rules, then make metadata and ownership checks required
   and enable native auto-merge.
5. Inspect one complete stale dry-run and activate the approved policy.
6. Merge the first Release Please PR to exercise stable artifact publication.

Every phase has a manual dry-run or observable check before the next mutation
is enabled.

## Implementation decomposition

This document is the program-level contract. The implementation plan must split
it into reviewable workstreams with explicit dependencies:

1. Community-health reconciliation, label policy, and additive label
   synchronization.
2. The tested JavaScript action foundation, PR metadata, DCO, and reviewer
   routing.
3. Slash commands, approval coverage, the review privilege bridge, and the
   auto-merge state machine.
4. Workflow hardening, stale automation, dependency review, and Scorecard.
5. Release Please, image/chart publication, attestations, and signing.

Workstreams may be developed in parallel only when they do not share files.
Activation remains ordered by the rollout section so no enforcement depends on
an uninstalled policy or repository setting.

## Required repository settings

Implementation adds a maintainer runbook but does not change remote settings.
Maintainers must configure:

- Allow GitHub Actions from GitHub and approved verified creators only.
- Require full-SHA action references where organization policy supports it.
- Allow native auto-merge.
- Allow squash merges and use the PR title as the squash commit title.
- Require the metadata, DCO, ownership, Go, Helm, E2E, and security checks chosen
  in the implementation plan.
- Require review, dismiss stale approvals, and require conversation resolution.
- Keep automatic head-branch deletion disabled.
- Limit bypass permissions to the minimum emergency maintainer set.

## Acceptance criteria

- PRs receive deterministic type, area, and size labels with the approved
  thresholds and title convention.
- Two eligible reviewers are routed from hierarchical `OWNERS`; no author is
  selected.
- The approved slash-command subset is parsed, authorized, and idempotent.
- LGTM is invalidated on every new commit.
- `approved` is present only when every changed file has a current-head GitHub
  approval from an inherited approver.
- Native auto-merge is enabled only for an LGTM, fully approved, unblocked PR;
  GitHub waits for required CI.
- Fork PRs cannot cause untrusted code to execute with write permissions.
- Community-health documents and templates cover contribution, conduct,
  governance, support, and private security reporting.
- Stale automation implements the approved issue and PR timing without branch
  deletion and begins in dry-run.
- Release Please creates one version used by the GitHub Release, image, and OCI
  chart; stable artifacts have SBOM, provenance, and signatures.
- All external Marketplace actions are verified first-party publishers and
  pinned to immutable full SHAs.
- Local action unit, fixture, contract, packaging, and workflow validation tests
  pass, together with relevant existing repository checks.

## References

- [GitHub `pull_request_target` guidance](https://docs.github.com/en/actions/reference/workflows-and-actions/events-that-trigger-workflows#pull_request_target)
- [GitHub secure-use reference](https://docs.github.com/en/actions/reference/security/secure-use)
- [GitHub auto-merge behavior](https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/configuring-pull-request-merges/managing-auto-merge-for-pull-requests-in-your-repository)
- [Kubernetes reviewers and approvers](https://docs.prow.k8s.io/docs/components/plugins/approve/approvers/)
- [Release Please action](https://github.com/googleapis/release-please-action)
- [GPU Operator stale workflow](https://github.com/NVIDIA/gpu-operator/blob/main/.github/workflows/stale.yaml)
- [Actions Stale options](https://github.com/actions/stale)
- [GitHub Container Registry](https://docs.github.com/en/packages/working-with-a-github-packages-registry/working-with-the-container-registry)
- [GitHub community-health files](https://docs.github.com/en/communities/setting-up-your-project-for-healthy-contributions/creating-a-default-community-health-file)
- [NVIDIA vulnerability reporting](https://www.nvidia.com/en-us/security/report-vulnerability/)
- [NVIDIA company policies and conduct reporting](https://www.nvidia.com/en-us/about-nvidia/company-policies/)
