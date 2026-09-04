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

## Security Expectations

Mokka is a test double. It implements the NVIDIA driver interfaces that GPU
software talks to, so that the device plugin, the DRA driver, the GPU Operator
and `nvidia-smi` behave as though hardware were present. That purpose shapes
what you should and should not expect from it:

- **Mokka is for test and CI clusters, not production.** The `nvml-mock`
  DaemonSet container runs privileged and stages driver-shaped files onto host
  paths. Do not install it on a cluster carrying production workloads.
- **Mokka makes no security claims about the software it simulates hardware
  for.** It provides no isolation, admission control, or policy enforcement for
  the GPU software that consumes its mock driver files.
- **Mokka processes no sensitive data.** It has no user accounts, stores no
  credentials, and keeps no persistent state beyond the driver files it stages
  and the node labels it patches.

What you can expect is the least-privilege posture and the supply-chain
practices described below.

## Secure Development Practices

### Design principles

Least privilege is applied per component rather than claimed globally:

- The ClusterRole grants `get` and `patch` on `nodes`, and nothing else.
- The control plane Deployment runs `runAsNonRoot` as UID 65532 with
  `readOnlyRootFilesystem`, `capabilities.drop: [ALL]`, and
  `seccompProfile: RuntimeDefault`.
- The allocation watcher drops all capabilities and runs with
  `readOnlyRootFilesystem`; it only reads the kubelet pod-resources socket.
- The container that creates device nodes drops all capabilities and adds back
  only `MKNOD`.
- The `nvml-mock` container runs `privileged: true`. Staging driver files and
  device nodes onto the host requires it. We disclose this rather than work
  around it, and we reduce what it exposes: the InfiniBand ping relay in that
  pod listens without authentication, so an opt-out NetworkPolicy restricts its
  ingress to peer nvml-mock daemon pods.

### Common implementation errors

Review attention goes to the error classes this codebase can actually hit:

- **Memory handling across the CGo boundary** in `shims/` and
  `pkg/gpu/mocknvml`, where Go code fills C structs consumed by real NVIDIA
  client software.
- **Path handling** in the code that writes to hostPath mounts and performs NRI
  injection, since that code runs privileged against host directories.

### Input validation

Mokka's inputs come from the cluster operator installing the chart, not from
untrusted third parties, so validation is scoped accordingly:

- **Chart values** are validated against a JSON Schema
  (`deployments/nvml-mock/helm/nvml-mock/values.schema.json`), which Helm
  enforces at install and upgrade time.
- **The YAML device profile** is validated by `validateYAMLConfig` in
  `pkg/gpu/mocknvml/engine/config.go`: it requires a config version and a
  driver version, and rejects duplicate device indices.

## Cryptography

Mokka implements no cryptographic functionality. It stores no passwords,
generates no keys, and defines no cryptographic protocols. Where TLS is needed
to reach the Kubernetes API server, it calls `client-go` and the Go standard
library, which perform certificate verification by default.

## Security Analysis

Static and dynamic analysis run in CI on every pull request and on every push to
`main`:

| Tool | Scope | Where |
|------|-------|-------|
| CodeQL | Semantic static analysis of the Go tree | `.github/workflows/code_scanning.yaml` |
| golangci-lint | Go linting, including tag-guarded e2e and integration sources | `.golangci.yml` |
| Race detector | `go test -race` across all packages | `make test` |
| Go fuzzing | `pkg/gpu/mocknvml/engine/fuzz_test.go` | `go test -fuzz` |
| `-Wall -Wextra` | The C shims | `shims/libibmock/Makefile`, `shims/libpcisysfs/Makefile` |
| Dependabot | Weekly Go module and GitHub Actions updates | `.github/dependabot.yml` |
| OpenSSF Scorecard | Supply-chain posture, reported publicly | `.github/workflows/scorecard.yaml` |

Findings from these tools are fixed rather than suppressed wholesale; the
per-path exclusions in `.golangci.yml` each carry a written justification.
