# Node-Wide Mock GPU Injection Implementation Plan

> **Status: Superseded (2026-07-01).** This plan implements the tiered, opt-in,
> CDI-based design that has been superseded by the "every pod, webhook-only"
> design at [`../2026-07-01-node-wide-mock-injection-every-pod-design.md`](../2026-07-01-node-wide-mock-injection-every-pod-design.md).
> A new implementation plan will be written for the successor design. Do not
> execute this plan.

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Extend nvml-mock so GPU Operator workloads, GPU-requesting pods, and opt-in discovery pods receive tiered mock GPU/IB/PCI environments via CDI + a mutating admission webhook.

**Architecture:** The DaemonSet continues to materialize host artifacts and generate tiered CDI specs. A new `nvml-mock-injector` webhook maps pod labels/annotations to `cdi.k8s.io/*` device annotations and sets `runtimeClassName: nvidia`. IB and PCI visibility inside containers uses existing/mock shims (`libibmock*`, new `libpcimocksys`) activated through CDI-injected `LD_PRELOAD`.

**Tech Stack:** Go 1.26, shell (setup.sh), C (LD_PRELOAD shims), Helm 3, Kind + containerd CDI, nvidia-container-runtime, Ginkgo/Gomega + testify.

**Design spec:** [`docs/designs/2026-06-30-node-wide-mock-injection-design.md`](../2026-06-30-node-wide-mock-injection-design.md)

---

## File Map

| File | Responsibility |
|------|----------------|
| `deployments/nvml-mock/scripts/start-mock-ib.sh` | Default socket path on host |
| `deployments/nvml-mock/scripts/setup.sh` | Tiered CDI spec generation + extended `nvidia.yaml` |
| `deployments/nvml-mock/scripts/generate-cdi-specs.sh` | **New** — CDI YAML fragments shared by setup.sh |
| `pkg/system/mockpcisysfs/c/shim.c` | **New** — PCI sysfs LD_PRELOAD redirect |
| `pkg/system/mockpcisysfs/Makefile` | **New** — build `libpcimocksys.so` |
| `pkg/system/mockpcisysfs/shim/shim_test.go` | **New** — integration test for PCI redirect |
| `deployments/nvml-mock/Dockerfile` | Copy/build PCI shim |
| `cmd/nvml-mock-injector/main.go` | **New** — webhook HTTP server |
| `cmd/nvml-mock-injector/mutate.go` | **New** — tier resolution + JSON patch |
| `cmd/nvml-mock-injector/mutate_test.go` | **New** — unit tests |
| `cmd/nvml-mock-injector/tier/tier.go` | **New** — tier constants + resolution |
| `deployments/nvml-mock/helm/nvml-mock/templates/injector-*.yaml` | **New** — Deployment, Service, webhook, TLS |
| `deployments/nvml-mock/helm/nvml-mock/values.yaml` | `injector:` block |
| `tests/e2e/validate-injection-tier.sh` | **New** — per-tier pod validation |
| `tests/e2e/injection-test-pods.yaml` | **New** — tier test pod templates |

---

## Task 1: Host-Exposed mock-ib Socket

**Files:**
- Modify: `deployments/nvml-mock/scripts/start-mock-ib.sh`
- Modify: `pkg/network/mockib/daemon/env.go`
- Modify: `cmd/mock-ib/main.go`
- Modify: `deployments/nvml-mock/helm/nvml-mock/templates/daemonset.yaml`
- Modify: `tests/e2e/validate-ibping.sh`, `validate-ibnetdiscover.sh`, `validate-sminfo.sh`, `validate-iblinkinfo.sh`

- [ ] **Step 1: Change default socket constant**

In `pkg/network/mockib/daemon/env.go`, update the default:

```go
const (
    // ...
    defaultMockIBPingSocket = "/var/lib/nvml-mock/run/mock-ib.sock"
)
```

Ensure `EnvOr(EnvMockIBPingSocket, defaultMockIBPingSocket)` uses this constant.

In `deployments/nvml-mock/scripts/start-mock-ib.sh` line 12:

```sh
SOCKET="${MOCK_IB_PING_SOCKET:-/var/lib/nvml-mock/run/mock-ib.sock}"
```

