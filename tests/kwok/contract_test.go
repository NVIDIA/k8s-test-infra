// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package kwok_test

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"sigs.k8s.io/yaml"

	mokkav1alpha1 "github.com/NVIDIA/k8s-test-infra/internal/controlplane/api/v1alpha1"
	"github.com/NVIDIA/k8s-test-infra/internal/mokka/materialize"
)

func TestTemplatesRenderValidScaleResources(t *testing.T) {
	profileData := readFile(t, "profile.yaml")
	profileData = strings.ReplaceAll(profileData, "__NODES_PER_RACK__", "100")
	var profile mokkav1alpha1.SGPURackProfile
	require.NoError(t, yaml.Unmarshal([]byte(profileData), &profile))
	require.Equal(t, int32(100), profile.Spec.Rack.NodesPerRack)
	require.NoError(t, materialize.ValidateProfile(profile.Spec))

	inventoryData := readFile(t, "inventory.yaml")
	inventoryData = strings.ReplaceAll(inventoryData, "__RACK_COUNT__", "10")
	var inventory mokkav1alpha1.SGPUInventory
	require.NoError(t, yaml.Unmarshal([]byte(inventoryData), &inventory))
	require.Equal(t, int32(10), inventory.Spec.RackGroups[0].Count)
	require.Equal(t, "kwok-scale", inventory.Spec.RackGroups[0].Placement.NodeSelector.MatchLabels["mokka.nvidia.com/pool"])

	nodeData := strings.ReplaceAll(readFile(t, "node-resource.yaml"), "__CLUSTER_LABEL__", "owned-cluster")
	var resource struct {
		APIVersion string `yaml:"apiVersion"`
		Kind       string `yaml:"kind"`
		Metadata   struct {
			Name string `yaml:"name"`
		} `yaml:"metadata"`
		Template string `yaml:"template"`
	}
	require.NoError(t, yaml.Unmarshal([]byte(nodeData), &resource))
	require.Equal(t, "config.kwok.x-k8s.io/v1alpha1", resource.APIVersion)
	require.Equal(t, "KwokctlResource", resource.Kind)
	require.Equal(t, "mokka-node", resource.Metadata.Name)
	require.Contains(t, resource.Template, "kwok.x-k8s.io/node: fake")
	require.Contains(t, resource.Template, `mokka.nvidia.com/sgpu-node: "true"`)
	require.Contains(t, resource.Template, "tests.mokka.nvidia.com/kwok-cluster: owned-cluster")
	require.Contains(t, resource.Template, "name: {{ Name }}")
}

func TestNodeResourceDefinesParameters(t *testing.T) {
	var resource struct {
		Parameters map[string]any `yaml:"parameters"`
	}
	require.NoError(t, yaml.Unmarshal([]byte(readFile(t, "node-resource.yaml")), &resource))
	require.NotNil(t, resource.Parameters)
}

func TestRunnerContract(t *testing.T) {
	runnerPath := filepath.Join(testDir(t), "run.sh")
	runnerData, err := os.ReadFile(runnerPath)
	require.NoError(t, err)
	runner := string(runnerData)

	require.Contains(t, runner, `readonly KWOK_VERSION="v0.8.0"`)
	require.Contains(t, runner, `--runtime=docker`)
	require.Contains(t, runner, `--kubeconfig="${KUBECONFIG_PATH}"`)
	require.Contains(t, runner, `--components=etcd,kube-apiserver`)
	require.NotContains(t, runner, `--disable-qps-limits`)
	require.NotContains(t, runner, `--kube-controller-manager-image`)
	require.NotContains(t, runner, `--kwok-controller-image`)
	require.Contains(t, runner, `"${CONTROLLER_BIN}"`)
	require.Contains(t, runner, `--leader-election-name=mokka-controller-kwok`)
	require.Contains(t, runner, `kwok scale mokka-node --replicas`)
	require.Contains(t, runner, `rack_statuses_ready() {`)
	require.Contains(t, runner, `wait_for "rack statuses for ${state}" rack_statuses_ready "${expected_racks}"`)
	require.NotContains(t, runner, `--show-error`)
	require.NotContains(t, runner, "curl | sh")
	require.NotContains(t, runner, "helm install")
	require.NotContains(t, runner, "deployment/mokka-controller")
	require.Contains(t, runner, `trap '' INT TERM`)
	require.NotContains(t, runner, `trap - EXIT INT TERM`)

	repoDir := filepath.Join(testDir(t), "..", "..")
	for _, packagePath := range []string{"./cmd/control-plane", "./tests/kwok/cmd/kwok-assert"} {
		require.Contains(t, runner, packagePath)
		command := exec.Command("go", "list", packagePath)
		command.Dir = repoDir
		output, err := command.CombinedOutput()
		require.NoError(t, err, string(output))
	}

	makefileData, err := os.ReadFile(filepath.Join(repoDir, "Makefile"))
	require.NoError(t, err)
	makefile := string(makefileData)
	require.Contains(t, makefile, `test "$(KWOK_SCALE)" = 1`)
	require.Contains(t, makefile, `KWOK_NODE_COUNT ?= 200`)
	require.NotContains(t, makefile, `export PATH := $(GOBIN):$(PATH)`)
	require.NotContains(t, makefile, "verify: kwok-scale")

	info, err := os.Stat(runnerPath)
	require.NoError(t, err)
	require.NotZero(t, info.Mode()&0o111)

	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash is not installed")
	}
	output, err := exec.Command(bash, "-n", runnerPath).CombinedOutput()
	require.NoError(t, err, string(output))
}

