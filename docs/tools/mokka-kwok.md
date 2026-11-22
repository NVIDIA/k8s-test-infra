# mokka-kwok

The KWOK data plane runs on a real Kubernetes node. It reads the Mokka racks
assigned to its replica and watches Pods that have a node name. It keeps racks
in memory by UID and keeps Pods only for nodes bound to those racks. When a
node gains a slot, the data plane lists Pods already on that node. When the
node leaves its last assigned rack, it drops those Pods from memory.

```sh
mokka-kwok data-plane --shard-name=mokka-kwok-data-plane-0
```

The shard name must match the `mokka.nvidia.com/kwok-shard` label on its racks.
The process uses its Kubernetes service account. It listens on `:8080` by
default. Use `--listen-addr` to change the address. `/healthz` reports process
health. `/readyz` becomes healthy after the rack, Pod, and Node watches sync.

## Hardware discovery labels

The data plane sets these labels on each bound KWOK node. The values come from
`spec.node.gpus` in the assigned rack profile.

| Node label | Source | Example |
| --- | --- | --- |
| `nvidia.com/gpu.product` | `model.productName`, with spaces replaced by hyphens | `NVIDIA-A100-SXM4-40GB` |
| `nvidia.com/gpu.count` | `count`, the number of whole GPUs on the node | `8` |
| `nvidia.com/gpu.memory` | `memory.capacity` of one GPU, in MiB | `40960` for `40Gi` |
| `nvidia.com/mig.strategy` | The KWOK simulation exposes whole GPUs | `none` |
| `feature.node.kubernetes.io/pci-10de.present` | `pci.vendorID` identifies NVIDIA, or the ID is absent and `model.vendor` is NVIDIA | `true` |

Use these labels in workload selectors. Use independent placement labels for
the rack group. An empty product name leaves the product label unset.

If the node source supplies a different value, Mokka keeps it, emits a
`DiscoveryLabelConflict` Warning Event, and publishes the other labels.
When the binding ends, Mokka gives up its fields. Kubernetes removes labels
that only Mokka owned.
