# Troubleshooting

Symptoms you are likely to hit running Mokka on a Kubernetes cluster, and what
each one usually means.

## First, get the mock to tell you what it is doing

```bash
MOCK_NVML_DEBUG=1 nvidia-smi
```

Every NVML call the mock answers is traced, including which config it loaded and
how many devices it built. Most of the questions below are answered by the first
five lines of that output.

## Kubernetes installs

### `nvidia-smi` in a pod reports no GPUs

Check the node actually has the staged tree, then that the pod can see it:

```bash
kubectl get pods -n mokka -l app.kubernetes.io/name=nvml-mock -o wide
kubectl logs -n mokka -l app.kubernetes.io/name=nvml-mock --tail=50
```

If the DaemonSet is not running on that node, it is usually the node selector:
the chart pins to nodes labelled `mokka.nvidia.com/type=sgpu`.

### The device plugin reports 0 GPUs

Two different causes, in the order worth checking:

1. **The mock files are not on the node.** Confirm the staged tree exists —
   see [checking a node by hand](helm-chart.md#checking-a-node-by-hand).
2. **The GPU Operator is trying to manage a real driver.** It must be installed
   with `driver.enabled=false` and `toolkit.enabled=false`; there is no real
   driver for it to manage.

```bash
kubectl -n gpu-operator logs -l app=nvidia-device-plugin-daemonset | head -20
```

### The GPU Operator validator crash-loops

The validator is a statically linked Go binary with no shell, and it probes the
driver root for expected files. A missing file in the staged tree looks like a
crash loop rather than an error message.

```bash
NODE=$(docker ps --filter name=control-plane -q)
docker exec "$NODE" ls -la /run/nvidia/driver/usr/lib64/libnvidia-ml.so*
docker exec "$NODE" cat /var/lib/nvml-mock/driver/config/config.yaml
```

### NFD logs `failed to get DMI entry` or `failed to detect NUMA nodes`

The node's kernel exposes no DMI or NUMA information, typical of Docker Desktop
on Apple Silicon. Mokka serves both on every node it runs on, but only pods
started afterwards see them, so restart the NFD workers once the mock is up:

```bash
kubectl -n gpu-operator rollout restart ds/gpu-operator-node-feature-discovery-worker
```

Nodes the mock does not select, such as the control plane of the local kind
cluster, keep logging the errors.
They are harmless there: discovery still completes and every other label is
published. See
[DMI and NUMA on nodes without them](components/node-daemon.md#dmi-and-numa-on-nodes-without-them).

### CDI specs never appear in `/var/run/cdi`

```bash
kubectl logs -n mokka -l app.kubernetes.io/name=nvml-mock | grep -i cdi
```

### The node pod is NotReady and `nvml-mock-nri` crash-loops

```text
nri-plugin: nri stub: failed to connect to NRI service: dial unix /var/run/nri/nri.sock: connect: no such file or directory
```

containerd on that node has NRI disabled, the default in containerd 1.7. The
node agent keeps running and workloads still start, but without the mock, and
`helm install --wait` times out. Enable NRI in containerd and restart it, as
[Set up NRI injection](guides/nri-injection.md#prerequisites) shows, or set
`nri.enabled=false`.

### A pod gets no mock GPUs even though NRI is enabled

First check that the pod is meant to receive GPUs. The plugin gives mock GPUs
only to containers that hold a GPU allocation from the device plugin or the
NVIDIA DRA driver, or whose pod carries the `nvml-mock.nvidia.com/devices:
"true"` annotation. The `nvml-mock.nvidia.com/infiniband` and
`nvml-mock.nvidia.com/imex-channels` annotations add only their own surface,
never GPUs. A pod that requested no GPU and has no annotation is left untouched
by design. A device plugin that runs without `--pass-device-specs=true` or the
`cdi-cri` strategy leaves no allocation the plugin can recognise, so its pods
also receive nothing.

The NRI plugin fails open: when it cannot inject, containers are created
without the mock rather than failing. That makes a silent window possible, so
check the plugin's probes rather than whether the process is alive — a plugin
containerd has unregistered stays running and simply stops injecting.

```bash
kubectl get pods -n mokka -l app.kubernetes.io/name=nvml-mock
kubectl describe pod -n mokka -l app.kubernetes.io/name=nvml-mock | grep -A3 Readiness
```

Injection is also skipped by design when the pod opts out or its namespace is
excluded. See
[NRI plugin failure modes](helm-chart.md#nri-plugin-failure-modes) and the
[NRI Plugin](components/nri-plugin.md) page.

### A runtime change does not take effect

`nvml-mock-ctl` writes an override file that each loaded copy of the library
re-reads on a TTL — about a second by default. Wait one interval and re-check.

If it never applies, confirm you targeted the right node: overrides are
per-node files, so changing state on one node has no effect on a consumer
running on another. See [Runtime Control](nvml-mock-ctl.md).

### Mounts pile up on a node with NRI enabled

Up to and including 0.4.0, an injected container that also had a
`Bidirectional` volume, such as a DRA kubelet plugin or the GPU Operator's
validator, copied the overlay's writable config bind onto the node. Each such
container doubled the copies at `/var/lib/nvml-mock/driver/config`, and they
stay after the pods are gone. A large stack slows container starts and anything
else that reads the node's mount table. See
[Mount propagation](components/nri-plugin.md#mount-propagation).

On a Kind cluster, count them in each node container. Kubernetes node names
can differ from the container names, so list the containers with `kind`. A
healthy node prints 0:

```bash
for NODE in $(kind get nodes --name mokka); do
  echo "${NODE}: $(docker exec "${NODE}" awk '$5 == "/var/lib/nvml-mock/driver/config" { n++ } END { print n + 0 }' /proc/self/mountinfo)"
done
```

To stay on 0.4.0 without the growth, keep the plugin out of the namespace
those pods run in, for example `--set 'nri.excludedNamespaces={gpu-operator}'`
for the GPU Operator. Pods in an excluded namespace get no overlay, so leave
out only namespaces whose pods do not need the node's GPU profile.

Upgrading stops the growth but leaves the copies in place. Rebooting the node
clears them; on Kind, recreate the cluster. To clear them in place, first make
sure no container injected before the upgrade still runs on the node:
unmounting the copies takes its writable config directory with it, and its
writes then fail as read-only. Then unmount them until none is left. A node
without copies is left as it is, and a node whose unmount fails is named:

```bash
for NODE in $(kind get nodes --name mokka); do
  docker exec "${NODE}" sh -c 'while mountpoint -q /var/lib/nvml-mock/driver/config; do umount /var/lib/nvml-mock/driver/config || exit 1; done' \
    || echo "${NODE}: unmount failed"
done
```

### `kind load docker-image` fails with "content digest not found"

```text
ctr: content digest sha256:...: not found
```

`ghcr.io/nvidia/nvml-mock` is a multi-arch image. Docker Desktop's containerd
image store keeps the whole manifest list, and `kind load docker-image` hands
the node an archive whose per-platform layers it does not have. The error names
the digest, not the cause. This is the default Docker Desktop configuration on
macOS.

Save a single platform and load the archive instead:

```bash
ARCH=$(uname -m | sed 's/x86_64/amd64/; s/aarch64/arm64/')
docker save --platform "linux/${ARCH}" ghcr.io/nvidia/nvml-mock:latest -o nvml-mock.tar
kind load image-archive nvml-mock.tar --name <cluster>
```

The platform must match the kind node's architecture. A locally built image is
single-arch already and loads unchanged.

### A profile value does not show up in `nvidia-smi`

Confirm the profile was loaded at all — `MOCK_NVML_DEBUG=1 nvidia-smi` names the
config and the device count in its first lines. If it was, the value is being
set at a different level: per-device settings override `device_defaults`, and a
runtime override beats both. [Configuration](configuration.md) documents the
precedence.

If the profile itself is not loading, the ConfigMap is usually the cause:

```bash
kubectl get configmap -n mokka -l app.kubernetes.io/name=nvml-mock
kubectl logs -n mokka -l app.kubernetes.io/name=nvml-mock | grep -i config
```

### Fields show `N/A` or return `NOT_SUPPORTED`

The mock answers from configuration, so a field the profile does not set is
genuinely not supported — the same answer an older real driver gives. Add the
value to the profile and `helm upgrade`.

A handful of NVML functions are generated stubs that always return
`NOT_SUPPORTED`. [Libraries and Shims](components/libraries-and-shims.md) explains which and
why; [Contributing](contributing/index.md) covers implementing one.

### `nvidia-smi` segfaults or a symbol is missing

Both mean the image predates the call being made. Confirm the node is running
the image you think it is:

```bash
kubectl get daemonset -n mokka -o jsonpath='{.items[*].spec.template.spec.containers[*].image}'
```

Run with `MOCK_NVML_DEBUG=1` to find which call is involved before filing a bug.

## Running the library outside Kubernetes

Building `libnvidia-ml.so` and running a binary against it with
`LD_LIBRARY_PATH` is the contributor workflow — see
[Contributing](contributing/index.md) for building it and
[Local Development](contributing/local-development.md) for the Tilt loop.

One failure is specific to that path and worth knowing: if the real NVML loads
despite `LD_LIBRARY_PATH`, something else is winning the search order.

```bash
LD_DEBUG=libs nvidia-smi 2>&1 | grep nvml   # what actually resolved
grep nvml /etc/ld.so.conf.d/*               # an ld.so entry can outrank your path
```

An absolute path is more reliable than a relative one.

## Still stuck

Pick the route that matches the problem — blank issues are disabled, so the
tracker expects one of these:

| Problem | Where |
|---|---|
| Mokka behaves incorrectly | [Bug report](https://github.com/NVIDIA/k8s-test-infra/issues/new?template=bug_report.yml) |
| Something on this site is wrong or missing | [Documentation correction](https://github.com/NVIDIA/k8s-test-infra/issues/new?template=documentation.yml) |
| You want Mokka to do something it does not | [Feature request](https://github.com/NVIDIA/k8s-test-infra/issues/new?template=feature_request.yml) |
| "How do I…?" | [Discussions](https://github.com/NVIDIA/k8s-test-infra/discussions) — the tracker is for tracked work |

!!! warning "Never open a public issue for a security vulnerability"
    Report it privately instead. See the
    [security policy](https://github.com/NVIDIA/k8s-test-infra/blob/main/SECURITY.md).

### What to include in a bug report

What you ran, what you expected, what happened, and:

```bash
helm list -n mokka                      # chart version and the profile in use
kubectl get pods -n mokka -o wide
kubectl logs -n mokka -l app.kubernetes.io/name=nvml-mock --tail=100
kubectl get nodes --show-labels
```

Plus `MOCK_NVML_DEBUG=1 nvidia-smi 2>&1 | head -40` from inside an affected pod.
