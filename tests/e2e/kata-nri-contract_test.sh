#!/usr/bin/env bash
# Copyright 2026 NVIDIA CORPORATION
# SPDX-License-Identifier: Apache-2.0
set -euo pipefail
ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
WORKFLOW="$ROOT/.github/workflows/nvml-mock-e2e.yaml"
KIND_CONFIG="$ROOT/tests/e2e/kind-kata-config.yaml"
RUNNER="$ROOT/tests/e2e/run-kata-nri.sh"
DOC="$ROOT/docs/integrations/kata.md"
fail() { echo "FAIL: $*" >&2; exit 1; }
contains() { grep -Fq -- "$2" "$1" || fail "$1 does not contain: $2"; }
contains_text() { grep -Fq -- "$3" <<<"$2" || fail "$1 does not contain: $3"; }
not_contains_text() { ! grep -Fq -- "$3" <<<"$2" || fail "$1 still contains: $3"; }
not_matches_text() { ! grep -Eqi -- "$3" <<<"$2" || fail "$1 still matches prohibited pattern: $3"; }
count_text() {
  local label=$1 text=$2 expected=$3 needle=$4 actual
  actual=$(grep -Fc -- "$needle" <<<"$text" || true)
  [[ "$actual" == "$expected" ]] || fail "$label contains '$needle' $actual times, expected $expected"
}
extract_kata_job() {
  awk '
    /^  e2e-kata:$/ { found=1 }
    found && /^  [[:alnum:]_-]+:$/ && $0 != "  e2e-kata:" { exit }
    found { print }
  ' "$WORKFLOW"
}
extract_manifest() {
  local pod=$1
  awk -v pod="$pod" '
    $0 == "  name: " pod { found=1 }
    found { print }
    found && /^PODEOF$/ { exit }
  ' "$RUNNER"
}
extract_section() {
  local pod=$1 next_pod=${2:-}
  awk -v pod="$pod" -v next_pod="$next_pod" '
    $0 == "  name: " pod { found=1 }
    found && next_pod != "" && $0 == "  name: " next_pod { exit }
    found { print }
  ' "$RUNNER"
}
extract_kata_step() {
  local step=$1
  awk -v step="$step" '
    $0 == "      - name: " step { found=1 }
    found && /^      - name:/ && $0 != "      - name: " step { exit }
    found { print }
  ' <<<"$KATA_JOB"
}
extract_doc_section() {
  local heading=$1
  awk -v heading="## $heading" '
    $0 == heading { found=1 }
    found && /^## / && $0 != heading { exit }
    found { print }
  ' "$DOC"
}
extract_fenced_yaml() {
  awk '
    /^```yaml$/ { yaml=1; next }
    yaml && /^```$/ { yaml=0; next }
    yaml { print }
  ' <<<"$1"
}

test -x "$RUNNER" || fail "$RUNNER is missing or not executable"
RUNNER_TEXT=$(<"$RUNNER")
KATA_JOB=$(extract_kata_job)
FAILURE_COLLECTOR=$(extract_kata_step 'Collect kata logs on failure')
AMBIENT_MANIFEST=$(extract_manifest kata-nri-ambient)
DEVICE_MANIFEST=$(extract_manifest kata-nri-devices)
OPTOUT_MANIFEST=$(extract_manifest kata-nri-optout)
AMBIENT_SECTION=$(extract_section kata-nri-ambient kata-nri-devices)
DEVICE_SECTION=$(extract_section kata-nri-devices kata-nri-optout)
OPTOUT_SECTION=$(extract_section kata-nri-optout)
DOC_TEXT=$(<"$DOC")
DELIVERY_DOC=$(extract_doc_section 'Delivery model')
REQUIREMENTS_DOC=$(extract_doc_section Requirements)
AMBIENT_DOC=$(extract_doc_section 'Ambient workload')
DEVICES_DOC=$(extract_doc_section 'Optional mock device nodes')
OPTOUT_DOC=$(extract_doc_section 'Opting out')
VERIFY_DOC=$(extract_doc_section 'Verifying the runtime')
COCO_DOC=$(extract_doc_section 'Plain Kata versus Confidential Containers')
AMBIENT_YAML=$(extract_fenced_yaml "$AMBIENT_DOC")
DOC_YAML=$(extract_fenced_yaml "$DOC_TEXT")
DEVICES_FLAT=$(tr '\n' ' ' <<<"$DEVICES_DOC")
DOC_FLAT=$(tr '\n' ' ' <<<"$DOC_TEXT")

