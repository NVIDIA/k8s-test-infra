# Release Please and Artifact Publication Implementation Plan

> **For Codex:** REQUIRED SUB-SKILL: Use `superpowers:subagent-driven-development` to implement this plan task-by-task.

**Goal:** Make Release Please the single version authority and publish one GitHub Release, multi-platform image, and OCI Helm chart with coherent SemVer tags, SBOMs, provenance, and digest signatures.

**Architecture:** A Release Please manifest starts from the current `0.2.1` release and updates `CHANGELOG.md` plus both Helm `version` and `appVersion`. One `release.yml` workflow owns release creation and publishing because a tag created by `GITHUB_TOKEN` does not trigger a separate tag workflow. Existing Docker/Helm build, retry, SBOM, and Cosign patterns are preserved, but all signing and attestation targets are immutable digests.

**Tech stack:** Release Please v5, Docker official actions, Helm, GHCR, Anchore SBOM action, GitHub attest v4, Cosign keyless OIDC, existing Helm unit tests.

**Depends on:** Plans 1-4 completed. In particular, PR title validation provides Conventional Commit history and Plan 4 validates all workflow pins.

**Immutable pins introduced or retained:**

- `googleapis/release-please-action@0dfd8538845b8e92600d271a895a5372865d4062 # v5`
- `actions/attest@f6bf1532d7d6793fce74eac584813a8eee607999 # v4`
- `actions/checkout@9c091bb21b7c1c1d1991bb908d89e4e9dddfe3e0 # v7.0.0`
- `azure/setup-helm@9bc31f4ebc9c6b171d7bfbaa5d006ae7abdb4310 # v5`
- Existing pinned Docker, Sigstore, and Anchore action SHAs from `nvml-mock-publish.yaml` and `helm-publish.yaml`.

---

## Task 1: Define the version contract with failing tests

**Files:**

- Create: `release-please-config.json`
- Create: `.release-please-manifest.json`
- Create: `.github/actions/repo-automation/test/release-policy.test.js`

### Step 1: Write the test before valid configuration

The test must load both JSON files, `Chart.yaml`, `values.yaml`, and later `release.yml`. Assert:

- manifest package `.` starts at `0.2.1`;
- package name is `nvml-mock`, release type is `simple`, tags include `v`, and component is omitted from the tag;
- `CHANGELOG.md` is the changelog path;
- Release Please updates `$.version` and `$.appVersion` in `deployments/nvml-mock/helm/nvml-mock/Chart.yaml` through `extra-files`;
- chart version and appVersion are equal SemVer values;
- default image tag is empty and templates fall back to `.Chart.AppVersion`;
- release image tags are `X.Y.Z`, `X.Y`, `X`, and `latest` with no leading `v`;
- main development tags are only `edge` and `sha-<short commit>`;
- chart target is `oci://ghcr.io/nvidia/charts` and artifact name is `nvml-mock`;
- all signing and attestation inputs use digest outputs, never the first mutable tag.

Confirm RED before completing the config and template changes.

### Step 2: Create exact manifest configuration

Use:

```json
{
  "$schema": "https://raw.githubusercontent.com/googleapis/release-please/main/schemas/config.json",
  "packages": {
    ".": {
      "release-type": "simple",
      "package-name": "nvml-mock",
      "changelog-path": "CHANGELOG.md",
      "include-v-in-tag": true,
      "include-component-in-tag": false,
      "extra-files": [
        {
          "type": "yaml",
          "path": "deployments/nvml-mock/helm/nvml-mock/Chart.yaml",
          "jsonpath": "$.version"
        },
        {
          "type": "yaml",
          "path": "deployments/nvml-mock/helm/nvml-mock/Chart.yaml",
          "jsonpath": "$.appVersion"
        }
      ]
    }
  }
}
```

Use `{ ".": "0.2.1" }` in `.release-please-manifest.json`. Do not rewrite historical changelog entries or bootstrap a fake release.

