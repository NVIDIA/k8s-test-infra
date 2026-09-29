# Support Policy

This page states which Mokka releases receive fixes. The model is adapted from
the
[NVIDIA GPU Operator life cycle](https://docs.nvidia.com/datacenter/cloud-native/gpu-operator/latest/platform-support.html):
two release lines receive fixes at any time, and each new release line moves the
older ones one step closer to the end of their support.

## Versioning

Mokka follows [Semantic Versioning](https://semver.org/). A *release line* is
every release that shares a major and minor version: 0.4.0, 0.4.1 and 0.4.2 all
belong to the 0.4 line. Each line is cut from its own `release-X.Y` branch.
Patch releases (`vX.Y.Z`) on that branch carry bug fixes, security fixes and
documentation corrections only. Features and breaking changes wait for the next
line.

Mokka has not reached 1.0, so a new minor release such as 0.5.0 starts a new
release line and plays the role of a major release in this policy.

Release candidates such as `v0.4.0-rc1` are previews of a line, not part of it.
They get no patch releases, and the final release supersedes them.

## Life cycle

| Status | Release line | Receives |
|---|---|---|
| **Supported** | The newest line | Patch releases for bug fixes and security fixes |
| **Maintenance** | The line before it | Patch releases for critical bug fixes and security fixes only |
| **Deprecated** | Every older line | No further releases. Upgrade to a line that is still receiving fixes. |

When a new release line ships, the Supported line enters Maintenance and the
line that was in Maintenance becomes Deprecated. Only the Supported and
Maintenance lines receive fixes and backports. Maintainers decide whether a bug
is critical enough to backport to the Maintenance line.

Security fixes include fixes for published CVEs (Common Vulnerabilities and
Exposures). To report a vulnerability, follow the
[security policy](https://github.com/NVIDIA/k8s-test-infra/blob/main/SECURITY.md).

Every fix merges to `main` first and is then cherry-picked onto the release
branches that need it, so no fix is lost when the next line is cut. New features
land only on `main` and ship with the next release line.

## Support status

| Release line | Status |
|---|---|
| 0.4.x | Supported |
| 0.3.x | Maintenance |
| 0.2.x and lower | Deprecated |

!!! note "Effective from v0.4.0"
    This policy takes effect with the v0.4.0 release. Until v0.4.0 is generally
    available, the 0.3 line is the only one that receives patch releases.

## Upgrades

Upgrade within a release line, or from one line to the next. Skipping a line,
for example going from 0.2.x straight to 0.4.x, is not supported: upgrade to
0.3.x first. Before each step, read that release's entry in the
[changelog](https://github.com/NVIDIA/k8s-test-infra/blob/main/CHANGELOG.md).
