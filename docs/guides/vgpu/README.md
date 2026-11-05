# vGPU Host and Guest

NVIDIA vGPU splits a physical GPU into virtual GPUs that a hypervisor hands to
virtual machines. Mokka simulates either side of that split on a CPU node: a
**host**, whose GPUs run the vGPU manager and a set of vGPU instances, or a
**guest**, whose GPUs are the licensed vGPUs a VM sees. `nvidia-smi` reads both
as it would on real hardware. No hypervisor or VM is involved.

A node plays one side at a time, chosen by `gpu.vgpu.mode`. The two values
files below are complete for an A100 node; every field they use, and the rules
a block must follow, are in the [vGPU reference](../../vgpu.md).

## Simulate a vGPU host

Save this as `host-values.yaml`. It declares one compute type,
`NVIDIA A100-4C`, and runs two instances of it on every GPU. An install always
declares its own types; the [reference](../../vgpu.md#host) explains why.

```yaml title="host-values.yaml"
gpu:
  profile: a100
  vgpu:
    mode: host
    hostVgpuMode: sriov
    types:
      - id: 474
        name: "NVIDIA A100-4C"
        framebuffer_mib: 4096
        max_instances: 10
        license: "NVIDIA-Virtual-Compute-Server,9.0"
    instances:
      - type: "NVIDIA A100-4C"
        count: 2
```

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
|      1001        NVIDIA A100-4C | f641...                      |   N/A      |
|      1002        NVIDIA A100-4C | 2385...                      |   N/A      |
+---------------------------------+------------------------------+------------+
|  1   NVIDIA A100-SXM4-40GB      | 00000000:0F:00.0             |  50%       |
|      2001        NVIDIA A100-4C | ff61...                      |   N/A      |
|      2002        NVIDIA A100-4C | 10bc...                      |   N/A      |
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
        VM UUID                           : f641e950-f095-42e5-aaed-85f4222ef023
        VM Name                           : 
        vGPU Name                         : NVIDIA A100-4C
        vGPU Type                         : 474
        vGPU UUID                         : 0f7566ae-0643-78b0-b8ab-4d9865eb8ff4
        Guest Driver Version              : N/A
        License Status                    : Licensed (Expiry: Permanent)
        GPU Instance ID                   : N/A
        Placement ID                      : N/A
        Accounting Mode                   : Disabled
        ECC Mode                          : Enabled
```

The UUIDs are derived from the GPU's own, so they read the same on every run.
Some rows print `N/A` because the mock does not implement the call, such as
`Placement ID`; the [reference](../../vgpu.md#host-behaviour) lists them.
`GPU Instance ID` prints `N/A` for another reason: the instance is time-sliced,
not backed by a MIG GPU instance.

`nvidia-smi vgpu -s` lists the supported types and `nvidia-smi vgpu -c` the
types that could still be created. With two of ten slots in use, both print
`NVIDIA A100-4C`.

## Simulate a vGPU guest

Save this as `guest-values.yaml`. It makes every GPU a licensed vGPU running
on host driver `550.163.01`, branch `r550_00`.

```yaml title="guest-values.yaml"
gpu:
  profile: a100
  vgpu:
    mode: guest
    guest:
      host_driver_version: "550.163.01"
      host_driver_branch: "r550_00"
      license:
        product: "NVIDIA Virtual Compute Server"
        state: licensed
```

It replaces the host values, so the same release is upgraded:

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

The host driver version and branch do not appear in `nvidia-smi`; they reach
GPU Feature Discovery through the PCI capability the
[reference](../../vgpu.md#guest) describes.

## Troubleshooting

**`helm install` fails with `gpu.vgpu.mode is host but gpu.vgpu.types declares
no vGPU types`.** A host needs at least one type, so the chart refuses to render
without one and nothing is deployed. Add `gpu.vgpu.types`, as
`host-values.yaml` [above](#simulate-a-vgpu-host) does.

**`helm install --wait` times out, and the `nvml-mock` pods stay in `Init:0/1`
and restart.** The node agent refused the `gpu.vgpu` block and never becomes
ready, so its startup probe fails; `--wait` reports that only as a timeout.
The node agent's log names the field:

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
| Every `gpu.vgpu` chart value | [vGPU: Under the chart](../../vgpu.md#under-the-chart) |
| Every profile field, the identity rules and what is not simulated | [vGPU](../../vgpu.md) |
| The rest of a profile's `virtualization` block | [Configuration](../../configuration.md#virtualization) |
