//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package kube

import (
	"context"
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
