# Governance

## Overview

The NVIDIA k8s-test-infra project (including nvml-mock) uses a maintainer-based
governance model. All major decisions are made through consensus among maintainers,
with discussion on GitHub issues and pull requests.

## Roles

### Maintainers

Maintainers have write access to the repository and are responsible for:
- Reviewing and merging pull requests
- Triaging issues
- Making architectural decisions
- Cutting releases

Current maintainers are listed in the [OWNERS](OWNERS) file.

### Contributors

Anyone who contributes code, documentation, bug reports, or other improvements.
Contributors must sign off their commits per the [DCO](CONTRIBUTING.md).

### Reviewers

Reviewers are listed in `OWNERS` and are expected to review PRs in their area
of expertise. Reviewers may be promoted to maintainers by consensus of existing
maintainers.

## Decision Making

- **Code changes:** Require at least one approving review from a maintainer
  listed in `OWNERS`.
- **Architectural decisions:** Discussed in GitHub issues; decided by maintainer
  consensus. If no consensus after 7 days, the majority of maintainers decides.
- **Adding/removing maintainers:** Requires consensus of existing maintainers.

## Conflict Resolution

If contributors disagree on a decision, the process is:

1. Discussion on the relevant GitHub issue or PR
2. If unresolved, maintainers vote (majority wins)
3. If still unresolved, NVIDIA's open source office mediates

## Changes to Governance

Changes to this document require approval from all active maintainers.
