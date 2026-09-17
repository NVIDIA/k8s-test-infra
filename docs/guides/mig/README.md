# MIG Partitioning

Carve mock GPUs into MIG instances, advertise each instance as a schedulable
resource, and run a pod on a single slice.

The real NVIDIA device plugin runs unmodified in `migStrategy=single`, and
`nvidia-smi mig` works against the mock the way it works against a driver. No
A100, H100 or B200 is involved.

!!! warning "The layout a node boots with is the layout that is allocatable"

    Repartitioning at runtime changes what NVML reports — what `nvidia-smi`,
    DCGM and GFD read. It does not change the driver capability surface the
    device plugin allocates against, which is staged once when the `nvml-mock`
    pod starts. Choose the partitioning at install time; use runtime
    repartitioning to exercise consumers that read NVML.

## Prerequisites

- [Docker](https://docs.docker.com/get-started/get-docker/) and
  [Kind](https://kind.sigs.k8s.io/)
- [Helm 3.8 or newer](https://helm.sh/docs/intro/install/)
- [kubectl](https://kubernetes.io/docs/reference/kubectl/)

Takes about 10 minutes.

## Step 1 — Create a cluster and install Mokka partitioned

```bash
kind create cluster --name mokka-mig

helm install nvml-mock oci://ghcr.io/nvidia/k8s-test-infra/chart/nvml-mock \
  --namespace mokka --create-namespace \
  --set gpu.profile=h100 \
  --set gpu.count=2 \
  --set gpu.mig.enabled=true \
  --set 'gpu.mig.gpuInstances[0].profile=1g.10gb' \
  --set 'gpu.mig.gpuInstances[0].count=7' \
  --wait --timeout 120s
```

Two boards, each carved into seven slices, so the node ends up with fourteen
partitions.

`gpu.mig.enabled` on its own is refused. No profile ships a layout — how a
board is carved is a deployment choice rather than a property of the silicon —
so every MIG install names its own partitioning through `gpu.mig.gpuInstances`.
A capable board left with no layout would boot MIG-enabled and unpartitioned,
which publishes no GPU resource at all.

Filling a board with its smallest slice gives every partition one profile name,
which is what `migStrategy=single` requires:

| Profile | Smallest slice | Instances per board |
|---|---|---|
| `a100` | `1g.5gb` | 7 |
| `h100` | `1g.10gb` | 7 |
| `b200` | `1g.24gb` | 7 |
| `gb200` | `1g.24gb` | 7 |
| `gb300` | `1g.36gb` | 7 |

`l40s` and `t4` are not MIG-capable. The chart refuses MIG on them, and NVML
answers `NVML_ERROR_NOT_SUPPORTED` exactly as it does on that hardware.

## Step 2 — See the partitions through `nvidia-smi`

```bash
POD=$(kubectl -n mokka get pod -l app.kubernetes.io/name=nvml-mock \
  -o jsonpath='{.items[0].metadata.name}')

kubectl -n mokka exec "$POD" -- nvidia-smi -L
kubectl -n mokka exec "$POD" -- nvidia-smi mig -lgi
```

`nvidia-smi -L` names each partition with its profile and a unique `MIG-…`
UUID. `mig -lgi` lists the GPU instances behind them, and `mig -lgip` shows how
many of each profile the board still has free.

## Step 3 — Label the node and deploy the device plugin

```bash
kubectl label node --all mokka.nvidia.com/type=sgpu
```

The plugin resolves MIG capabilities through
`/proc/driver/nvidia-caps/mig-minors` at a hardcoded absolute path, and it does
so while building its device map — so a plugin that cannot read that file
advertises nothing at all rather than degrading to whole GPUs. The path cannot
be delivered by a volume mount, because runc refuses any mount whose target is
inside `/proc`. What works is letting the container mount Mokka's staged copy
over its own `/proc/driver`, which the kernel allows from a privileged
container:

```bash
kubectl apply -f - <<'EOF'
apiVersion: apps/v1
kind: DaemonSet
metadata:
  name: nvidia-device-plugin-mock
  namespace: kube-system
spec:
  selector:
    matchLabels:
      name: nvidia-device-plugin-mock
  template:
    metadata:
      labels:
        name: nvidia-device-plugin-mock
    spec:
      nodeSelector:
        mokka.nvidia.com/type: sgpu
      tolerations:
        - operator: Exists
      containers:
        - name: nvidia-device-plugin
          image: nvcr.io/nvidia/k8s-device-plugin:v0.18.2
          # $0 is the "--" below, so "$@" is exactly the args list.
          command:
            - /bin/sh
            - -c
            - |
              set -eu
              staged=/var/lib/nvml-mock/driver/proc/driver
              # The node agent stages the capability surface asynchronously and
              # this DaemonSet is often admitted first, so wait for it. The
              # mount has to happen in this container's mount namespace.
              waited=0
              while [ ! -f "$staged/nvidia-caps/mig-minors" ]; do
                if [ "$waited" -ge 180 ]; then
                  echo "no MIG capability table staged after ${waited}s;" \
                       "is the chart installed with gpu.mig.enabled=true?" >&2
                  exit 1
                fi
                waited=$((waited + 1))
                sleep 1
              done
              mount -o bind "$staged" /proc/driver
              exec nvidia-device-plugin "$@"
            - --
          args:
            - "--nvidia-driver-root=/var/lib/nvml-mock/driver"
            - "--driver-root-ctr-path=/var/lib/nvml-mock/driver"
            - "--device-discovery-strategy=nvml"
            - "--mig-strategy=single"
            - "--pass-device-specs=true"
          securityContext:
            privileged: true
          volumeMounts:
            - name: mock-root
              mountPath: /var/lib/nvml-mock
              readOnly: true
            - name: device-plugins
              mountPath: /var/lib/kubelet/device-plugins
      volumes:
        - name: mock-root
          hostPath:
            path: /var/lib/nvml-mock
        - name: device-plugins
          hostPath:
            path: /var/lib/kubelet/device-plugins
EOF

kubectl -n kube-system wait --for=condition=ready \
  pod -l name=nvidia-device-plugin-mock --timeout=180s
```

| Argument | Why |
|---|---|
| `--mig-strategy=single` | Publish every MIG partition as `nvidia.com/gpu`. Rejects a node whose partitions do not all carry one profile, which is why the layout fills the board with a single slice size |
| `--nvidia-driver-root`, `--driver-root-ctr-path` | Point the plugin at Mokka's staged tree instead of a real driver root |
| `--device-discovery-strategy=nvml` | Discover through NVML, which is the interface Mokka implements |
| `--pass-device-specs=true` | Deliver the allocated partition's device nodes into the container: the parent `/dev/nvidiaN` plus the two cap nodes guarding its GPU and compute instance |

## Step 4 — One resource per partition

```bash
kubectl get nodes -o custom-columns='NODE:.metadata.name,GPUS:.status.allocatable.nvidia\.com/gpu'
# NODE                     GPUS
# mokka-mig-control-plane  14
```

Fourteen, not two. This is the shape change that matters to everything above
the plugin: a partitioned node stops advertising one resource per board and
starts advertising one per slice, so the same `nvidia.com/gpu: 1` request now
buys a seventh of an H100.

## Step 5 — Schedule a pod onto a slice

```bash
kubectl apply -f - <<'EOF'
apiVersion: v1
kind: Pod
metadata:
  name: mig-pod
spec:
  restartPolicy: Never
  containers:
    - name: app
      image: busybox:1.36
      command: ["sleep", "300"]
      resources:
        limits:
          nvidia.com/gpu: 1
EOF

kubectl wait --for=condition=ready pod/mig-pod --timeout=120s

# which partition it was given
kubectl exec mig-pod -- printenv NVIDIA_VISIBLE_DEVICES
# MIG-ff3a2b1c-…

# and the cap nodes guarding it
kubectl exec mig-pod -- ls /dev/nvidia-caps
```

The pod was given a partition's own identity — a `MIG-…` UUID, one of those
`nvidia-smi -L` listed in Step 2 — rather than its parent board's. Two cap
nodes arrive with it, one for the GPU instance and one for the compute
instance, which is what a MIG-aware runtime reads to open the partition.

## Repartition at runtime

`nvidia-smi` drives this, not `nvml-mock-ctl`. Run it in the `nvml-mock` pod on
the node you want to change — the scope is that one node, so repeat it per pod
to change several:

```bash
SMI="kubectl -n mokka exec $POD -- nvidia-smi"

# tear one partition down by id, then rebuild it. The compute instance goes
# first: NVML refuses to destroy a GPU instance that still holds one.
$SMI mig -i 0 -dci -ci 0 -gi 3
$SMI mig -i 0 -dgi -gi 3
$SMI mig -i 0 -cgi 1g.10gb -C

# turn MIG off on GPU 0, destroying every partition it has, then back on
$SMI -i 0 -mig 0
$SMI -i 0 -mig 1
$SMI mig -i 0 -cgi 1g.10gb,1g.10gb,1g.10gb,1g.10gb,1g.10gb,1g.10gb,1g.10gb -C

# read it back from a different process
$SMI mig -lgi
```

Each mutation is recorded in the override document, so it outlives the
`nvidia-smi` process that made it and reaches every other consumer on the node
within one TTL. That is what makes a partition visible to a *separate*
`nvidia-smi`, to DCGM and to GFD at all, since every process gets its own
engine. A mutation that cannot be recorded fails with
`NVML_ERROR_NO_PERMISSION` rather than succeeding in one process only, which is
what the driver reports when the same call is made without the permissions it
needs.

`nvml-mock-ctl status` shows the recorded layout and `nvml-mock-ctl reset`
clears it. There is deliberately no `nvml-mock-ctl mig`: it would be a second,
non-standard spelling of an interface `nvidia-smi` already covers, and a second
writer of the same state.

### Run the lifecycle as a script

`run.sh` beside this page walks the whole lifecycle on one GPU — enable,
create, list, delete, disable — and asserts each step instead of only printing
it. It installs an *unpartitioned* board and carves it at runtime, so it
covers the `nvidia-smi` path above rather than Step 1's chart layout, and it
never involves the device plugin.

```bash
cd docs/guides/mig
BUILD_LOCAL=true ./run.sh
```

`BUILD_LOCAL=true` builds the image from source and side-loads it into a Kind
cluster of its own. Use it until the MIG instance lifecycle reaches a published
release; without it the script installs `ghcr.io/nvidia/nvml-mock:latest` into
your current context, and stops with that advice if the image cannot enable
MIG. It defaults to an `h100`, so pass `GPU_PROFILE=a100` or
`MIG_PROFILE=3g.40gb` to carve a different board or a larger slice.

Two of its steps are assertions rather than demonstrations, because both were
real defects: that `mig -lgip` never reports more free instances of a profile
than exist in total, and that `mig -dgi` is refused — leaving the partition
intact — while a compute instance is still live.

## How slices are named

A slice is named for the share of *its own* board it holds, so the name follows
the memory the profile declares rather than NVIDIA's published listing for the
hardware that profile stands in for. Where the two describe the same board the
names agree — `a100` offers `1g.5gb`, `h100` offers `1g.10gb`. Where they do
not, they diverge: the `b200` profile describes a 192GiB board and so offers
`1g.24gb`, while NVIDIA publishes `1g.23gb` for a 180GB B200.

Take the names a board accepts from `nvidia-smi mig -lgip` rather than from the
MIG user guide. On Blackwell, name partitions by `profile` rather than
`profile_id`: NVIDIA publishes no profile IDs for those boards, so the mock
reports NVML's enum instead and the ids will not match a real board's listing.

## Limits

A runtime repartition changes the **NVML view** only. `/dev/nvidia-caps` and
the `mig-minors` table are staged once when the `nvml-mock` pod starts, so the
device plugin cannot *allocate* what a repartition produces — including a
layout identical to the one installed, since rebuilding it draws fresh
GPU-instance IDs. Clearing the override, `nvml-mock-ctl reset` included,
restores the layout NVML reports but not the instance IDs it reports them
under, so only restarting the `nvml-mock` pod returns the node to an
allocatable state.

There is no CUDA, so a MIG slice schedules and admits a pod but runs no kernels
on it.

## Troubleshooting

**`helm install` fails saying `gpu.mig.gpuInstances` declares no partitions.**
`gpu.mig.enabled=true` was set without a layout. Name one — see Step 1.

**`helm install` fails saying the profile is not a MIG-capable board.** The
profile declares no `mig.max_gpu_instances`; `l40s` and `t4` cannot partition.

**Allocatable stays at zero after enabling MIG.** The plugin is running but
found no capability table, or it found partitions it will not serve. Check
whether the layout is uniform — `migStrategy=single` rejects a mix of profiles
— and read its logs:

```bash
kubectl -n kube-system logs -l name=nvidia-device-plugin-mock --tail=30
```

**A partition `nvidia-smi` reports cannot be allocated.** It was created at
runtime. The capability surface is staged at pod start; restart the
`nvml-mock` pod on that node.

**`mig -dgi` fails saying the GPU instance is in use.** It still holds a
compute instance. Destroy that first with `mig -dci -gi <id> -ci <id>`, as on
real hardware — see [Repartition at runtime](#repartition-at-runtime).

## Clean up

```bash
kind delete cluster --name mokka-mig
```

## Related

| To read about | See |
|---|---|
| The `mig:` config schema, including per-device layouts and fixed instance ids | [Configuration](../../configuration.md#mig) |
| Every chart value | [Installation](../../helm-chart.md) |
| Whole-GPU allocation, and the plugin without MIG | [NVIDIA Device Plugin](../device-plugin.md) |
| Changing temperature, power or health at runtime | [Runtime control](../../nvml-mock-ctl.md) |
