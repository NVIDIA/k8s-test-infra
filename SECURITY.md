# Security Policy

## Supported Versions

| Version | Supported          |
|---------|--------------------|
| 0.1.x   | :white_check_mark: |

## Reporting a Vulnerability

If you discover a security vulnerability in this project, please report it
responsibly.

**Please do NOT open a public GitHub issue for security vulnerabilities.**

Instead, please report them via GitHub's private vulnerability reporting:

1. Go to the [Security Advisories page](https://github.com/NVIDIA/k8s-test-infra/security/advisories)
2. Click **"Report a vulnerability"**
3. Fill in the details

Alternatively, email **psirt@nvidia.com** with:
- Description of the vulnerability
- Steps to reproduce
- Affected versions
- Any potential impact

## Response Timeline

- **Acknowledgment:** within 3 business days
- **Initial assessment:** within 10 business days
- **Fix timeline:** depends on severity, typically within 30-90 days

## Scope

This project provides **mock GPU infrastructure for testing**. It does not
handle real GPU workloads or sensitive data. However, we take security of
our CI/CD pipelines, container images, and supply chain seriously.

Areas of particular interest:
- Container image vulnerabilities
- GitHub Actions workflow security
- Supply chain integrity (dependencies, build process)
- Helm chart security (RBAC, privileges)

## Secure Development

Mokka is a test double for CI and test clusters, not production. The `nvml-mock`
DaemonSet container runs `privileged: true` because staging driver-shaped files
and device nodes onto the host requires it; we disclose that rather than work
around it. Every other component is least-privileged: the ClusterRole grants
`get` and `patch` on `nodes` and nothing else, the control plane and allocation
watcher drop all capabilities and run with `readOnlyRootFilesystem`, and the
container that creates device nodes adds back only `MKNOD`.

Inputs come from the operator installing the chart rather than from untrusted
third parties, so validation is scoped accordingly: chart values are checked
against `deployments/nvml-mock/helm/nvml-mock/values.schema.json`, and the YAML
device profile is checked by `validateYAMLConfig` in
`pkg/gpu/mocknvml/engine/config.go`. Review attention goes to the error classes
this codebase can actually hit — memory handling across the CGo boundary in
`shims/` and `pkg/gpu/mocknvml`, and path handling in the privileged code that
writes to hostPath mounts and performs NRI injection.

Mokka implements no cryptography. It stores no passwords and generates no keys,
and where TLS is needed to reach the Kubernetes API server it calls `client-go`
and the Go standard library, which verify certificates by default.

## Security Analysis

CodeQL, `golangci-lint`, and `go test -race` run on every pull request and every
push to `main`. `-Wall -Wextra` gates the C shims, and
`pkg/gpu/mocknvml/engine/fuzz_test.go` provides a Go fuzz target. Dependabot
proposes weekly Go module and GitHub Actions updates, and
[OpenSSF Scorecard](https://scorecard.dev/viewer/?uri=github.com/NVIDIA/k8s-test-infra)
reports supply-chain posture publicly.
