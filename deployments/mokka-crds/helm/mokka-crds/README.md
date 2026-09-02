# mokka-crds

CRDs for the `mokka.nvidia.com/v1alpha1` API group:

- `SGPUProfile`
- `SGPUInventory`
- `SGPURuntimePolicy`

## Install

```
helm install mokka-crds deployments/mokka-crds/helm/mokka-crds
```

Requires cluster-admin RBAC. CRDs live under `templates/` so `helm upgrade`
rolls schema versions. Regenerated from Go types via `make gen`.
