# What not to raise

Findings that any of the checks below already produce are noise on a pull
request: they arrive twice, and the second copy trains reviewers to skim.

`golangci-lint` runs on every pull request in this repository with `revive`,
`staticcheck` (all checks), `testifylint` (enable-all), `cyclop`, `nestif`,
`bodyclose`, `contextcheck`, `errname`, `exhaustive`, `misspell`, `nolintlint`,
`perfsprint`, `prealloc`, `predeclared`, `asasalint`, `unconvert`,
`usestdlibvars` and `loggercheck`. It lints `e2e` and `integration`
tag-guarded sources too. Do not raise anything inside that scope, including:

- Formatting, import order, unused variables, type errors. The compiler and
  `gofmt` reject these before review.
- Test assertion style, `require` against `assert`, expected and actual
  argument order. `testifylint` runs with every check enabled.
- Cyclomatic complexity and nesting depth.
- Unclosed response bodies, context that is not propagated, misspellings,
  stale `//nolint` directives.

Also leave these alone:

- Problems on lines this change did not touch. Report only what the diff
  introduces or makes newly reachable.
- Nitpicks that a senior engineer would not stop a merge for.
- Missing tests or documentation, unless `AGENTS.md` asks for them.
- Anything silenced in code by a `//nolint` directive that carries an
  explanation. `nolintlint` requires both a specific linter and a reason, so a
  directive that survives lint was deliberate.
- Choices that are plainly part of the stated purpose of the change.

# Weighing a finding before posting it

Post a finding when you can name the input that reaches it and the behaviour
that results. A reader should be able to act on the comment without asking a
follow-up question.

Hold a finding back when it rests on a guess about code you cannot see in the
diff, when the same defect already has a comment elsewhere in this review, or
when the only supporting argument is that a different structure would read
better. Two symptoms of one defect are one comment, not two.
