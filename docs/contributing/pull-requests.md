# Pull Requests

## Commits

Mokka uses [Conventional Commits](https://www.conventionalcommits.org/):

```text
type(scope): imperative summary

Why the change is needed, and what it does about it. Wrap at 72 columns.
```

Types in use: `feat`, `fix`, `docs`, `refactor`, `test`, `chore`, `ci`. The
scope is the component you touched — `chart`, `mocknvml`, `node-agent`, `nri`,
`e2e`, `demo`, `helm`. Append `!` for a breaking change.

```text
feat(nri): mock IMEX channel injection over NRI
fix(chart): correct GPU Operator flags in NOTES.txt
docs(e2e): correct the harness lifecycle
chore!: remove vendor/ and resolve dependencies through the proxy
```

Write the body for someone reading `git log` in a year with no access to the
pull request. Explain the reason, not the diff.

### Sign off every commit

The project uses the Developer Certificate of Origin:

```bash
git commit -s
```

## Before you open the PR

```bash
make lint-fix
make test
make helm-tests     # if you touched a chart
```

See [Testing](testing.md) for the full list of gates and what each one checks.

Also confirm:

- **New files carry an SPDX header.** Match the surrounding files.
- **A changelog fragment is added** if the change is user-facing. See
  [Changelog entries](#changelog-entries).
- **Documentation is updated** alongside the behaviour it describes, not in a
  follow-up.

## Changelog entries

Each entry is a [changie](https://changie.dev) fragment: a small YAML file
under `.changes/unreleased/`, one per change. Do not edit `CHANGELOG.md`. It
holds released versions only and a maintainer writes it when cutting a release,
so two pull requests never conflict over it.

```bash
make changelog                                    # prompts for each field
make changelog KIND=Fixed BODY="..." ISSUE=637    # non-interactive
```

`KIND` is one of the [Keep a Changelog](https://keepachangelog.com/en/1.1.0/)
sections: `Added`, `Changed`, `Deprecated`, `Removed`, `Fixed` or `Security`.
`ISSUE` is optional and renders as a trailing link to that issue or pull
request. Write the body the way it should read in `CHANGELOG.md`, without the
leading `- `. For a long entry, open the generated file and wrap the `body:`
block with continuation lines indented two spaces past the first, the way the
released entries are wrapped. To see how your entry renders:

```bash
make changelog-preview RELEASE_VERSION=0.0.0
```

Refactors, tests, CI and documentation-only changes do not get an entry. A
maintainer applies the `skip-changelog` label to those instead; CI fails a pull
request that has neither a fragment nor the label. Dependabot pull requests are
exempt through the `dependencies` label.

## Opening it

Say what the change does and why. Link the issue it closes. If the change is
substantial enough to need a design first, it needs a
[MEP](enhancements.md) rather than a large pull request.

Keep them small. A single reviewer covers this repository, so three focused
pull requests land faster than one that touches everything.

!!! note "E2E runs on a mirrored branch"
    Unit, lint and chart checks run on the pull request itself. The E2E suite
    runs from a `pull-request/N` branch that copy-pr-bot creates, so it appears
    under a separate run rather than on the PR event. Nothing is wrong if you do
    not see it immediately.

## Review

Reviewers look for the same things the linters cannot check: whether the change
does what its message claims, whether the tests would fail if the behaviour
regressed, and whether comments explain intent rather than restate the code.

An automated reviewer also comments on pull requests. Treat its findings as
suggestions — it is configured to stay out of territory the linters already
cover, but it is not authoritative.

## Related

| To read about | See |
|---|---|
| Running the checks | [Testing](testing.md) |
| Proposing a design first | [Enhancement Proposals](enhancements.md) |
| Using maintainer commands and merge policy | [Repository automation](../maintainers/repository-automation.md) |
| Getting a cluster up | [Local Development](local-development.md) |
