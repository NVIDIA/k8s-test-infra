# Driver Stand-in

An image the GPU Operator's driver DaemonSet runs on a Mokka node in place of
NVIDIA's driver. It installs nothing, but lets the operator's driver lifecycle run:
install and startup probe, upgrades, and the stock `k8s-driver-manager` around
each change. It needs the NRI plugin's
[`nri.driverManager`](../helm-chart.md#gpu-operator-driver-manager).

## What it does

After `STUB_INSTALL_SECONDS` (default 0), it:

- writes `nvidia`, `nvidia_uvm` and `nvidia_modeset` into `/sys/module`, with
  `refcnt`, `holders/` and `initstate` as the kernel publishes them; the NRI plugin
  binds that directory from the node, and without it the stand-in exits. The plugin
  binds it read-only into the GPU operands too, whose gate on
  `/sys/module/nvidia/initstate` then waits for the stand-in;
- binds Mokka's mock driver root on `/run/nvidia/driver` (the driver manager
  unmounts it before each install) and Mokka's module list over the node's
  `/proc/modules`, which the container toolkit's runtime wrappers read;
- removes both binds when it stops.

The operator's startup probe is unchanged; its 60 s initial delay floors every
install.

## Using it

```bash
make driver-stub-image DRIVER_STUB_VERSIONS="580.0.1 580.0.2" DRIVER_STUB_OS=ubuntu24.04
make test-driver-stub
```

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

## Limits

- No process holds the driver: an unload after the holders always succeeds.
- `/proc/modules` goes when the stand-in stops, not when the driver manager unloads.
- No same-configuration skip: every driver restart unloads and reloads.
- The driver version does not reach NVML: an upgrade changes the pod's image only.
