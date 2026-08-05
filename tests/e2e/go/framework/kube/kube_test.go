//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package kube

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// installFakeKubectl puts a stub `kubectl` at the front of PATH for the rest of
// the test. The framework shells out to kubectl by name, so this substitutes
// the external binary — the outermost boundary — and leaves every layer inside
// it (kube.Client, runner.Run) running for real.
func installFakeKubectl(t *testing.T, script string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "kubectl")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatalf("write fake kubectl: %v", err)
	}
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
	if err != nil {
		t.Fatalf("New default kubeconfig client: %v", err)
	}
	return c
}

// The defect: a sidecar's output never reached the diagnostics dump, so a
// watcher crash was invisible in CI. Assert on content from BOTH containers —
// exit status alone cannot tell the two cases apart.
func TestLogsCapturesEveryContainerNotJustTheDefault(t *testing.T) {
	installFakeKubectl(t, multiContainerLogs)

	out, err := newTestClient(t).Logs(context.Background(), "gpu-operator", "app.kubernetes.io/name=nvml-mock", 100)
	if err != nil {
		t.Fatalf("Logs: %v", err)
	}

	if !strings.Contains(out, "[sidecar] watch-allocations: 8 GPUs, polling") {
		t.Fatalf("sidecar container output missing from collected logs, got:\n%s", out)
	}
	if !strings.Contains(out, "[nvml-mock] mock ready") {
		t.Fatalf("primary container output missing from collected logs, got:\n%s", out)
	}
	if strings.Contains(out, "Defaulted container") {
		t.Fatalf("kubectl defaulted to one container instead of collecting all, got:\n%s", out)
	}
}

func TestLogsArgsRequestAllContainersAndNotPreviousInstance(t *testing.T) {
	got := logsArgs("gpu-operator", "app.kubernetes.io/name=nvml-mock", 100, false)

	want := []string{
		"logs", "-n", "gpu-operator",
		"-l", "app.kubernetes.io/name=nvml-mock",
		"--all-containers=true", "--tail=100",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expected kubectl logs args %#v, got %#v", want, got)
	}
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
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expected kubectl logs args %#v, got %#v", want, got)
	}
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
	if err != nil {
		t.Fatalf("RestartedPods on a restarted pod: %v", err)
	}
	if !reflect.DeepEqual(restarted, []string{"nvml-mock-abcde"}) {
		t.Fatalf("expected the restarted pod to be reported, got %#v", restarted)
	}

	healthy, err := c.RestartedPods(context.Background(), "healthy", "app.kubernetes.io/name=nvml-mock")
	if err != nil {
		t.Fatalf("RestartedPods on a healthy pod: %v", err)
	}
	if len(healthy) != 0 {
		t.Fatalf("expected no pods reported when nothing restarted, got %#v", healthy)
	}
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
	if err != nil {
		t.Fatalf("GetConfigMap: %v", err)
	}

	if got := cm.Labels["fake-gpu-operator/gpu-profile"]; got != "true" {
		t.Fatalf("expected the FGO discovery label to be readable, got %q", got)
	}
	if got := cm.Data["profile.yaml"]; got != "version: \"1.0\"\n" {
		t.Fatalf("expected the profile.yaml body to be readable, got %q", got)
	}
}

// A wrong name must surface as an error rather than an empty ConfigMap, or the
// name half of the contract check would silently pass.
func TestGetConfigMapErrorsOnAMissingName(t *testing.T) {
	installFakeKubectl(t, profileConfigMapJSON)

	_, err := newTestClient(t).GetConfigMap(context.Background(), "nvml-mock-system", "nvml-mock-profile-a100")
	if err == nil {
		t.Fatal("expected an error for a ConfigMap name that does not exist, got nil")
	}
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

			if got := ds.rolledOutAndReady(); got != tc.want {
				t.Fatalf("rolledOutAndReady() = %t, want %t", got, tc.want)
			}
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
	if err := json.Unmarshal([]byte(payload), &ds); err != nil {
		t.Fatalf("unmarshal daemonset: %v", err)
	}

	got := []int{
		int(ds.Metadata.Generation), int(ds.Status.ObservedGeneration),
		ds.Status.DesiredNumberScheduled, ds.Status.UpdatedNumberScheduled,
		ds.Status.NumberReady,
	}
	if want := []int{5, 4, 3, 2, 1}; !reflect.DeepEqual(got, want) {
		t.Fatalf("decoded [generation observedGeneration desired updated ready] = %v, want %v", got, want)
	}
}

func TestBaseUsesDefaultKubeconfigWhenUnset(t *testing.T) {
	c, err := New("kind-nvml-mock-e2e")
	if err != nil {
		t.Fatalf("New default kubeconfig client: %v", err)
	}
	args := c.base()

	for _, arg := range args {
		if arg == "--kubeconfig" {
			t.Fatal("did not expect --kubeconfig when kubectl should use the default kubeconfig")
		}
	}
}

func TestBaseTargetsContext(t *testing.T) {
	c, err := New("kind-nvml-mock-e2e")
	if err != nil {
		t.Fatalf("New default kubeconfig client: %v", err)
	}
	args := c.base()

	if len(args) != 2 || args[0] != "--context" || args[1] != "kind-nvml-mock-e2e" {
		t.Fatalf("expected kubectl context args, got %#v", args)
	}
}
