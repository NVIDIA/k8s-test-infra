# Kata Containers

nvml-mock works under the Kata Containers runtime class (plain Kata,
`kata-qemu`): workload pods run inside a lightweight VM, and the mock GPU
stack still reaches them. This page covers what works, what differs from
runc, and the caveats. Confidential Containers (CoCo) support — where no
host filesystem sharing exists — is phase 2 and ships as a guest-payload
artifact (see the roadmap note at the end).

The working, always-green reference for everything below is the `e2e-kata`
job in [`.github/workflows/nvml-mock-e2e.yaml`](../../.github/workflows/nvml-mock-e2e.yaml),
its kind config [`tests/e2e/kind-kata-config.yaml`](../../tests/e2e/kind-kata-config.yaml),
and the device-plugin manifest
[`tests/e2e/device-plugin-kata.yaml`](../../tests/e2e/device-plugin-kata.yaml).
When in doubt, copy from there — it is CI-verified against kata-deploy 3.32.0.

## How it works

The standard nvml-mock DaemonSet is unchanged: it lays the mock libraries,
`nvidia-smi`, config, and inert `/dev/nvidia*` char nodes under
`/var/lib/nvml-mock/driver` on the node. What differs is CDI injection. With
the device plugin in `cdi-cri` mode (required — see Requirements), the plugin
**generates its own CDI spec**, `/var/run/cdi/k8s.device-plugin.nvidia.com-gpu.json`,
and that spec — not `setup.sh`'s `/var/run/cdi/nvidia.yaml` — wins injection:
both declare kind `nvidia.com/gpu`, and containerd applies the plugin's. The
edits it carries split under the kata handler:

| CDI edit | runc | kata-qemu |
|---|---|---|
| Library bind mount (versioned lib only, e.g. `libnvidia-ml.so.550.163.01`) | bind mount | virtiofs share into the guest |
| `/dev/nvidia*` device nodes | bind-mounted host nodes | mknod'd inside the guest by kata-agent (mode 000, root:root) |
| `update-ldcache` createContainer hook (creates the `.so.1` soname link) | runs in the container | runs host-side — never in the guest |

Two consequences drive everything the workload pod has to do (both surfaced
by CI, not by the first draft of this doc):

- **The `.so.1` soname link is missing in the guest.** The plugin spec mounts
  only the versioned library; the soname link that consumers `dlopen` is
  normally created by the `update-ldcache` hook, which under kata runs
  host-side and never touches the guest rootfs. Consumers inside the guest
  must create it themselves —
  `ln -sf /usr/lib64/libnvidia-ml.so.*.*.* /usr/lib64/libnvidia-ml.so.1` —
  or `dlopen` the versioned name directly.
- **No config is injected.** The plugin spec carries no mock config mount and
  no `MOCK_NVML_CONFIG` env. Deliver the profile config yourself with an
  explicit pod `hostPath` volume for
  `/var/lib/nvml-mock/driver/config/config.yaml`, mounted at
  `/etc/nvml-mock/config.yaml`, plus `MOCK_NVML_CONFIG=/etc/nvml-mock/config.yaml`.
  `hostPath` file volumes reach the guest via virtiofs, the same path the
  library mount takes.

The mock's device nodes are inert — the library never issues ioctls, and it
never opens the nodes — so kata-agent's guest-local mknod (mode 000,
root:root) is a faithful substitute for device passthrough.

## Requirements