### Step 3: Validate and commit the version contract

```bash
jq empty release-please-config.json .release-please-manifest.json
cd .github/actions/repo-automation
npm test -- test/release-policy.test.js
cd ../../..
```

The test remains RED only for workflow/template pieces intentionally delivered in Tasks 2-3; do not commit a broken default branch. Complete Tasks 2 and 3 before the first release-plan commit.

## Task 2: Make chart image defaults follow the release version

**Files:**

- Modify: `deployments/nvml-mock/helm/nvml-mock/Chart.yaml`
- Modify: `deployments/nvml-mock/helm/nvml-mock/values.yaml`
- Modify: `deployments/nvml-mock/helm/nvml-mock/templates/daemonset.yaml`
- Modify: `deployments/nvml-mock/helm/nvml-mock/templates/nri-daemonset.yaml`
- Modify: `deployments/nvml-mock/helm/nvml-mock/tests/daemonset_test.yaml`
- Modify: `deployments/nvml-mock/helm/nvml-mock/tests/nri_daemonset_test.yaml`
- Modify: `deployments/nvml-mock/helm/nvml-mock/tests/__snapshot__/daemonset_test.yaml.snap`
- Modify: `deployments/nvml-mock/helm/nvml-mock/tests/__snapshot__/configmap_test.yaml.snap`
- Modify: `deployments/nvml-mock/helm/nvml-mock/tests/__snapshot__/rbac_test.yaml.snap`
- Modify: `deployments/nvml-mock/helm/nvml-mock/README.md`

### Step 1: Add failing Helm assertions

Assert the default daemon and NRI images render as `ghcr.io/nvidia/nvml-mock:0.2.1`, while explicit `image.tag` and `nri.image.tag` overrides still win. Assert driver version remains derived from GPU profile data and is not taken from chart `appVersion`.

### Step 2: Implement defaulting

- Set `Chart.yaml` `appVersion: "0.2.1"` to match current chart version.
- Set `values.yaml` `image.tag: ""` and document empty as “defaults to chart appVersion”.
- Render main image tag with `default .Chart.AppVersion .Values.image.tag`.
- Render NRI merged image tag with `default $.Chart.AppVersion $image.tag` so explicit NRI override wins, then explicit root image tag, then appVersion.
- Update only snapshots affected by release-version labels or image defaults using the Helm unittest update mechanism, then review every snapshot diff.
- Update README defaults and explain `edge`, `sha-*`, stable SemVer, and `latest`; local examples may still use explicit `image.tag=local`.

### Step 3: Verify Helm behavior

```bash
helm lint deployments/nvml-mock/helm/nvml-mock
helm unittest deployments/nvml-mock/helm/nvml-mock
```

Expected: PASS; profile-derived driver tests remain unchanged.

## Task 3: Build one release workflow with dry-run and resumable publication

**Files:**

- Create: `.github/workflows/release.yml`
- Create: `.github/scripts/release-state.mjs`
- Create: `.github/scripts/release-state.test.mjs`
- Modify: `.github/actions/repo-automation/test/release-policy.test.js`
- Modify: `.github/actions/repo-automation/test/workflow-policy.test.js`

### Step 1: Define triggers, inputs, and job outputs

Stage this workflow with `workflow_dispatch` only. The automatic `push` trigger
is added atomically with legacy-publisher retirement in Task 5, so there is no
commit where two publishers race. Manual dispatch inputs:

- `publish`: boolean, default `false`;
- `version`: optional SemVer without `v`, used only for an explicitly authorized republish.

On manual `publish: false`, perform a non-mutating plan: validate configuration, derive prospective tags from the checked-out chart version, and print image/chart/signature/attestation targets without login, push, release creation, or write permission.

