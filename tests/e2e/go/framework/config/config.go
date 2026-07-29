//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package config is the single authoritative source of harness configuration.
// Spec generation is driven by the profile NAMES returned here (read from the
// E2E_PROFILES env at package-init time, since Ginkgo's spec tree is built
// before flags are parsed); Ginkgo Labels are attached for reporting and
// optional --label-filter narrowing. This avoids the "ran everything / ran
// nothing" selector trap by having exactly one selector: the env-provided
// profile list (per workflow input), never a hardcoded 7.
package config

import (
	"os"
	"strings"
	"time"
)

// DefaultProfiles is the local/default profile set. Broader profile sweeps are
// opt-in via E2E_PROFILES or the workflow input.
var DefaultProfiles = []string{"gb200"}

const (
	defaultProfilesDir = "deployments/nvml-mock/helm/nvml-mock/profiles"
	defaultImage       = "nvml-mock:e2e"
	defaultArtifacts   = "artifacts/e2e/go"
	defaultDockerfile  = "deployments/nvml-mock/Dockerfile"
)

func env(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func envBool(key string) bool {
	return envBoolDefault(key, false)
}

func envBoolDefault(key string, def bool) bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	switch v {
	case "":
		return def
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return def
	}
}

// ProfilesDir is the chart profiles directory (the deployed source of truth).
func ProfilesDir() string { return env("E2E_PROFILES_DIR", defaultProfilesDir) }

// SelectedProfileNames returns the profile ids to generate specs for, from the
// E2E_PROFILES env (comma/space separated). Defaults to DefaultProfiles.
func SelectedProfileNames() []string {
	raw := strings.TrimSpace(os.Getenv("E2E_PROFILES"))
	if raw == "" {
		return append([]string(nil), DefaultProfiles...)
	}
	fields := strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' })
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, f)
		}
	}
	if len(out) == 0 {
		return append([]string(nil), DefaultProfiles...)
	}
	return out
}

// Image is the local image ref the harness builds and kind-loads.
func Image() string { return env("E2E_IMAGE", defaultImage) }

// Dockerfile is the path to the nvml-mock Dockerfile.
func Dockerfile() string { return env("E2E_DOCKERFILE", defaultDockerfile) }

// GolangVersion is the --build-arg GOLANG_VERSION passed to the image build
// (empty => Dockerfile default).
func GolangVersion() string { return os.Getenv("E2E_GOLANG_VERSION") }

// SkipBuild reports whether the SynchronizedBeforeSuite image build is skipped
// (local fast loops with a pre-built E2E_IMAGE).
func SkipBuild() bool { return envBool("E2E_SKIP_BUILD") }

// BuildxGHACache reports whether to add --cache-to/--cache-from type=gha to the
// buildx build (set in CI only).
func BuildxGHACache() bool { return envBool("E2E_BUILDX_GHA_CACHE") }

// KeepCluster reports whether clusters should survive teardown for debugging.
func KeepCluster() bool { return envBoolDefault("E2E_KEEP_CLUSTER", true) }

// AttachExisting reports whether the harness should attach to an already-created
// cluster (skipping kind create + image load + helm install) instead of owning
// the full lifecycle. Set by CI when the environment is rolled out separately
// (e.g. via `tilt ci`). Requires E2E_KUBE_CONTEXT and E2E_CLUSTER_NAME.
func AttachExisting() bool { return envBool("E2E_ATTACH_EXISTING") }

// KubeContext is the kubeconfig context to attach to when AttachExisting is set.
func KubeContext() string { return os.Getenv("E2E_KUBE_CONTEXT") }

// ClusterName is the Kind cluster name to attach to when AttachExisting is set.
// Used by `kind get nodes --name` for node-role assertions.
func ClusterName() string { return os.Getenv("E2E_CLUSTER_NAME") }

// ArtifactsDir is where diagnostics are written.
func ArtifactsDir() string { return env("E2E_ARTIFACTS", defaultArtifacts) }

// RunNGCSpecs reports whether the NGC-auth standalone GFD/CUDA specs run
// (default skipped). Set when an NGC pull secret/credentials are available.
func RunNGCSpecs() bool { return envBool("E2E_RUN_NGC") }

// Timeouts (overridable; conservative defaults matching the bash waits).

func ClusterTimeout() time.Duration { return durEnv("E2E_CLUSTER_TIMEOUT", 5*time.Minute) }
func HelmTimeout() time.Duration    { return durEnv("E2E_HELM_TIMEOUT", 5*time.Minute) }
func ReadyTimeout() time.Duration   { return durEnv("E2E_READY_TIMEOUT", 2*time.Minute) }
func PollInterval() time.Duration   { return durEnv("E2E_POLL_INTERVAL", 2*time.Second) }

func durEnv(key string, def time.Duration) time.Duration {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}
