//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package kube

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// installFakeKubectl puts a stub `kubectl` at the front of PATH for the rest of
// the test. The framework shells out to kubectl by name, so this substitutes
// the external binary — the outermost boundary — and leaves every layer inside
// it (kube.Client, runner.Run) running for real.
func installFakeKubectl(t *testing.T, script string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "kubectl")
	require.NoError(t, os.WriteFile(path, []byte("#!/bin/sh\n"+script), 0o755), "write fake kubectl")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// multiContainerLogs reproduces the real kubectl behaviour behind issue #562:
// asked for a multi-container pod's logs WITHOUT --all-containers, kubectl
// silently picks the default container and prints a "Defaulted container"
// notice; asked WITH it, every container's output appears.
const multiContainerLogs = `
all=""
prev=""
for arg in "$@"; do
  case "$arg" in
    --all-containers=true) all=1 ;;
    --previous) prev=1 ;;
  esac
done
if [ -n "$prev" ]; then echo "--- previous instance ---"; fi
if [ -n "$all" ]; then
  echo "[nvml-mock] mock ready"
  echo "[sidecar] watch-allocations: 8 GPUs, polling"
else
  echo 'Defaulted container "nvml-mock" out of: nvml-mock, sidecar'
  echo "[nvml-mock] mock ready"
fi
`

func newTestClient(t *testing.T) *Client {
	t.Helper()
	c, err := New("kind-nvml-mock-e2e")
	require.NoError(t, err, "New default kubeconfig client")
	return c
}

// The defect: a sidecar's output never reached the diagnostics dump, so a
// watcher crash was invisible in CI. Assert on content from BOTH containers —
// exit status alone cannot tell the two cases apart.
func TestLogsCapturesEveryContainerNotJustTheDefault(t *testing.T) {
	installFakeKubectl(t, multiContainerLogs)

	out, err := newTestClient(t).Logs(context.Background(), "gpu-operator", "app.kubernetes.io/name=nvml-mock", 100)
	require.NoError(t, err, "Logs")
	require.Contains(t, out, "[sidecar] watch-allocations: 8 GPUs, polling",
		"sidecar container output missing from collected logs")
	require.Contains(t, out, "[nvml-mock] mock ready",
		"primary container output missing from collected logs")
	require.NotContains(t, out, "Defaulted container",
		"kubectl defaulted to one container instead of collecting all")
}

func TestLogsArgsRequestAllContainersAndNotPreviousInstance(t *testing.T) {
	got := logsArgs("gpu-operator", "app.kubernetes.io/name=nvml-mock", 100, false)

	want := []string{
		"logs", "-n", "gpu-operator",
		"-l", "app.kubernetes.io/name=nvml-mock",
		"--all-containers=true", "--tail=100",
	}
	require.Equal(t, want, got, "kubectl logs args")
}

// `kubectl logs --previous` errors when a container has no previous instance,
// which is the normal case. Adding it unconditionally would turn a working
// diagnostic into a failing one on every healthy pod.
func TestPreviousLogsArgsAskForPreviousInstance(t *testing.T) {
	got := logsArgs("gpu-operator", "app.kubernetes.io/name=nvml-mock", 100, true)

	want := []string{
		"logs", "-n", "gpu-operator",
		"-l", "app.kubernetes.io/name=nvml-mock",
		"--all-containers=true", "--tail=100", "--previous",
	}
	require.Equal(t, want, got, "kubectl logs args")
}

// podsJSON serves a two-container pod whose restart counts depend on the
// requested namespace, so one fake covers both sides of the gate.
const podsJSON = `
ns=""
next=""
for arg in "$@"; do
  if [ "$next" = "1" ]; then ns="$arg"; next=""; fi
  if [ "$arg" = "-n" ]; then next=1; fi
done
if [ "$ns" = "restarted" ]; then counts='0, "x": 0}, {"name": "sidecar", "restartCount": 3'; else counts='0, "x": 0}, {"name": "sidecar", "restartCount": 0'; fi
cat <<EOF
{"items": [{"metadata": {"name": "nvml-mock-abcde"},
 "status": {"phase": "Running", "containerStatuses": [{"name": "nvml-mock", "restartCount": $counts}]}}]}
EOF
`

func TestRestartedPodsGatesOnRestartCount(t *testing.T) {
	installFakeKubectl(t, podsJSON)
	c := newTestClient(t)

	restarted, err := c.RestartedPods(context.Background(), "restarted", "app.kubernetes.io/name=nvml-mock")
	require.NoError(t, err, "RestartedPods on a restarted pod")
	require.Equal(t, []string{"nvml-mock-abcde"}, restarted, "restarted pod")

	healthy, err := c.RestartedPods(context.Background(), "healthy", "app.kubernetes.io/name=nvml-mock")
	require.NoError(t, err, "RestartedPods on a healthy pod")
	require.Empty(t, healthy, "pods reported when nothing restarted")
}

