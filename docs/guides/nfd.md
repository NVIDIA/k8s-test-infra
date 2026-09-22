# Node Feature Discovery

Install [Node Feature Discovery (NFD)](https://kubernetes-sigs.github.io/node-feature-discovery/)
next to Mokka and verify that NFD turns Mokka's feature file into the same
GPU-presence label that a real node would expose.

Mokka does not write Kubernetes node labels. Its node agent writes
`pci-10de.present=true` to the NFD local-source directory, and NFD owns the
resulting `feature.node.kubernetes.io/pci-10de.present=true` label. This keeps
the simulated path aligned with a real deployment.

## Prerequisites

- [Docker](https://docs.docker.com/get-started/get-docker/) and
  [Kind](https://kind.sigs.k8s.io/)
- [Helm 3.8 or newer](https://helm.sh/docs/intro/install/)
- [kubectl](https://kubernetes.io/docs/reference/kubectl/)

This guide takes about 10 minutes and does not require a GPU.

## Step 1 — Create a cluster and install Mokka

Kind's default cluster contains only a control-plane node. That node may be
tainted `NoSchedule`, so the NFD worker would never run there. Create one
worker explicitly for this guide:

```console
$ cat > mokka-nfd-kind.yaml <<'EOF'
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
nodes:
- role: control-plane
- role: worker
EOF
$ kind create cluster --name mokka-nfd --config mokka-nfd-kind.yaml
$ helm install nvml-mock oci://ghcr.io/nvidia/k8s-test-infra/chart/nvml-mock \
    --namespace mokka --create-namespace \
    --wait --timeout 120s
$ kubectl label node --all mokka.nvidia.com/type=sgpu
$ rm mokka-nfd-kind.yaml
```

The chart's default `nodeLabels.featuresDir` is
`/etc/kubernetes/node-feature-discovery/features.d`. It is mounted from each
node so that NFD can read the file after the node agent writes it.

## Step 2 — Install NFD

The repository's NFD end-to-end scenario is pinned to version `0.19.0`. Use the
same version here so the local-source behavior is reproducible:

```console
$ helm repo add nfd https://kubernetes-sigs.github.io/node-feature-discovery/charts
$ helm repo update
$ helm upgrade --install nfd nfd/node-feature-discovery \
    --namespace node-feature-discovery --create-namespace \
    --version 0.19.0 \
    --wait --timeout 120s
```

Wait for NFD to become ready before checking the label:

```console
$ kubectl -n node-feature-discovery wait \
    --for=condition=ready pod \
    -l app.kubernetes.io/name=node-feature-discovery \
    --timeout=120s
```

## Step 3 — Verify the label and its provenance

NFD polls the local-source directory and adds the namespaced label to nodes
where the feature file exists:

```console
$ kubectl get nodes \
    -o custom-columns='NODE:.metadata.name,NFD_GPU:.metadata.labels.feature\.node\.kubernetes\.io/pci-10de\.present'
```

The simulated nodes should report `true`:

```text
NODE                         NFD_GPU
mokka-nfd-control-plane      true
mokka-nfd-worker             true
```

The input is a feature file, not a label written by Mokka. You can inspect it
from the node-agent container:

```console
$ kubectl -n mokka exec daemonset/nvml-mock -c node-agent -- \
    cat /host/etc/kubernetes/node-feature-discovery/features.d/nvml-mock.features
```

Expected output:

```text
pci-10de.present=true
```

NFD records labels that it owns in the
`nfd.node.kubernetes.io/feature-labels` node annotation. Check that annotation
when a workload or admission rule needs to distinguish NFD-owned labels from
labels written by another component:

```console
$ kubectl get node mokka-nfd-control-plane \
    -o jsonpath='{.metadata.annotations.nfd\.node\.kubernetes\.io/feature-labels}{"\n"}'
```

The annotation includes `pci-10de.present` without the
`feature.node.kubernetes.io/` prefix.

## Use the label in a workload

Select simulated nodes with the NFD label just as you would select a real GPU
node:

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: nfd-labelled-workload
spec:
  nodeSelector:
    feature.node.kubernetes.io/pci-10de.present: "true"
  containers:
    - name: shell
      image: busybox:1.36
      command: ["sleep", "300"]
```

Apply it and confirm that Kubernetes scheduled it on a labelled node:

```console
$ kubectl apply -f nfd-labelled-workload.yaml
$ kubectl get pod nfd-labelled-workload -o wide
```

## Troubleshooting

**The label is missing.** Check that the Mokka DaemonSet is ready and that the
feature file is present in the node-agent container. The NFD chart and Mokka
must use the same `featureFilesDir` and `nodeLabels.featuresDir` paths.

**The NFD pod is ready but no label appears.** Inspect the NFD worker logs:

```console
$ kubectl -n node-feature-discovery logs -l app.kubernetes.io/name=node-feature-discovery
```

**The node has a label but the workload is pending.** Confirm that the pod's
`nodeSelector` uses the complete namespaced key and the string value `"true"`.

## Clean up

```console
$ kind delete cluster --name mokka-nfd
```

## Related

| To read about | See |
|---|---|
| Mokka chart values | [Installation](../helm-chart.md) |
| GPU allocation through the NVIDIA device plugin | [NVIDIA Device Plugin](device-plugin.md) |
| NFD label behavior in the chart | [Helm chart reference](../helm-chart.md#node-labels) |