In `cmd/mock-ib/main.go`, update the `-socket` flag default to match.

- [ ] **Step 2: Ensure host run directory exists in setup.sh**

Add before the IB block in `deployments/nvml-mock/scripts/setup.sh` (after `mkdir -p "$DEV_ROOT"`):

```sh
MOCK_RUN_DIR="$HOST/run"
mkdir -p "$MOCK_RUN_DIR"
```

- [ ] **Step 3: Mount host run dir in DaemonSet**

In `deployments/nvml-mock/helm/nvml-mock/templates/daemonset.yaml`, add env:

```yaml
- name: MOCK_IB_PING_SOCKET
  value: /var/lib/nvml-mock/run/mock-ib.sock
```

Add volumeMount (reuse `host-nvml-mock` — already mounts `/var/lib/nvml-mock`; no change needed if socket lives under that tree).

- [ ] **Step 4: Update E2E scripts**

In each of `tests/e2e/validate-ibping.sh`, `validate-ibnetdiscover.sh`, `validate-sminfo.sh`, `validate-iblinkinfo.sh`, change:

```bash
MOCK_IBPING_SOCKET="${MOCK_IB_PING_SOCKET:-/var/lib/nvml-mock/run/mock-ib.sock}"
```

- [ ] **Step 5: Run unit tests**

Run: `go test -race $(go list ./pkg/network/mockib/... | grep -v vendor)`

Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add pkg/network/mockib/daemon/env.go cmd/mock-ib/main.go \
  deployments/nvml-mock/scripts/start-mock-ib.sh \
  deployments/nvml-mock/scripts/setup.sh \
  deployments/nvml-mock/helm/nvml-mock/templates/daemonset.yaml \
  tests/e2e/validate-ib*.sh tests/e2e/validate-sminfo.sh
git commit -s -m "feat(nvml-mock): expose mock-ib socket on host path"
```

---

## Task 2: PCI Sysfs LD_PRELOAD Shim (`libpcimocksys.so`)

**Files:**
- Create: `pkg/system/mockpcisysfs/c/shim.c`
- Create: `pkg/system/mockpcisysfs/Makefile`
- Create: `pkg/system/mockpcisysfs/shim/shim_test.go`
- Modify: `deployments/nvml-mock/Dockerfile`

- [ ] **Step 1: Write the failing integration test**

Create `pkg/system/mockpcisysfs/shim/shim_test.go`:

```go
//go:build integration

package shim_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/NVIDIA/k8s-test-infra/pkg/system/mockpcisysfs/render"
	"github.com/NVIDIA/k8s-test-infra/pkg/system/mockpcisysfs/config"
	"github.com/stretchr/testify/require"
)

func TestReadlink_PCIRedirect(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("requires linux")
	}
	wd, err := os.Getwd()
	require.NoError(t, err)
	shim := filepath.Join(wd, "..", "libpcimocksys.so")
	if _, err := os.Stat(shim); err != nil {
		t.Skipf("shim not built: %v (run make -C pkg/system/mockpcisysfs)", err)
	}

	root := t.TempDir()
	topo := &config.PCIeTopology{
		RootComplexes: []config.RootComplex{{
			ID:       "pci0000:00",
			NumaNode: 0,
			Devices:  []string{"0000:07:00.0"},
		}},
	}
	require.NoError(t, render.Render(render.Options{Topology: topo, Output: root}))

	cmd := exec.Command("readlink", "/sys/bus/pci/devices/0000:07:00.0")
	cmd.Env = append(os.Environ(),
		"LD_PRELOAD="+shim,
		"MOCK_PCI_ROOT="+root,
	)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "readlink failed: %s", out)
	require.Contains(t, string(out), "pci0000:00/0000:07:00.0")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -tags=integration ./pkg/system/mockpcisysfs/shim/... -v`

Expected: SKIP or FAIL (shim not built)

- [ ] **Step 3: Implement shim (mirror mockib pattern)**

Create `pkg/system/mockpcisysfs/c/shim.c` — copy structure from `pkg/network/mockib/c/shim.c` with these prefixes:

```c
static const char *const k_prefixes[] = {
    "/sys/bus/pci/devices/",
    "/sys/bus/pci/",
    "/sys/devices/pci",
    NULL,
};
```

Activation: enabled when `MOCK_PCI_ROOT` is set and non-empty; no-op otherwise.

Rewrite rule: `MOCK_PCI_ROOT + path` (same as mockib: splice root before absolute path).

Hook at minimum: `readlink`, `readlinkat`, `open`, `openat`, `stat`, `lstat`, `fstatat`, `access`, `faccessat`, `opendir`.

Create `pkg/system/mockpcisysfs/Makefile`:

```makefile
CC      ?= gcc
CFLAGS  ?= -O2 -Wall -Wextra -fPIC -fvisibility=default
LDFLAGS ?= -shared -ldl