contains "$KIND_CONFIG" '[plugins."io.containerd.nri.v1.nri"]'
contains "$KIND_CONFIG" 'socket_path = "/var/run/nri/nri.sock"'
contains "$KIND_CONFIG" 'hostPath: /dev/kvm'
contains "$KIND_CONFIG" 'hostPath: /dev/vhost-vsock'

contains_text "e2e-kata job" "$KATA_JOB" '--set nri.enabled=true'
contains_text "e2e-kata job" "$KATA_JOB" "--namespace \"\$NVML_MOCK_NAMESPACE\""
contains_text "e2e-kata job" "$KATA_JOB" './tests/e2e/run-kata-nri.sh'
contains_text "e2e-kata job" "$KATA_JOB" 'configured by runtime'
contains_text "e2e-kata job" "$KATA_JOB" '/dev/kvm'
contains_text "e2e-kata job" "$KATA_JOB" '/dev/vhost-vsock'
contains_text "e2e-kata job" "$KATA_JOB" 'remount,size=8G'
contains_text "e2e-kata job" "$KATA_JOB" '--set debug=true'
contains_text "e2e-kata job" "$KATA_JOB" "if [ \"\$GUEST_KERNEL\" = \"\$NODE_KERNEL\" ]; then"
not_contains_text "e2e-kata job" "$KATA_JOB" 'device-plugin-kata'
test ! -e "$ROOT/tests/e2e/device-plugin-kata.yaml" || fail "obsolete device-plugin-kata.yaml still exists"

contains_text "Kata failure collector" "$FAILURE_COLLECTOR" "kubectl -n default describe pod \"\$pod\""
contains_text "Kata failure collector" "$FAILURE_COLLECTOR" "kubectl -n default logs \"\$pod\""
contains_text "Kata failure collector" "$FAILURE_COLLECTOR" \
  'for pod in kata-smoke kata-nri-ambient kata-nri-devices kata-nri-optout; do'
not_contains_text "Kata failure collector" "$FAILURE_COLLECTOR" 'kubectl describe pod'
not_contains_text "Kata failure collector" "$FAILURE_COLLECTOR" "kubectl logs \"\$pod\""

contains_text "ambient manifest" "$AMBIENT_MANIFEST" 'namespace: default'
contains_text "ambient manifest" "$AMBIENT_MANIFEST" 'runtimeClassName: kata-qemu'
contains_text "ambient manifest" "$AMBIENT_MANIFEST" 'test -d /opt/nvml-mock'
contains_text "ambient manifest" "$AMBIENT_MANIFEST" 'test -L /opt/nvml-mock/driver/usr/lib64/libnvidia-ml.so.1'
contains_text "ambient manifest" "$AMBIENT_MANIFEST" 'test -r /opt/nvml-mock/driver/config/config.yaml'
contains_text "ambient manifest" "$AMBIENT_MANIFEST" 'if touch /opt/nvml-mock/.write-test 2>/dev/null; then exit 1; fi'
contains_text "ambient manifest" "$AMBIENT_MANIFEST" '! ls /dev/nvidia[0-9]* >/dev/null 2>&1'
not_contains_text "ambient manifest" "$AMBIENT_MANIFEST" 'annotations:'
not_contains_text "ambient manifest" "$AMBIENT_MANIFEST" 'MOCK_'
not_contains_text "ambient manifest" "$AMBIENT_MANIFEST" 'env:'
not_contains_text "ambient manifest" "$AMBIENT_MANIFEST" 'volumeMounts:'
not_contains_text "ambient manifest" "$AMBIENT_MANIFEST" 'volumes:'
not_contains_text "ambient manifest" "$AMBIENT_MANIFEST" 'resources:'
not_contains_text "ambient manifest" "$AMBIENT_MANIFEST" 'nvidia.com/gpu'
contains_text "ambient section" "$AMBIENT_SECTION" 'kubectl -n default logs kata-nri-ambient'
contains_text "ambient section" "$AMBIENT_SECTION" 'kubectl -n default delete pod kata-nri-ambient'
contains_text "ambient section" "$AMBIENT_SECTION" "[[ \"\$GUEST_KERNEL\" != \"\$NODE_KERNEL\" ]]"
contains_text "ambient section" "$AMBIENT_SECTION" "[[ \"\$GPU_COUNT\" == 2 ]]"
contains_text "ambient section" "$AMBIENT_SECTION" \
  "grep -qF \"NVIDIA A100-SXM4-40GB\" <<<\"\$AMBIENT_LOGS\""

