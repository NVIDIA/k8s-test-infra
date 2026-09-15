---
title: Updating IMEX
---

# Updating the IMEX dependency

The standard Mokka image contains the real NVIDIA IMEX daemon and control
client for both supported image platforms. Mokka installs its wrapper as
`/usr/bin/nvidia-imex` and retains the downloaded daemon unchanged at
`/usr/bin/nvidia-imex.real`; the wrapper adds `--nogpu` so the userspace peer
protocol can run against Mokka's simulated driver surface.

Including these files does not start IMEX or enable channel simulation. The
daemon runs only when a consumer starts it, and the channel surface remains
controlled by `imex.mockChannels.enabled`.

## Source of truth

[`deployments/nvml-mock/build/imex.lock`](https://github.com/NVIDIA/k8s-test-infra/blob/main/deployments/nvml-mock/build/imex.lock)
is the only source of the selected version, NVIDIA redistribution-archive path,
and SHA-256 digest. It maps OCI architectures as follows:

| OCI platform | NVIDIA archive family |
|---|---|
| `linux/amd64` | `linux-x86_64` |
| `linux/arm64` | `linux-sbsa` |

The main Dockerfile downloads and verifies the selected archive. The local DRA
adapter copies the already verified executables from that Mokka image; it must
not contain another version, URL, or checksum.

## Update procedure

1. Select one IMEX release that has both archive families in NVIDIA's official
   driver redistribution manifest.
2. Download both archives independently and calculate their SHA-256 digests.
3. Update all five values in `deployments/nvml-mock/build/imex.lock` in one
   change. Do not put the values in a Dockerfile or CI workflow.
4. Review the archive's `LICENSE` and `third-party-notices.txt`. The image keeps
   them under `/usr/share/doc/nvidia-imex/` alongside a copy of the lock file.
5. Run the deterministic acquisition tests, shim tests, and both platform
   builds:

   ```bash
   go test ./tests/imexredist
   make test-nvidia-imex-shim
   docker buildx build --platform linux/amd64,linux/arm64 \
       -f deployments/nvml-mock/Dockerfile .
   ```

6. Run the focused lifecycle test on a multi-worker NRI-enabled cluster:

   ```bash
   make e2e E2E_PROFILES=gb200 \
       E2E_GINKGO_FLAGS='--label-filter="imex-lifecycle"'
   ```

The lifecycle test is the compatibility check: a successful download alone
does not prove that the daemon, control client, wrapper, configuration, and
peer protocol still agree.

## Publication approval

Redistribution approval is a release gate, not a code-review inference. A pull
request that adds or updates the NVIDIA-provided artifacts must link the
applicable legal approval before it can merge into a branch that publishes the
Mokka image. The approval must cover both platforms, the retained notices, and
the wrapper's unchanged-byte rename of the real executable.

If redistribution is not approved, remove IMEX from the published image and
document a user-built derived image instead. Do not silently switch to an OS
package or a second published Mokka variant; either choice changes the reviewed
provenance and product experience.
