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

Open an issue using the [bug report template](.github/ISSUE_TEMPLATE/bug_report.md).
Include steps to reproduce, expected vs actual behavior, and your environment.

### Suggesting Features

Open an issue using the [feature request template](.github/ISSUE_TEMPLATE/feature_request.md).

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
4. **Review required** — at least one maintainer approval from [OWNERS](OWNERS)

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

Add a sign-off line to every git commit message:

    Signed-off-by: Your Name <your.email@example.com>

Use your real name. If you set `user.name` and `user.email` in your git config,
you can sign automatically with:

```bash
git commit -s
```
