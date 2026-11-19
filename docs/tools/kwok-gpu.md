# Give a KWOK node a simulated GPU

This proof of concept lets an ordinary Kubernetes Pod request a whole GPU on an existing KWOK node. The Mokka control plane binds the node to a slot in an `SGPURack`. The `kwok-gpu` controller publishes the slot's GPU count in Node capacity and allocatable. Once the scheduler binds a Pod, the controller copies the assigned rack GPU UUIDs to the Pod.

The example uses a one-GPU node, regular containers and the default scheduler. KWOK simulates Pod lifecycle. Wait for the allocation annotation when testing device identity. The default KWOK stages can mark a Pod Running before the allocation appears.

## Try it on a configured cluster

Run these commands from the repository root with your local Kind kubeconfig selected. The setup below creates the namespace, controllers, inventory and node.

```sh
kubectl get node mokka-kwok-gpu -o custom-columns='NODE:.metadata.name,GPUS:.status.allocatable.nvidia\.com/gpu'
first=$(kubectl -n mokka-kwok-poc create -f examples/kwok-gpu/pod.yaml -o name)
kubectl -n mokka-kwok-poc wait "$first" --for='jsonpath={.metadata.annotations.mokka\.nvidia\.com/gpu-allocation}' --timeout=60s
kubectl -n mokka-kwok-poc get "$first" -o go-template='{{index .metadata.annotations "mokka.nvidia.com/gpu-allocation"}}{{"\n"}}'
```

The result maps the container name `worker` to one GPU UUID. Submit a second Pod while the first holds the GPU. It should stay Pending. Delete the first Pod to make the same GPU available to the second.

```sh
second=$(kubectl -n mokka-kwok-poc create -f examples/kwok-gpu/pod.yaml -o name)
kubectl -n mokka-kwok-poc describe "$second"
kubectl -n mokka-kwok-poc delete "$first"
kubectl -n mokka-kwok-poc wait "$second" --for='jsonpath={.metadata.annotations.mokka\.nvidia\.com/gpu-allocation}' --timeout=60s
kubectl -n mokka-kwok-poc get "$second" -o go-template='{{index .metadata.annotations "mokka.nvidia.com/gpu-allocation"}}{{"\n"}}'
kubectl -n mokka-kwok-poc delete "$second"
```

## Set up a local Kind cluster

These commands assume an existing local Kind cluster with a real worker, Docker, Go, Helm and kubectl. Set `cluster` to the Kind cluster name. Use a cluster without another Mokka control plane or fake GPU publisher. If KWOK is already installed, keep its controller and stages.

```sh
cluster=my-kind-cluster
work=$(mktemp -d)
export KUBECONFIG="$work/kubeconfig"
kind get kubeconfig --name "$cluster" > "$KUBECONFIG"
chmod 600 "$KUBECONFIG"
kubectl apply --server-side -f https://github.com/kubernetes-sigs/kwok/releases/download/v0.8.0/kwok.yaml
kubectl apply -f https://github.com/kubernetes-sigs/kwok/releases/download/v0.8.0/stage-fast.yaml
helm install mokka-kwok-poc-crds deployments/mokka-crds/helm/mokka-crds --namespace mokka-kwok-poc --create-namespace
```

Build the two binaries for the Kind node architecture and load their image locally.

```sh
arch=$(kubectl get nodes -l node-role.kubernetes.io/control-plane -o jsonpath='{.items[0].status.nodeInfo.architecture}')
CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go build -o "$work/" ./cmd/control-plane ./cmd/kwok-gpu
mkdir "$work/image"
mv "$work/control-plane" "$work/kwok-gpu" "$work/image/"
docker build --platform "linux/$arch" -t mokka-kwok-poc:dev -f examples/kwok-gpu/Dockerfile "$work/image"
kind load docker-image --name "$cluster" mokka-kwok-poc:dev
```

Render the existing control-plane Deployment and its permissions. Install the PoC controller and bind the example node to the A100 profile.

