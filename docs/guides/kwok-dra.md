# Use GPU claims and ComputeDomains on KWOK Nodes

Use this guide on a cluster with the Kubernetes WithOut Kubelet (KWOK)
controller, Mokka's Pod admission Stages, and an inventory that binds existing
KWOK Nodes to rack slots. The GPU example needs one bound Node with two free
GPUs. The ComputeDomain example needs four available Nodes in one NVLink rack.
Use Helm, kubectl and jq. The examples use the four-Node GB300 rack from the
end-to-end tests.

## Choose what to publish

| Workload | Mokka Helm setting | What Kubernetes sees |
|---|---|---|
| Pod requests a whole GPU with `limits: {nvidia.com/gpu: 1}` | `kwok.dataPlane.gpuPublishing: DevicePlugin` (default) | A GPU count on each Node. Mokka selects a free GPU after the Pod is bound. |
| Pod requests an individual GPU through Dynamic Resource Allocation (DRA) | `kwok.dataPlane.gpuPublishing: DRA` | A ResourceSlice lists the GPUs on each Node. The scheduler selects a device and writes its name to the ResourceClaim. |
| Pods request channels in a shared NVLink ComputeDomain | `kwok.dataPlane.computeDomains.enabled: true` (default `false`) | Each bound NVLink Node publishes a channel and a daemon device. This works with either GPU mode. |

