---
name: release
description: Run a Mokka (NVIDIA/k8s-test-infra) release end to end. Covers the release-prep PR (changelog fold), merge, signed tag, publish pipeline checks, `make verify-release`, the GitHub release notes, the stacked chart bump to the next -dev version, and the Slack announcement draft. Use this whenever the user wants to cut, prepare, tag, publish, verify or announce a Mokka release, says "release 0.7.0", "prepare v0.7.0", "fold the changelog for the release", "push the tag", "create the GitHub release", "bump the charts after the release" or "draft the release announcement", or picks up a release that is already partway done. Also use it for an out-of-cycle patch release (vX.Y.Z).
---

# Mokka release

A release takes `main` to published, signed images and charts plus a GitHub
release. The repository's own checklists are the source of truth:
`.github/ISSUE_TEMPLATE/release.yml` (scheduled `vX.Y.0`) and
`.github/ISSUE_TEMPLATE/patch_release.yml` (out-of-cycle). Read the one that
applies at the start, because it can change between releases and wins over this
skill. This skill adds the order of operations, the commands, and the lessons
from earlier releases (see `references/gotchas.md`).

## Ground rules

- **Find out where the release stands first.** A release spans hours and often
  more than one session. Before doing anything, check: does a `vX.Y.Z` tag
  exist (`git ls-remote --tags upstream`)? Is there a GitHub release
  (`gh release view`)? Is there an open or merged prep PR? Is there a tracking
  issue? Then resume at the first unfinished phase.
- **Confirm each outward step.** Merging, pushing a tag, creating the GitHub
  release and closing PRs are visible to everyone, and a pushed tag starts
  publishing artefacts that cannot be unpublished. Get a yes for each one, even
  when the user asked for the whole release. A yes for one step does not cover
  the next.
- **Sign everything.** Use `git commit -s -S`, `git rebase --signoff -S` and
  `git tag -s`. If signing fails, stop and report it. Never fall back to
  unsigned.
- **Remotes.** `upstream` is `NVIDIA/k8s-test-infra` and `origin` is the
  user's fork. The local `main` is usually stale, so always base work on
  `upstream/main` after `git fetch upstream --tags`.
- **Use a dedicated worktree** for the release branch, so the user's other
  checkouts are untouched.
- Write version numbers without the `v` for images, charts, CHANGELOG headings
  and `make` targets (`0.7.0`). Use the `v` only for git tags (`v0.7.0`).

## Phase 0: Plan

1. Work out the version. A scheduled release bumps Y. For a patch release,
   follow the version check in `patch_release.yml`: it is the next Z only
   when every change since the last tag is a fix.
2. Find the previous release, which is the latest stable tag on any branch.
   Check that every released version has its section in `main`'s
   CHANGELOG.md: `git show vA.B.C:CHANGELOG.md` against `main`. A patch cut
   from a release branch may never have been ported (v0.5.1 was not). Port a
   missing section and its compare link in the prep PR, and delete any fragment
   that release already shipped, so the new notes don't repeat it.
3. Record the base commit, `upstream/main`. Offer to open the tracking issue
   from the matching template, but only open it if the user agrees.

## Phase 1: Release-prep PR

Branch from `upstream/main` in a new worktree.

1. **Review the fragments** in `.changes/unreleased/` before folding. The
   notes tell a user of the previous release what changed for them. Apply the
   rules in AGENTS.md ("Changelog") and the pre-flight checks in the template:
   - Remove fragments that describe states no release shipped, such as fixes
     to a feature that is new in this release. Also remove fragments for
     build-time-only tools, refactors and CI, which users never run.
   - Every breaking change starts with `**Breaking:**` and states the user's
     action.
   - Every `Removed` entry needs a deprecation announced three Y releases
     earlier. Don't silently drop or keep one that fails this: flag it in the
     PR for the release engineer.
   - Check a fix against the previous tag (`git grep … vA.B.C -- …`) before
     deciding that it fixes something released.
   - Commit these edits with a body that lists each removal and the reason.
2. **Fold**: run `make changelog-preview RELEASE_VERSION=X.Y.Z`, then
   `make changelog-release RELEASE_VERSION=X.Y.Z`. Add the compare link
   `[X.Y.Z]: …/compare/vPREV...vX.Y.Z` above the previous one. Count the
   fragments the fold removed and the bullets in the new section; the two
   counts must match, and both go in the PR.
3. **Charts**: leave `Chart.yaml` unchanged, at `X.Y.0-dev`. The tag sets the
   published `version` and `appVersion`, and `main` refuses to publish a
   non-`-dev` chart version (`hack/chart-publish-version.sh`). Setting it to
   `X.Y.Z` here breaks every publish run on `main` until the bump PR lands.
4. **Docs pins**: `git grep -nF '<previous-version>' -- docs`. Since #1081
   the docs don't pin Mokka. Most hits are other projects' versions (DRA driver
   chart), minimum versions, or "last validated with" records. Leave those.
5. **Gates**: run `make helm-tests`, `make helm-crds-tests`, `make lint`,
   `make build` and `make test`. On macOS, read `references/gotchas.md`
   ("Local gates on macOS") before reporting a failure.
6. Squash into one commit, `chore(release): prepare vX.Y.Z`, unless the user
   wants the steps as separate commits.

## Phase 2: Chart bump, stacked on the prep PR

On a branch on top of the prep branch, bump both charts
(`deployments/nvml-mock/helm/nvml-mock/Chart.yaml` and
`deployments/mokka-crds/helm/mokka-crds/Chart.yaml`) `version` and
`appVersion` to the next minor's `-dev` (`0.8.0-dev` after `v0.7.0`). Also
update the `-dev` example in `docs/helm-chart.md`. Commit it as
`chore(release): bump the charts to X.Y+1.0-dev`.

