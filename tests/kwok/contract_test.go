// Copyright (c) 2026, NVIDIA CORPORATION. All rights reserved.
// Licensed under the Apache License, Version 2.0 (the "License");

package kwok_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"sigs.k8s.io/yaml"

	mokkav1alpha1 "github.com/NVIDIA/k8s-test-infra/internal/controlplane/api/v1alpha1"
	"github.com/NVIDIA/k8s-test-infra/pkg/mokka/materialize"
)

func TestTemplatesRenderValidScaleResources(t *testing.T) {
	profileData := readFile(t, "profile.yaml")
	profileData = strings.ReplaceAll(profileData, "__NODES_PER_RACK__", "100")
	var profile mokkav1alpha1.SGPUProfile
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
	require.Contains(t, runner, `"${CONTROLLER_BIN}"`)
	require.Contains(t, runner, `--leader-election-name=mokka-controller-kwok`)
	require.Contains(t, runner, `kwok scale mokka-node --replicas`)
	require.NotContains(t, runner, `--show-error`)
	require.NotContains(t, runner, "curl | sh")
	require.NotContains(t, runner, "helm install")
	require.NotContains(t, runner, "deployment/mokka-controller")

	makefileData, err := os.ReadFile(filepath.Join(testDir(t), "..", "..", "Makefile"))
	require.NoError(t, err)
	makefile := string(makefileData)
	require.Contains(t, makefile, `test "$(KWOK_SCALE)" = 1`)
	require.Contains(t, makefile, `KWOK_NODE_COUNT ?= 200`)
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
	data, err := os.ReadFile(filepath.Join(testDir(t), name))
	require.NoError(t, err)
	return string(data)
}

func testDir(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	require.True(t, ok)
	return filepath.Dir(file)
}
