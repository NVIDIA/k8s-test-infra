# Contributing to NVIDIA k8s-test-infra

Thank you for your interest in contributing! This guide covers everything you
need to get started.

## Code of Conduct

This project follows the [Contributor Covenant Code of Conduct](CODE_OF_CONDUCT.md).
By participating, you agree to uphold this code.

## Getting Started

### Prerequisites

- Go 1.25+
- Docker 20.10+
- Helm 3.x
- Kind (for E2E testing)
- Make

### Development Setup

```bash
# Clone the repository
git clone https://github.com/NVIDIA/k8s-test-infra.git
cd k8s-test-infra

# Verify Go version
go version  # must be >= 1.25

# Run unit tests
go test -v -race $(go list ./... | grep -v vendor)

# Run linter
golangci-lint run -v --timeout 5m

# Check Go modules
make check-modules
```

### Building the Mock NVML Library

```bash
cd pkg/gpu/mocknvml
make
# Produces libnvidia-ml.so.1, libnvidia-ml.so.1.550.163.01, and symlinks
```

### Running nvidia-smi Locally

```bash
cd pkg/gpu/mocknvml
LD_LIBRARY_PATH=. nvidia-smi
```

## How to Contribute

### Reporting Bugs

Open the [issue chooser](https://github.com/NVIDIA/k8s-test-infra/issues/new/choose)
and select the bug report form.
Include steps to reproduce, expected vs actual behavior, and your environment.

### Suggesting Features

Open the [issue chooser](https://github.com/NVIDIA/k8s-test-infra/issues/new/choose)
and select the feature request form.

### Submitting Changes

1. Fork the repository
2. Create a feature branch from `main`
3. Make your changes with tests
4. Ensure all checks pass (see Testing below)
5. Submit a pull request

## Pull Request Process

1. **One concern per PR** — keep PRs focused and reviewable
2. **Tests required** — new features need tests; bug fixes need regression tests
3. **CI must pass** — all checks (lint, unit tests, E2E) must be green
4. **DCO required** — every human-authored commit must have a matching
   `Signed-off-by` trailer
5. **Review required** — obtain GitHub reviews from applicable owners in
   [OWNERS](OWNERS); the automation-backed LGTM and approval policy described
   below applies after maintainers enable the corresponding workflows

### Pull Request Titles

Pull request titles must use the Conventional Commit form:

```text
<type>[optional scope][optional !]: <description>
```

Accepted types are `feat`, `fix`, `docs`, `test`, `refactor`, `perf`, `build`,
`ci`, `chore`, `chore(deps)`, and `revert`. Use `!` for a breaking change, for
example `feat(api)!: remove legacy configuration`.

### Review Commands

The following command interface is planned. When repository command automation
is enabled, it will recognize these exact commands at the beginning of a
comment line:

- `/lgtm` and `/lgtm cancel` will grant or withdraw reviewer LGTM.
- `/assign` and `/unassign` will manage eligible assignees; add one or more
  GitHub usernames after the command.
- `/hold` and `/hold cancel` will add or remove an explicit merge hold.
- `/retest` will rerun eligible failed checks for the current pull request head.
- `/help` will display supported syntax and authorization.

When the command and review workflows are enabled, command authorization will
depend on the commenter and applicable ownership rules. `/approve` is
unsupported. Automation-backed approval will be a GitHub
`APPROVED` review from an applicable approver in [OWNERS](OWNERS), must apply to
the current pull request head, and must cover every changed file.

At that rollout stage, new commits will invalidate LGTM and automation-backed
approval state, requiring fresh review after each update. Until command
automation is enabled, use ordinary GitHub comments, review requests, and
reviews; slash commands may not take effect.

### Planned Auto-merge Eligibility

After maintainers enable native auto-merge, the merge evaluator workflow, and
the required repository settings, automation may enable native squash
auto-merge only when all of the following are true:

- The pull request is open, is not a draft, and targets a protected release
  branch (`main` or `release-*`).
- The pull request has LGTM.
- Every changed file has a current-head GitHub approval from an applicable
  approver, and the automation-derived `approved` label is present.
- No `do-not-merge/*` label is present.
- GitHub reports the pull request mergeable, with no unresolved ownership
  coverage.

These criteria describe the planned policy and do not imply that auto-merge is
currently active. Once enabled, GitHub will complete a merge only after all
required checks and repository review requirements pass. This includes the
required metadata and DCO checks; enabling auto-merge will not bypass them.

## Testing

### Unit Tests

```bash
go test -v -race $(go list ./... | grep -v vendor)
```

### Helm Chart Tests

```bash
# Lint
ct lint --chart-dirs deployments/nvml-mock/helm --all

# Unit tests
helm unittest deployments/nvml-mock/helm/nvml-mock
```

### E2E Tests

E2E tests run on Kind clusters. See [tests/e2e/README.md](tests/e2e/README.md)
for the full guide.

## Coding Standards

- Follow existing code patterns in the repository
- Use `golangci-lint` for Go code
- Add SPDX license headers to new files:
  ```
  // SPDX-License-Identifier: Apache-2.0
  // SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION
  ```

## Sign Your Work (DCO)

All commits must be signed off per the
[Developer Certificate of Origin](http://developercertificate.org/).

Add a `Signed-off-by` trailer containing your real name and the email address
used to author the commit to every commit message.

Use your real name. If you set `user.name` and `user.email` in your git config,
you can sign automatically with:

```bash
git commit -s
```