LIB_NAME    := libpcimocksys.so
LIB_SONAME  := $(LIB_NAME).1
LIB_VERSION ?= 1.0.0
LIB_REAL    := $(LIB_NAME).$(LIB_VERSION)

.PHONY: all clean
all: $(LIB_NAME)
$(LIB_REAL): c/shim.c
	$(CC) $(CFLAGS) $(LDFLAGS) -Wl,-soname,$(LIB_SONAME) -o $@ $<
$(LIB_NAME): $(LIB_REAL)
	ln -sf $(LIB_REAL) $(LIB_SONAME)
	ln -sf $(LIB_SONAME) $(LIB_NAME)
clean:
	rm -f $(LIB_NAME)*
```

- [ ] **Step 4: Build and run integration test**

Run:

```bash
make -C pkg/system/mockpcisysfs
go test -tags=integration ./pkg/system/mockpcisysfs/shim/... -v
```

Expected: PASS

- [ ] **Step 5: Add to Dockerfile**

In `deployments/nvml-mock/Dockerfile` builder stage, after mockib build:

```dockerfile
RUN cd pkg/system/mockpcisysfs && make clean && make
```

In runtime stage:

```dockerfile
COPY --from=builder /src/pkg/system/mockpcisysfs/libpcimocksys.so.* /usr/local/lib/
```

- [ ] **Step 6: Commit**

```bash
git add pkg/system/mockpcisysfs/ deployments/nvml-mock/Dockerfile
git commit -s -m "feat(mockpcisysfs): add libpcimocksys LD_PRELOAD shim"
```

---

## Task 3: Tiered CDI Spec Generation

**Files:**
- Create: `deployments/nvml-mock/scripts/generate-cdi-specs.sh`
- Modify: `deployments/nvml-mock/scripts/setup.sh`
- Create: `deployments/nvml-mock/scripts/cdi-specs_test.sh`

- [ ] **Step 1: Write CDI generation helper**

Create `deployments/nvml-mock/scripts/generate-cdi-specs.sh` (sourced by setup.sh, not executed standalone). Export functions:

```sh
# Usage: generate_mock_nvml_cdi_edits >> file
generate_mock_nvml_cdi_edits() {
  cat <<'EOF'
    - hostPath: /var/lib/nvml-mock/driver/usr/lib64/libnvidia-ml.so.1
      containerPath: /usr/lib64/libnvidia-ml.so.1
      options: [ro, nosuid, nodev, bind]
    - hostPath: /var/lib/nvml-mock/driver/usr/bin/nvidia-smi
      containerPath: /usr/bin/nvidia-smi
      options: [ro, nosuid, nodev, bind]
    - hostPath: /var/lib/nvml-mock/driver/config/config.yaml
      containerPath: /etc/nvml-mock/config.yaml
      options: [ro, nosuid, nodev, bind]
    - hostPath: /var/lib/nvml-mock/driver/proc/driver/nvidia/version
      containerPath: /proc/driver/nvidia/version
      options: [ro, nosuid, nodev, bind]
    - hostPath: /var/lib/nvml-mock/driver/proc/driver/nvidia/params
      containerPath: /proc/driver/nvidia/params
      options: [ro, nosuid, nodev, bind]
EOF
}

