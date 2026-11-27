# gb300-ib

One compute tray of a GB300 NVL72 system on an InfiniBand fabric, captured on
24 November 2026. It holds the NIC surfaces only.

| | |
| --- | --- |
| Platform | GB300 NVL72 compute tray, 4 GPUs, aarch64 |
| NICs | Two Dual ConnectX-8 SuperNIC C8280Z mezzanine boards (`900-9X86E-00CX-ST0`), so four ConnectX-8 ASICs, one per GPU, firmware 40.47.3576. InfiniBand XDR: each ASIC has one PF and one 800 Gb/s port split into four planes (`NUM_OF_PF=1`, `NUM_OF_PLANES_P1=4` in `fw-mlxconfig.txt`). A BlueField-3 (`900-9D3B6-00CN-PA0`) is the front-end, on Ethernet |
| RDMA devices | `mlx5_0` to `mlx5_3` with an IPoIB interface each, the SMI plane devices `smi0` to `smi3`, which a cluster DaemonSet creates with `rdma dev add`, and the BlueField-3's two ports |
| Host | DOCA-OFED `mlx5_core` 25.10-2.4.1, RDMA namespaces shared |
| Kubernetes | NVIDIA Network Operator with the NIC Configuration Operator; GPU Operator |

`node-a/` was taken read-only, creating no pod:

```bash
mokka-capture collect -exec-in nvidia-network-operator/nic-configuration-daemon network <node>
mokka-capture collect driver <node>
```

The first runs in the NIC Configuration Operator's daemon pod, which has
`lspci`, `ibstat`, `ibv_devinfo`, `mlxconfig`, `flint` and `mlxlink`; the second
in the GPU Operator's driver container. The node was in service.

## What is not in this capture

- **Everything but the NICs.** Of the `driver` output only `smi-topo-m.txt` is
  kept; the kernel log and the cluster's objects are left out.
- **`ip`, `rdma`, `devlink` and `ethtool`**, which the pod does not have; their
  files are left out.
- **The fabric side:** switches, links and planes beyond the HCA.
- **Counter values** beyond port 1's, in `rdma-counters.txt`.
