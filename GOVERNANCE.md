# Governance

## Overview

The NVIDIA k8s-test-infra project (including nvml-mock) uses a maintainer-based
governance model. Major decisions are made through maintainer consensus after
public discussion in GitHub issues or pull requests.

## Roles

### Contributors

Anyone who contributes code, documentation, bug reports, or other improvements.
Contributors must sign off their commits per the [DCO](CONTRIBUTING.md).

### Reviewers

Reviewers are listed in `OWNERS` and are expected to review PRs in their area
of expertise. An applicable reviewer or approver may grant or cancel LGTM with
`/lgtm` or `/lgtm cancel`. LGTM records technical review; it is not a GitHub
approval.

### Approvers

Approvers are listed in `OWNERS`. An applicable approver authorizes a change by
submitting a GitHub `APPROVED` review for the current pull request head. Approval
coverage follows the `OWNERS` hierarchy and must cover every changed file.
`/approve` is not supported.

### Maintainers

Maintainers have repository write or administrative access. The root
[OWNERS](OWNERS) roster identifies the current project maintainers through its
reviewer and approver entries. Maintainers are responsible for:

- Triaging issues and shepherding public project decisions
- Maintaining reviewer and approver rosters
- Merging pull requests after automated policy and required checks pass
- Cutting releases and maintaining release policy
- Managing repository settings, rules, workflows, and emergency access

## Decision Making

- **Code changes:** Require reviewer LGTM and current-head GitHub approval from
  applicable approvers in `OWNERS`, covering every changed file. Required checks
  and merge policy must also pass.
- **Architectural decisions:** Discussed in GitHub issues and decided by
  maintainer consensus. If consensus cannot be reached, maintainers decide by
  majority.
- **Adding/removing maintainers:** Requires consensus of existing maintainers.

## Conflict Resolution

If contributors disagree on a decision, the process is:

1. Discussion on the relevant GitHub issue or PR
2. If unresolved, maintainers vote (majority wins)
3. If still unresolved, the maintainers make and document the final project
   decision

Conduct concerns are handled through the reporting instructions in
[CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md), not through public project debate.

## Changes to Governance

Changes to this document require approval from all active maintainers.