generate_mock_ib_cdi_edits() {
  cat <<'EOF'
    - hostPath: /usr/local/lib/libibmockumad.so.1
      containerPath: /usr/local/lib/libibmockumad.so.1
      options: [ro, nosuid, nodev, bind]
    - hostPath: /usr/local/lib/libibmockverbs.so.1
      containerPath: /usr/local/lib/libibmockverbs.so.1
      options: [ro, nosuid, nodev, bind]
    - hostPath: /usr/local/lib/libibmocksys.so.1
      containerPath: /usr/local/lib/libibmocksys.so.1
      options: [ro, nosuid, nodev, bind]
    - hostPath: /var/lib/nvml-mock/ib
      containerPath: /var/lib/nvml-mock/ib
      options: [ro, nosuid, nodev, bind]
    - hostPath: /var/lib/nvml-mock/run/mock-ib.sock
      containerPath: /var/lib/nvml-mock/run/mock-ib.sock
      options: [rw, nosuid, nodev, bind]
EOF
}

generate_mock_pci_cdi_edits() {
  cat <<'EOF'
    - hostPath: /usr/local/lib/libpcimocksys.so.1
      containerPath: /usr/local/lib/libpcimocksys.so.1
      options: [ro, nosuid, nodev, bind]
EOF
}

write_nvml_mock_cdi_spec() {
  local cdi_dir="$1" tier="$2" include_ib="$3" include_pci="$4"
  local preload ib_env pci_env
  preload=""
  ib_env=""
  pci_env=""
  if [ "$include_ib" = "1" ]; then
    preload="/usr/local/lib/libibmockumad.so.1:/usr/local/lib/libibmockverbs.so.1:/usr/local/lib/libibmocksys.so.1"
    ib_env='
    - MOCK_IB=full
    - MOCK_IB_ROOT=/var/lib/nvml-mock/ib
    - MOCK_IB_PING_SOCKET=/var/lib/nvml-mock/run/mock-ib.sock'
  fi
  if [ "$include_pci" = "1" ]; then
    preload="${preload:+$preload:}/usr/local/lib/libpcimocksys.so.1"
    pci_env='
    - MOCK_PCI_ROOT=/var/lib/nvml-mock'
  fi
  local ld_env=""
  [ -n "$preload" ] && ld_env="
    - LD_PRELOAD=$preload"

  cat > "$cdi_dir/nvml-mock-${tier}.yaml" <<EOF
cdiVersion: "0.6.0"
kind: "nvml-mock.nvidia.com/mock"
devices:
  - name: "${tier}"
    containerEdits:
      mounts:
$(generate_mock_nvml_cdi_edits)
$([ "$include_ib" = "1" ] && generate_mock_ib_cdi_edits)
$([ "$include_pci" = "1" ] && generate_mock_pci_cdi_edits)
      hooks:
        - hookName: createContainer
          path: /usr/bin/nvidia-cdi-hook
          args: [nvidia-cdi-hook, update-ldcache, --folder, /usr/lib64]
      env:
        - MOCK_NVML_CONFIG=/etc/nvml-mock/config.yaml${ld_env}${ib_env}${pci_env}
EOF
}
```

Note: IB shim `.so` files must be copied to `/var/lib/nvml-mock/driver/usr/local/lib/` during setup (or CDI hostPath points at DaemonSet image path on host — prefer copying to host in setup.sh step 2c):

```sh
# In setup.sh after driver lib copy:
mkdir -p "$DRIVER_ROOT/usr/local/lib"
cp /usr/local/lib/libibmock*.so.* /usr/local/lib/libpcimocksys.so.* "$DRIVER_ROOT/usr/local/lib/" 2>/dev/null || true
```

Update CDI hostPaths to use `/var/lib/nvml-mock/driver/usr/local/lib/...` for shims.

- [ ] **Step 2: Wire setup.sh**

At top of setup.sh after HOST definition:

```sh
. /scripts/generate-cdi-specs.sh
```

After existing `nvidia.yaml` generation (step 3b), add:

```sh
write_nvml_mock_cdi_spec "$CDI_DIR" nvml 0 0
write_nvml_mock_cdi_spec "$CDI_DIR" ib   1 0
write_nvml_mock_cdi_spec "$CDI_DIR" full 1 1
```

Extend the existing `nvidia.yaml` `containerEdits` section to append IB + PCI edits (same as `full` tier) in the global `containerEdits` block so GPU pods get the full stack.

- [ ] **Step 3: Write shell test for CDI output**

Create `deployments/nvml-mock/scripts/cdi-specs_test.sh`:

```bash
#!/bin/bash
set -euo pipefail
cd "$(dirname "$0")"
. ./generate-cdi-specs.sh
TMP=$(mktemp -d)
write_nvml_mock_cdi_spec "$TMP" nvml 0 0
write_nvml_mock_cdi_spec "$TMP" ib 1 0
write_nvml_mock_cdi_spec "$TMP" full 1 1
grep -q 'kind: "nvml-mock.nvidia.com/mock"' "$TMP/nvml-mock-ib.yaml"
grep -q 'MOCK_IB=full' "$TMP/nvml-mock-ib.yaml"
grep -q 'MOCK_PCI_ROOT' "$TMP/nvml-mock-full.yaml"
grep -q 'libpcimocksys' "$TMP/nvml-mock-full.yaml"
echo "PASS"
```

Run: `bash deployments/nvml-mock/scripts/cdi-specs_test.sh`

Expected: `PASS`

- [ ] **Step 4: Commit**

```bash
git add deployments/nvml-mock/scripts/generate-cdi-specs.sh \
  deployments/nvml-mock/scripts/setup.sh \
  deployments/nvml-mock/scripts/cdi-specs_test.sh