The staged workflow contains the release jobs and conditions, but before Task 5
manual dispatch is restricted to `publish: false`; any `publish: true` request
must fail with an “automation not activated” message before login or mutation.
After atomic activation, pushes run the `release-please` job with the pinned v5
action and export `release_created`, `version`, `tag_name`, and `sha`. Grant
that job only `contents: write`, `issues: write`, and `pull-requests: write`.
Use the default `GITHUB_TOKEN`; add no PAT or app private key.

Extend `workflow-policy.test.js` with an exact write-permission allowlist for
`release.yml`: `release-please` may write contents/issues/pull requests;
`publish-image` and `publish-chart` may write contents/packages/attestations
and use OIDC. No other release job receives write access.

Record the deliberate no-PAT tradeoff in the maintainer runbook: pull requests
created by `GITHUB_TOKEN` do not emit new workflow runs. Before merging a
Release Please PR, a maintainer must trigger the repository's required checks
against its head using the documented manual workflow dispatch/re-run path (or
make a signed, DCO-compliant maintainer update that emits `synchronize`). Do
not weaken branch protection or mark missing checks successful in code.

For manual `publish: true`, validate `version` against `^[0-9]+\.[0-9]+\.[0-9]+$`, verify tag `v<version>` already exists, and checkout that tag. A human dispatch with `publish: true` is the explicit publication authorization and resume path.

### Step 2: Publish the image once per workflow run

The image job uses existing pinned checkout, QEMU, Buildx, login, metadata, build-push, Cosign, and Anchore actions. On ordinary main pushes, tags are exactly:

```text
ghcr.io/nvidia/nvml-mock:edge
ghcr.io/nvidia/nvml-mock:sha-<short commit>
```

When `release_created == 'true'` or authorized manual republish is active, add:

```text
ghcr.io/nvidia/nvml-mock:<X.Y.Z>
ghcr.io/nvidia/nvml-mock:<X.Y>
ghcr.io/nvidia/nvml-mock:<X>
ghcr.io/nvidia/nvml-mock:latest
```

Build `linux/amd64,linux/arm64` once and capture `steps.build.outputs.digest`. Then:

Before a stable build, inspect `X.Y.Z`, `X.Y`, `X`, and `latest`. If immutable
`X.Y.Z` exists, verify its OCI source-revision label identifies the selected
release tag/SHA and verify any existing provenance; a missing provenance or
SBOM is a resumable operation, not an identity mismatch. Then reuse that digest
and skip the build/push. If `X.Y.Z` does not exist, build and publish it once.
For moving aliases, update only when the selected release is newer than the
alias's recorded SemVer; skip an equal digest and fail closed rather than
moving backward. Apply the same immutable rule to `sha-<commit>`; `edge` moves
only on a newer main commit. Put this decision logic in a tested script or
source module, not an unaudited inline expression.

Implement those pure decisions in `.github/scripts/release-state.mjs` as
`planImagePublication({version, releaseSha, stable, minor, major, latest})`
and `planChartPublication({version, localTreeDigest, remoteTreeDigest,
remoteManifestDigest})`.
The workflow gathers registry metadata with fixed commands, writes JSON, and
passes the file path to the script; registry values are never interpolated into
shell source. Unit tests cover absent, identical, mismatched, newer-alias, and
partial-publication states.

1. `cosign sign --yes ghcr.io/nvidia/nvml-mock@<digest>`;
2. generate SPDX JSON from `ghcr.io/nvidia/nvml-mock@<digest>`;
3. `cosign attest --yes --predicate image-sbom.spdx.json --type spdxjson ghcr.io/nvidia/nvml-mock@<digest>`;
4. use `actions/attest@f6bf1532d7d6793fce74eac584813a8eee607999 # v4` with fixed subject name, `subject-digest: <digest>`, and `push-to-registry: true` for SLSA provenance.

Grant only `contents: write`, `packages: write`, `id-token: write`, and
`attestations: write` to this job. `contents: write` is required only for the
immutable GitHub Release asset upload. Use non-cancelling concurrency
`publish-${{ github.sha }}`.

### Step 3: Publish and attest the chart only for a stable release

