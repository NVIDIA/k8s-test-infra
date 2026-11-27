# gb300-roce

One bare-metal GB300 NVL72 compute tray at a cloud provider, on an Ethernet
(RoCE) fabric, captured on 24 November 2026. It holds the NIC surfaces only.
The NIC boards are the same as in [`gb300-ib`](../gb300-ib/README.md); their
port configuration differs.

| | |
| --- | --- |
| Platform | GB300 NVL72 compute tray, 4 GPUs, aarch64 |
| NICs | Two Dual ConnectX-8 SuperNIC C8280Z mezzanine boards (`900-9X86E-00CX-ST0`), four ConnectX-8 ASICs, firmware 40.48.1102. Ethernet: each ASIC has four PFs (`.0` to `.3`), one port each, in switchdev mode with one VF on the first PF. A dual-port ConnectX-7 card is the front-end |
| RDMA devices | One per ASIC on its first PF, spanning its four PFs, with 72 ports (uplinks and representors), named `rdma_rail0` to `rdma_rail3` by cluster software; one on each VF; the front-end card's two ports |
| Host | DOCA-OFED `mlx5_core` 26.01-1.0.0, RDMA namespaces shared |
| Kubernetes | NVIDIA Network Operator with the SR-IOV device plugin; GPU Operator |

`node-a/` was taken read-only, creating no pod:

```bash
mokka-capture collect -exec-in network-operator/network-operator-sriov-device-plugin network <node>
mokka-capture collect driver <node>
```

The first runs in the SR-IOV device plugin's pod, which has BusyBox only; the
second in the GPU Operator's driver container. The node was in service.
`nvidia-smi topo -m` lists the front-end cards and the VFs' RDMA devices, but
not the devices that span each ASIC's PFs.

## What is not in this capture

- **Tool views:** no `lspci`, `ibstat`, `ibv_devinfo`, `ip`, `rdma`,
  `devlink`, `ethtool`, `mlxconfig`, `flint` or `mlxlink`, so the firmware's
  port configuration is not recorded; their files are left out. Everything
  else comes from sysfs, the vital product data and `nvidia-smi topo -m`.
- **Everything but the NICs**, the fabric side and most counter values, as in
  [`gb300-ib`](../gb300-ib/README.md).
