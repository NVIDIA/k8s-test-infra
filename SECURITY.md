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
