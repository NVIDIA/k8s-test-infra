# Security Policy

## Supported Versions

Supported versions are identified in project releases and security advisories.
Because support may change as the project evolves, this policy does not make a
permanent support commitment for a hard-coded version line.

## Reporting a Vulnerability

If you discover a security vulnerability in this project, please report it
responsibly.

**Please do NOT open a public GitHub issue for security vulnerabilities.**

Instead, please report them via GitHub's private vulnerability reporting:

1. Go to the [Security Advisories page](https://github.com/NVIDIA/k8s-test-infra/security/advisories)
2. Click **"Report a vulnerability"**
3. Fill in the details

Alternatively, report the vulnerability to
[NVIDIA PSIRT](https://www.nvidia.com/en-us/security/report-vulnerability/),
including by email at **psirt@nvidia.com**, with:

- Description of the vulnerability
- Steps to reproduce
- Affected versions
- Any potential impact

## What to Expect

The responding security team will communicate the report's status as it is
triaged, investigated, and resolved. Response and remediation timing depends on
the report's complexity, severity, and affected components; this project does
not promise fixed deadlines.

## Scope

This project provides **mock GPU infrastructure for testing**. It does not
handle real GPU workloads or sensitive data. However, we take security of
our CI/CD pipelines, container images, and supply chain seriously.

Areas of particular interest:
- Container image vulnerabilities
- GitHub Actions workflow security
- Supply chain integrity (dependencies, build process)
- Helm chart security (RBAC, privileges)
