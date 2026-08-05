# MEP-0002: Compose device-plugin advertisement with NRI delivery

<!-- toc -->
- [Summary](#summary)
- [Motivation](#motivation)
  - [What we measured](#what-we-measured)
  - [Finding 1: there is no double injection](#finding-1-there-is-no-double-injection)
  - [Finding 2: the allocation is inert](#finding-2-the-allocation-is-inert)
  - [Finding 3: the mock already has the mechanism](#finding-3-the-mock-already-has-the-mechanism)
  - [Finding 4: the NRI device annotation defeats it](#finding-4-the-nri-device-annotation-defeats-it)
  - [Goals](#goals)
  - [Non-Goals](#non-goals)
- [Proposal](#proposal)
  - [User Stories](#user-stories)
  - [Notes/Constraints/Caveats](#notesconstraintscaveats)
  - [Risks and Mitigations](#risks-and-mitigations)
- [Design Details](#design-details)
  - [Change 1: turn on device specs in the device-plugin manifests](#change-1-turn-on-device-specs-in-the-device-plugin-manifests)
  - [Change 2: suppress blanket device injection in the NRI plugin](#change-2-suppress-blanket-device-injection-in-the-nri-plugin)
  - [Change 3: e2e scenario](#change-3-e2e-scenario)
  - [What MEP-0002 permits and forbids for #436](#what-mep-0002-permits-and-forbids-for-436)
  - [Test plan](#test-plan)
- [Drawbacks](#drawbacks)
- [Alternatives](#alternatives)
<!-- /toc -->

## Summary

Mokka has two independent paths that put mock GPU state into a container. The
upstream NVIDIA device plugin advertises `nvidia.com/gpu` to the scheduler after
it discovers devices through the mock `libnvidia-ml`. The `nvml-mock-nri` plugin
injects the mock overlay, the environment and the device nodes at container
creation. Neither path knows about the other.

This MEP proposes to compose them at the device-node layer. The device plugin
keeps advertisement **and** takes back delivery of the allocated device node.
The NRI plugin keeps delivery of the overlay and the environment, and stops
injecting device nodes into a container that the device plugin already served.

The result is the acceptance criterion in issue #440: a pod that requests
`nvidia.com/gpu: 1` schedules, runs, and sees exactly one GPU, with no
mock-aware wiring in its spec.

No new component is built. The change is one flag in two manifests, one
suppression rule in `pkg/nri/nvmlmock/adjust.go`, and one e2e scenario.

## Motivation

Issue #440 asks how to "compose mock device plugin advertisement with NRI
delivery". The premise needs one correction before the design starts.

**There is no mock device plugin in this repository.** There is no
`cmd/device-plugin`, no `pkg/deviceplugin`, and no path matching
`deviceplugin` anywhere in the tree. What exists is
[`tests/e2e/go/assets/device-plugin-mock.yaml`](../../../tests/e2e/go/assets/device-plugin-mock.yaml),
a DaemonSet that deploys the **genuine** upstream plugin,
`nvcr.io/nvidia/k8s-device-plugin:v0.18.2`, pointed at the mock driver root:

```yaml
args:
  - "--nvidia-driver-root=/var/lib/nvml-mock/driver"
  - "--driver-root-ctr-path=/var/lib/nvml-mock/driver"
  - "--device-discovery-strategy=nvml"
```

Advertisement already works, and it works with the real plugin discovering
through our mock NVML. That is a feature, not a gap: we test the production
component, not a re-implementation of it. So #440 is not "build advertisement".
It is "decide what happens on a node where both paths act on one container".

### What we measured

Every claim below comes from a run, not from reading the source. The
environment was kind v0.32.0, `kindest/node:v1.35.0`, containerd 2.2.0, NRI API
v0.10.0, chart `0.3.0-rc1`, image `ghcr.io/nvidia/nvml-mock:0.3.0-rc1`, profile
`a100` with `gpu.count=2`, three nodes. The chart and image refs are left at
`0.3.0-rc1` deliberately: that is what the measurement ran against. The same
code ships in `v0.3.0`. The OCI specs come from
`ctr --namespace k8s.io containers info` on the node that ran each container.

| Case | Pod spec | Device-plugin flags | `linux.devices` in the OCI spec | `nvidia-smi -L` |
| --- | --- | --- | --- | --- |
| A | `nvidia.com/gpu: 1` | default | *(none)* | 2 GPUs |
| B | `devices: "true"`, no request | default | all 5 | 2 GPUs |
| C | plain pod | default | *(none)* | 2 GPUs |
| D | request **and** annotation | default | all 5 | 2 GPUs |
| E | `nvidia.com/gpu: 1` | `--pass-device-specs=true` | `/dev/nvidia0` only | **1 GPU, the allocated one** |
| F | request **and** annotation | `--pass-device-specs=true` | all 5, one duplicate cgroup rule | 2 GPUs |

"default" is the configuration the repository ships today. The plugin logs its
effective config at startup: `passDeviceSpecs: false`,
`deviceListStrategy: ["envvar"]`, `deviceIDStrategy: "uuid"`.

### Finding 1: there is no double injection

In the shipped configuration the two paths write to **disjoint** fields of the
OCI spec.

- The device plugin contributes exactly one thing:
  `NVIDIA_VISIBLE_DEVICES=GPU-…-123456780000`.
- The NRI plugin contributes the `/opt/nvml-mock` bind mount, the `PATH`,
  `LD_LIBRARY_PATH`, `LD_PRELOAD` and `MOCK_*` environment, and — only when the
  pod carries `nvml-mock.nvidia.com/devices: "true"` — the device nodes.

Case A shows one mount at `/opt/nvml-mock`, no duplicate environment key, and an
`LD_PRELOAD` with the four expected shims and nothing else. No conflicting
device entry, no clobbered `LD_PRELOAD`, no environment-precedence problem.
The composition is benign, and it is benign by construction rather than by
accident: `buildEnv` in `adjust.go` emits only the keys it adds or changes, and
`shouldSkip` already declines a container that carries the overlay mount.

### Finding 2: the allocation is inert

Case A requests one GPU, is allocated `…780000`, and reports **two** GPUs.
Case C requests nothing, consumes no capacity, and also reports two GPUs. The
scheduler decision never reaches the container.

`NVIDIA_VISIBLE_DEVICES` is written and never read. Three controlled probes
inside a running pod confirm the mock ignores it. Unsetting the variable,
setting it to `all`, and setting it to a *different* GPU's UUID all leave
`nvidia-smi -L` unchanged:

```
$ kubectl exec sched-3 -- sh -c 'env -u NVIDIA_VISIBLE_DEVICES nvidia-smi -L'
GPU 0: NVIDIA A100-SXM4-40GB (UUID: GPU-12345678-1234-1234-1234-123456780001)
$ kubectl exec sched-3 -- sh -c 'NVIDIA_VISIBLE_DEVICES=…780000 nvidia-smi -L'
GPU 0: NVIDIA A100-SXM4-40GB (UUID: GPU-12345678-1234-1234-1234-123456780001)
```

So `nvidia.com/gpu: 1` gates scheduling and changes nothing inside the
container. Any test of GPU **isolation** — a workload that shards by device
count, a time-slicing or MPS configuration, an operator that reconciles
allocated against visible — passes today against the wrong reality.

### Finding 3: the mock already has the mechanism

`detectVisibleDevicesAt` in
[`pkg/gpu/mocknvml/engine/engine.go`](../../../pkg/gpu/mocknvml/engine/engine.go)
scans `/dev/nvidia<N>` for `N < NumDevices` and filters the visible set to the
subset present. Its own comment states the intent: it "mimics real NVML
behavior where cgroup device permissions limit which GPUs a container can see."

The function returns `nil` — meaning no filtering — in two cases: when every
node is present, and when none is. The "none present" branch is what makes the
node-wide NRI story work, because a plain unmodified pod must still see the mock
GPUs. It is also what makes case A report two GPUs.

The filter is live and unit-tested. The deployed manifest simply never triggers
it. Case E is the proof: with `--pass-device-specs=true` the plugin delivers
only the allocated node and the container reports exactly its allocated GPU.
The plugin resolves the host path through `--nvidia-driver-root`, which the
manifest already points at the mock tree — `/dev/nvidia*` does not exist on a
kind node, and the container's `/dev/nvidia0` carries major/minor 195:0,
matching `/var/lib/nvml-mock/driver/dev/nvidia0`.

**This is the composition point. It exists, it is deliberate, and it is
disconnected.**

### Finding 4: the NRI device annotation defeats it

Blanket device injection stages every `/dev/nvidiaN`, so `absent == 0`, the
filter returns `nil`, and all GPUs become visible again. Case F is allocated
`…780001` and reports both GPUs.

Case F also produces a literal duplicate. The kubelet added a cgroup allow rule
for the allocated device and containerd added a second one for the NRI device:

```
{'allow': True, 'type': 'c', 'major': 195, 'minor': 1, 'access': 'rw'}   <- kubelet
…
{'allow': True, 'type': 'c', 'major': 195, 'minor': 1, 'access': 'rw'}   <- NRI
```

`linux.devices` is deduplicated by path; the cgroup rules are not. The duplicate
is harmless at runtime — two identical allow rules have one effect — but it is
direct evidence that the two paths do not coordinate.

### Goals

1. A pod that requests `nvidia.com/gpu: N` sees exactly `N` GPUs, and their
   UUIDs are the ones the device plugin allocated. No mock-aware wiring in the
   pod spec.
2. A pod with the device opt-in annotation and **no** resource request keeps
   today's behaviour: all device nodes, all GPUs visible, no scheduler capacity
   consumed.
3. A plain pod with neither keeps today's behaviour: no device nodes, all GPUs
   visible through the overlay. The node-wide NRI story does not regress.
4. Scheduling stays correct on multi-node kind: capacity is per node, the
   allocated UUID set is disjoint within a node, and an over-subscribed pod
   stays `Pending`.
5. One e2e scenario co-deploys both paths and asserts goals 1 to 4. No CI job
   does this today.

### Non-Goals

- **Building a mock device plugin.** The real plugin works against the mock.
  Replacing it would delete the fidelity that makes the test meaningful.
- **Teaching the NRI plugin to advertise.** Epic #441 states that scheduling
  advertisement is out of scope for the plugin itself. This MEP does not change
  that; it makes the plugin *defer* to the advertiser, which is the opposite of
  taking the job on.
- **Making the mock read `NVIDIA_VISIBLE_DEVICES`.** See
  [Alternatives](#alternatives).
- **Choosing the CDI end state.** That is #436. This MEP constrains it; it does
  not decide it.
- **MIG, time-slicing or MPS composition.** Each needs its own design.

## Proposal

Compose at the device-node layer, in three changes.

1. **Advertisement and allocation stay with the device plugin, and it also
   delivers the allocated device node.** Add `--pass-device-specs=true` to the
   two device-plugin manifests. This is the flag the real plugin uses on a node
   with no container runtime hook, which is exactly what a kind node is.

2. **The NRI plugin stops injecting device nodes into a container the device
   plugin already served.** It keeps injecting the overlay and the environment
   into every container, unchanged. Only the device list is suppressed, and only
   when the incoming container already carries GPU device nodes or CDI devices.

3. **One e2e scenario proves it**, co-deploying both paths for the first time.

The mock's `detectVisibleDevices` filter then does the rest. Nothing new
computes an allocation, and nothing new needs to agree with the scheduler.

### User Stories

**Story 1 — an unmodified GPU workload.** A user has a Helm chart that requests
`nvidia.com/gpu: 1` and runs a job that calls `nvmlDeviceGetCount`. They install
it on a Mokka cluster with no edits. It schedules on a mock node, reports one
GPU, and the UUID matches what the scheduler allocated.

**Story 2 — a node-wide agent.** A user runs a DaemonSet that inspects every
GPU on the node and requests no GPU resources. It keeps seeing all of them, as
it does today.

**Story 3 — an isolation test.** A test author starts two pods on one two-GPU
node, each requesting one GPU, and asserts that each sees a different single
GPU. Today this test cannot be written; both pods see both GPUs.

### Notes/Constraints/Caveats

- **`--pass-device-specs=true` delivers only `/dev/nvidiaN`.** It does not
  deliver `/dev/nvidiactl` or the UVM nodes; on a real node the container
  runtime hook adds those. The mock does not need them, because
  `detectVisibleDevicesAt` stats only `/dev/nvidia%d`. A workload that opens
  `/dev/nvidiactl` directly will not find it. Document this; do not paper over
  it by having the NRI plugin add the control nodes back, which would make the
  suppression rule ambiguous.
- **The annotation's contract narrows.** `nvml-mock.nvidia.com/devices: "true"`
  currently means "give me every device node". It comes to mean "give me every
  device node **unless** the device plugin already gave me some". A pod that
  wants every device and wants to consume capacity can request
  `nvidia.com/gpu: <count>`; it then receives every node through the allocation
  path and the visible set is unfiltered, which is the same result.
- **Capacity is advertised on the control-plane in single-cluster kind.** The
  chart has no default `nodeSelector`, so `nvml-mock` runs on the control-plane,
  labels it `nvidia.com/gpu.present=true`, and the device plugin follows. The
  `node-role.kubernetes.io/control-plane:NoSchedule` taint still keeps
  workloads off, and the measured `FailedScheduling` message reports
  `1 node(s) had untolerated taint(s), 2 Insufficient nvidia.com/gpu`. This is
  pre-existing and out of scope, but the e2e assertions must count capacity on
  workers, not cluster-wide.
- The NRI plugin already excludes the release namespace and `kube-system`, so it
  never injects into the device plugin's own pod.

### Risks and Mitigations

| Risk | Mitigation |
| --- | --- |
| **`gpu-validator-mock` regresses.** [`validator-mock.yaml`](../../../tests/e2e/go/assets/validator-mock.yaml) requests `nvidia.com/gpu: "1"` and runs the real `cuda-vectorAdd` sample. Today it sees all GPUs. With device specs on, it will be allocated one GPU and the visible-index mapping will remap index 0 onto the allocated device. | This must be **run**, not reasoned about, before the change merges. If `cuda-vectorAdd` fails when allocated a non-zero device, that is a genuine bug in the visible-index mapping and it belongs to this MEP's implementation, not to a follow-up. |
| Other scenarios that deploy the device plugin (`standalone`, `multi-node`, `gpu-operator`) change behaviour. | The only other GPU-requesting workloads are `gpu-scheduling-test` (busybox, echoes its hostname) and the nv-sentinel sample (`pause` image). Neither observes GPUs. Re-run the full matrix regardless. |
| The device plugin's default strategy changes in a future release and the flag becomes wrong. | The e2e scenario asserts the *outcome* (one GPU visible), not the flag. A strategy change that preserves the outcome passes; one that breaks it fails loudly. |
| Suppression hides a real staging failure: a container gets no device nodes because the device plugin failed, and the NRI plugin stays quiet. | Suppression triggers only when GPU device nodes are **present**. An empty device list is not a trigger, so the annotation path still injects. |
| `--pass-device-specs=true` is not honoured on a runtime that resolves devices only through CDI. | Verified on containerd 2.2.0, the version in `kindest/node:v1.35.0`. The e2e scenario pins the same node image. |

## Design Details

### Change 1: turn on device specs in the device-plugin manifests

Add one argument to both copies of the DaemonSet:

```yaml
args:
  - "--nvidia-driver-root=/var/lib/nvml-mock/driver"
  - "--driver-root-ctr-path=/var/lib/nvml-mock/driver"
  - "--device-discovery-strategy=nvml"
  - "--pass-device-specs=true"      # new
```

- [`tests/e2e/go/assets/device-plugin-mock.yaml`](../../../tests/e2e/go/assets/device-plugin-mock.yaml) — the embedded asset the Go e2e suite applies.
- [`tests/e2e/device-plugin-mock.yaml`](../../../tests/e2e/device-plugin-mock.yaml) — the copy the README tells users to `kubectl apply` from raw GitHub.

The two files are currently identical. Keep them identical.

### Change 2: suppress blanket device injection in the NRI plugin

The data needed for the decision already arrives in the `CreateContainer`
payload. This was verified with a throwaway NRI plugin run alongside the real
one on a kind node. For a pod requesting `nvidia.com/gpu: 1`, containerd
delivered:

```json
{
  "pod": "probe-gpu", "container": "app",
  "devices": [ {"path": "/dev/nvidia0", "type": "c", "major": 195, …} ],
  "cgroupDevs": [ {"access":"rwm"}, {"allow":true,"type":"c","major":{"value":195},…} ],
  "cdiDevices": null
}
```

So `Container.Linux.Devices` is populated with the device plugin's allocation,
and `Container.CDIDevices` sits on the same message for the CDI case. No new API
surface, no kubelet coupling, and no watch is required.

The implementation shape:

1. Add `Devices []Device` and `CDIDevices []string` to `nvmlmock.Container` in
   `pkg/nri/nvmlmock/adjust.go`, and populate them in `fromNRI` in
   `cmd/nvml-mock-nri/main.go` from `container.GetLinux().GetDevices()` and
   `container.GetCDIDevices()`.
2. In `Adjust`, gate the existing device block. It currently reads:

   ```go
   if strings.EqualFold(container.PodAnnotations[cfg.DeviceAnnotation], "true") {
       devices, err := discoverDevices(cfg.DeviceHostPath)
       …
   }
   ```

   Add a predicate — `alreadyHasGPUDevices(container)` — that reports true when
   any incoming device path matches `/dev/nvidia*`, or any incoming CDI device
   has the `nvidia.com/` vendor prefix. When it reports true, skip the device
   block and log once at info level, naming the container and the device the
   plugin found. Keep the overlay mount and the environment unchanged.
3. Do **not** put this behind a flag. It is a correctness fix, and a flag that
   restores a known-wrong behaviour is dead weight. If a future case needs the
   old behaviour, it arrives with its own justification.

The precedence rule is **the resource request wins**. A pod carrying both a
request and the annotation receives only its allocated device. The scheduler-
backed signal is the stronger one, and honouring it is the point of the MEP.
The alternative — annotation wins — is discussed in
[Alternatives](#alternatives).

`shouldSkip` is not the right place for this. That function decides whether to
touch the container at all; here we still inject the overlay and environment,
and suppress only the device list.

### Change 3: e2e scenario

A new scenario, or a new `Context` inside the existing NRI scenario, that for
the first time deploys **both** the `nvml-mock` chart with `nri.enabled=true`
**and** the device-plugin DaemonSet on the same nodes. Today
`scenario_nri_test.go` never deploys the device plugin, and
`scenario_multi_node_test.go` never enables NRI, so this interaction has no
coverage at all.

Assertions, mapped to the goals:

1. A pod requesting `nvidia.com/gpu: 1`, with an otherwise plain spec, becomes
   `Ready`; `nvidia-smi -L` reports exactly one GPU; the reported UUID equals
   the pod's `NVIDIA_VISIBLE_DEVICES`.
2. Two such pods on one node report **different** UUIDs.
3. A pod with the annotation and no request reports all `gpu.count` GPUs.
4. A plain pod reports all `gpu.count` GPUs.
5. Over-subscribing the workers leaves the surplus pods `Pending` with
   `Insufficient nvidia.com/gpu`.

Assertion 2 is the one that must be mutation-checked: revert change 2 and it has
to go red. If it stays green with the blanket injection restored, it is theater.

### What MEP-0002 permits and forbids for #436

Issue #436 will teach `adjust.go` to inject CDI device references instead of raw
device nodes. Both issues modify the same file, and #436 is briefed from this
document. The contract:

**#436 may:**

- Replace the *raw device node* injection with CDI device references on the
  annotation-gated path, that is, the no-request path from goal 2.
- Generate CDI specs during overlay staging, in `setup.sh` or the NRI pod.
- Keep `nvml-mock.nvidia.com/devices: "true"` as the trigger.

**#436 must not:**

- **Bypass the suppression rule.** Whatever mechanism #436 uses, it does not
  inject into a container that already carries GPU device nodes or
  `nvidia.com/…` CDI devices. The rule is about *who already served the
  container*, not about which mechanism serves it.
- **Turn on `--device-list-strategy=cdi-annotations` or `cdi-cri` on the device
  plugin.** Two CDI sources targeting one container is a separate decision. If
  #436 wants it, it comes back as an amendment to this MEP with its own
  evidence. The invariant to preserve is: **exactly one component emits CDI
  device references for a given container.**
- **Remove the raw-device fallback.** Raw injection stays the default and CDI is
  the opt-in path.

  > **Amended by #436.** This clause originally rested on the claim that the
  > stock `kindest/node` has "no container toolkit and no CDI configuration at
  > all", and therefore that CDI could not be verified until the NRI legs moved
  > to a toolkit-bearing image. **The second half of that claim is wrong.** The
  > toolkit is indeed absent, but containerd 2.2.0 — the version in
  > `kindest/node:v1.35.0` — ships `enable_cdi = true` with
  > `cdi_spec_dirs = ['/etc/cdi', '/var/run/cdi']` by default, and an NRI
  > plugin's `ContainerAdjustment.CDIDevices` resolves against it. Measured on
  > stock kind with a throwaway NRI plugin: a spec staged in `/var/run/cdi`
  > applied both its `env` and its `deviceNodes` to the container, with no
  > toolkit binary present. CDI injection therefore needs no new node image, and
  > #436 verifies it on the existing `e2e-nri` leg.
  >
  > The directive survives the correction, on grounds the correction does not
  > touch: the raw path is the only one that works where the runtime's CDI
  > support is absent or disabled (containerd 1.x gates it behind `enable_cdi`),
  > and it is what every current deployment uses. Removing it would migrate the
  > majority onto a path they have not exercised. Retiring it is a separate
  > decision needing its own evidence, not a consequence of this correction.
  >
  > `deployments/kind-nvidia-cdi/` remains built by no *runtime* CI job. #436
  > added a paths-filtered build guard so an edit cannot break it silently; that
  > guard proves the image builds, not that it works.
- **Break the `detectVisibleDevices` oracle.** Whatever CDI spec #436 generates
  must land the correct *subset* of `/dev/nvidiaN` inside the container. A CDI
  spec that stages all `N` devices reintroduces case F and silently un-isolates
  every pod. This is the single most important constraint, because it fails
  green: the container starts, `nvidia-smi` works, and only the *count* is
  wrong.

Sequencing: #440 lands first. #436 rebases onto it.

### Test plan

- Unit tests in `pkg/nri/nvmlmock/adjust_test.go` for `alreadyHasGPUDevices`:
  no devices; a non-NVIDIA device; one `/dev/nvidia0`; an `nvidia.com/gpu=0` CDI
  device; and an annotation-plus-devices case asserting the device list comes
  back empty while mounts and environment stay intact.
- The e2e scenario above, with assertion 2 mutation-checked.
- A full re-run of the e2e matrix, with the `gpu-validator-mock` result read
  directly rather than inferred from a green badge.

## Drawbacks

- It changes the meaning of an existing user-visible annotation. The change is
  narrow and the old behaviour remains reachable, but it is a contract change
  and it needs a release note.
- It couples two components that are currently independent. The coupling is
  one-way — the NRI plugin defers to the device plugin, never the reverse — but
  a future device-plugin release that stops populating `Linux.Devices` would
  silently restore the old behaviour. The e2e assertion is what catches that.
- `--pass-device-specs=true` moves the e2e further from the *default* upstream
  configuration. We gain fidelity of outcome and lose fidelity of configuration.
  On a real node the default configuration works because the container runtime
  hook does the delivery; a kind node has no such hook, so matching the default
  flag would mean matching a configuration that cannot work here.

## Alternatives

**1. Keep them separate and document the split as intentional.** Epic #441 says
scheduling advertisement is out of scope for the plugin, which assigns #440
ownership of the composition; it does not require that the composition do
anything. This option is real and it is cheap: write down that `nvidia.com/gpu`
is advertisement-only in Mokka and that every container sees every mock GPU.

Rejected, for one reason that is not "it is untidy". The repository already
contains a working, unit-tested device-visibility filter whose stated purpose is
to mimic cgroup-based GPU isolation, and the shipped manifests never trigger it.
Documenting the split means documenting that a shipped feature is unreachable in
the shipped configuration. It also makes a class of test unwritable — story 3
above — and that class is precisely what a GPU test harness exists to support.
The status quo is not broken in the sense of crashing; it is broken in the sense
of passing when it should fail.

**2. Teach the mock to honour `NVIDIA_VISIBLE_DEVICES`.** The device plugin
already sets it, so the mock could parse the UUID list and filter on that.

Rejected. It duplicates a filter the engine already has, keyed on a different
input, so the two could disagree. It also encodes the *envvar* strategy into the
mock, which is one of four device-list strategies the real plugin supports; a
user who switches to `cdi-cri` would silently lose the filter. Device-node
presence is strategy-independent and it is what the real cgroup mechanism keys
on, so it is the more faithful oracle.

**3. Teach the NRI plugin to read the pod's resource request and inject that
many devices.** The plugin could count `nvidia.com/gpu` and stage a matching
subset.

Rejected on layering. The NRI `CreateContainer` payload carries the container's
Linux resources, not the device plugin's allocation decision. The plugin would
have to *re-implement* allocation, pick which specific GPU indices to hand out,
and stay consistent with the plugin that actually owns the accounting. Two
independent allocators for one resource is a race, and the outcome would be
wrong exactly when it matters — two pods on one node could receive overlapping
device sets. Reading `Linux.Devices` asks the question the right way round.

**4. Compose at the CDI layer now.** Have overlay staging generate CDI specs and
switch the device plugin to `--device-list-strategy=cdi-cri`, so the runtime
resolves everything and the NRI plugin stays out of the device business.

Rejected as premature, not as wrong — it is likely the end state. It requires a
CDI-capable node image on the NRI legs, and today `e2e-nri` runs on the stock
`kindest/node` with no toolkit at all; the two toolkit-bearing images the
repository does carry are used by one other job and by nothing, respectively. It
also inverts the dependency: #440 would wait on #436 rather than gate it. The
constraints above keep the door open.

**5. Build a mock device plugin.** Write our own plugin that advertises capacity
and cooperates with the NRI plugin by construction.

Rejected. The genuine `k8s-device-plugin:v0.18.2` already discovers through the
mock NVML and allocates correctly — cases E and the scheduling run prove it,
including disjoint UUID assignment within a node and correct `Pending` behaviour
on over-subscription. Replacing it with our own would delete the most valuable
property of the setup: that the component under test is the one users run.
