//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package helm

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/runner"
)

func TestBaseUsesDefaultKubeconfigWhenUnset(t *testing.T) {
	args := New("kind-nvml-mock-e2e").base()
	require.NotContains(t, args, "--kubeconfig",
		"did not expect --kubeconfig when Helm should use the default kubeconfig")
}

func TestBaseTargetsKubeContext(t *testing.T) {
	args := New("kind-nvml-mock-e2e").base()
	require.Equal(t, []string{"--kube-context", "kind-nvml-mock-e2e"}, args,
		"expected Helm kube context args")
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
			require.NoError(t, err, "expected helm install to succeed")

			flag := -1
			for i, arg := range args {
				if arg == "--version" {
					flag = i
					break
				}
			}

			if !tc.wantVersion {
				require.Equal(t, -1, flag,
					"expected no --version for a release without a pinned version, got %#v", args)
				return
			}
			require.NotEqual(t, -1, flag, "expected --version in the helm argv, got %#v", args)
			require.Less(t, flag+1, len(args), "--version has no value, got %#v", args)
			require.Equal(t, tc.version, args[flag+1],
				"expected --version to be followed by %q, got %#v", tc.version, args)
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
	require.NoError(t, err, "expected quiet helm release to succeed")
	require.Zero(t, loudRuns, "expected quiet release not to use loud runner")
	require.Equal(t, 1, quietRuns, "expected quiet release to use quiet runner once")
}
