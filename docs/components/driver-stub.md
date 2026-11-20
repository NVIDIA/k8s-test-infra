# Driver Stand-in

An image the GPU Operator's driver DaemonSet runs on a Mokka node in place of
NVIDIA's driver. It installs nothing, but lets the operator's driver lifecycle run:
install and startup probe, upgrades, and the stock `k8s-driver-manager` around
each change. It needs the NRI plugin's
[`nri.driverManager`](../helm-chart.md#gpu-operator-driver-manager).

## What it does

After `STUB_INSTALL_SECONDS` (default 0), it:

- binds Mokka's driver root on `/run/nvidia/driver` and Mokka's module list over the node's
  `/proc/modules`;
- writes `nvidia`, `nvidia_uvm` and `nvidia_modeset` into `/sys/module`, with `refcnt`,
  `holders/` and `initstate` as the kernel publishes them; the NRI plugin binds that directory
  from the node (without it, the stand-in exits) and shows it the GPU operands too;
- removes its binds when it stops.

The operator's startup probe is unchanged; its 60 s initial delay floors every
install.

## Using it

```bash
make driver-stub-image DRIVER_STUB_VERSIONS="580.0.1 580.0.2" DRIVER_STUB_OS=ubuntu24.04
make test-driver-stub
```

Images are built for the build host's architecture; set `DRIVER_STUB_ARCH` (for example
`amd64`) when the nodes differ.

Load or push the images where the nodes pull from, install Mokka with
`nri.driverManager.enabled=true`, and the GPU Operator with:

```yaml
driver:
  enabled: true
  repository: <registry>
  image: mokka-driver-stub
  version: "580.0.1"            # upgrade by changing it to another built version
daemonsets:
  annotations:
    required-plugins.noderesource.dev/container.k8s-driver-manager: '["<nri.pluginName>"]'
```

The stand-in reads Mokka's files under `/var/lib/nvml-mock`. For another Mokka root, set
`STUB_DRIVER_ROOT` and `STUB_PROC_MODULES` in `driver.env`.

## Limits

- No process holds the driver: an unload after the holders always succeeds.
- `/proc/modules` goes when the stand-in stops, not when the driver manager unloads.
- No same-configuration skip: every driver restart unloads and reloads.
- The driver version does not reach NVML: an upgrade changes the pod's image only.
- Only `nvidia`, `nvidia_uvm` and `nvidia_modeset` are loaded: the GPU operands' `/sys/module`
  has none of Mokka's other modules (`nvidia_peermem`, `nvidia_fs`, `gdrdrv`, `mlx5_core`), so
  an operand that checks one of them (GPUDirect RDMA, GDS, GDRCopy) finds it missing, while a
  container given Mokka's static tree through CDI has it.
