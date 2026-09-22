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
	// Match the KIND_CLUSTER_NAME in Makefile and the `name:` in
	// local/kind/default.kind.yaml — that is what `make cluster-create`
	// produces, and Kind derives the kubeconfig context as `kind-<name>`. Local
	// runs of `make e2e-<scenario>` then work without extra env; CI still
	// overrides via E2E_CLUSTER_NAME / E2E_KUBE_CONTEXT when the workflow
	// provisions a differently-named cluster.
	defaultClusterName = "mokka"
	defaultKubeContext = "kind-mokka"
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

// Image is the mock image ref already loaded in the externally-owned cluster.
// The scenarios that reshape the mock via helm upgrade pin
// image.repository / image.tag to this ref.
func Image() string { return env("E2E_IMAGE", defaultImage) }

// KubeContext is the kubeconfig context of the externally-owned cluster the
// suite attaches to. Defaults to `kind-mokka` — the context `make cluster-create`
// produces from local/kind/default.kind.yaml.
func KubeContext() string { return env("E2E_KUBE_CONTEXT", defaultKubeContext) }

// ClusterName is the Kind cluster name of the externally-owned cluster. It
// identifies the cluster in harness attach errors and diagnostics; node
// discovery goes through the kubeconfig context instead. Defaults to `mokka` —
// the cluster name `make cluster-create` produces.
func ClusterName() string { return env("E2E_CLUSTER_NAME", defaultClusterName) }

// ArtifactsDir is where diagnostics are written.
func ArtifactsDir() string { return env("E2E_ARTIFACTS", defaultArtifacts) }

// RunNGCSpecs reports whether the NGC-auth standalone GFD specs run
// (default skipped). Set when an NGC pull secret/credentials are available.
func RunNGCSpecs() bool { return envBool("E2E_RUN_NGC") }

// Timeouts (overridable; conservative defaults matching the bash waits).

// ClusterTimeout bounds the Kind cluster-create/attach wait.
func ClusterTimeout() time.Duration { return durEnv("E2E_CLUSTER_TIMEOUT", 5*time.Minute) }

// HelmTimeout bounds a `helm upgrade --install` wait.
func HelmTimeout() time.Duration { return durEnv("E2E_HELM_TIMEOUT", 5*time.Minute) }

// ReadyTimeout bounds the wait for a DaemonSet/pod to become Ready.
func ReadyTimeout() time.Duration { return durEnv("E2E_READY_TIMEOUT", 2*time.Minute) }

// PollInterval is how often Eventually retries during a Ready wait.
func PollInterval() time.Duration { return durEnv("E2E_POLL_INTERVAL", 2*time.Second) }

// OperandSettleTimeout bounds a wait that has to outlast a GPU Operator
// reconcile replacing its operands, rather than the single rollout ReadyTimeout
// is sized for.
func OperandSettleTimeout() time.Duration {
	return durEnv("E2E_OPERAND_SETTLE_TIMEOUT", 5*time.Minute)
}

func durEnv(key string, def time.Duration) time.Duration {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}
