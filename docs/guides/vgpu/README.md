# vGPU Host and Guest

NVIDIA vGPU splits a physical GPU into virtual GPUs that a hypervisor hands to
virtual machines. Mokka simulates either side of that split on a CPU node: a
**host**, whose GPUs run the vGPU manager and a set of vGPU instances, or a
**guest**, whose GPUs are the licensed vGPUs a VM sees. `nvidia-smi` reads both
as it would on real hardware. No hypervisor or VM is involved.

A node plays one side at a time, chosen by `gpu.vgpu.mode`. The two values
files beside this guide are complete for an A100 node; every field they use,
and the rules a block must follow, are in the [vGPU reference](../../vgpu.md).

```bash
export MOKKA_ASSET_BASE=https://raw.githubusercontent.com/NVIDIA/k8s-test-infra/main/docs/guides/vgpu
curl -fsSLO "$MOKKA_ASSET_BASE/host-values.yaml"
curl -fsSLO "$MOKKA_ASSET_BASE/guest-values.yaml"
```

## Simulate a vGPU host

[`host-values.yaml`](host-values.yaml) declares one compute type,
`NVIDIA A100-4C`, and runs two instances of it on every GPU. No profile ships
vGPU types, because which types a board offers depends on the vGPU software
release.

```bash
helm install nvml-mock oci://ghcr.io/nvidia/k8s-test-infra/chart/nvml-mock \
  --namespace mokka --create-namespace \
  -f host-values.yaml \
  --wait

POD=$(kubectl -n mokka get pod -l app.kubernetes.io/name=nvml-mock \
  -o jsonpath='{.items[0].metadata.name}')
kubectl -n mokka exec "$POD" -- nvidia-smi vgpu
```

Each GPU lists its two instances (output trimmed to the first two of eight
GPUs):

```text
+-----------------------------------------------------------------------------+
| NVIDIA-SMI 580.65.06              Driver Version: 550.163.01                |
|---------------------------------+------------------------------+------------+
| GPU  Name                       | Bus-Id                       | GPU-Util   |
|      vGPU ID     Name           | VM ID     VM Name            | vGPU-Util  |
|=================================+==============================+============|
|  0   NVIDIA A100-SXM4-40GB      | 00000000:07:00.0             |  50%       |
|      1001        NVIDIA A100-4C | c57a...                      |   N/A      |
|      1002        NVIDIA A100-4C | c57a...                      |   N/A      |
+---------------------------------+------------------------------+------------+
|  1   NVIDIA A100-SXM4-40GB      | 00000000:0F:00.0             |  50%       |
|      2001        NVIDIA A100-4C | c57a...                      |   N/A      |
|      2002        NVIDIA A100-4C | c57a...                      |   N/A      |
+---------------------------------+------------------------------+------------+
```

The vGPU ID encodes its GPU: GPU 0's instances are `1001` and `1002`, GPU 1's
are `2001` and `2002`. The [reference](../../vgpu.md#host-identities) gives the
rule.

`nvidia-smi vgpu -q -i 0` details one GPU's instances (trimmed to the first
instance's identity and license rows):

```text
GPU 00000000:07:00.0
    Active vGPUs                          : 2
    vGPU ID                               : 1001
        VM UUID                           : c57aebaf-fa21-fb20-79d2-7b4cbffc1109
        VM Name                           : 
        vGPU Name                         : NVIDIA A100-4C
        vGPU Type                         : 474
        vGPU UUID                         : 2156c6d4-9b85-ad88-7cac-fd944b7d18da
        Guest Driver Version              : 550.163.01
        License Status                    : Licensed (Expiry: Permanent)
        GPU Instance ID                   : N/A
        Placement ID                      : N/A
        Accounting Mode                   : Disabled
        ECC Mode                          : Enabled
```

The UUIDs are derived from the GPU's own, so they read the same on every run.
Rows that print `N/A` are calls the mock does not implement; the
[reference](../../vgpu.md#host-behaviour) lists them.

`nvidia-smi vgpu -s` lists the supported types and `nvidia-smi vgpu -c` the
types that could still be created. With two of ten slots in use, both print
`NVIDIA A100-4C`.

## Simulate a vGPU guest

[`guest-values.yaml`](guest-values.yaml) makes every GPU a licensed vGPU
running on host driver `550.163.01`, branch `r550_00`. It replaces the host
values, so the same release is upgraded:

```bash
helm upgrade --install nvml-mock oci://ghcr.io/nvidia/k8s-test-infra/chart/nvml-mock \
  --namespace mokka --create-namespace \
  -f guest-values.yaml \
  --wait

POD=$(kubectl -n mokka get pod -l app.kubernetes.io/name=nvml-mock \
  -o jsonpath='{.items[0].metadata.name}')
kubectl -n mokka exec "$POD" -- nvidia-smi -q -i 0
```

The GPU reports itself as a vGPU and shows its license (trimmed to the
virtualization and license rows):

```text
    GPU Virtualization Mode
        Virtualization Mode               : VGPU
        Host VGPU Mode                    : N/A
        vGPU Heterogeneous Mode           : N/A
    vGPU Software Licensed Product
        Product Name                      : NVIDIA Virtual Compute Server
        License Status                    : Licensed (Expiry: Permanent)
```

The host driver version and branch do not appear in `nvidia-smi`. A guest reads
them from a capability in its GPUs' PCI config space, which the node agent
writes into the mock's PCI sysfs tree. GPU Feature Discovery (GFD) labels the
node `nvidia.com/vgpu.present`, `nvidia.com/vgpu.host-driver-version` and
`nvidia.com/vgpu.host-driver-branch` when it reads that tree; how a container
gets the tree mounted is described in
[PCI sysfs in containers](../../helm-chart.md#pci-sysfs-in-containers).

## Troubleshooting

**The `nvml-mock` pods stay in `Init:0/1` and restart after install.** The node
agent refused the `gpu.vgpu` block and never becomes ready, so its startup
probe fails. Its log names the field:

```bash
kubectl -n mokka logs "$POD" -c node-agent | grep 'state source error'
```

```text
{"level":"warn","time":"2026-10-14T07:05:29.955Z","msg":"state source error; keeping cached state","error":"/etc/nvml-mock/config.yaml: device_defaults.virtualization.vgpu.instances[0].type: \"NVIDIA A100-8C\" is not a declared vGPU type"}
```

Fix the field against the [validation rules](../../vgpu.md#validation) and run
`helm upgrade` again.

**`nvidia-smi vgpu` says `No supported devices in vGPU mode`.** The node is not
a vGPU host: `gpu.vgpu.mode` is not `host`. A guest node prints the same,
because `nvidia-smi vgpu` is a host-side view.

**`nvidia-smi pmon` fails on a host node.** Expected: `pmon` declines a GPU
running the vGPU manager, as it does on a real hypervisor.

## Related

| To read about | See |
|---|---|
| Every `gpu.vgpu` field, the identity rules and what is not simulated | [vGPU](../../vgpu.md) |
| Every chart value | [Installation](../../helm-chart.md) |
| The rest of a profile's `virtualization` block | [Configuration](../../configuration.md#virtualization) |
