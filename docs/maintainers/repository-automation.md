# Repository Automation Maintainer Runbook

This runbook describes the repository settings and activation checks required
by the [approved repository automation design](../superpowers/specs/2026-07-16-github-actions-repository-automation-design.md).
The files in this repository do not change GitHub repository, organization, or
ruleset settings. A repository administrator must apply and review those
settings separately.

The public contribution and escalation context remains in
[CONTRIBUTING.md](../../CONTRIBUTING.md),
[GOVERNANCE.md](../../GOVERNANCE.md), [SUPPORT.md](../../SUPPORT.md), and
[SECURITY.md](../../SECURITY.md). This runbook does not introduce service-level
commitments or private support channels.

## Actions policy

In the repository or organization Actions settings:

1. Allow GitHub-owned actions and only the specifically approved actions from
   verified creators used by this repository.
2. Require actions to be referenced by a full commit SHA when the organization
   policy supports that control. The version comments beside SHA references are
   informational; the SHA is the executable identity.
3. Do not add a personal access token, application private key, long-lived
   signing key, or fork-provided secret. Publication uses the scoped
   `GITHUB_TOKEN` and short-lived OIDC credentials.
4. Keep workflow changes covered by `.github/CODEOWNERS` and protected-branch
   review.

## Merge and protected-branch settings

For `main` and each protected `release-*` branch:

- Allow native auto-merge.
- Allow squash merge and configure the squash commit title to use the pull
  request title. The automation requests squash auto-merge.
- Require pull requests and approving reviews. Dismiss stale approvals when new
  commits are pushed and require all review conversations to be resolved.
- Prevent force pushes and branch deletion. Limit bypass to the smallest
  emergency maintainer set.
- Keep automatic deletion of merged pull request head branches disabled.

Do not enable native auto-merge until the ordered activation procedure below
has completed.

### Required check identities

A context may become a branch-wide required check only after proving that it
reports on every applicable pull request head. A required workflow skipped by
path or branch filtering can remain `Pending` and block an unrelated pull
request indefinitely. Completing a conditional check on one PR does not prove
that it is safe to require globally.

GitHub can decorate reusable-workflow and matrix jobs with caller and matrix
suffixes, so select an observed context in the ruleset UI instead of typing a
guessed context. These are the exact workflow and job identities declared by
the current YAML and their present branch-wide suitability:

| Purpose | Workflow name | Job name or job key from YAML | Branch-wide use |
| --- | --- | --- | --- |
| PR policy, DCO, and ownership | `PR metadata` | `PR metadata` | Candidate after proving it reports on every protected-branch PR head |
| Dependency policy | `Dependency review` | `Dependency review` | Candidate after proving API availability and reporting on every protected-branch PR head |
| Local action and workflow validation | `Repository automation CI` | `Repository automation CI` | Conditional only; its path filter makes it unsafe as a global requirement |
| Go checks | `basic checks` calling `Golang` | caller key `golang`; called job key `check` | Candidate after proving the observed reusable context reports on every PR head |
| CodeQL | `basic checks` calling `CodeQL` | caller key `code-scanning`; called job name `Analyze Go code with CodeQL` | Candidate after proving the observed reusable context reports on every PR head |
| Helm lint | `helm` | job key `lint` | Conditional only; its Helm path filter makes it unsafe as a global requirement |
| Helm unit tests | `helm` | job key `unittest` | Conditional only; its Helm path filter makes it unsafe as a global requirement |
| E2E | `CI Pipeline` calling `nvml-mock E2E (Go)` | caller key `nvml-mock-e2e`; called job keys `e2e`, `e2e-dra`, `e2e-gpu-operator`, `e2e-multi-node`, and `e2e-nri` | Conditional only; the current caller is push-only rather than an ordinary PR gate |

Do not globally require the current path-filtered Automation CI or Helm
contexts, or the push-only E2E contexts. Defer those requirements until an
always-reporting PR or aggregate gate exists and is proven on unrelated paths
as well as matching paths. Matrix E2E jobs also have generated profile suffixes;
preserve the observed-context rule when an always-reporting gate is introduced.

