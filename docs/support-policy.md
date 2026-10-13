# Support Policy

This page states how Mokka releases are numbered, which release receives fixes,
and how long a deprecated feature stays.

## Versioning

A Mokka version is `X.Y.Z`:

| Part | Increases for |
|---|---|
| X (major) | From 1.0, a change that breaks the API: the CRDs, configuration files, environment variables, or how Mokka works. |
| Y (minor) | A scheduled release, cut from `main` every second week with whatever has merged. |
| Z (patch) | A release that carries only security and bug fixes. |

Before 1.0, a change that breaks the API ships in the next Y release, with a
`**Breaking:**` changelog entry that states the action a user takes; from 1.0,
it makes the release an X.

Mokka publishes no release candidates, only stable `vX.Y.Z` releases. The
images and charts a release publishes carry its version without the `v`: the
`v0.5.0` tag publishes `0.5.0`.

## Life cycle

Only the latest release receives fixes: the
[latest release on the GitHub releases page](https://github.com/NVIDIA/k8s-test-infra/releases/latest).

Fixes land on `main` and ship in the next scheduled release. When a fix cannot
wait, maintainers cut an out-of-cycle release from `main`. It is a Z release
when every change since the last release is a fix, and the next Y otherwise,
or from 1.0 the next X when a change breaks the API.

There are no release branches and no backports, so an older release gets no
fixes. To get a fix, upgrade to the latest release.

Security fixes include fixes for published CVEs (Common Vulnerabilities and
Exposures). To report a vulnerability, follow the
[security policy](https://github.com/NVIDIA/k8s-test-infra/blob/main/SECURITY.md).

!!! note "Effective 2026-10-12"
    This policy takes effect on 2026-10-12. The `release-X.Y` branches are
    retired, and v0.4.1 was the last release cut from one.

## Deprecation

A deprecation is announced in a Y release, in the `Deprecated` section of its
[changelog](https://github.com/NVIDIA/k8s-test-infra/blob/main/CHANGELOG.md)
entry. The deprecated feature is removed no earlier than three Y releases
later: a deprecation announced in 0.6.0 can be removed in 0.9.0 at the
earliest.

## Upgrades

Before upgrading, read the
[changelog](https://github.com/NVIDIA/k8s-test-infra/blob/main/CHANGELOG.md)
entry of every release between your version and the target.
