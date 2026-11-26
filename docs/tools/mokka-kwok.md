# mokka-kwok

The KWOK data plane runs on a real Kubernetes node. It reads the Mokka racks
assigned to its replica and watches Pods that have a node name. It keeps racks
in memory by UID and keeps Pods only for nodes bound to those racks. When a
node gains a slot, the data plane lists Pods already on that node. When the
node leaves its last assigned rack, it drops those Pods from memory.

```sh
mokka-kwok data-plane --shard-name=mokka-kwok-data-plane-0 \
  --control-plane-url=http://nvml-mock-control-plane:8080
```

The shard name must match the `mokka.nvidia.com/kwok-shard` label on its racks.
The process uses its Kubernetes service account. It listens on `:8080` by
default. Use `--listen-addr` to change the address. `/healthz` reports process
health. Before the first Pod list, the replica waits for a rack-state reply for
each initially owned rack with a published generation, or for that rack to
leave the shard. A per-rack `NotReady` or `NotFound` reply defers the rack.
When it becomes available, the replica lists Pods on its newly bound Nodes.
`/readyz` becomes healthy after the rack, Pod, and Node watches sync.

## Rack state

The rack watch supplies the rack UID and its delivery generation. When a generation
advances, the replica requests the compiled state from the control-plane Service.
Helm supplies the Service address. Set `kwok.dataPlane.controlPlaneURL` if the
Service belongs to another release.

The default batch holds up to 100 racks. A partial batch goes out after one
second. At most two requests run at once. Adjust `size`, `window`, and
`maxInFlight` under `kwok.dataPlane.batch` to change these limits.

The replica retries unavailable racks with backoff and keeps the last accepted
state. A new generation preserves the retry deadline. If a rack changes during
a successful request, the replica applies the reply that satisfies the requested
generation, then fetches the newer generation. A rack that leaves the shard loses its cached state. A late reply for
its previous ownership is discarded. For each Node UID, the assignment revision
takes precedence. Within one revision, the rack delivery generation orders updates.

## Hardware discovery labels

The data plane sets these labels on each bound KWOK node. The values come from
the compiled rack state and the GPU list for the assigned slot.

| Node label | Source | Example |
| --- | --- | --- |
| `nvidia.com/gpu.product` | `gpuModel.productName`, with spaces replaced by hyphens | `NVIDIA-A100-SXM4-40GB` |
| `nvidia.com/gpu.count` | Number of GPUs in the slot | `8` |
| `nvidia.com/gpu.memory` | `gpuModel.memory` of one GPU, in MiB | `40960` for `40Gi` |
| `nvidia.com/mig.strategy` | The KWOK simulation exposes whole GPUs | `none` |
| `feature.node.kubernetes.io/pci-10de.present` | `gpuModel.pci.vendorID` identifies NVIDIA, or the ID is absent and `gpuModel.vendor` is NVIDIA | `true` |

Use these labels in workload selectors. Use independent placement labels for
the rack group. An empty product name leaves the product label unset.

If the node source supplies a different value, Mokka keeps it, emits a
`DiscoveryLabelConflict` Warning Event, and publishes the other labels.
When the binding ends, Mokka gives up its fields. Kubernetes removes labels
that only Mokka owned.

### Clusters with the NVIDIA GPU Operator

The Operator can detect a KWOK node from the NVIDIA PCI label. Set the OS
release labels in the virtual Node template so the Operator can read the OS
identity. Use the values for the OS you simulate. This example uses Debian 12.
The first label keeps the Operator's node components off the virtual node.

```yaml
metadata:
  labels:
    nvidia.com/gpu.deploy.operands: "false"
    feature.node.kubernetes.io/system-os_release.ID: debian
    feature.node.kubernetes.io/system-os_release.VERSION_ID: "12"
```