The chart job runs only for `release_created` or authorized republish. Verify checked-out `Chart.yaml` version and appVersion both equal the selected release version before login.

Package to `.cr-release-packages/nvml-mock-X.Y.Z.tgz`, push to `oci://ghcr.io/nvidia/charts`, retain the existing bounded three-attempt GHCR retry, and parse a non-empty digest. Target identity must be:

```text
ghcr.io/nvidia/charts/nvml-mock@sha256:<digest>
```

Before push, pull any existing `X.Y.Z` chart. Compare its unpacked normalized
file tree to the newly packaged chart: reuse the registry digest when identical
and fail closed on any content difference. Do not rely on `.tgz` byte equality,
because archive timestamps can differ.

Sign that digest with Cosign. Generate `chart-sbom.spdx.json` from the packaged chart, attach it with `cosign attest` to the chart digest, and create GitHub provenance with the pinned `actions/attest` using chart subject name and digest. Never sign `:X.Y.Z` directly. Grant this job only `contents: write`, `packages: write`, `id-token: write`, and `attestations: write`.

### Step 4: Attach each SBOM from its owning job without overwriting

For stable releases, the image job uploads `image-sbom.spdx.json` and the chart
job uploads `chart-sbom.spdx.json`; no cross-job file is assumed. Before
uploading, query the named asset. If absent, upload it. If present, download it
to a temporary path and compare SHA-256: skip identical content and fail closed
on mismatch. Never use `gh release upload --clobber`.

### Step 5: Make reruns converge

- Rebuilding the same Git commit must produce the same source inputs and reapply the same tag set.
- Treat `X.Y.Z` image and chart tags as immutable. If either already exists,
  verify its source revision/provenance and content against the selected Git
  tag, reuse its digest when identical, and fail closed on mismatch; never
  overwrite it.
- Treat `X.Y`, `X`, and `latest` as moving aliases only during the first
  authorized publication of a newer release. A resume skips aliases already at
  the release digest and fails rather than moving an alias backward from a
  newer version.
- Every job checks the selected release tag, chart version, and source SHA before pushing.
- A failed downstream job can be rerun in the original workflow attempt; authorized dispatch can resume a released version.
- No retry deletes an image, chart, tag, release, branch, or attestation.

### Step 6: Validate, finish the RED contract, and commit Tasks 1-3

```bash
jq empty release-please-config.json .release-please-manifest.json
cd .github/actions/repo-automation
npm test -- test/release-policy.test.js
cd ../../..
helm lint deployments/nvml-mock/helm/nvml-mock
helm unittest deployments/nvml-mock/helm/nvml-mock
actionlint .github/workflows/release.yml
git diff --check
git add release-please-config.json .release-please-manifest.json \
  .github/workflows/release.yml \
  .github/scripts/release-state.mjs \
  .github/scripts/release-state.test.mjs \
  .github/actions/repo-automation/test/release-policy.test.js \
  .github/actions/repo-automation/test/workflow-policy.test.js \
  deployments/nvml-mock/helm/nvml-mock
git commit -s -S -m "feat: coordinate versioned image and chart releases"
```

## Task 4: Validate artifacts without publishing

**Files:**

- Modify: `docs/maintainers/repository-automation.md`

### Step 1: Run local builds

```bash
docker buildx build --platform linux/amd64 --load \
  -f deployments/nvml-mock/Dockerfile \
  -t nvml-mock:release-test .
helm package deployments/nvml-mock/helm/nvml-mock --destination /tmp/nvml-mock-chart
helm show chart /tmp/nvml-mock-chart/nvml-mock-0.2.1.tgz
```

Expected: image build passes; packaged chart reports version/appVersion `0.2.1`.

### Step 2: Exercise manual dry-run

After the workflow reaches the default branch, dispatch with `publish: false`. Confirm logs show prospective edge/sha and stable targets but contain no GHCR login, push, Cosign signing, attestation, tag creation, or GitHub Release mutation.