// profileConfigMapJSON answers only for the exact name FGO's loader Gets, and
// exits non-zero for anything else — the same way kubectl reports NotFound.
// Without that branch a test asserting the name would pass against any name.
const profileConfigMapJSON = `
name=""
for arg in "$@"; do name="$arg"; done
if [ "$name" != "gpu-profile-a100" ]; then
  echo "Error from server (NotFound): configmaps \"$name\" not found" >&2
  exit 1
fi
cat <<EOF
{"metadata": {"name": "gpu-profile-a100",
  "labels": {"fake-gpu-operator/gpu-profile": "true", "run.ai/gpu-profile": "true"}},
 "data": {"profile.yaml": "version: \"1.0\"\n"}}
EOF
`

func TestGetConfigMapReturnsLabelsAndData(t *testing.T) {
	installFakeKubectl(t, profileConfigMapJSON)

	cm, err := newTestClient(t).GetConfigMap(context.Background(), "nvml-mock-system", "gpu-profile-a100")
	require.NoError(t, err, "GetConfigMap")
	require.Equal(t, "true", cm.Labels["fake-gpu-operator/gpu-profile"],
		"FGO discovery label")
	require.Equal(t, "version: \"1.0\"\n", cm.Data["profile.yaml"], "profile.yaml body")
}

// A wrong name must surface as an error rather than an empty ConfigMap, or the
// name half of the contract check would silently pass.
func TestGetConfigMapErrorsOnAMissingName(t *testing.T) {
	installFakeKubectl(t, profileConfigMapJSON)

	_, err := newTestClient(t).GetConfigMap(context.Background(), "nvml-mock-system", "nvml-mock-profile-a100")
	require.Error(t, err, "missing ConfigMap name")
}

// Readiness has to mean "this spec rolled out and is ready", not "some pod is
// ready": a caller polling straight after a restart would otherwise be answered
// by the very pod it asked to have replaced, then talk to it as it is deleted.
// Every case here keeps numberReady == desiredNumberScheduled, which is exactly
// the shape that fools a ready count. The settled case is the counterweight —
// too strict and every wait built on this would simply hang.
func TestDaemonSetRolledOutAndReady(t *testing.T) {
	for _, tc := range []struct {
		name       string
		generation int64
		observed   int64
		desired    int
		updated    int
		ready      int
		want       bool
	}{
		{
			name:       "fully rolled out",
			generation: 3, observed: 3, desired: 1, updated: 1, ready: 1,
			want: true,
		},
		{
			name:       "new spec not rolled out yet",
			generation: 3, observed: 3, desired: 1, updated: 0, ready: 1,
			want: false,
		},
		{
			name:       "status still describes the previous spec",
			generation: 3, observed: 2, desired: 1, updated: 1, ready: 1,
			want: false,
		},
		{
			// Nothing scheduled at all: a DaemonSet whose node selector matches
			// no node is vacuously "all ready" on counts alone.
			name:       "nothing scheduled",
			generation: 1, observed: 1, desired: 0, updated: 0, ready: 0,
			want: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var ds daemonSetObj
			ds.Metadata.Generation = tc.generation
			ds.Status.ObservedGeneration = tc.observed
			ds.Status.DesiredNumberScheduled = tc.desired
			ds.Status.UpdatedNumberScheduled = tc.updated
			ds.Status.NumberReady = tc.ready

			require.Equal(t, tc.want, ds.rolledOutAndReady(), "rolledOutAndReady()")
		})
	}
}

// The predicate above reads five fields straight off `kubectl get -o json`, so a
// mistyped tag would silently leave one at zero and skew every readiness wait
// without failing to compile. Distinct values catch a swap between them too.
func TestDaemonSetObjDecodesRolloutFields(t *testing.T) {
	const payload = `{
	  "metadata": {"name": "nvidia-dcgm-exporter", "generation": 5},
	  "status": {"observedGeneration": 4, "desiredNumberScheduled": 3,
	             "updatedNumberScheduled": 2, "numberReady": 1}
	}`

	var ds daemonSetObj
	require.NoError(t, json.Unmarshal([]byte(payload), &ds), "unmarshal daemonset")

	got := []int{
		int(ds.Metadata.Generation), int(ds.Status.ObservedGeneration),
		ds.Status.DesiredNumberScheduled, ds.Status.UpdatedNumberScheduled,
		ds.Status.NumberReady,
	}
	require.Equal(t, []int{5, 4, 3, 2, 1}, got,
		"decoded [generation observedGeneration desired updated ready]")
}

