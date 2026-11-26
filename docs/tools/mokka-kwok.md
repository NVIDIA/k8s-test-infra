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

## Optional Kubernetes APIs

The chart uses device-plugin mode by default. To select the APIs that the
KWOK data plane will use, set these values:

```yaml
kwok:
  enabled: true
  dataPlane:
    gpuPublishing: DRA
    gpuDriver: gpu.nvidia.com
    computeDomains:
      enabled: false
      driverNamespace: nvidia
    nodeResourceTopology:
      enabled: false
```

In DRA mode the replica checks for ResourceSlices, ResourceClaims, and
DeviceClasses. If its GPU DeviceClass does not map to `nvidia.com/gpu`, it
logs a warning and continues. A KWOK Node with positive GPU capacity gets
a warning Event. Mokka withdraws its own device-plugin capacity and leaves
capacity owned by another component in place.

ComputeDomains require the resource and NVIDIA ComputeDomain APIs.
NodeResourceTopology requires the NFD topology API. During startup the replica
serves `/healthz` and retries missing APIs before it starts its workers.
Its log and the response body from `/readyz` identify the missing API. In DRA mode, each bound KWOK Node owns one GPU ResourceSlice. The pool name
is the Node name. Each device is named `gpu-<minor>` and carries its UUID,
memory, PCI and NUMA placement, and the model and software attributes supplied
by the rack. Set `spec.node.gpus.model.addressingMode` on the rack profile
when known (`ATS`, `HMM`, or `None`). The optional `brand` defaults to
`Nvidia`. Changes to the devices advance the pool generation.

Release the KWOK rack slots in DRA mode before switching back to device-plugin
mode. Mokka waits for the slices to disappear before it clears the Node's
discovery fields. A Node that still carries its DRA pool marker will not
advertise device-plugin capacity.

For a bound Pod in DRA mode, Mokka reads the devices the scheduler selected
in the Pod's ResourceClaims. This includes named claims, claims from templates,
and the generated claim named in `status.extendedResourceClaimStatus` for an
`nvidia.com/gpu` request. Mokka looks up each selected device in the Node's rack
slot. It writes its UUID to the container entry in the `SGPURackAllocation`
and then to the Pod's `mokka.nvidia.com/gpu-allocation` annotation. A Pod that
uses only another driver's devices has an empty GPU annotation (`{}`) and
no GPU entry in the rack. The claims remain under Kubernetes control.

## GPU capacity

In device-plugin mode a bound KWOK node advertises the number of GPUs in its rack slot as
`nvidia.com/gpu` in both `status.capacity` and `status.allocatable`. A Pod
can request this resource through its container limits. Both totals stay
fixed as Pods bind and leave. The scheduler tracks the requests against that total.

The data plane patches only the GPU entries. If a node source supplies a
different GPU total, Mokka keeps the source values and emits a
`GPUCapacityConflict` Warning Event. On unbinding, it removes GPU entries
that it alone owns.

## Pod GPU allocations

Once the scheduler binds a whole-GPU Pod to a KWOK node, the data plane
selects GPU UUIDs from the node’s rack slot. It writes one
`SGPURackAllocation` per rack that needs a record. The record names each
Pod by namespace, name and UID and lists the UUIDs for each GPU-requesting
container. To inspect the assignments, run:

```sh
kubectl get sgpurackallocations
kubectl get sgpurackallocation "$RACK" -o yaml
```

All containers in a Pod must fit before the data plane saves its assignment.
A Pod that is already terminating keeps its saved reservation until release.
Mokka skips terminating Pods when it makes new assignments.
Sequential init containers can reuse GPUs within the same Pod. Restartable
init containers keep their GPUs while the application runs.

The data plane recovers saved assignments after restart. When a Pod reaches
`Succeeded` or `Failed`, or is deleted, its GPUs become available for other
Pods. The rack record can remain empty.

### Make whole-GPU Pods wait for allocation

On a KWOK v0.8.0 cluster that uses `stage-fast.yaml`, apply the admission stages
after the upstream stages and before creating test Pods:

```sh
kubectl apply -f deployments/mokka-kwok/stages/pod-admission.yaml
```

This replaces the cluster-wide KWOK `pod-ready` stage. Use it on test clusters
where Mokka manages the KWOK nodes that receive whole-GPU Pods. Pods without
whole-GPU requests use the upstream ready behavior. A GPU Pod on an unmanaged
KWOK node waits for an allocation that Mokka cannot supply.

After the rack write succeeds, Mokka copies each container’s UUIDs to the Pod:

```yaml
metadata:
  annotations:
    mokka.nvidia.com/gpu-allocation: '{"trainer":["GPU-0d9170ae-bf02-5d35-b505-d37a29ee1bae"]}'
```

Leave this annotation to the data plane. KWOK sets the Pod IP while the Pod is
Pending and marks the Pod Ready once the annotation exists. If a saved GPU
leaves the slot while its Pod is Pending, Mokka removes the annotation and
keeps the UUID reserved. If the slot contains all the saved GPUs again, Mokka
restores the annotation. Before failing a Pod for a shortage, Mokka checks the
live node binding and the current rack delivery. If an already-bound
Pod cannot fit on its node, Mokka sets its phase to `Failed` and its reason to
`UnexpectedAdmissionError`. An unbound Pod still waits for the scheduler.

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
