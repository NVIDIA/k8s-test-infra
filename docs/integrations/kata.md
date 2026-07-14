# Kata Containers

nvml-mock supports plain Kata Containers workloads through the `kata-qemu`
runtime class and containerd NRI. The workload runs in a lightweight VM while
the mock GPU stack is delivered at container creation. Confidential Containers
(CoCo) are excluded from this integration because they do not provide the host
filesystem sharing used by this delivery path.

The executable source of truth is the `e2e-kata` job in
[`.github/workflows/nvml-mock-e2e.yaml`](../../.github/workflows/nvml-mock-e2e.yaml),
with [`tests/e2e/kind-kata-config.yaml`](../../tests/e2e/kind-kata-config.yaml)
for the Kind node and
[`tests/e2e/run-kata-nri.sh`](../../tests/e2e/run-kata-nri.sh) for the workload
contracts.

## Delivery model

containerd calls the nvml-mock NRI plugin when it creates a container. The
plugin adjusts the OCI specification before Kata starts the guest and exposes
the staged nvml-mock overlay read-only at `/opt/nvml-mock`. Kata then shares
that host path with the guest.

The same NRI adjustment supplies the executable and library search paths, the
NVML profile location, and the PCI and InfiniBand environment. ComputeDomain
topology is conditional: `NODE_NAME` and `MOCK_TOPOLOGY_CONFIG` are injected
only when `topology.enabled=true` has staged a topology document; the chart
value defaults to `false`. Workloads do not need to author mock environment
variables, mount the profile, or perform manual library setup. This path is
ambient NRI delivery; it does not depend on GPU allocation.

## Requirements

- A node with KVM available at `/dev/kvm`.
- The `vhost_vsock` kernel module loaded and `/dev/vhost-vsock` available to
  the node. Kata uses AF_VSOCK to communicate with the guest agent.
- For the reference Kind node, `/dev/shm` remounted to 8 GiB. QEMU backs guest
  memory there when filesystem sharing is enabled; Kind's default 64 MiB is
  insufficient. The CI lane runs
  `mount -o remount,size=8G /dev/shm` in the node container.
- containerd NRI enabled with a socket at `/var/run/nri/nri.sock`. The reference
  containerd patch is in
  [`tests/e2e/kind-kata-config.yaml`](../../tests/e2e/kind-kata-config.yaml).
- [kata-deploy](https://github.com/kata-containers/kata-containers/tree/main/tools/packaging/kata-deploy)
  3.32.0 with the QEMU shim enabled. The CI lane installs it with:

  ```sh
  helm install kata-deploy \
    oci://ghcr.io/kata-containers/kata-deploy-charts/kata-deploy \
    --version 3.32.0 \
    --namespace kube-system \
    --set k8sDistribution=k8s \
    --set shims.qemu.enabled=true \
    --set node-feature-discovery.enabled=false \
    --set debug=true \
    --wait --timeout 300s
  ```

- The nvml-mock chart installed in `nvml-mock-system` with NRI enabled:

  ```sh
  helm install nvml-mock deployments/nvml-mock/helm/nvml-mock \
    --namespace nvml-mock-system --create-namespace \
    --set nri.enabled=true \
    --wait --timeout 180s
  ```

  The chart deploys the `nvml-mock-nri` DaemonSet. Confirm its logs report
  `configured by runtime` before starting workloads.

## Ambient workload

An ambient workload adds only the Kata runtime class. It does not request a
GPU, declare volumes, or author mock environment variables:

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: kata-nvml
  namespace: default
spec:
  runtimeClassName: kata-qemu
  restartPolicy: Never
  containers:
    - name: app
      image: ubuntu:22.04
      command: ["nvidia-smi", "-L"]
```

The default ambient pod intentionally has no visible `/dev/nvidia*` device
nodes. With zero visible GPU device nodes, nvml-mock uses unfiltered,
node-wide enumeration and reports every GPU in the selected profile. This is
not scheduler allocation and the pod does not consume an `nvidia.com/gpu`
extended resource.

## Optional mock device nodes

Some software checks for NVIDIA character devices even though nvml-mock does
not need them. Such a workload can opt in to the staged mock nodes:

```yaml
metadata:
  annotations:
    nvml-mock.nvidia.com/devices: "true"
```

This annotation is a trust boundary: a pod author who can set it asks the
node-local plugin to add host device nodes to the container specification.
Allow it only in trusted workload namespaces. Keep untrusted namespaces out of
the injection scope by adding them to `nri.excludedNamespaces`; the Helm
release namespace and `kube-system` are excluded automatically.

## Opting out

A pod can disable ambient injection explicitly:

```yaml
metadata:
  annotations:
    nvml-mock.nvidia.com/inject: "false"
```

For an opted-out pod, NRI adds neither the `/opt/nvml-mock` overlay nor the
injected environment. The pod runs with only the image and environment from
its authored specification.

## Verifying the runtime

Verify that the workload actually booted a Kata guest by comparing kernels:

```sh
NODE_KERNEL=$(docker exec nvml-mock-kata-control-plane uname -r)
GUEST_KERNEL=$(kubectl exec kata-nvml -- uname -r)
test "$GUEST_KERNEL" != "$NODE_KERNEL"
```

The guest kernel must differ from the node kernel. Do not use the CPU
`hypervisor` flag as the discriminator: cloud CI nodes are themselves virtual
machines, so the flag can also be present in an ordinary host-runtime pod.
The CI implementation uses a direct node-container `uname -r` comparison; see
[`tests/e2e/run-kata-nri.sh`](../../tests/e2e/run-kata-nri.sh).

## Plain Kata versus Confidential Containers

Plain `kata-qemu` supports the host filesystem sharing that carries the
read-only nvml-mock overlay into the guest. Confidential Containers remove
that trust relationship, so a host overlay is not an appropriate delivery
mechanism. CoCo support therefore requires a future guest payload containing
the mock libraries, tools, configuration, and any required device setup; it is
not part of the NRI-native lane documented here.