The GPU mode applies to all managed KWOK Nodes. Before changing modes or
disabling ComputeDomains, see the release instructions for
[GPU slices](../tools/mokka-kwok.md#optional-kubernetes-apis) and
[domain channels](../tools/mokka-kwok.md#computedomain-channels).

## Configure the two charts

1. **NVIDIA DRA driver.** Use a cluster with the `resource.k8s.io/v1` API.
   The tests use Kubernetes 1.35 and NVIDIA's driver chart 0.5.0. Download the
   [driver values](compute-domain/dra-driver.values.yaml) as `dra-driver.values.yaml`.
   These values enable ComputeDomains and restrict the **kubelet plugin** to
   Nodes labeled `mokka.nvidia.com/type=sgpu`. Reserve that label for real
   Nodes that run Mokka's mock driver. Keep it off virtual Nodes. Apply this
   restriction to the kubelet plugin only. NVIDIA's controller must be able to
   place its IMEX (Internode Memory Exchange) daemon Pods on the virtual Nodes
   that hold channel claims.

   ```sh
   helm upgrade --install nvidia-dra-driver dra-driver-nvidia-gpu \
     --repo https://helm.ngc.nvidia.com/nvidia --version 0.5.0 \
     -n nvidia --create-namespace -f dra-driver.values.yaml \
     --set resources.gpus.enabled=true --wait
   kubectl get deviceclass gpu.nvidia.com compute-domain-default-channel.nvidia.com compute-domain-daemon.nvidia.com
   ```

2. **Mokka.** Apply these values to your Mokka release to run both examples.
   Set `driverNamespace` to the namespace of the NVIDIA driver.

   ```yaml
   controlPlane: {enabled: true}
   kwok:
     enabled: true
     dataPlane:
       gpuPublishing: DRA
       gpuDriver: gpu.nvidia.com
       computeDomains: {enabled: true, driverNamespace: nvidia}
   ```

## Request a GPU

Run the commands in one shell. Set the four variables to the hostname labels
of your virtual Nodes. The first command lists the devices that the scheduler
can select on the first Node.

```sh
node0=kwok-dra-e2e-0; node1=kwok-dra-e2e-1
node2=kwok-dra-e2e-2; node3=kwok-dra-e2e-3
kubectl get resourceslices --field-selector "spec.nodeName=$node0,spec.driver=gpu.nvidia.com" \
  -o jsonpath='{range .items[*].spec.devices[*]}{.name}{"\t"}{.attributes.uuid.string}{"\n"}{end}'
kubectl create namespace kwok-dra-guide
kubectl -n kwok-dra-guide apply -f - <<'YAML'
apiVersion: resource.k8s.io/v1
kind: ResourceClaimTemplate
metadata: {name: gpu}
spec:
  spec:
    devices:
      requests: [{name: gpu, exactly: {deviceClassName: gpu.nvidia.com}}]
YAML
```

Use the Pod's hostname selector so the scheduler can allocate the claim.
Each Pod gets a separate claim from the same template.

```sh
claim_pod() {
  kubectl -n kwok-dra-guide apply -f - <<YAML
apiVersion: v1
kind: Pod
metadata: {name: $1}
spec:
  nodeSelector: {kubernetes.io/hostname: $2}
  tolerations: [{key: kwok.x-k8s.io/node, operator: Exists, effect: NoSchedule}]
  resourceClaims: [{name: device, resourceClaimTemplateName: $3}]
  containers:
    - name: job
      image: registry.k8s.io/pause:3.10
      resources: {claims: [{name: device}]}
YAML
}
claim_pod first "$node0" gpu
claim_pod second "$node0" gpu
kubectl -n kwok-dra-guide wait --for=condition=Ready pod/first pod/second --timeout=2m
kubectl -n kwok-dra-guide get resourceclaims \
  -o 'custom-columns=CLAIM:.metadata.name,POOL:.status.allocation.devices.results[0].pool,DEVICE:.status.allocation.devices.results[0].device'
kubectl -n kwok-dra-guide get pods first second \
  -o 'custom-columns=POD:.metadata.name,GPUS:.metadata.annotations.mokka\.nvidia\.com/gpu-allocation'
```

Expect two different device names in the same pool. Match each name to its
UUID in the ResourceSlice. The Pod annotation must contain that UUID for `job`.

### Update selectors from Run:ai Fake GPU Operator

The Fake GPU Operator (FGO) uses the UUID as its GPU device name. Mokka names
each GPU `gpu-<minor>`, such as `gpu-0`, within its Node's pool. To request a
specific GPU, match the `uuid` attribute. For a model selector, use
`productName`. Mokka devices have no `model` attribute. Read the exact values
from the ResourceSlice. For example, this selector matches the test rack:

```yaml
# Under requests[].exactly in the claim template
selectors:
  - cel:
      expression: 'device.attributes["gpu.nvidia.com"].productName == "NVIDIA GB300 NVL"'
```

## Check a four-Node ComputeDomain

Create the domain in the workload namespace. NVIDIA's controller creates the
channel claim template. Request that template from one Pod on each Node.

```sh
kubectl -n kwok-dra-guide apply -f - <<'YAML'
apiVersion: resource.nvidia.com/v1beta1
kind: ComputeDomain
metadata: {name: four-node-job}
spec:
  numNodes: 4
  channel:
    allocationMode: Single
    resourceClaimTemplate: {name: four-node-job-channel}
YAML
claim_pod worker-0 "$node0" four-node-job-channel
claim_pod worker-1 "$node1" four-node-job-channel
claim_pod worker-2 "$node2" four-node-job-channel
claim_pod worker-3 "$node3" four-node-job-channel
```

Wait for the expected status and member Nodes in the same report. The
controller can update the domain several times as the daemon Pods start.

```sh
wait_domain() {
  expected_status=$1
  shift
  for attempt in $(seq 1 90); do
    if kubectl -n kwok-dra-guide get computedomain four-node-job -o json |
      jq -e --arg state "$expected_status" --args '
        .status | select(.status == $state and
          ([.nodes[]?.name] | sort) == ($ARGS.positional | sort))' "$@"; then
      return 0
    fi
    sleep 2
  done
  echo "Timed out waiting for $expected_status and Nodes $*" >&2
  return 1
}
wait_domain Ready "$node0" "$node1" "$node2" "$node3"
kubectl -n kwok-dra-guide delete pod worker-3
wait_domain NotReady "$node0" "$node1" "$node2"
```

With four channel holders, Mokka reports four simulated IMEX daemons, and
NVIDIA's controller sets the domain to `Ready`. After one holder leaves,
three Nodes remain and the controller sets it to `NotReady`. This checks
the controller on virtual Nodes. Testing GPU memory transfers requires hardware.

```sh
kubectl delete namespace kwok-dra-guide
```
