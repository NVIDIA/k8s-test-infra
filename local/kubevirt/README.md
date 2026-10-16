# KubeVirt GPU VM

The `--kubevirt` Tilt scenario runs a [KubeVirt](https://kubevirt.io) virtual machine (VM) that requests a mock GPU the way a pod does, and whose guest operating system sees exactly that GPU:

```text
$ nvidia-smi -L                     # inside the guest
GPU 0: NVIDIA GB300 NVL (UUID: GPU-b300b300-0000-0000-0000-000000000002)
$ nvidia-smi -q | grep 'Virtualization Mode'
        Virtualization Mode               : Pass-Through
```

The VM is stock KubeVirt: no hook sidecar, no custom image, no Mokka-specific API. The nvml-mock NRI plugin does the work on the node.

It runs on an amd64 Linux host. It does not run on an Apple-silicon Mac; [Why it does not run on a Mac](#why-it-does-not-run-on-a-mac) explains why.

## Run it

```bash
make cluster-create
tilt up -- --kubevirt            # add --gpu-profile gb300 for another profile
```

`--kubevirt` implies `--gpu-operator`, because only the GPU Operator's device plugin advertises `nvidia.com/gpu`. It cannot be combined with `--slinky`: both define the `gpu-operator-ready` resource.

Tilt installs KubeVirt v1.9.0 with emulation (`useEmulation: true`), so the Kind nodes need no `/dev/kvm`, and the alpha `HostDisk` feature gate, then starts `gpu-vm` in the `default` namespace. Under emulation the guest takes two to four minutes from launcher start to its report.

`tilt ci` going green only means the objects exist; it stays green while a VM crash-loops. The real check is the manual **kubevirt-smoke** trigger under the `kubevirt-tests` label, or the script directly:

```bash
bash local/kubevirt/scenarios/guest-smoke.sh gpu-vm
```

It waits for the guest report on the serial console, then asserts that:

- the launcher pod runs on a Mokka GPU node and its `compute` container holds one `nvidia.com/gpu`;
- `compute`, which runs libvirt and QEMU, carries no `LD_PRELOAD` or `MOCK_*` environment and no mount of the overlay at `/opt/nvml-mock`;
- the guest lists exactly one GPU, with the name and UUID the host reports for the allocated minor number, in Pass-Through mode;
- a temperature pinned with `nvml-mock-ctl` on the node reaches the running guest, which logs the SHA-256 of the override document it applied.

To explore the guest yourself, open its serial console with [`virtctl`](https://kubevirt.io/user-guide/user_workloads/virtctl_client_tool/) and log in as `debian` with password `mokka`. Exit with `Ctrl+]`.

```bash
virtctl console gpu-vm
```

The guest has no network, so `virtctl ssh` does not work.

## What a VM declares

[`gpu-vm.yaml`](gpu-vm.yaml) is the whole contract. A VM needs three things:

1. `nvidia.com/gpu: N` under `spec.domain.resources.limits`, the number of GPUs the guest sees. KubeVirt's `spec.domain.devices.gpus` does not work with mock GPUs: it attaches the allocation through VFIO PCI passthrough, which needs a real PCI device.
2. A `hostDisk` volume at `/run/nvml-mock-guest/bundle.tar` with `type: Disk`, attached as a read-only disk with a `serial`, so the guest finds it at `/dev/disk/by-id/virtio-<serial>`.
3. A guest that extracts that disk at `/`, runs `ldconfig` and starts the bundled `nvml-mock-guest` unit, here from cloud-init.

Every VM uses the same path. `/run/nvml-mock-guest` is an empty placeholder directory on the node; the NRI plugin replaces it, in each launcher, with that launcher's own bundle.

## How it works

```mermaid
sequenceDiagram
    participant DP as GPU Operator device plugin
    participant NRI as nvml-mock NRI plugin
    participant VH as virt-handler
    participant L as virt-launcher compute<br/>(libvirt + QEMU)
    participant G as Guest

    DP->>L: allocates /dev/nvidiaN
    NRI->>NRI: writes bundle.tar for this allocation<br/>under /run/nvml-mock-guests/<container-id>
    NRI->>L: binds that directory read-only over the<br/>placeholder mount; no overlay, no LD_PRELOAD
    VH->>L: hostDisk ownership check (already qemu's)
    L->>G: bundle.tar as a read-only virtio disk
    G->>G: tar -xf at /, ldconfig, start nvml-mock-guest, nvidia-smi
    NRI->>G: node override changes, through the disk's live area
```

The bundle holds the node's GPU profile with every GPU set to passthrough mode, the allocated minor numbers, the mock NVML library and `nvidia-smi`, and the `nvml-mock-guest` service that mirrors the node's runtime overrides. [KubeVirt launchers](../../docs/components/nri-plugin.md#kubevirt-launchers) is the canonical description of what the plugin does and when it leaves a launcher alone.

To watch a runtime change reach the guest, pin the allocated GPU's temperature on the node and query the guest from its console:

```bash
kubectl -n mokka exec <nvml-mock pod on the VM's node> -- nvml-mock-ctl temp --gpu <GPU UUID> 87
# in the guest, within a few seconds:
nvidia-smi --query-gpu=temperature.gpu --format=csv
```

An Xid injected with `nvml-mock-ctl fail --xid` reaches the guest the same way, as an NVML event.

## Why it does not run on a Mac

No KubeVirt VM boots in this setup on an Apple-silicon Mac, with or without a GPU. Two facts combine:

1. **No KVM.** Docker Desktop's Linux VM exposes no `/dev/kvm`, so the Kind nodes cannot use hardware virtualization. KubeVirt falls back to software emulation: QEMU's TCG, which translates every guest instruction.
2. **No usable CPU model on arm64.** On Apple silicon the Kind nodes are arm64. There, KubeVirt always sets the VM's CPU to `host-passthrough` and its admission webhook rejects any other model. `host-passthrough` means "give the guest the host's physical CPU", which only KVM can do. Under emulation libvirt refuses to start the domain.

On amd64 the second fact does not apply: KubeVirt picks a named CPU model that emulation can run (`EPYC` on an AWS `m7i.xlarge`), so the same stock VM boots without KVM.

Working around it means changing the libvirt domain after KubeVirt generates it. An earlier version of this scenario did so with a hook sidecar that rewrote the CPU to `cortex-a57`. The guest then booted in about four minutes, but every VM needed the `Sidecar` feature gate and a hook annotation, and the point of this scenario is that a stock VM works. The hook was removed, and `tilt up -- --kubevirt` now fails on an arm64 host without `/dev/kvm` instead of starting a VM that crash-loops.

To run the scenario from a Mac, run it on an amd64 Linux machine and point `kubectl` at it. An AWS `m7i.xlarge` (4 vCPU, 16 GiB, Ubuntu 24.04) runs it with the GB300 profile. An arm64 host that exposes `/dev/kvm` to the Kind nodes, such as an arm64 bare-metal instance, passes the Tilt check, but that path is untested.

## Iterating

The plugin writes a bundle when a launcher container is created and afterwards updates only its runtime overrides. A change that should alter the rest of the bundle, such as a new `--gpu-profile` or a rebuilt plugin, reaches the guest only after:

```bash
kubectl delete vmi gpu-vm      # runStrategy: Always starts a new one
```

When the VM never starts, see [A KubeVirt VM with a GPU never starts](../../docs/troubleshooting.md#a-kubevirt-vm-with-a-gpu-never-starts).

## Tear down

`tilt down` deletes the `KubeVirt` resource, so the operator removes KubeVirt's components, but it keeps the operator itself: deleted together, the operator could go before it clears the resource's finalizer. Remove the operator with:

```bash
kubectl delete -f https://github.com/kubevirt/kubevirt/releases/download/v1.9.0/kubevirt-operator.yaml
```

## Files

| File | Purpose |
|---|---|
| [`kubevirt.tiltfile`](kubevirt.tiltfile) | Installs the pinned KubeVirt operator and CR, waits for KubeVirt and the GPU Operator, starts `gpu-vm` and defines `kubevirt-smoke` |
| [`kubevirt-cr.yaml`](kubevirt-cr.yaml) | The `KubeVirt` resource: emulation and the `HostDisk` feature gate |
| [`gpu-vm.yaml`](gpu-vm.yaml) | The VM: one GPU, the bundle disk, and cloud-init that prints what the guest sees between `MOKKA-GUEST` markers |
| [`scenarios/guest-smoke.sh`](scenarios/guest-smoke.sh) | The smoke test above |
