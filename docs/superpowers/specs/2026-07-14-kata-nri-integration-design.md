# Kata Containers NRI Integration Design

## Context

PR #450 currently proves that nvml-mock can reach a plain `kata-qemu` guest
through the NVIDIA device plugin's CDI allocation path. It predates PR #433,
which added an opt-in containerd NRI plugin that injects the complete
nvml-mock overlay into ordinary workloads.

Rebasing the existing CDI-only lane would preserve two workarounds that NRI
now makes unnecessary: an explicit mock-config `hostPath` volume and manual
creation of the `libnvidia-ml.so.1` soname link inside the guest. It would also
leave the new primary node-wide injection path untested under Kata.

## Goal

Convert PR #450 into an NRI-native plain-Kata E2E lane. The lane must prove
that containerd applies nvml-mock NRI adjustments before handing the workload
to the Kata runtime, and that Kata carries the resulting overlay, environment,
and optional mock device nodes into the guest.

## Non-goals

- Do not validate Kubernetes extended-resource scheduling or allocation in
  this lane. NRI ambient injection does not advertise `nvidia.com/gpu`.
- Do not combine NRI and device-plugin CDI in one workload. That would make
  delivery failures difficult to attribute and would retain redundant mounts.
- Do not claim Confidential Containers support. This design relies on host
  filesystem sharing available to plain Kata; CoCo guest-payload delivery
  remains separate follow-up work.
- Do not change the merged NRI plugin or mock NVML visibility semantics unless
  the E2E run exposes an implementation defect.

## Architecture

The lane uses three distinct layers:

1. Kind starts containerd with NRI enabled and the standard NRI socket at
   `/var/run/nri/nri.sock`. The node also receives `/dev/kvm`,
   `/dev/vhost-vsock`, and the larger `/dev/shm` required by Kata.
2. `kata-deploy` installs the `kata-qemu` runtime. A small smoke pod proves the
   pod kernel differs from the Kind node kernel before nvml-mock is involved.
3. The nvml-mock chart is installed with `nri.enabled=true` in a dedicated
   `nvml-mock-system` namespace. The main DaemonSet stages the overlay on the
   node, while the NRI DaemonSet registers with containerd. Workloads run in
   `default`, which is outside the NRI plugin's excluded namespaces.

For each eligible workload container, NRI adds a read-only bind mount from
`/var/lib/nvml-mock` to `/opt/nvml-mock` plus the PATH, loader, config, and
mock subsystem environment. Kata translates that host-directory mount into
its guest filesystem-sharing mechanism. When the pod opts into mock devices,
NRI also adds Linux character-device entries and Kata's agent creates the
corresponding inert nodes in the guest.

## Workflow Changes

### Kind and runtime setup

- Replace the CDI-only containerd patch in `tests/e2e/kind-kata-config.yaml`
  with the NRI configuration used by the merged node-wide injection demo.
  CDI is not required by the NRI-native lane.
- Retain KVM, vhost-vsock, Kata debug logging, and the enlarged node
  `/dev/shm`; they remain independent prerequisites for a functioning guest.
- Install `kata-deploy` before nvml-mock so the NRI plugin registers against
  the final containerd configuration rather than racing a subsequent runtime
  restart.

### Chart installation and readiness

- Create and use the `nvml-mock-system` namespace.
- Install the chart with the locally built image, the A100 profile, two mock
  GPUs, and `nri.enabled=true`.
- Wait for both `daemonset/nvml-mock` and
  `daemonset/nvml-mock-nri` in `nvml-mock-system`.
- Confirm the NRI plugin log contains its successful containerd configuration
  message before creating test workloads. A missing socket or registration is
  therefore reported as an infrastructure failure rather than a later missing
  file inside the guest.

### Workload contracts

The lane runs three Kata workloads in `default`:

1. **Ambient injection:** The pod has `runtimeClassName: kata-qemu` but no
   mock annotations, resource request, `MOCK_*` environment, or volumes. It
   proves the guest kernel differs from the node kernel, `/opt/nvml-mock` is
   mounted read-only, `nvidia-smi` resolves through NRI-injected PATH, the
   soname symlink and profile config exist in the overlay, and the A100 profile
   reports both configured GPUs. With no `/dev/nvidiaN` entries, the engine's
   established "none present means unfiltered" behavior intentionally exposes
   the configured node-wide simulation.
2. **Device opt-in:** The pod adds
   `nvml-mock.nvidia.com/devices: "true"`. It asserts that the expected
   `/dev/nvidia0`, `/dev/nvidia1`, `/dev/nvidiactl`, and UVM nodes exist inside
   the guest and that NVML remains functional. This proves NRI device metadata
   survives the Kata boundary; the device nodes remain inert mock nodes, not
   hardware passthrough.
3. **Injection opt-out:** The pod adds
   `nvml-mock.nvidia.com/inject: "false"`. It asserts that
   `/opt/nvml-mock` is absent and `nvidia-smi` is unavailable. This replaces
   the old no-resource-request negative control, which is incompatible with
   intentional ambient NRI injection.

Each workload is deleted before the next is created. This keeps pod logs and
failure attribution unambiguous and avoids carrying a VM across contracts.

## Repository Changes

- Rewrite the `e2e-kata` job in
  `.github/workflows/nvml-mock-e2e.yaml` around NRI readiness and the three
  contracts above.
- Update `tests/e2e/kind-kata-config.yaml` for NRI.
- Delete `tests/e2e/device-plugin-kata.yaml`; the bespoke device plugin is no
  longer part of the design.
- Rewrite `docs/integrations/kata.md` to describe NRI ambient delivery,
  optional device injection, opt-out behavior, and the plain-Kata/CoCo
  boundary.
- Keep the integration index entry in `docs/README.md`.

## Failure Handling

The failure collector must retain the Kata diagnostics already learned from
earlier CI rounds and add NRI-specific evidence:

- nvml-mock and nvml-mock-nri pod status and logs;
- the node's NRI socket and relevant containerd configuration;
- containerd journal lines for both NRI and Kata;
- the failed workload description and logs;
- Kata runtime journal, QEMU process, KVM/vsock, and containerd drop-in data.

Commands used only for evidence collection remain best-effort so the original
failure is not hidden.

## Verification

Local verification covers repository-level contracts:

- `go test ./...`
- Helm chart tests or rendering that exercise `nri.enabled=true`
- workflow/YAML validation using the repository's available checks
- focused diff review confirming the obsolete device-plugin manifest and CDI
  instructions are removed

The authoritative behavior check is the `e2e-kata` GitHub Actions job because
it requires runner KVM, vhost-vsock, Kind, Kata, and containerd NRI. Success
requires all three workload contracts plus the independent guest-kernel guard.