git commit -s -m "feat(nvml-mock): generate tiered CDI specs for mock injection"
```

---

## Task 4: Mutating Admission Webhook

**Files:**
- Create: `cmd/nvml-mock-injector/tier/tier.go`
- Create: `cmd/nvml-mock-injector/tier/tier_test.go`
- Create: `cmd/nvml-mock-injector/mutate.go`
- Create: `cmd/nvml-mock-injector/mutate_test.go`
- Create: `cmd/nvml-mock-injector/main.go`

- [ ] **Step 1: Write failing tier resolution tests**

Create `cmd/nvml-mock-injector/tier/tier.go`:

```go
package tier

import (
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
)

const (
	AnnotationTier         = "nvml-mock.nvidia.com/tier"
	AnnotationInjectedTier = "nvml-mock.nvidia.com/injected-tier"

	CDIKindMock   = "nvml-mock.nvidia.com/mock"
	CDIAnnotMock  = "cdi.k8s.io/nvml-mock.nvidia.com-mock"
	CDIAnnotGPU   = "cdi.k8s.io/nvidia.com-gpu"
	RuntimeNvidia = "nvidia"

	TierNVML = "nvml"
	TierIB   = "ib"
	TierFull = "full"
	TierGPU  = "gpu"
)

type Config struct {
	GPUOperatorComponents map[string]struct{}
}

func (c Config) Resolve(pod *corev1.Pod) (string, error) {
	if t, ok := pod.Annotations[AnnotationTier]; ok {
		switch strings.ToLower(t) {
		case TierNVML, TierIB, TierFull:
			return strings.ToLower(t), nil
		case TierGPU:
			return "", fmt.Errorf("tier %q cannot be set via annotation; request nvidia.com/gpu instead", t)
		default:
			return "", fmt.Errorf("unknown tier %q", t)
		}
	}
	if component := pod.Labels["app.kubernetes.io/component"]; component != "" {
		if _, ok := c.GPUOperatorComponents[component]; ok {
			return TierGPU, nil
		}
	}
	for _, ctn := range pod.Spec.Containers {
		if qty, ok := ctn.Resources.Limits[corev1.ResourceName("nvidia.com/gpu")]; ok && !qty.IsZero() {
			return TierGPU, nil
		}
		if qty, ok := ctn.Resources.Requests[corev1.ResourceName("nvidia.com/gpu")]; ok && !qty.IsZero() {
			return TierGPU, nil
		}
	}
	for _, ctn := range pod.Spec.InitContainers {
		if qty, ok := ctn.Resources.Limits[corev1.ResourceName("nvidia.com/gpu")]; ok && !qty.IsZero() {
			return TierGPU, nil
		}
	}
	return "", nil
}

