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
`/readyz` becomes healthy after the rack and Pod watches sync.

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
