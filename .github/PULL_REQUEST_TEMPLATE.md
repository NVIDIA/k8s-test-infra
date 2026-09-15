## What This PR Does

<!-- Brief description of the changes -->

## Why

<!-- Link to issue or explain motivation -->

## Backport

<!--
While a release candidate is open, `main` keeps taking new work and the release
line lives on its own branch. If this PR is a fix that belongs in the current
release rather than the next one, say so here AND apply the matching
`backport/release-X.Y` label.

Leave the line below in place if it applies; delete this whole section if it
does not. Features and breaking changes are not backported.
-->

This PR SHOULD BE BACKPORTED TO release-0.4 BRANCH

## Checklist

- [ ] Commits are signed off (`git commit -s`)
- [ ] Tests pass (`go test -v -race ./...`)
- [ ] Linter passes (`make lint-fix`)
- [ ] New code has SPDX license headers
- [ ] Documentation updated (if applicable)
- [ ] CHANGELOG.md updated (if user-facing change)
- [ ] If this is a fix for the open release, the `backport/release-X.Y` label is applied