contains_text "device manifest" "$DEVICE_MANIFEST" 'namespace: default'
contains_text "device manifest" "$DEVICE_MANIFEST" 'runtimeClassName: kata-qemu'
contains_text "device manifest" "$DEVICE_MANIFEST" 'nvml-mock.nvidia.com/devices: "true"'
for node in /dev/nvidia0 /dev/nvidia1 /dev/nvidiactl /dev/nvidia-uvm /dev/nvidia-uvm-tools; do
  contains_text "device manifest" "$DEVICE_MANIFEST" "$node"
done
contains_text "device manifest" "$DEVICE_MANIFEST" "test -c \"\$node\""
contains_text "device manifest" "$DEVICE_MANIFEST" 'nvidia-smi -L'
contains_text "device section" "$DEVICE_SECTION" 'kubectl -n default logs kata-nri-devices'
contains_text "device section" "$DEVICE_SECTION" 'kubectl -n default delete pod kata-nri-devices'

contains_text "opt-out manifest" "$OPTOUT_MANIFEST" 'namespace: default'
contains_text "opt-out manifest" "$OPTOUT_MANIFEST" 'runtimeClassName: kata-qemu'
contains_text "opt-out manifest" "$OPTOUT_MANIFEST" 'nvml-mock.nvidia.com/inject: "false"'
contains_text "opt-out manifest" "$OPTOUT_MANIFEST" '! test -e /opt/nvml-mock'
contains_text "opt-out manifest" "$OPTOUT_MANIFEST" '! command -v nvidia-smi'
contains_text "opt-out manifest" "$OPTOUT_MANIFEST" 'echo CLEAN'
contains_text "opt-out section" "$OPTOUT_SECTION" 'wait_for_success kata-nri-optout 24'
contains_text "opt-out section" "$OPTOUT_SECTION" 'kubectl -n default logs kata-nri-optout'
contains_text "opt-out section" "$OPTOUT_SECTION" 'kubectl -n default delete pod kata-nri-optout'

count_text "runner" "$RUNNER_TEXT" 3 'kubectl -n default apply -f -'
contains "$RUNNER" "kubectl -n default get pod \"\$pod\""
contains "$RUNNER" "kubectl -n default describe pod \"\$pod\""
contains "$RUNNER" "kubectl -n default logs \"\$pod\""
not_contains_text "runner" "$RUNNER_TEXT" 'kubectl apply'
not_contains_text "runner" "$RUNNER_TEXT" 'kubectl get pod'
not_contains_text "runner" "$RUNNER_TEXT" 'kubectl describe pod'
not_contains_text "runner" "$RUNNER_TEXT" 'kubectl logs'
not_contains_text "runner" "$RUNNER_TEXT" 'kubectl delete pod'
not_contains_text "runner" "$RUNNER_TEXT" 'nvidia.com/gpu'

not_matches_text "Kata guide" "$DOC_TEXT" '(^|[^[:alnum:]])CDI([^[:alnum:]]|$)|cdi-cri|device[- ]plugin'
not_matches_text "Kata guide" "$DOC_TEXT" 'nvidia\.com/gpu:[[:space:]]*[0-9]'
not_matches_text "Kata guide" "$DOC_TEXT" '(export[[:space:]]+MOCK_|name:[[:space:]]*MOCK_|MOCK_[A-Z0-9_]+[[:space:]]*=)'
not_matches_text "Kata guide" "$DOC_FLAT" '(manual.{0,120}(soname|library[ -]?link|symbolic[ -]?link|symlink)|(create|add|make).{0,120}(soname|libnvidia-ml\.so\.1)|ln[[:space:]]+-s(f)?[[:space:]])'
not_contains_text "Kata guide YAML examples" "$DOC_YAML" 'nvidia.com/gpu'
not_contains_text "Kata guide YAML examples" "$DOC_YAML" 'MOCK_'
not_contains_text "Kata guide YAML examples" "$DOC_YAML" 'env:'
not_contains_text "Kata guide YAML examples" "$DOC_YAML" 'resources:'
not_contains_text "Kata guide YAML examples" "$DOC_YAML" 'volumeMounts:'
not_contains_text "Kata guide YAML examples" "$DOC_YAML" 'volumes:'
not_contains_text "Kata guide YAML examples" "$DOC_YAML" 'hostPath:'

contains_text "Kata delivery model" "$DELIVERY_DOC" '/opt/nvml-mock'
contains_text "Kata delivery model" "$DELIVERY_DOC" 'read-only'
contains_text "Kata delivery model" "$DELIVERY_DOC" 'NODE_NAME'
contains_text "Kata delivery model" "$DELIVERY_DOC" 'MOCK_TOPOLOGY_CONFIG'
contains_text "Kata delivery model" "$DELIVERY_DOC" 'topology.enabled=true'
contains_text "Kata delivery model" "$DELIVERY_DOC" 'defaults to `false`'
contains_text "Kata delivery model" "$DELIVERY_DOC" 'perform manual library setup'

