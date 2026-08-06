//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package diagnostics

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/kube"
)

// fakeKubectl stands in for the kubectl binary — the outermost boundary — so
// the Collector and kube.Client underneath it run for real.
//
// It reproduces the two behaviours that matter here:
//   - asked for a multi-container pod's logs WITHOUT --all-containers, kubectl
//     silently picks the default container and says so (issue #562);
//   - asked for --previous when no previous instance exists, it fails.
//
// Restart counts follow the requested namespace so one stub covers both sides
// of the gate.
// Every invocation is appended to $FAKE_KUBECTL_LOG so a test can assert on the
// calls that were NOT made — the restart gate is otherwise invisible, because
// tolerating the error produces the same files as never asking.
const fakeKubectl = `
echo "$*" >> "${FAKE_KUBECTL_LOG:-/dev/null}"
case "$*" in
  *" logs "*)
    all=""; prev=""
    for arg in "$@"; do
      case "$arg" in
        --all-containers=true) all=1 ;;
        --previous) prev=1 ;;
      esac
    done
    case "$*" in *" -n healthy "*) restarted="" ;; *) restarted=1 ;; esac
    if [ -n "$prev" ] && [ -z "$restarted" ]; then
      echo 'error: previous terminated container "sidecar" not found' >&2
      exit 1
    fi
    if [ -z "$all" ]; then
      echo 'Defaulted container "nvml-mock" out of: nvml-mock, sidecar'
      echo "[nvml-mock] mock ready"
      exit 0
    fi
    if [ -n "$prev" ]; then
      echo "[sidecar] dial /var/lib/kubelet/pod-resources: connection refused"
    else
      echo "[nvml-mock] mock ready"
      echo "[sidecar] watch-allocations: 8 GPUs, polling"
    fi
    ;;
  *)
    case "$*" in *" -n healthy "*) n=0 ;; *) n=3 ;; esac
    echo "{\"items\": [{\"metadata\": {\"name\": \"nvml-mock-abcde\"}, \"status\": {\"phase\": \"Running\", \"containerStatuses\": [{\"name\": \"nvml-mock\", \"restartCount\": 0}, {\"name\": \"sidecar\", \"restartCount\": $n}]}}]}"
    ;;
esac
`

// newCollector returns a Collector wired to the stub kubectl, plus the path of
// the file recording every kubectl invocation it makes.
func newCollector(t *testing.T) (*Collector, string) {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "kubectl")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"+fakeKubectl), 0o755); err != nil {
		t.Fatalf("write fake kubectl: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	calls := filepath.Join(dir, "calls.log")
	t.Setenv("FAKE_KUBECTL_LOG", calls)

	k, err := kube.New("kind-nvml-mock-e2e")
	if err != nil {
		t.Fatalf("kube.New: %v", err)
	}
	return &Collector{Dir: filepath.Join(t.TempDir(), "artifacts"), Kube: k}, calls
}

func read(t *testing.T, c *Collector, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(c.Dir, name))
	if err != nil {
		t.Fatalf("read collected %s: %v", name, err)
	}
	return string(b)
}

// The defect: the dump held only the default container, so a sidecar failure
// was invisible in CI. Assert the collected FILE carries both containers.
func TestPodLogsCollectsEverySidecarContainer(t *testing.T) {
	c, _ := newCollector(t)

	c.PodLogs(context.Background(), "nvml-mock", "gpu-operator", "app.kubernetes.io/name=nvml-mock", 100)

	got := read(t, c, "nvml-mock-logs.txt")
	for _, want := range []string{
		"[nvml-mock] mock ready",
		"[sidecar] watch-allocations: 8 GPUs, polling",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("collected logs missing %q, got:\n%s", want, got)
		}
	}
	if strings.Contains(got, "Defaulted container") {
		t.Fatalf("kubectl defaulted to one container instead of collecting all, got:\n%s", got)
	}
}

// A restarted container is the only case where --previous can succeed, and it
// is where a crash loop's cause lives.
func TestPodLogsCollectsPreviousInstanceAfterARestart(t *testing.T) {
	c, _ := newCollector(t)

	c.PodLogs(context.Background(), "nvml-mock", "gpu-operator", "app.kubernetes.io/name=nvml-mock", 100)

	got := read(t, c, "nvml-mock-logs-previous.txt")
	if want := "[sidecar] dial /var/lib/kubelet/pod-resources: connection refused"; !strings.Contains(got, want) {
		t.Fatalf("previous-instance logs missing %q, got:\n%s", want, got)
	}
}

// The trap: `kubectl logs --previous` errors when no previous instance exists,
// which is the normal case. The healthy path must not ask for it, and must
// still produce the current-log dump.
func TestPodLogsSkipsPreviousInstanceOnHealthyPods(t *testing.T) {
	c, calls := newCollector(t)

	c.PodLogs(context.Background(), "nvml-mock", "healthy", "app.kubernetes.io/name=nvml-mock", 100)

	if got := read(t, c, "nvml-mock-logs.txt"); !strings.Contains(got, "[sidecar] watch-allocations") {
		t.Fatalf("healthy-pod dump lost the sidecar, got:\n%s", got)
	}
	if _, err := os.Stat(filepath.Join(c.Dir, "nvml-mock-logs-previous.txt")); !os.IsNotExist(err) {
		t.Fatalf("expected no previous-instance dump for a pod that never restarted (stat err: %v)", err)
	}
	// The gate itself: asking at all would have failed the request. Tolerating
	// that error yields the same files, so assert on the call that was skipped.
	made, err := os.ReadFile(calls)
	if err != nil {
		t.Fatalf("read kubectl call log: %v", err)
	}
	if strings.Contains(string(made), "--previous") {
		t.Fatalf("expected no --previous request for a pod that never restarted, kubectl calls were:\n%s", made)
	}
}
