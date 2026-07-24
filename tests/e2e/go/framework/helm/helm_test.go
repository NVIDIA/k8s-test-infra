//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package helm

import (
	"context"
	"strings"
	"testing"

	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/runner"
)

func TestBaseUsesDefaultKubeconfigWhenUnset(t *testing.T) {
	args := New("kind-nvml-mock-e2e").base()

	for _, arg := range args {
		if arg == "--kubeconfig" {
			t.Fatal("did not expect --kubeconfig when Helm should use the default kubeconfig")
		}
	}
}

func TestBaseTargetsKubeContext(t *testing.T) {
	args := New("kind-nvml-mock-e2e").base()

	if len(args) != 2 || args[0] != "--kube-context" || args[1] != "kind-nvml-mock-e2e" {
		t.Fatalf("expected Helm kube context args, got %#v", args)
	}
}

func TestRunHidesReleaseOutputWhenRequested(t *testing.T) {
	oldRunCommand := runCommand
	oldRunQuietCommand := runQuietCommand
	t.Cleanup(func() {
		runCommand = oldRunCommand
		runQuietCommand = oldRunQuietCommand
	})

	var loudRuns, quietRuns int
	runCommand = func(context.Context, string, ...string) (runner.Result, error) {
		loudRuns++
		return runner.Result{}, nil
	}
	runQuietCommand = func(context.Context, string, ...string) (runner.Result, error) {
		quietRuns++
		return runner.Result{}, nil
	}

	err := New("kind-nvml-mock-e2e").UpgradeInstall(context.Background(), Release{
		Name:       "nvml-mock",
		Chart:      "chart",
		HideOutput: true,
	})

	if err != nil {
		t.Fatalf("expected quiet helm release to succeed, got %v", err)
	}
	if loudRuns != 0 {
		t.Fatalf("expected quiet release not to use loud runner, got %d loud runs", loudRuns)
	}
	if quietRuns != 1 {
		t.Fatalf("expected quiet release to use quiet runner once, got %d quiet runs", quietRuns)
	}
}

func TestRunPassesVersionWhenSet(t *testing.T) {
	oldRunCommand := runCommand
	t.Cleanup(func() { runCommand = oldRunCommand })

	var got []string
	runCommand = func(_ context.Context, _ string, args ...string) (runner.Result, error) {
		got = append([]string(nil), args...)
		return runner.Result{}, nil
	}

	err := New("kind-nvml-mock-e2e").UpgradeInstall(context.Background(), Release{
		Name:    "gpu-operator",
		Chart:   "nvidia/gpu-operator",
		Version: "v26.3.3",
	})
	if err != nil {
		t.Fatalf("expected release to succeed, got %v", err)
	}

	joined := strings.Join(got, " ")
	if !strings.Contains(joined, "--version v26.3.3") {
		t.Fatalf("expected `--version v26.3.3` in helm args, got %v", got)
	}
}

func TestRunOmitsVersionWhenEmpty(t *testing.T) {
	oldRunCommand := runCommand
	t.Cleanup(func() { runCommand = oldRunCommand })

	var got []string
	runCommand = func(_ context.Context, _ string, args ...string) (runner.Result, error) {
		got = append([]string(nil), args...)
		return runner.Result{}, nil
	}

	err := New("kind-nvml-mock-e2e").UpgradeInstall(context.Background(), Release{
		Name:  "nvml-mock",
		Chart: "chart",
	})
	if err != nil {
		t.Fatalf("expected release to succeed, got %v", err)
	}

	for _, a := range got {
		if a == "--version" {
			t.Fatalf("did not expect `--version` in helm args when Version is empty, got %v", got)
		}
	}
}