```sh
helm template kwok-poc deployments/nvml-mock/helm/nvml-mock \
  --namespace mokka-kwok-poc --kube-version 1.35.0 \
  --set controlPlane.enabled=true --set controlPlane.replicas=1 \
  --set controlPlane.image.repository=mokka-kwok-poc --set controlPlane.image.tag=dev \
  --show-only templates/controlplane/rbac.yaml --show-only templates/controlplane/serviceaccount.yaml \
  --show-only templates/controlplane/deployment.yaml > "$work/control-plane.yaml"
kubectl -n mokka-kwok-poc apply -f "$work/control-plane.yaml" -f examples/kwok-gpu/controller.yaml
kubectl apply -f examples/controlplane-crds/sgpu-rack-profile.yaml -f examples/kwok-gpu/inventory.yaml -f examples/kwok-gpu/node.yaml
kubectl wait node/mokka-kwok-gpu --for='jsonpath={.status.allocatable.nvidia\.com/gpu}=1' --timeout=120s
```

## How the PoC works

- The node source sets `kwok.x-k8s.io/node: fake` as an annotation and the labels `mokka.nvidia.com/sgpu-node: "true"` and `mokka.nvidia.com/node-type: KWOK`. The existing rack group selects the node with a placement selector.
- A single controller polls the Kubernetes API every two seconds. It verifies the node UID and the control plane's exact rack binding. It leaves nodes that already advertise GPUs from another source alone.
- The controller stores assignments by Pod UID in a node annotation. It reserves all devices for a Pod in one version-checked update before publishing the Pod annotation. A restart preserves active assignments. Deleting a Pod or moving it to a terminal phase releases its devices.
- To withdraw capacity, remove the `mokka.nvidia.com/sgpu-node` label while the controller is running. It removes its GPU count and annotations. The source retains the node.

The PoC covers whole-GPU requests from regular containers with one controller replica. GPU requests from init containers, DRA, metric ranges, NUMA placement and hardware execution are outside this example. The Pod annotation is a simulation result. The controller uses no scheduler-specific labels or annotations.

## Read simulated metrics through a PodMonitor

The example controller serves the three MEP 0009 DCGM fields on port 9400. Idle GPUs report zero utilization and zero used memory. On an allocated GPU, use fixed Pod annotations `mokka.nvidia.com/gpu-utilization` and `mokka.nvidia.com/gpu-memory-used` (0–100). Defaults are 0% utilization and 50% memory for an allocated GPU. Memory metrics are in MiB. GPU UUID, index, model and usable memory come from the bound rack and its profile. This PoC uses one replica.

Apply the PodMonitor in a namespace selected by your Prometheus instance. It discovers the controller Pods directly and copies each sample’s virtual node name from `Hostname` into `node`. No KWOK exporter Service is required. For the 100-node example, run this on a cluster with at least 100 available node CIDRs if the Kubernetes controller allocates Pod CIDRs.

```sh
examples/kwok-gpu/metrics-lab.sh
kubectl -n runai apply -f examples/kwok-gpu/podmonitor.yaml
```

Query `count by (node) (DCGM_FI_DEV_GPU_UTIL{node=~"kwok-metrics-.*"})` in Prometheus. Each of the 100 nodes should return eight. Run:ai installations that derive the node from the exporter Pod IP must use the explicit node label for these samples. New NVIDIA exporters can use ratios and bytes for physical-node metrics. Convert those series to the units expected by your Run:ai version when combining the two sources.

To remove the 100-node example, delete its virtual nodes and inventory before removing the controllers.

```sh
kubectl get nodes -l type=kwok -o name | sed -n '/^node\/kwok-metrics-[0-9][0-9][0-9]$/p' | xargs -n 20 kubectl delete
kubectl delete sgpuinventory kwok-metrics
kubectl delete sgpurackprofile kwok-metrics-a100
kubectl -n runai delete podmonitor mokka-kwok-gpu
```

## Remove the example

Delete the demo Pods, node and inventory before stopping the controllers. These commands leave KWOK and the Mokka CRDs installed.

```sh
kubectl -n mokka-kwok-poc delete pods -l app=mokka-kwok-gpu-demo
kubectl delete -f examples/kwok-gpu/node.yaml -f examples/kwok-gpu/inventory.yaml
kubectl delete -f examples/controlplane-crds/sgpu-rack-profile.yaml
kubectl -n mokka-kwok-poc delete -f examples/kwok-gpu/controller.yaml -f "$work/control-plane.yaml"
```