- Nodes with KVM (`/dev/kvm`) and [kata-deploy](https://github.com/kata-containers/kata-containers/tree/main/tools/packaging/kata-deploy)
  (validated with the Helm chart, version 3.32.0):

  ```sh
  helm install kata-deploy \
    oci://ghcr.io/kata-containers/kata-deploy-charts/kata-deploy \
    --version 3.32.0 -n kube-system \
    --set shims.qemu.enabled=true
  ```

  > Do **not** add a `--set defaultShim=...` flag. Chart 3.32.0 models
  > `defaultShim` as a per-arch map that already defaults to `qemu`; a scalar
  > `--set defaultShim=kata-qemu` overwrites the map with a string and breaks
  > template rendering. Enabling the qemu shim (above) is all that is needed.

- **The `vhost_vsock` kernel module.** Kata's shim reaches the guest agent
  over `AF_VSOCK`. The module is present on GitHub runners but not
  auto-loaded, and without it sandbox creation times out connecting to the
  vsock CID. Run `modprobe vhost_vsock` on the host and confirm
  `/dev/vhost-vsock` is visible inside the node container (alongside
  `/dev/kvm`).

- **A node `/dev/shm` at least the size of the guest RAM.** QEMU backs the
  guest's memory with a `share=on` file on the node's `/dev/shm` (required by
  virtiofsd). kind node containers inherit Docker's 64M default, which is far
  too small — the guest hangs before its agent starts. The `e2e-kata` lane
  remounts it to 8G (`mount -o remount,size=8G /dev/shm`); tmpfs is virtual,
  so the size costs nothing until used.

- CDI enabled in containerd (`enable_cdi = true`; default in containerd 2.x).

- **Device plugin in `cdi-cri` mode.** The default `envvar` strategy depends
  on the NVIDIA container runtime wrapper, which the kata handler bypasses —
  allocations would silently inject nothing. See
  [`tests/e2e/device-plugin-kata.yaml`](../../tests/e2e/device-plugin-kata.yaml)
  for a working configuration (`--device-list-strategy=cdi-cri`,
  `--device-id-strategy=index`).

## Running a workload

The pod must carry `runtimeClassName: kata-qemu`, deliver the config via a
`hostPath` volume, and create the soname link before loading the library:

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: kata-gpu-workload
spec:
  runtimeClassName: kata-qemu
  restartPolicy: Never
  containers:
    - name: app
      image: ubuntu:22.04
      env:
        - name: LD_LIBRARY_PATH          # Ubuntu's loader does not search /usr/lib64
          value: /usr/lib64
        - name: MOCK_NVML_CONFIG          # plugin spec injects no config
          value: /etc/nvml-mock/config.yaml
      volumeMounts:
        - name: mock-config
          mountPath: /etc/nvml-mock/config.yaml
      command:
        - sh
        - -c
        - |
          set -e
          # The update-ldcache hook runs host-side under kata, so the .so.1
          # soname link is absent in the guest — create it from the versioned
          # library the CDI mount delivered.
          ln -sf "$(ls /usr/lib64/libnvidia-ml.so.*.*.* | head -1)" \
            /usr/lib64/libnvidia-ml.so.1
          nvidia-smi
      resources:
        limits:
          nvidia.com/gpu: 1
  volumes:
    - name: mock-config
      hostPath:
        path: /var/lib/nvml-mock/driver/config/config.yaml
        type: File
```

## Caveats

- **The `.so.1` soname link is not created in the guest.** The CDI
  `update-ldcache` hook executes on the host under kata, so no soname link is
  laid down and the guest's `ld.so.cache` is never refreshed. `nvidia-smi`
  itself is fine (its RPATH covers `/usr/lib64` and it links the versioned
  `libnvidia-ml`), but generic NVML consumers that `dlopen("libnvidia-ml.so.1")`
  need the link created first (see the prologue above) or must `dlopen` the
  versioned name. Debian/Ubuntu images additionally need
  `LD_LIBRARY_PATH=/usr/lib64` because their loader does not search it.
- **Verify you are actually in a VM.** If kata-deploy misconfigures the
  runtime, pods silently fall back to runc and everything still passes.
  Compare kernels: `uname -r` in the pod must differ from the node's.
  (On cloud runners, do not use the `hypervisor` cpuinfo flag — the node
  itself is a VM, so it shows up even under a runc fallback.)
- **Enumeration is allocation-scoped.** The engine's `detectVisibleDevices()`
  (`pkg/gpu/mocknvml/engine/engine.go`) enumerates only the GPUs whose
  `/dev/nvidiaN` node is present, and kata-agent mknod's exactly the ones the
  scheduler allocated. So a pod that requested 1 GPU sees 1 GPU in
  `nvidia-smi`, even if the profile is configured for more — the same view a
  real driver gives an allocated container. The allocated node index follows
  the scheduler (it may be `/dev/nvidia1`, not `/dev/nvidia0`); do not assume
  index 0.
- **System plane stays on runc.** The nvml-mock DaemonSet, device plugin,
  GFD, and GPU Operator components keep the default runtime class; only
  workload pods use `runtimeClassName: kata-qemu`.

## Roadmap: Confidential Containers (phase 2)

CoCo disables host filesystem sharing entirely; the mock must be baked
into the guest rootfs. The planned delivery is an `nvml-mock-guest-payload`
OCI artifact (libraries, `nvidia-smi`, config, and a `tmpfiles.d` entry
that creates the device nodes at guest boot) consumed by Kata rootfs
builds. Tracked in the project issue for Kata support.
