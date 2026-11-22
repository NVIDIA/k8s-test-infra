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
health. `/readyz` becomes healthy after the rack, Pod, and product-label watches complete their initial sync.

For each bound KWOK node, the data plane reads
`spec.node.gpus.model.productName` from the assigned rack profile. It replaces
spaces with hyphens and sets the Node label `nvidia.com/gpu.product`. For
example, `NVIDIA A100-SXM4-40GB` becomes `NVIDIA-A100-SXM4-40GB`. Use this
label in workload selectors. A KWOK rack group must use independent labels for
placement because binding the node changes its product label.

If the profile has no product name, the data plane omits the label. If the node
source already set a different value, it keeps that value and emits a Warning
Event. When the binding ends, the data plane removes the label it owns.