func CDIDeviceForTier(t string) string {
	switch t {
	case TierGPU, TierFull:
		return TierFull
	case TierIB:
		return TierIB
	case TierNVML:
		return TierNVML
	default:
		return ""
	}
}
```

Create `cmd/nvml-mock-injector/tier/tier_test.go` with table tests for opt-in tiers, GPU resource, GPU Operator label, and rejection of `tier=gpu`.

- [ ] **Step 2: Run tests — verify fail then pass**

Run: `go test -race ./cmd/nvml-mock-injector/tier/... -v`

- [ ] **Step 3: Implement mutate.go**

```go
package main

import (
	"encoding/json"
	"fmt"
	"net/http"

	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/NVIDIA/k8s-test-infra/cmd/nvml-mock-injector/tier"
)

func mutatePod(pod *corev1.Pod, cfg tier.Config) (*corev1.Pod, error) {
	t, err := cfg.Resolve(pod)
	if err != nil {
		return nil, err
	}
	if t == "" {
		return pod, nil
	}
	if pod.Annotations == nil {
		pod.Annotations = map[string]string{}
	}
	device := tier.CDIDeviceForTier(t)
	pod.Annotations[tier.CDIAnnotMock] = device
	pod.Annotations[tier.AnnotationInjectedTier] = t
	if t == tier.TierGPU {
		pod.Annotations[tier.CDIAnnotGPU] = "all"
	}
	if pod.Spec.RuntimeClassName == nil || *pod.Spec.RuntimeClassName == "" {
		nvidia := tier.RuntimeNvidia
		pod.Spec.RuntimeClassName = &nvidia
	}
	return pod, nil
}

func patchResponse(uid types.UID, pod *corev1.Pod) (*admissionv1.AdmissionResponse, error) {
	patch, err := json.Marshal(pod)
	if err != nil {
		return nil, err
	}
	patchType := admissionv1.PatchTypeJSONPatch
	return &admissionv1.AdmissionResponse{
		UID:       uid,
		Allowed:   true,
		PatchType: &patchType,
		Patch:     patch,
	}, nil
}
```

Use JSON merge patch or strategic merge — for simplicity use `jsonpatch` to add annotations. Prefer `github.com/evanphx/json-patch` or manual RFC6902 patches. Minimal approach: marshal original + modified and use `jsonpatch.CreateMergePatch` from vendor.

Write `mutate_test.go` testing opt-in `ib` pod gets `cdi.k8s.io/nvml-mock.nvidia.com-mock: ib` and `runtimeClassName: nvidia`.

- [ ] **Step 4: Implement main.go HTTP handler**

```go
func main() {
	components := strings.Split(os.Getenv("GPU_OPERATOR_COMPONENTS"), ",")
	cfg := tier.Config{GPUOperatorComponents: map[string]struct{}{}}
	for _, c := range components {
		if c = strings.TrimSpace(c); c != "" {
			cfg.GPUOperatorComponents[c] = struct{}{}
		}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/mutate", func(w http.ResponseWriter, r *http.Request) {
		// decode AdmissionReview, mutate, respond
	})
	server := &http.Server{
		Addr:      ":8443",
		Handler:   mux,
		TLSConfig: loadTLS(),
	}
	log.Fatal(server.ListenAndServeTLS(certFile, keyFile))
}
```

Scope filter: if pod has `spec.nodeName` set and node lacks `nvidia.com/gpu.present=true`, skip mutation. For scheduling-time mutation (no nodeName yet), always mutate when tier annotation present or GPU request present.

- [ ] **Step 5: Add Dockerfile build target**

In builder stage:

```dockerfile
&& go build -mod=vendor -o /out/nvml-mock-injector ./cmd/nvml-mock-injector
```

Copy to `/usr/local/bin/nvml-mock-injector` in runtime image.

- [ ] **Step 6: Run unit tests**

Run: `go test -race ./cmd/nvml-mock-injector/... -v`

Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add cmd/nvml-mock-injector/ deployments/nvml-mock/Dockerfile
git commit -s -m "feat(nvml-mock): add tiered CDI mutating admission webhook"
```

---

## Task 5: Helm Chart — Injector Deployment