Mokka has no application version in Go code. The `CDIVersion: "0.6.0"`
strings in `internal/agent/cri` and `internal/nri` are the CDI spec version, so
leave them alone.

Run `make helm-tests`, `make helm-crds-tests` and `go test ./tests/hack/...`.

**Open both PRs as a GitHub stack** with gh-stack. The branches must be in
`upstream`, because a stack bases each PR on the branch below it:

```bash
gh stack init --base main <prep-branch> <bump-branch>
GH_STACK_NO_UPDATE_NOTIFIER=1 gh stack submit --auto --remote upstream </dev/null
```

`--auto` creates drafts with generated titles. Afterwards, set each PR's title,
body and the `skip-changelog` label with `gh pr edit`. `gh stack init`
attaches any existing PR whose head branch has the same name, fork PRs
included. If that happens, run `gh stack unstack --local`, rename the branches,
and init again. Later pushes go through `gh stack push --remote upstream`.

Write the PR bodies with `.github/pull_request_template.md`. The prep PR's body
covers the fragment corrections with reasons, the fold counts, gate results, and
a "For the release engineer" list of anything that needs a decision. The bump
PR says it merges after the tag.

## Phase 3: Merge and tag

1. **Merge the prep PR** only with approval and green checks, after the user
   confirms. Don't rebase it just because `main` moved: every push dismisses
   approvals, and a PR that is behind `main` still merges. Rebase only when it
   gets the `needs-rebase` label (`git rebase --signoff -S upstream/main`),
   then rebase the bump branch onto the new prep head, check that every commit
   is still signed (`git cat-file -p <sha> | grep -q '^gpgsig'`) and
   `gh stack push --remote upstream`. Fragments that merged after the fold
   stay out of the prep PR: step 3 lists them and Phase 5 moves them into the
   changelog.
2. **CI must be green on the prep merge commit** before tagging:
   `gh run list --workflow "CI Pipeline" --commit <merge-sha>`. Watch it with
   `gh run watch <id> --exit-status` in the background. If the user wants to
   tag before CI finishes, say what that risks and let them decide.
3. **List the fragments that merged after the base commit**:
   `git diff --name-only --diff-filter=A <base> <merge-sha> -- .changes/unreleased`.
   Paste the list in the issue, even when it is empty.
4. **Tag** the merge commit, given explicitly, with a signed annotated tag. The
   message has a one-paragraph headline and the breaking changes with their
   actions:

   ```bash
   git tag -s -F <message-file> vX.Y.Z <merge-sha>
   git cat-file -p vX.Y.Z | head -5   # check: object is <merge-sha>, type commit
   git push upstream vX.Y.Z
   ```

   Never push an `-rc` or other non-stable tag; every publish workflow
   refuses it. Pushing the tag needs bypass on the tag ruleset.

## Phase 4: Check the tag pipeline

Three runs start from the tag: `nvml-mock Publish`,
`Mokka Control Plane Publish` and `helm-publish`
(`gh run list --branch vX.Y.Z`). Watch them in the background. When all three
succeed, run:

```bash
make verify-release RELEASE_VERSION=X.Y.Z
```

It checks the runs, platforms, `X.Y` and `latest` tags, chart versions, every
cosign signature and every SBOM attestation. Every line must be `ok:`. Keep the
output for the issue.

If a publish run failed, read its log (`gh run view <id> --log-failed`)
before acting. `references/gotchas.md` has the known failures. Before rerunning,
check that nobody else already did: `gh run view <id> --json attempt,status`.

## Phase 5: GitHub release

1. **Late fragments first.** If Phase 3 listed fragments, follow the
   template's "After the release" item before writing any notes: a PR against
   `main` moves each listed fragment's entry into this release's section of
   CHANGELOG.md, under its kind heading, and deletes the fragment. It also
   adds a missing `**Breaking:**` marker and user action. Paste the three
   counts (folded, listed, bullets in the section) in the PR and apply
   `skip-changelog`. Merge it after the user confirms. Skip this step when the
   list was empty.
2. **Then create the release**, once that PR has merged (or right after the
   tag when the list was empty). Otherwise the notes miss shipped changes,
   breaking ones included.

Render the notes from the published artefacts:

```bash
.claude/skills/release/scripts/release-notes.sh X.Y.Z > <scratch>/notes.md
```

Run it from a checkout of the merged `main`, because it reads the release's
section from CHANGELOG.md. Replace the `SUMMARY` line with two short
paragraphs:
- the headline changes, by component, in plain words;
- "Upgrading:" with the `helm upgrade` target, the number of **Breaking:**
  entries to read first, and any default the user should know about, such as
  a feature gate that is on by default and reaches the internet.

Then:

```bash
gh release create vX.Y.Z -R NVIDIA/k8s-test-infra --verify-tag --latest \
  --title vX.Y.Z --notes-file <scratch>/notes.md
```

If the user wants the release out before the publish runs finish, create it
with the digests that are final, mark the rest as pending, and finish with
`gh release edit --notes-file` once `verify-release` passes. Never invent a
digest.

## Phase 6: After the release

- Merge the stacked chart bump PR soon after the tag.
- Update the tracking issue with the tag, the release link, the
  `verify-release` output and the changelog section, then close it.

## Phase 7: Slack announcement

Draft the message for the user to paste; don't post it. Use Slack mrkdwn
(`*bold*`, `•` bullets, backticks), keep it to about 15 lines, and build it
from the release notes. `references/gotchas.md` ("Slack template") has the
shape. Link the GitHub release, which exists by now.

## Reporting

At each pause, say what is done (with SHAs, PR numbers and URLs), what is
running, and the next step that needs the user. When a step was skipped or ran
against the checklist (tagging before green CI, for example), say so plainly.