func TestControllerReadinessExitsWhenControllerDies(t *testing.T) {
	t.Parallel()

	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash is not installed")
	}

	tempDir := t.TempDir()
	controllerLog := filepath.Join(tempDir, "controller.log")
	require.NoError(t, os.WriteFile(controllerLog, []byte("controller startup failed: test marker\n"), 0o600))

	runner := readFile(t, "run.sh")
	script := strings.Join([]string{
		"set -u",
		`log() { printf '%s\n' "$*"; }`,
		extractBashFunction(t, runner, "wait_for"),
		extractBashFunction(t, runner, "controller_ready"),
		`wait_for "host controller readiness" controller_ready`,
	}, "\n")

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, bash, "-c", script)
	command.Env = append(os.Environ(),
		"TIMEOUT_SECONDS=30",
		"CONTROLLER_LOG="+controllerLog,
		"CONTROLLER_PID=99999999",
		"CONTROLLER_PORT=18081",
	)
	output, err := command.CombinedOutput()
	require.Error(t, err)
	require.NoError(t, ctx.Err(), "controller death was retried until the subprocess deadline: %s", output)
	require.Contains(t, string(output), "controller PID 99999999 exited before becoming ready")
	require.Contains(t, string(output), "controller startup failed: test marker")
}

func TestControllerPortReservationSerializesConcurrentHarnesses(t *testing.T) {
	t.Parallel()

	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash is not installed")
	}

	lockRoot := filepath.Join(t.TempDir(), "controller-ports")
	require.NoError(t, os.MkdirAll(lockRoot, 0o755))
	runner := readFile(t, "run.sh")
	script := strings.Join([]string{
		"set -euo pipefail",
		`die() { printf 'ERROR: %s\n' "$*" >&2; exit 1; }`,
		extractBashFunction(t, runner, "reserve_controller_port"),
		extractBashFunction(t, runner, "release_controller_port"),
		extractBashFunction(t, runner, "stop_controller"),
		"trap release_controller_port EXIT",
		"reserve_controller_port",
		`reserved_lock="${PORT_LOCK_DIR}"`,
		"stop_controller",
		`[[ -d "${reserved_lock}" ]] || die "stop_controller released the port reservation"`,
		`printf '%s\n' "${CONTROLLER_PORT}" >"${OUTPUT_FILE}"`,
		`if [[ "${HOLD_LOCK}" == true ]]; then`,
		`  touch "${READY_FILE}"`,
		`  while [[ ! -e "${RELEASE_FILE}" ]]; do sleep 0.05; done`,
		"fi",
	}, "\n")

	readyFile := filepath.Join(lockRoot, "holder.ready")
	releaseFile := filepath.Join(lockRoot, "holder.release")
	firstOutput := filepath.Join(lockRoot, "first.port")
	secondOutput := filepath.Join(lockRoot, "second.port")
	newCommand := func(outputFile string, hold bool) *exec.Cmd {
		command := exec.Command(bash, "-c", script)
		command.Env = append(os.Environ(),
			"PORT_LOCK_ROOT="+lockRoot,
			"PORT_LOCK_DIR=",
			"CONTROLLER_PORT=",
			"CONTROLLER_PID=",
			"OUTPUT_FILE="+outputFile,
			"READY_FILE="+readyFile,
			"RELEASE_FILE="+releaseFile,
			"HOLD_LOCK="+map[bool]string{true: "true", false: "false"}[hold],
		)
		return command
	}

	var firstLog bytes.Buffer
	first := newCommand(firstOutput, true)
	first.Stdout = &firstLog
	first.Stderr = &firstLog
	require.NoError(t, first.Start())
	firstFinished := false
	t.Cleanup(func() {
		if !firstFinished {
			_ = first.Process.Kill()
			_ = first.Wait()
		}
	})
	require.Eventually(t, func() bool {
		_, err := os.Stat(readyFile)
		return err == nil
	}, 2*time.Second, 10*time.Millisecond, "port-reservation holder did not become ready")

	second := newCommand(secondOutput, false)
	output, err := second.CombinedOutput()
	require.NoError(t, err, string(output))
	firstPort := strings.TrimSpace(readPath(t, firstOutput))
	secondPort := strings.TrimSpace(readPath(t, secondOutput))
	require.NotEqual(t, firstPort, secondPort)
	require.DirExists(t, filepath.Join(lockRoot, firstPort))
	require.NoDirExists(t, filepath.Join(lockRoot, secondPort))

	require.NoError(t, os.WriteFile(releaseFile, nil, 0o600))
	require.NoError(t, first.Wait(), firstLog.String())
	firstFinished = true
	require.NoDirExists(t, filepath.Join(lockRoot, firstPort))
}

