# Mokka + NVIDIA GPU DRA Driver Demo Guide

This guide walks through setting up [Mokka (`nvml-mock`)](https://github.com/NVIDIA/k8s-test-infra/tree/main/deployments/nvml-mock) with two 9-node cliques (4 simulated GB200 GPUs per node), installing the upstream [NVIDIA GPU DRA driver](https://github.com/kubernetes-sigs/dra-driver-nvidia-gpu) (`v0.5.1`), and running a 1-pod [JobSet](https://github.com/kubernetes-sigs/jobset) that requests all 4 GPUs on a node via Kubernetes Dynamic Resource Allocation (DRA).

---

## Prerequisites

1. **Set environment variables** (fill in `PROJECT_ID` and `MOKKA_REPO` first):
   ```bash
   export PROJECT_ID=""   # Your GCP project ID
   export MOKKA_REPO=""   # Local path for cloning https://github.com/NVIDIA/k8s-test-infra

   export ZONE="us-east1-b"
   export CLUSTER_NAME="mokka-demo"
   export REGISTRY="us-docker.pkg.dev/${PROJECT_ID}/nvml-mock"
   export WORK_DIR="/tmp/mokka-demo"
   mkdir -p "${WORK_DIR}"
   ```

2. **Create an 18-node GKE 1.37 cluster**:
   **Note:** `--image-type="UBUNTU_CONTAINERD"` is required because Mokka needs a writable `/etc/containerd` to configure NRI and the mock NVIDIA container runtime.

   ```bash
   gcloud container clusters create "${CLUSTER_NAME}" \
     --project="${PROJECT_ID}" \
     --zone="${ZONE}" \
     --cluster-version="1.37.1-gke.1697000" \
     --image-type="UBUNTU_CONTAINERD" \
     --machine-type="e2-small" \
     --num-nodes=18 \
     --disk-size="100GB"

   gcloud container clusters get-credentials "${CLUSTER_NAME}" \
     --zone="${ZONE}" \
     --project="${PROJECT_ID}"
   ```

3. **Create the Artifact Registry repository, clone Mokka, and build/push the `nvml-mock` image**:
   ```bash
   gcloud auth configure-docker us-docker.pkg.dev --quiet

   gcloud artifacts repositories create nvml-mock \
     --repository-format=docker \
     --location=us \
     --project="${PROJECT_ID}"

   git clone https://github.com/NVIDIA/k8s-test-infra.git "${MOKKA_REPO}"

   docker build --platform=linux/amd64 \
     -f "${MOKKA_REPO}/deployments/nvml-mock/Dockerfile" \
     -t "${REGISTRY}/nvml-mock:demo" \
     "${MOKKA_REPO}"

   docker push "${REGISTRY}/nvml-mock:demo"
   ```

---

## Step 1: Install Mokka with Two 9-Node Cliques

1. **Collect the node names and label all nodes**:
   ```bash
   kubectl get nodes -o custom-columns=NAME:.metadata.name --no-headers \
     | sort > "${WORK_DIR}/worker-nodes.txt"

   kubectl label nodes --all \
     mokka.nvidia.com/type=sgpu \
     nvidia.com/gpu.present=true \
     --overwrite
   ```

2. **Generate the 2-clique topology configuration**:
   ```bash
   {
     cat <<'EOF'
   topology:
     enabled: true
     domains:
       - name: demo-domain
         uuid: "00000000-0000-0000-0000-0000000000ab"
         cliques:
           - id: 0
             nodes:
   EOF
     sed -n '1,9s/^/            - /p' "${WORK_DIR}/worker-nodes.txt"
     cat <<'EOF'
           - id: 1
             nodes:
   EOF
     sed -n '10,18s/^/            - /p' "${WORK_DIR}/worker-nodes.txt"
   } > "${WORK_DIR}/topology-2cliques-18nodes.yaml"
   ```

3. **Install Mokka (`nvml-mock`) with 4 simulated GB200 GPUs per node**:
   ```bash
   cat <<EOF > "${WORK_DIR}/mokka-values.yaml"
   image:
     repository: ${REGISTRY}/nvml-mock
     tag: demo
     pullPolicy: IfNotPresent

   gpu:
     profile: gb200
     count: 4

   nodeSelector:
     mokka.nvidia.com/type: sgpu

   nri:
     enabled: true
     socketPath: /var/run/nri/nri.sock

   updateStrategy:
     rollingUpdate:
       maxUnavailable: "100%"
   EOF

   helm upgrade --install nvml-mock \
     "${MOKKA_REPO}/deployments/nvml-mock/helm/nvml-mock" \
     --namespace mokka --create-namespace \
     -f "${WORK_DIR}/mokka-values.yaml" \
     -f "${WORK_DIR}/topology-2cliques-18nodes.yaml" \
     --wait --timeout 5m

   kubectl get pods -n mokka
   ```

---

## Step 2: Install the NVIDIA GPU DRA Driver

1. **Create the driver namespace and GKE `ResourceQuota`**:
   ```bash
   kubectl create namespace nvidia-dra-driver-gpu --dry-run=client -o yaml | kubectl apply -f -

   cat <<'EOF' | kubectl apply -f -
   apiVersion: v1
   kind: ResourceQuota
   metadata:
     name: gcp-critical-pods
     namespace: nvidia-dra-driver-gpu
   spec:
     hard:
       pods: 1000
     scopeSelector:
       matchExpressions:
       - operator: In
         scopeName: PriorityClass
         values:
         - system-node-critical
         - system-cluster-critical
   EOF
   ```

2. **Install `dra-driver-nvidia-gpu` (`v0.5.1`)**:
   Point `nvidiaDriverRoot` to Mokka's staged driver directory, annotate the kubelet plugin pod with `nvml-mock.nvidia.com/devices: "true"` so Mokka injects the simulated NVML libraries and device nodes, and disable `computeDomains`:
   ```bash
   cat <<'EOF' > "${WORK_DIR}/dra-driver-gpu-values.yaml"
   nvidiaDriverRoot: /var/lib/nvml-mock/driver
   gpuResourcesEnabledOverride: true

   resources:
     gpus:
       enabled: true
     computeDomains:
       enabled: false

   kubeletPlugin:
     podAnnotations:
       nvml-mock.nvidia.com/devices: "true"
   EOF

   helm upgrade --install dra-driver-nvidia-gpu \
     oci://registry.k8s.io/dra-driver-nvidia/charts/dra-driver-nvidia-gpu \
     --version 0.5.1 \
     --namespace nvidia-dra-driver-gpu \
     -f "${WORK_DIR}/dra-driver-gpu-values.yaml" \
     --wait --timeout 5m
   ```

3. **Verify the driver pods, `DeviceClass`es, and published `ResourceSlice`s**:
   ```bash
   kubectl get pods -n nvidia-dra-driver-gpu
   kubectl get deviceclass
   kubectl get resourceslices \
     -o custom-columns='NODE:.spec.nodeName,DRIVER:.spec.driver,DEVICES:.spec.devices[*].name'
   ```
   Each of the 18 nodes publishes a `gpu.nvidia.com` `ResourceSlice` with 4 devices (`gpu-0,gpu-1,gpu-2,gpu-3`).

---

## Step 3: Install `JobSet` and Run a 1-Pod `JobSet` Requesting All 4 GPUs on a Node

1. **Install the `JobSet` controller**:
   ```bash
   helm upgrade --install jobset \
     oci://registry.k8s.io/jobset/charts/jobset \
     --version 0.11.1 \
     --namespace jobset-system --create-namespace \
     --wait --timeout 2m
   ```

2. **Create the `demo-ns` namespace, `ResourceClaimTemplate`, and `JobSet`**:
   ```bash
   kubectl create namespace demo-ns --dry-run=client -o yaml | kubectl apply -f -

   cat <<'EOF' | kubectl apply -f -
   apiVersion: resource.k8s.io/v1
   kind: ResourceClaimTemplate
   metadata:
     name: all-gpus
     namespace: demo-ns
   spec:
     spec:
       devices:
         requests:
         - name: gpu
           exactly:
             deviceClassName: gpu.nvidia.com
             allocationMode: ExactCount
             count: 4
   ---
   apiVersion: jobset.x-k8s.io/v1alpha2
   kind: JobSet
   metadata:
     name: gpu-demo
     namespace: demo-ns
   spec:
     replicatedJobs:
     - name: workers
       replicas: 1
       template:
         spec:
           parallelism: 1
           completions: 1
           template:
             spec:
               restartPolicy: Never
               containers:
               - name: worker
                 image: ubuntu:24.04
                 command: ["bash", "-lc", "nvidia-smi -L && sleep 3600"]
                 resources:
                   claims:
                   - name: gpus
               resourceClaims:
               - name: gpus
                 resourceClaimTemplateName: all-gpus
   EOF
   ```

3. **Wait for the pod to become `Ready`**:
   ```bash
   kubectl wait --for=condition=Ready pod \
     -l jobset.sigs.k8s.io/jobset-name=gpu-demo \
     -n demo-ns --timeout=60s
   ```

---

## Step 4: Verify GPU Allocation

1. **Check the pod placement and allocated `ResourceClaim`**:
   ```bash
   kubectl get pods -n demo-ns \
     -o custom-columns='NAME:.metadata.name,STATUS:.status.phase,NODE:.spec.nodeName'

   kubectl get resourceclaims -n demo-ns -o yaml
   ```
   The `ResourceClaim` status shows all 4 GPUs (`gpu-0`..`gpu-3`) on the selected node allocated to the pod:
   ```yaml
   status:
     allocation:
       devices:
         results:
         - device: gpu-0
           driver: gpu.nvidia.com
           pool: gke-mokka-demo-default-pool-1960b7f7-6lp0
           request: gpu
         - device: gpu-1
           driver: gpu.nvidia.com
           pool: gke-mokka-demo-default-pool-1960b7f7-6lp0
           request: gpu
         - device: gpu-2
           driver: gpu.nvidia.com
           pool: gke-mokka-demo-default-pool-1960b7f7-6lp0
           request: gpu
         - device: gpu-3
           driver: gpu.nvidia.com
           pool: gke-mokka-demo-default-pool-1960b7f7-6lp0
           request: gpu
   ```

2. **List the GPUs inside the container (`nvidia-smi -L`)**:
   ```bash
   POD=$(kubectl get pods -n demo-ns -l jobset.sigs.k8s.io/jobset-name=gpu-demo -o jsonpath='{.items[0].metadata.name}')
   kubectl exec -n demo-ns "${POD}" -- nvidia-smi -L
   ```
   Output:
   ```text
   GPU 0: NVIDIA GB200 (UUID: GPU-b200b200-0000-0000-0000-000000000000)
   GPU 1: NVIDIA GB200 (UUID: GPU-b200b200-0000-0000-0000-000000000001)
   GPU 2: NVIDIA GB200 (UUID: GPU-b200b200-0000-0000-0000-000000000002)
   GPU 3: NVIDIA GB200 (UUID: GPU-b200b200-0000-0000-0000-000000000003)
   ```

3. **Inspect the simulated NVLink fabric metadata (`nvidia-smi -q`)**:
   ```bash
   kubectl exec -n demo-ns "${POD}" -- nvidia-smi -q | grep -A 5 "^    Fabric$"
   ```
   Output:
   ```text
       Fabric
           State                             : Completed
           Status                            : Success
           CliqueId                          : 0
           ClusterUUID                       : 00000000-0000-0000-0000-0000000000ab
   ```

---

## Step 5: Cleanup

1. **Delete the demo workload resources**:
   ```bash
   kubectl delete jobset gpu-demo -n demo-ns
   kubectl delete resourceclaimtemplate all-gpus -n demo-ns
   kubectl delete namespace demo-ns
   ```

2. **Delete the GKE cluster**:
   ```bash
   gcloud container clusters delete "${CLUSTER_NAME}" \
     --project="${PROJECT_ID}" \
     --zone="${ZONE}" \
     --quiet
   ```