**Files:**
- Create: `deployments/nvml-mock/helm/nvml-mock/templates/injector-deployment.yaml`
- Create: `deployments/nvml-mock/helm/nvml-mock/templates/injector-service.yaml`
- Create: `deployments/nvml-mock/helm/nvml-mock/templates/injector-webhook.yaml`
- Create: `deployments/nvml-mock/helm/nvml-mock/templates/injector-rbac.yaml`
- Create: `deployments/nvml-mock/helm/nvml-mock/templates/injector-tls-secret.yaml`
- Create: `deployments/nvml-mock/helm/nvml-mock/tests/injector_test.yaml`
- Modify: `deployments/nvml-mock/helm/nvml-mock/values.yaml`

- [ ] **Step 1: Add values**

In `values.yaml`:

```yaml
injector:
  enabled: true
  replicaCount: 1
  port: 8443
  gpuOperator:
    operandComponents:
      - nvidia-device-plugin
      - gpu-feature-discovery
      - nvidia-operator-validator
  certManager:
    enabled: false
  resources: {}
```

- [ ] **Step 2: Create webhook templates**

`injector-webhook.yaml` — `MutatingWebhookConfiguration` with:
- `failurePolicy: Fail` when pod has `nvml-mock.nvidia.com/tier` annotation (use `matchConditions` or objectSelector)
- `failurePolicy: Ignore` as default for unannotated pods
- `namespaceSelector: {}` (cluster-wide)
- `rules`: CREATE on pods
- `clientConfig.service`: `{{ fullname }}-injector`

Use Helm `genSelfSignedCert` in `injector-tls-secret.yaml` when `certManager.enabled=false`:

```yaml
{{- if and .Values.injector.enabled (not .Values.injector.certManager.enabled) }}
{{- $cn := printf "%s-injector.%s.svc" (include "nvml-mock.fullname" .) .Release.Namespace }}
{{- $ca := genSelfSignedCert $cn nil (list $cn) (list (printf "%s-injector" (include "nvml-mock.fullname" .))) 3650 }}
apiVersion: v1
kind: Secret
metadata:
  name: {{ include "nvml-mock.fullname" . }}-injector-tls
type: kubernetes.io/tls
data:
  ca.crt: {{ $ca.Cert | b64enc }}
  tls.crt: {{ $ca.Cert | b64enc }}
  tls.key: {{ $ca.Key | b64enc }}
{{- end }}
```

- [ ] **Step 3: Write Helm unittest**

Create `deployments/nvml-mock/helm/nvml-mock/tests/injector_test.yaml`:

```yaml
suite: injector
templates:
  - injector-deployment.yaml
  - injector-webhook.yaml
tests:
  - it: should not render when injector disabled
    set:
      injector.enabled: false
    asserts:
      - hasDocuments:
          count: 0
  - it: should render webhook when enabled
    set:
      injector.enabled: true
    asserts:
      - isKind:
          of: MutatingWebhookConfiguration
      - equal:
          path: webhooks[0].clientConfig.service.name
          value: RELEASE-NAME-nvml-mock-injector
```

Run: `helm unittest deployments/nvml-mock/helm/nvml-mock`

Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add deployments/nvml-mock/helm/nvml-mock/
git commit -s -m "feat(nvml-mock): helm chart for injection webhook"
```

---

## Task 6: E2E Injection Tests

**Files:**
- Create: `tests/e2e/injection-test-pods.yaml`
- Create: `tests/e2e/validate-injection-tier.sh`
- Modify: `.github/workflows/nvml-mock-e2e.yaml` (add job step if appropriate)

- [ ] **Step 1: Create test pod manifests**

`tests/e2e/injection-test-pods.yaml`:

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: injection-tier-nvml
  annotations:
    nvml-mock.nvidia.com/tier: nvml
spec:
  restartPolicy: Never
  containers:
    - name: test
      image: ubuntu:22.04
      command: ["sh", "-c", "nvidia-smi -L && test ! -e /dev/nvidia0"]
---
apiVersion: v1
kind: Pod
metadata:
  name: injection-tier-ib
  annotations:
    nvml-mock.nvidia.com/tier: ib
spec:
  restartPolicy: Never
  containers:
    - name: test
      image: ghcr.io/nvidia/nvml-mock:latest
      command: ["sh", "-c", "nvidia-smi -L && ibstat -l"]
---
apiVersion: v1
kind: Pod
metadata:
  name: injection-tier-full
  annotations:
    nvml-mock.nvidia.com/tier: full
spec:
  restartPolicy: Never
  containers:
    - name: test
      image: ghcr.io/nvidia/nvml-mock:latest
      command:
        - sh
        - -c
        - |
          readlink /sys/bus/pci/devices/0000:07:00.0 || readlink /sys/bus/pci/devices/0000:03:00.0
```

