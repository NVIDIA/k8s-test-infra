# Release gotchas

Things that went wrong or nearly went wrong in earlier releases, with what to do.

## Local gates on macOS

- `make lint` reports `nolintlint` on `internal/agent/imex/stage.go`: the
  `//nolint:unconvert` is "unused". On Darwin `stat.Rdev` is `int32`, so the
  conversion is real and the directive looks unused; on Linux, where CI runs,
  it is needed. Check with
  `GOOS=linux tmp/bin/golangci-lint run ./internal/agent/imex/...`, and report
  it as a darwin-only result. Never let `make lint-fix` remove the directive.
- `make lint` stops at golangci-lint, so govulncheck never runs. Run it on its
  own with CI's Go toolchain (`FROM golang:<v>` in
  `deployments/devel/Dockerfile`):
  `GOTOOLCHAIN=go<v> tmp/bin/govulncheck -tags=e2e,integration ./...`.
  An older local Go reports standard-library CVEs that CI never sees.
- If a gitignored scratch package under `tmp/` breaks `make test` or `lint`,
  it is local noise. Verify against everything outside `tmp/`.

## Changelog

- A patch release cut from a `release/X.Y` branch (v0.5.1) left its section
  and its compare link off `main`, while its fragment stayed in
  `.changes/unreleased/`. Port the section and delete the fragment in the next
  prep PR.
- `make changelog-release` dates the section today. If the tag lands on
  another day, fix the heading.
- A fragment that merges between the fold and the merge of the prep PR is
  shipped by the tag but missing from its section. Don't rebase the prep PR
  to fold it, because the push dismisses approvals. The follow-up PR moves it
  in, and the GitHub release waits for that PR.

## Publishing

- **Every publish run on `main` fails while a chart is not `-dev`**
  (`refusing to publish chart version X.Y.Z from main`). This happens when the
  prep PR sets `Chart.yaml` to the release version. Merging the `-dev` bump
  fixes it.
- **cosign `createLogEntryConflict` (409) from Rekor** in
  `Mokka Control Plane Publish`: the transparency log already had the entry
  from a retried request, signing aborted, and the image was pushed but left
  unsigned with no SBOM. `cosign verify` reports "no signatures found".
  Rerun the failed job (`gh run rerun <id> --failed`). It rebuilds the image,
  so the `X.Y.Z` digest can change. Take digests only after the rerun passes.
- `gh run rerun` fails with "already running" when another maintainer
  already reran it. Watch that attempt instead.

## gh-stack

- Stack branches live in `upstream`. Fork-head PRs cannot form a stack.
- `gh stack init` attaches existing PRs by head-branch name, fork PRs
  included, and records the local `main` SHA as the trunk, which is often
  stale. The bases on GitHub are still right. Before `gh stack sync` or
  `gh stack rebase`, update the local `main` or check what they will do.
- Pass `</dev/null` and `GH_STACK_NO_UPDATE_NOTIFIER=1` so it never waits for
  input.

## Tag message template

```
Mokka vX.Y.Z

<One paragraph: the headline changes by component.>

Breaking:
- <component>: <what changed>. <What the user does.>
```

## Slack template

```
:rocket: *Mokka vX.Y.Z is out*

*Highlights*
• *<Feature>*: <one line>
• *<Feature>*: <one line>
• *Fixes*: <the two or three a user would notice>

:warning: *Breaking changes*
• <what changed>: <what the user does>

*Upgrade*
`helm upgrade --install nvml-mock oci://ghcr.io/nvidia/k8s-test-infra/chart/nvml-mock --version X.Y.Z -n mokka`

:memo: Release notes: https://github.com/NVIDIA/k8s-test-infra/releases/tag/vX.Y.Z
```

Drop the breaking section when there are none. Add a line for any default
that affects users, such as a feature gate that downloads from the internet.