func TestRunnerUIDLookupsPropagateFailures(t *testing.T) {
	runner := readFile(t, "run.sh")

	for _, uid := range []string{"OLD_UID", "NEW_UID"} {
		assignment := uid + `="$(kctl get node "${REPLACED_NODE}" -o jsonpath='{.metadata.uid}')"`
		require.Contains(t, runner, assignment+"\nreadonly "+uid)
		require.NotContains(t, runner, "readonly "+assignment)
	}
}

func TestRunnerRendersCRDChartBeforeApply(t *testing.T) {
	runner := readFile(t, "run.sh")
	require.Contains(t, runner, "for command in kwokctl kubectl helm ")
	require.NotContains(t, runner, "/mokka-crds/crds")
	require.Contains(t, runner, `readonly CRD_CHART_DIR="${REPO_DIR}/deployments/mokka-crds/helm/mokka-crds"`)
	require.Contains(t, runner, `readonly CRD_MANIFEST="${WORK_DIR}/mokka-crds.yaml"`)

	render := `helm template mokka-crds "${CRD_CHART_DIR}" --include-crds >"${CRD_MANIFEST}"`
	apply := `kctl apply --server-side --field-manager=mokka-kwok-poc -f "${CRD_MANIFEST}"`
	renderIndex := strings.Index(runner, render)
	applyIndex := strings.Index(runner, apply)
	require.NotEqual(t, -1, renderIndex)
	require.Greater(t, applyIndex, renderIndex)
}

func TestRunnerStartsTimingBeforeScenarioActions(t *testing.T) {
	runner := readFile(t, "run.sh")
	checkStateStart := strings.Index(runner, "check_state() {")
	checkStateEnd := strings.Index(runner, "snapshot_assignments() {")
	require.NotEqual(t, -1, checkStateStart)
	require.Greater(t, checkStateEnd, checkStateStart)
	checkState := runner[checkStateStart:checkStateEnd]
	require.Contains(t, checkState, `local started_seconds="$2"`)
	require.NotContains(t, checkState, `started_seconds="$(date -u +%s)"`)
	require.Equal(t, 9, strings.Count(runner, `scenario_started_seconds="$(date -u +%s)"`))
	require.Equal(t, 9, strings.Count(runner, `"${scenario_started_seconds}"`))

	require.Contains(t, runner, `scenario_started_seconds="$(date -u +%s)"
scale_nodes "${INITIAL_NODE_COUNT}"
apply_inventory "${FULL_RACKS}"
check_state scale-up-half "${scenario_started_seconds}"`)
	require.Contains(t, runner, `scenario_started_seconds="$(date -u +%s)"
scale_nodes "${NODE_COUNT}"
check_state steady-state "${scenario_started_seconds}"`)
	require.Contains(t, runner, `scenario_started_seconds="$(date -u +%s)"
stop_controller
start_controller
check_state controller-restart "${scenario_started_seconds}"`)
	require.Contains(t, runner, `scenario_started_seconds="$(date -u +%s)"
kctl delete node "${REPLACED_NODE}"`)
	require.Contains(t, runner, `scenario_started_seconds="$(date -u +%s)"
apply_inventory "${SHRUNK_RACKS}"
check_state inventory-shrink "${scenario_started_seconds}"`)
	require.Contains(t, runner, `scenario_started_seconds="$(date -u +%s)"
kctl label node mokka-node-000000 mokka.nvidia.com/sgpu-node-`)
}

func readFile(t *testing.T, name string) string {
	t.Helper()
	return readPath(t, filepath.Join(testDir(t), name))
}

func readPath(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(data)
}

func extractBashFunction(t *testing.T, script, name string) string {
	t.Helper()
	start := strings.Index(script, name+"() {\n")
	require.NotEqual(t, -1, start, "function %s not found", name)
	end := strings.Index(script[start:], "\n}\n")
	require.NotEqual(t, -1, end, "function %s has no closing brace", name)
	return script[start : start+end+3]
}

func testDir(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	require.True(t, ok)
	return filepath.Dir(file)
}
