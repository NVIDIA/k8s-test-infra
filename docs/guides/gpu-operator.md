# NVIDIA GPU Operator

Run the real GPU Operator against Mokka: the device plugin, GPU Feature
Discovery, DCGM and the validator all come up and behave as they would on a node
with hardware.

## Why it needs an overlay

The GPU Operator's job is to install and manage a driver. Mokka has already
provided one, so the operator has to be told to stop at the parts that consume
a driver rather than install one — and pointed at where Mokka staged it.

That is what the values below do. Installing the operator with its defaults
against a Mokka node fails: the driver DaemonSet tries to build a kernel module
that cannot exist.

## Prerequisites

- [Docker](https://docs.docker.com/get-started/get-docker/) and
  [Kind](https://kind.sigs.k8s.io/)
- [Helm 3.8 or newer](https://helm.sh/docs/intro/install/)
- [kubectl](https://kubernetes.io/docs/reference/kubectl/)

Takes about 15 minutes, most of it pulling operator images.

## Step 1 — Create a cluster with CDI enabled

The operator resolves GPUs through the Container Device Interface, so
containerd needs CDI turned on and the NVIDIA container toolkit present.

```bash
kind create cluster --name mokka-operator

NODE=mokka-operator-control-plane

docker exec "$NODE" bash -c '
  apt-get update -qq && apt-get install -y -qq curl gpg
  curl -fsSL https://nvidia.github.io/libnvidia-container/gpgkey \
    | gpg --dearmor -o /usr/share/keyrings/nvidia-container-toolkit-keyring.gpg
  curl -fsSL https://nvidia.github.io/libnvidia-container/stable/deb/nvidia-container-toolkit.list \
    | sed "s#deb https://#deb [signed-by=/usr/share/keyrings/nvidia-container-toolkit-keyring.gpg] https://#g" \
    > /etc/apt/sources.list.d/nvidia-container-toolkit.list
  apt-get update -qq && apt-get install -y -qq nvidia-container-toolkit
'

docker exec "$NODE" nvidia-ctk runtime configure \
  --runtime=containerd --cdi.enabled --set-as-default
docker exec "$NODE" systemctl restart containerd
```

## Step 2 — Install Mokka

```bash
helm install nvml-mock oci://ghcr.io/nvidia/k8s-test-infra/chart/nvml-mock \
  --namespace mokka --create-namespace \
  --wait --timeout 120s
```

## Step 3 — Install the GPU Operator

```bash
cat > gpu-operator-values.yaml <<'EOF'
driver:
  enabled: false
toolkit:
  enabled: false
dcgm:
  enabled: false
mig:
  strategy: none
migManager:
  enabled: false
nodeStatusExporter:
  enabled: false

cdi:
  enabled: true
  default: true

devicePlugin:
  enabled: true
  config:
    name: ""
  env:
    - name: NVIDIA_DRIVER_ROOT
      value: "/var/lib/nvml-mock/driver"

gfd:
  enabled: true
  env:
    - name: NVIDIA_DRIVER_ROOT
      value: "/var/lib/nvml-mock/driver"
    - name: GFD_MACHINE_TYPE_FILE
      value: "/etc/nvml-mock/machine-type"

dcgmExporter:
  enabled: true
  env:
    - name: NVIDIA_DRIVER_ROOT
      value: "/var/lib/nvml-mock/driver"
    - name: DCGM_EXPORTER_COLLECT_INTERVAL
      value: "5000"

validator:
  driver:
    env:
      - name: DRIVER_INSTALL_DIR
        value: "/run/nvidia/driver"
      - name: LD_LIBRARY_PATH
        value: "/run/nvidia/driver/usr/lib64"
      - name: DISABLE_DEV_CHAR_SYMLINK_CREATION
        value: "true"
  toolkit:
    env:
      - name: NVIDIA_VISIBLE_DEVICES
        value: "all"
  cuda:
    env:
      - name: WITH_WORKLOAD
        value: "false"
  plugin:
    env:
      - name: LD_LIBRARY_PATH
        value: "/run/nvidia/driver/usr/lib64"
EOF

helm repo add nvidia https://helm.ngc.nvidia.com/nvidia && helm repo update

helm install gpu-operator nvidia/gpu-operator \
  --namespace gpu-operator --create-namespace \
  -f gpu-operator-values.yaml \
  --wait --timeout 600s
```

Pin `--version` in anything you keep. The overlay tracks the operator's chart
schema, and an unpinned install can pick up a release that renames a value.

## Step 4 — Verify

```bash
kubectl -n gpu-operator wait --for=condition=ready pod --all --timeout=300s

kubectl get nodes -o jsonpath='{.items[0].status.allocatable.nvidia\.com/gpu}'
# 4 — the gb300 default profile has four devices

kubectl get nodes -o json | jq '.items[0].metadata.labels | with_entries(select(.key | startswith("nvidia.com")))'
```

The validator pod reaching `Completed` is the meaningful signal: it probes the
driver root, exercises CDI injection, and checks the node advertises GPUs.

## What each value is doing

| Value | Why |
|---|---|
| `driver.enabled: false` | Mokka is the driver. The real DaemonSet would try to build a kernel module |
| `toolkit.enabled: false` | The mock libraries are staged on the host by Mokka's DaemonSet, not injected by the toolkit |
| `dcgm.enabled: false` | `dcgm-exporter` embeds the host engine in-process, so the standalone DaemonSet is redundant |
| `NVIDIA_DRIVER_ROOT` | Points every operand at `/var/lib/nvml-mock/driver` instead of the real driver root |
| `GFD_MACHINE_TYPE_FILE` | GFD's default reads `/sys/class/dmi/id/product_name`, which says `kind` here and is absent on hosts with no DMI. Mokka writes a file of its own |
| `mig.strategy: none` | MIG is not simulated. Without this the device plugin enumerates MIG devices and CDI spec generation fails |
| `validator.cuda.WITH_WORKLOAD: false` | The CUDA validation step launches a kernel, and [CUDA is not simulated](../faq.md#can-i-run-cuda-workloads-against-mokka) |
| `DISABLE_DEV_CHAR_SYMLINK_CREATION` | The driver validator would otherwise try to create device-node symlinks that Mokka already staged |

## Troubleshooting

**The validator crash-loops.** It is a statically linked Go binary with no
shell, so a missing file in the staged driver root looks like a crash rather
than an error. Check what is actually there:

```bash
docker exec "$NODE" ls -la /run/nvidia/driver/usr/lib64/libnvidia-ml.so*
```

**The device plugin reports 0 GPUs.** Almost always `driver.enabled` or
`toolkit.enabled` left at their defaults — the operator is managing a driver
that does not exist. Confirm with `helm get values gpu-operator`.

**GFD labels the node `kind`.** `GFD_MACHINE_TYPE_FILE` is missing from the
overlay.

More symptoms in [Troubleshooting](../troubleshooting.md).

## Clean up

```bash
kind delete cluster --name mokka-operator
```

## Related

| To read about | See |
|---|---|
| Every chart value | [Installation](../helm-chart.md) |
| Changing GPU state while the operator watches | [Runtime Control](../nvml-mock-ctl.md) |
| Driving this from CI | [Use in CI/CD](ci-cd.md) |
| The full health loop on top of this stack | [NVSentinel](nv-sentinel/README.md) |