contains_text "Kata requirements" "$REQUIREMENTS_DOC" '/dev/kvm'
contains_text "Kata requirements" "$REQUIREMENTS_DOC" 'vhost_vsock'
contains_text "Kata requirements" "$REQUIREMENTS_DOC" '/dev/vhost-vsock'
contains_text "Kata requirements" "$REQUIREMENTS_DOC" '/dev/shm'
contains_text "Kata requirements" "$REQUIREMENTS_DOC" '8 GiB'
contains_text "Kata requirements" "$REQUIREMENTS_DOC" 'remount,size=8G'
contains_text "Kata requirements" "$REQUIREMENTS_DOC" '/var/run/nri/nri.sock'
contains_text "Kata requirements" "$REQUIREMENTS_DOC" '--set debug=true'
contains_text "Kata requirements" "$REQUIREMENTS_DOC" 'nvml-mock-system'
contains_text "Kata requirements" "$REQUIREMENTS_DOC" 'nri.enabled=true'

contains_text "Kata ambient guide" "$AMBIENT_DOC" 'runtimeClassName: kata-qemu'
contains_text "Kata ambient guide" "$AMBIENT_DOC" 'namespace: default'
contains_text "Kata ambient YAML" "$AMBIENT_YAML" \
  'command: ["sh", "-c", "echo GUEST_KERNEL=$(uname -r); nvidia-smi -L"]'
not_contains_text "Kata ambient YAML" "$AMBIENT_YAML" 'nvidia.com/gpu'
not_contains_text "Kata ambient YAML" "$AMBIENT_YAML" 'annotations:'
not_contains_text "Kata ambient YAML" "$AMBIENT_YAML" 'MOCK_'
not_contains_text "Kata ambient YAML" "$AMBIENT_YAML" 'env:'
not_contains_text "Kata ambient YAML" "$AMBIENT_YAML" 'resources:'
not_contains_text "Kata ambient YAML" "$AMBIENT_YAML" 'volumeMounts:'
not_contains_text "Kata ambient YAML" "$AMBIENT_YAML" 'volumes:'

contains_text "Kata optional device guide" "$DEVICES_DOC" 'nvml-mock.nvidia.com/devices: "true"'
contains_text "Kata optional device guide" "$DEVICES_DOC" 'trusted workload namespaces'
contains_text "Kata optional device guide" "$DEVICES_FLAT" 'Helm release namespace'
contains_text "Kata optional device guide" "$DEVICES_DOC" '`kube-system`'
contains_text "Kata optional device guide" "$DEVICES_DOC" 'excluded automatically'
contains_text "Kata opt-out guide" "$OPTOUT_DOC" 'nvml-mock.nvidia.com/inject: "false"'
contains_text "Kata opt-out guide" "$OPTOUT_DOC" '/opt/nvml-mock'
contains_text "Kata opt-out guide" "$OPTOUT_DOC" 'injected environment'

contains_text "Kata runtime verification" "$VERIFY_DOC" 'NODE_KERNEL='
contains_text "Kata runtime verification" "$VERIFY_DOC" \
  "GUEST_KERNEL=\$(kubectl logs kata-nvml | sed -n 's/^GUEST_KERNEL=//p')"
contains_text "Kata runtime verification" "$VERIFY_DOC" 'test "$GUEST_KERNEL" != "$NODE_KERNEL"'
contains_text "Kata runtime verification" "$VERIFY_DOC" 'guest kernel must differ from the node kernel'
not_contains_text "Kata runtime verification" "$VERIFY_DOC" 'kubectl exec'

contains_text "Kata/CoCo boundary" "$COCO_DOC" 'Plain `kata-qemu`'
contains_text "Kata/CoCo boundary" "$COCO_DOC" 'host filesystem sharing'
contains_text "Kata/CoCo boundary" "$COCO_DOC" 'Confidential Containers'
contains_text "Kata/CoCo boundary" "$COCO_DOC" 'future guest payload'
contains_text "Kata/CoCo boundary" "$COCO_DOC" 'not part of the NRI-native lane'

contains "$DOC" '](../../.github/workflows/nvml-mock-e2e.yaml)'
contains "$DOC" '](../../tests/e2e/kind-kata-config.yaml)'
contains "$DOC" '](../../tests/e2e/run-kata-nri.sh)'
echo "PASS: Kata NRI repository contracts"