- [ ] **Step 2: Create validation script**

`tests/e2e/validate-injection-tier.sh`:

```bash
#!/bin/bash
set -euo pipefail
TIER="${1:?Usage: $0 <nvml|ib|full>}"
POD="injection-tier-${TIER}"
kubectl apply -f tests/e2e/injection-test-pods.yaml
kubectl wait --for=condition=Ready "pod/${POD}" --timeout=120s 2>/dev/null || true
PHASE=$(kubectl get pod "$POD" -o jsonpath='{.status.phase}')
if [ "$PHASE" != "Succeeded" ]; then
  kubectl logs "$POD" || true
  echo "FAIL: pod $POD phase=$PHASE"
  exit 1
fi
INJECTED=$(kubectl get pod "$POD" -o jsonpath='{.metadata.annotations.nvml-mock\.nvidia\.com/injected-tier}')
if [ "$INJECTED" != "$TIER" ]; then
  echo "FAIL: expected injected-tier=$TIER got $INJECTED"
  exit 1
fi
echo "PASS: injection tier $TIER"
kubectl delete -f tests/e2e/injection-test-pods.yaml --ignore-not-found
```

- [ ] **Step 3: Manual Kind smoke test (document in plan)**

```bash
kind create cluster --name inject-test --config tests/e2e/kind-gpu-operator-config.yaml
# install nvidia-container-toolkit in node per existing e2e workflow
helm install nvml-mock deployments/nvml-mock/helm/nvml-mock --set injector.enabled=true --wait
tests/e2e/validate-injection-tier.sh nvml
tests/e2e/validate-injection-tier.sh ib
```

- [ ] **Step 4: Commit**

```bash
git add tests/e2e/injection-test-pods.yaml tests/e2e/validate-injection-tier.sh
git commit -s -m "test(e2e): add tiered injection validation scripts"
```

---

## Task 7: Documentation

**Files:**
- Modify: `deployments/nvml-mock/helm/nvml-mock/README.md`
- Modify: `docs/quickstart.md`

- [ ] **Step 1: Add "Pod Injection Tiers" section to Helm README**

Document:
- Tier matrix table
- Opt-in annotation example
- Auto-injection for `nvidia.com/gpu`
- Requirement: Kind cluster with CDI + nvidia runtime
- `injector.enabled` value

- [ ] **Step 2: Add quickstart snippet**

In `docs/quickstart.md`, add Option 4: Discovery pod with tier annotation.

- [ ] **Step 3: Commit**

```bash
git add deployments/nvml-mock/helm/nvml-mock/README.md docs/quickstart.md
git commit -s -m "docs: document tiered pod injection for nvml-mock"
```

---

## Spec Coverage Checklist

| Spec requirement | Task |
|------------------|------|
| Tiered CDI specs | Task 3 |
| Extended nvidia.yaml (GPU tier) | Task 3 |
| PCI shim | Task 2 |
| Host mock-ib socket | Task 1 |
| Admission webhook | Task 4 |
| Helm injector | Task 5 |
| E2E per tier | Task 6 |
| GPU Operator regression | Task 6 (existing e2e-gpu-operator job) |
| DCGM deferred | N/A |
| Error: invalid tier | Task 4 tests |
| Audit annotation | Task 4 |

## Self-Review Notes

- IB shim CDI hostPaths use `/var/lib/nvml-mock/driver/usr/local/lib/` after setup.sh copies shims to host — verify copy step in Task 3.
- Webhook JSON patch implementation must use RFC6902 or merge patch compatible with apiserver — test with `kubectl apply` early.
- `failurePolicy: Fail` only for annotated pods requires `matchConditions` (K8s 1.28+) or split into two webhooks.
- GPU tier CDI device index: start with `"all"`; refine to specific GPU index in follow-up if device plugin expects it.