Do not require `Commands`, `Review observer`, `Merge evaluator`, stale,
Scorecard, label synchronization, or publication workflows as pull request
checks; those workflows do not provide ordinary PR check contexts.

For a private repository, confirm that its GitHub plan and GitHub Advanced
Security or GitHub Code Security entitlement, as applicable, provides the
dependency review API before making `Dependency review` required. The workflow
uses no fallback token or alternate dependency service.

## Ordered activation

Perform these steps in order. Record the workflow run URLs and the protected
branch settings changed at each checkpoint.

1. Merge the declarative label policy and local action to the default branch.
   Dispatch `Label synchronization` with `apply: false`, inspect the complete
   additive create/update plan, then dispatch with `apply: true`. The action
   does not delete unmanaged labels.
2. Open a test pull request and confirm `PR metadata` classifies its title,
   size, and areas, validates DCO, and requests only applicable owners. Do not
   make the check required until it has reported successfully.
3. Exercise the same path with a pull request from a fork. Confirm the
   privileged workflow checks out the default branch, never the fork head,
   treats the title, body, paths, and commits only as API data, and does not use
   fork-controlled artifacts, caches, submodules, or executable files.
4. Enable and exercise `Commands`, `Review observer`, and `Merge evaluator`.
   Use `Merge evaluator` workflow dispatch with a selected `pr-number` and
   `dry-run: true`; confirm it re-queries live state and plans no mutation.
5. Wait for at least one scheduled 15-minute `Merge evaluator` reconciliation
   and inspect its logs. Confirm a current-head approval, LGTM provenance,
   ownership coverage, blockers, and the final head fence are evaluated from
   live state.
6. Add only the observed checks proven to report on every applicable PR head to
   the protected-branch ruleset. Keep path-filtered and push-only checks
   informational until an always-reporting PR or aggregate gate replaces them.
   Require reviews, stale-review dismissal, and conversation resolution before
   enabling native auto-merge.
7. Enable native auto-merge and exercise one non-release test pull request.
   Confirm GitHub waits for every required check and review rule before merging.

After any workflow or local-action change is merged, repeat the manual evaluator
dry-run and fork exercise before treating the new revision as operational.

### Concurrency caveat

`PR metadata`, `Commands`, and `Merge evaluator` deliberately share the
non-cancelling `repository-automation-state` concurrency group. GitHub permits
one running and one pending run per concurrency group; a newly queued run can
replace an older pending run even when `cancel-in-progress` is `false`.
Therefore:

- inspect the run list when several PR events or commands arrive together;
- reissue a command whose pending run was replaced after the active state
  writer finishes;
- use `Merge evaluator` with `dry-run: true` to inspect current state; and
- rely on the scheduled evaluator to reconcile derived merge state, not to
  replay a dropped slash command.

## Stale lifecycle checkpoint

The stale workflow remains non-mutating with `debug-only: true`. Its schedule
must first produce a complete candidate log using issue 90/30-day and pull
request 30/14-day windows. Review every proposed label and closure, including
the `lifecycle/frozen` and `kind/feature` exemptions.

Changing `debug-only` to `false` is a separate destructive activation. Before
that change, list the exact issue and pull request numbers, effects, and recovery
path, then obtain the explicit confirmation required by `AGENTS.md`. Do not
dispatch or activate stale processing merely as part of repository-settings
setup.

## Emergency rollback

1. Disable the affected workflow in GitHub Actions or revert its default-branch
   workflow file.
2. Disable native auto-merge in repository settings.
3. Manually disable auto-merge on every currently affected pull request;
   changing the repository setting does not substitute for checking existing
   PR state.
4. Run `Merge evaluator` with `dry-run: true` for affected PRs and record the
   remaining labels, reviews, and blockers.

Rollback does not require deleting labels, comments, branches, releases, or
artifacts. Preserve those records for audit and repair the declarative policy
before reactivation.

## Release activation and first-release verification

Release automation is not active until the release plan is implemented,
validated in dry-run, and the two legacy publishers are retired with explicit
confirmation. Publishing a tag, GitHub Release, image, or chart remains an
explicitly authorized remote action.

Before activation:

1. Validate Release Please configuration and confirm the chart `version` and
   `appVersion` match.