// With nri.enabled the chart runs the node agent as a native sidecar, so its
// env lives under initContainers and containers[0] is the NRI plugin.
func TestDaemonSetContainerEnvFindsTheNamedContainer(t *testing.T) {
	t.Parallel()
	const agentEnv = `{"name": "node-agent", "env": [{"name": "MOCK_FABRICMANAGER_STATE_DIR", "value": "/var/lib/nvml-mock/fabric-state"}]}`
	const nriPlugin = `{"name": "nvml-mock-nri", "env": [{"name": "NODE_NAME"}]}`
	for name, podSpec := range map[string]string{
		"regular container": `{"containers": [` + agentEnv + `, ` + nriPlugin + `]}`,
		"native sidecar":    `{"initContainers": [` + agentEnv + `], "containers": [` + nriPlugin + `]}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var ds daemonSetObj
			require.NoError(t, json.Unmarshal([]byte(`{"spec": {"template": {"spec": `+podSpec+`}}}`), &ds), "unmarshal daemonset")

			value, found, err := ds.containerEnv("node-agent", "MOCK_FABRICMANAGER_STATE_DIR")
			require.NoError(t, err)
			require.True(t, found, "MOCK_FABRICMANAGER_STATE_DIR on node-agent")
			require.Equal(t, "/var/lib/nvml-mock/fabric-state", value)

			_, found, err = ds.containerEnv("nvml-mock-nri", "MOCK_FABRICMANAGER_STATE_DIR")
			require.NoError(t, err)
			require.False(t, found, "variable read off the wrong container")
		})
	}
}

// A renamed container must not read as the variable being unset: the
// fabricmanager gate treats unset as "fabricmanager not deployed" and skips.
func TestDaemonSetContainerEnvRejectsAMissingContainer(t *testing.T) {
	t.Parallel()
	var ds daemonSetObj
	require.NoError(t, json.Unmarshal([]byte(`{"spec": {"template": {"spec": {"containers": [{"name": "nvml-mock-nri"}]}}}}`), &ds),
		"unmarshal daemonset")

	_, _, err := ds.containerEnv("node-agent", "MOCK_FABRICMANAGER_STATE_DIR")
	require.ErrorContains(t, err, `no container "node-agent"`)
}

func TestBaseUsesDefaultKubeconfigWhenUnset(t *testing.T) {
	c, err := New("kind-nvml-mock-e2e")
	require.NoError(t, err, "New default kubeconfig client")
	args := c.base()
	require.NotContains(t, args, "--kubeconfig",
		"kubectl should use the default kubeconfig")
}

func TestBaseTargetsContext(t *testing.T) {
	c, err := New("kind-nvml-mock-e2e")
	require.NoError(t, err, "New default kubeconfig client")
	args := c.base()
	require.Equal(t, []string{"--context", "kind-nvml-mock-e2e"}, args, "kubectl context args")
}

func TestDRAClaimAllocatedDevices(t *testing.T) {
	var claim draClaimObj
	require.NoError(t, json.Unmarshal([]byte(`{"status":{"allocation":{"devices":{"results":[
	  {"request":"gpu","driver":"gpu.nvidia.com","pool":"worker-1","device":"gpu-1"}]}}}}`), &claim),
		"unmarshal claim")

	refs, ok := claim.allocatedDevices()
	require.True(t, ok, "claim with an allocation")
	require.Equal(t, []draDeviceRef{{"gpu.nvidia.com", "worker-1", "gpu-1"}}, refs)
}

func TestDRAClaimWithoutAllocationIsNotAllocated(t *testing.T) {
	var claim draClaimObj
	require.NoError(t, json.Unmarshal([]byte(`{"status":{}}`), &claim), "unmarshal claim")

	_, ok := claim.allocatedDevices()
	require.False(t, ok, "claim without an allocation")
}

// Every pool publishes the same device names, so a lookup that ignores the
// pool resolves the wrong UUID.
func TestDRASliceUUIDsResolveTheDeviceInItsPool(t *testing.T) {
	var slices draSliceList
	require.NoError(t, json.Unmarshal([]byte(`{"items":[
	  {"spec":{"driver":"gpu.nvidia.com","pool":{"name":"worker-1"},"devices":[
	    {"name":"gpu-0","basic":{"attributes":{"uuid":{"string":"GPU-other"}}}},
	    {"name":"gpu-1","basic":{"attributes":{"uuid":{"string":"GPU-allocated"}}}}]}},
	  {"spec":{"driver":"gpu.nvidia.com","pool":{"name":"worker-0"},"devices":[
	    {"name":"gpu-1","basic":{"attributes":{"uuid":{"string":"GPU-wrong"}}}}]}}]}`), &slices),
		"unmarshal slices")

	uuids, err := slices.uuidsOf([]draDeviceRef{{"gpu.nvidia.com", "worker-1", "gpu-1"}})
	require.NoError(t, err)
	require.Equal(t, []string{"GPU-allocated"}, uuids)

	_, err = slices.uuidsOf([]draDeviceRef{{"gpu.nvidia.com", "worker-2", "gpu-1"}})
	require.ErrorContains(t, err, "no uuid attribute", "device in an unpublished pool")
}

// The v1 API moved device attributes out of basic and onto the device.
func TestDRASliceUUIDsReadTheV1DeviceShape(t *testing.T) {
	var slices draSliceList
	require.NoError(t, json.Unmarshal([]byte(`{"items":[
	  {"spec":{"driver":"gpu.nvidia.com","pool":{"name":"n"},"devices":[
	    {"name":"gpu-0","attributes":{"uuid":{"string":"GPU-0"}}},
	    {"name":"gpu-1","attributes":{"uuid":{"string":"GPU-1"}}}]}}]}`), &slices),
		"unmarshal slices")

	uuids, err := slices.uuidsOf([]draDeviceRef{{"gpu.nvidia.com", "n", "gpu-1"}})
	require.NoError(t, err)
	require.Equal(t, []string{"GPU-1"}, uuids)
}
