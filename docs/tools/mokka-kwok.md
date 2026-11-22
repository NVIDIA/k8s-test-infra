# mokka-kwok

The KWOK data plane runs on a real Kubernetes node. It reads the Mokka racks
assigned to its replica and watches Pods that have a node name. It keeps racks
in memory by UID and indexes Pods by node name. A restart rebuilds both indexes
from the Kubernetes API.

```sh
mokka-kwok data-plane --shard-name=mokka-kwok-data-plane-0
```

The shard name must match the `mokka.nvidia.com/kwok-shard` label on its racks.
The process uses its Kubernetes service account. It listens on `:8080` by
default. Use `--listen-addr` to change the address. `/healthz` reports process
health. `/readyz` becomes healthy after both watches complete their initial sync.