2. Build the image locally and package/lint/test the Helm chart.
3. Dispatch `release.yml` with `publish: false`. Logs must show prospective
   image, chart, signature, SBOM, and provenance targets without a registry
   login, push, signature, attestation, tag, or GitHub Release mutation.
4. Confirm `release.yml` is the only automatic publisher before enabling its
   `main` push trigger. Retiring the legacy workflows requires the separate
   destructive confirmation and remains recoverable from Git history.

Pull request events created by the default `GITHUB_TOKEN` may produce workflow
runs that require maintainer approval. Before merging a Release Please PR:

1. Inspect the checks attached to its exact head commit.
2. Approve supported approval-required runs and confirm every required context
   reports against that exact head.
3. If a required context is absent, use its supported manual dispatch or rerun
   path for the exact head. As a fallback only, add a signed and DCO-compliant
   maintainer commit to emit a `synchronize` event.

Never weaken branch protection or mark a missing context successful.

For the first authorized stable release, replace `X.Y.Z` and digest placeholders
below with the published values. First confirm GitHub CLI authentication and log
in to GHCR with a human credential limited to registry/package read access. Use
a short-lived credential where available, enter it through stdin, and retain it
only in the local Docker credential store for this verification. Do not add it
as a repository PAT, Actions secret, or other long-lived workflow credential.

```bash
gh auth status

export GHCR_USER='<github-login>'
read -r -s GHCR_READ_TOKEN
printf '%s' "${GHCR_READ_TOKEN}" | \
  docker login ghcr.io --username "${GHCR_USER}" --password-stdin
unset GHCR_READ_TOKEN

gh release view vX.Y.Z --repo NVIDIA/k8s-test-infra
docker buildx imagetools inspect ghcr.io/nvidia/nvml-mock:X.Y.Z
helm pull oci://ghcr.io/nvidia/charts/nvml-mock --version X.Y.Z

cosign verify \
  --certificate-identity-regexp='^https://github.com/NVIDIA/k8s-test-infra/.github/workflows/release.yml@refs/' \
  --certificate-oidc-issuer=https://token.actions.githubusercontent.com \
  ghcr.io/nvidia/nvml-mock@sha256:<image-digest>
cosign verify \
  --certificate-identity-regexp='^https://github.com/NVIDIA/k8s-test-infra/.github/workflows/release.yml@refs/' \
  --certificate-oidc-issuer=https://token.actions.githubusercontent.com \
  ghcr.io/nvidia/charts/nvml-mock@sha256:<chart-digest>

cosign verify-attestation --type spdxjson \
  --certificate-identity-regexp='^https://github.com/NVIDIA/k8s-test-infra/.github/workflows/release.yml@refs/' \
  --certificate-oidc-issuer=https://token.actions.githubusercontent.com \
  ghcr.io/nvidia/nvml-mock@sha256:<image-digest>
cosign verify-attestation --type spdxjson \
  --certificate-identity-regexp='^https://github.com/NVIDIA/k8s-test-infra/.github/workflows/release.yml@refs/' \
  --certificate-oidc-issuer=https://token.actions.githubusercontent.com \
  ghcr.io/nvidia/charts/nvml-mock@sha256:<chart-digest>

gh attestation verify oci://ghcr.io/nvidia/nvml-mock:X.Y.Z \
  --repo NVIDIA/k8s-test-infra
gh attestation verify oci://ghcr.io/nvidia/charts/nvml-mock:X.Y.Z \
  --repo NVIDIA/k8s-test-infra

docker logout ghcr.io
```

Also confirm:

- image tags `X.Y.Z`, `X.Y`, `X`, and `latest` resolve to the intended release
  digest;
- the pulled chart reports `version: X.Y.Z` and `appVersion: X.Y.Z` and renders
  the image tag `X.Y.Z` by default;
- `image-sbom.spdx.json` and `chart-sbom.spdx.json` are present as immutable
  GitHub Release assets or an identical rerun-safe upload is skipped;
- image and chart provenance use their immutable digests, not mutable tags; and
- no Go binary is expected. GoReleaser remains deferred until the project has a
  supported end-user binary; the image and OCI chart are the complete current
  distribution set.
