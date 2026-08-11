//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package helm

import (
	"context"
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

func TestRunPinsChartVersionOnlyWhenSet(t *testing.T) {
	for _, tc := range []struct {
		name        string
		version     string
		wantVersion bool
	}{
		{name: "pinned chart", version: "0.19.0", wantVersion: true},
		{name: "unpinned chart", version: "", wantVersion: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			oldRunCommand := runCommand
			t.Cleanup(func() { runCommand = oldRunCommand })

			var args []string
			runCommand = func(_ context.Context, _ string, a ...string) (runner.Result, error) {
				args = a
				return runner.Result{}, nil
			}

			err := New("kind-nvml-mock-e2e").Install(context.Background(), Release{
				Name:    "nfd",
				Chart:   "nfd/node-feature-discovery",
				Version: tc.version,
			})
			if err != nil {
				t.Fatalf("expected helm install to succeed, got %v", err)
			}

			flag := -1
			for i, arg := range args {
				if arg == "--version" {
					flag = i
					break
				}
			}

			if !tc.wantVersion {
				if flag != -1 {
					t.Fatalf("expected no --version for a release without a pinned version, got %#v", args)
				}
				return
			}
			if flag == -1 {
				t.Fatalf("expected --version in the helm argv, got %#v", args)
			}
			if flag+1 >= len(args) || args[flag+1] != tc.version {
				t.Fatalf("expected --version to be followed by %q, got %#v", tc.version, args)
			}
		})
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