### Step 3: Document operator verification

Add exact first-release checks:

```bash
docker buildx imagetools inspect ghcr.io/nvidia/nvml-mock:X.Y.Z
helm pull oci://ghcr.io/nvidia/charts/nvml-mock --version X.Y.Z
cosign verify \
  --certificate-identity-regexp='^https://github.com/NVIDIA/k8s-test-infra/.github/workflows/release.yml@refs/' \
  --certificate-oidc-issuer=https://token.actions.githubusercontent.com \
  ghcr.io/nvidia/nvml-mock@sha256:<digest>
cosign verify \
  --certificate-identity-regexp='^https://github.com/NVIDIA/k8s-test-infra/.github/workflows/release.yml@refs/' \
  --certificate-oidc-issuer=https://token.actions.githubusercontent.com \
  ghcr.io/nvidia/charts/nvml-mock@sha256:<digest>
gh attestation verify oci://ghcr.io/nvidia/nvml-mock:X.Y.Z --repo NVIDIA/k8s-test-infra
```

Document that GoReleaser remains deferred until a supported end-user binary exists; GHCR image plus OCI chart are the complete current distribution set.

### Step 4: Commit

```bash
git add docs/maintainers/repository-automation.md
git commit -s -S -m "docs: add release publication verification"
```

## Task 5: Retire the two conflicting legacy publishers only after explicit confirmation

**Files:**

- Modify after confirmation: `.github/workflows/release.yml`
- Delete after confirmation: `.github/workflows/nvml-mock-publish.yaml`
- Delete after confirmation: `.github/workflows/helm-publish.yaml`

### Step 1: Stop and request the destructive-action confirmation

Before deletion, tell the user exactly:

- targets: the two workflow files above;
- activation target: add the `main` push trigger and remove the publication
  guard in `.github/workflows/release.yml`;
- impact: removes separate main/tag image publishing and post-Helm chart
  publishing, then makes `release.yml` the only automatic publisher, so the
  workflows cannot race or move `latest` on ordinary main pushes;
- recovery: both files remain recoverable from Git history and can be restored in a signed revert;
- precondition: `release.yml` dry-run, Helm tests, workflow validation, and local image build all passed.

Do not treat approval of the general release design as approval to delete these exact files. Wait for one explicit confirmation in chat or voice as required by `AGENTS.md`.

### Step 2: Delete with a reviewable patch and verify no duplicate publisher remains

After confirmation, use one `apply_patch` change to delete both files and add
the `push.branches: [main]` activation to `release.yml`. Remove the staged
“automation not activated” guard in that same patch. GitHub evaluates the push
using the new commit, so the deleted workflows and replacement publisher are
never active together. Then run:

```bash
rg -n "nvml-mock Publish|helm Publish|type=raw,value=latest|oci://ghcr.io/nvidia/k8s-test-infra/chart" .github/workflows
actionlint .github/workflows/*.yml .github/workflows/*.yaml
```

Expected: old workflow names and old OCI location are absent; `latest` exists only in the stable-release branch of `release.yml`.

### Step 3: Commit

```bash
git add .github/workflows/nvml-mock-publish.yaml \
  .github/workflows/helm-publish.yaml \
  .github/workflows/release.yml
git commit -s -S -m "ci: retire legacy artifact publishers"
```

## Task 6: Final release verification

```bash
jq empty release-please-config.json .release-please-manifest.json
cd .github/actions/repo-automation
npm ci
npm test
npm run lint
cd ../../..
helm lint deployments/nvml-mock/helm/nvml-mock
helm unittest deployments/nvml-mock/helm/nvml-mock
make actionlint
git diff --check
git status --short
```

Review the final workflow permissions, all external action SHAs, release tag transformations, chart target, digest use, OIDC scopes, and absence of PATs or static signing keys. Publishing a real tag or release is a remote publish action and still requires the explicit human authorization described in the maintainer runbook.
