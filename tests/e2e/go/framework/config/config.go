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
	// 580.159.04-ubuntu24.04: a different driver from the one
	// local/kernel-mods/gpu-operator.values.yaml installs.
	defaultDriverUpgradeVersion = "sha256:512e9168499ea80f49a3c9fc3220849d7690d7487c1fd035bc21f60960e4d5d8"
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

// ExpectNRI reports whether the cluster is meant to run the nvml-mock NRI
// plugin. NRI-only specs skip when the plugin is absent; with this set they fail
// instead, so a CI job that stops enabling NRI cannot go green on skips.
func ExpectNRI() bool { return envBool("E2E_EXPECT_NRI") }

// IMEXDownload reports whether the node agent may download IMEX from NVIDIA.
// CI sets E2E_IMEX_DOWNLOAD=false, so no job contacts NVIDIA from inside a
// cluster; every nvml-mock install the suite makes then turns the
// IMEXDownload feature gate off.
func IMEXDownload() bool { return envBoolDefault("E2E_IMEX_DOWNLOAD", true) }

// IMEXBinariesImage is the IMEX binaries image (repo:tag) the ComputeDomain specs
// install the chart with, already loaded into the cluster. With it set, the
// node agent never downloads IMEX; empty leaves it to the IMEXDownload default.
func IMEXBinariesImage() string { return env("E2E_IMEX_BINARIES_IMAGE", "") }

// KernelDriverMode reports whether the cluster runs the chart's kernel driver
// mode (E2E_DRIVER_MODE=kernel), in which the GPU Operator's own driver
// container loads Mokka's simulated kernel modules. The job declares the mode
// rather than the suite detecting it, so a cluster that came up in the wrong
// mode fails instead of running the wrong specs.
func KernelDriverMode() bool { return strings.EqualFold(env("E2E_DRIVER_MODE", "userspace"), "kernel") }

// DriverUpgradeVersion is the driver version the kernel-mode upgrade spec moves
// the ClusterPolicy to. It is an image index digest, as the installed one is:
// the operator names a driver image after the node's OS, and NVIDIA publishes
// none for the Debian that Kind nodes run.
func DriverUpgradeVersion() string {
	return env("E2E_DRIVER_UPGRADE_VERSION", defaultDriverUpgradeVersion)
}

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

// DriverInstallTimeout bounds the GPU Operator's driver installation in the
// kernel driver mode: its driver container installs the node kernel's headers
// and compiles NVIDIA's driver before it loads the simulated one.
func DriverInstallTimeout() time.Duration {
	return durEnv("E2E_DRIVER_INSTALL_TIMEOUT", 20*time.Minute)
}

func durEnv(key string, def time.Duration) time.Duration {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}
