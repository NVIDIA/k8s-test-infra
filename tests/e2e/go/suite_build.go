//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/config"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/runner"
)

func buildImage(ctx context.Context) {
	GinkgoHelper()
	args := []string{"buildx", "build", "-t", config.Image(), "-f", dockerfilePath(), "--load"}
	if gv := config.GolangVersion(); gv != "" {
		args = append(args, "--build-arg", "GOLANG_VERSION="+gv)
	}
	if config.BuildxGHACache() {
		args = append(args, "--cache-to", "type=gha,mode=max", "--cache-from", "type=gha")
	}
	args = append(args, repoRoot())
	By("building nvml-mock image " + config.Image())
	_, err := runner.Run(ctx, "docker", args...)
	Expect(err).NotTo(HaveOccurred(), "docker buildx build")
}

// buildMockDriverImage builds the mock-driver image the managed-driver GPU
// Operator scenario substitutes for the real driver DaemonSet image. Skipped
// (reused from E2E_MOCK_DRIVER_IMAGE) when E2E_MOCK_DRIVER_SKIP_BUILD is set,
// mirroring the nvml-mock build-once/pull-by-digest CI flow.
func buildMockDriverImage(ctx context.Context) {
	GinkgoHelper()
	if config.MockDriverSkipBuild() {
		By("reusing pre-built mock-driver image " + config.MockDriverImage())
		return
	}
	args := []string{"buildx", "build", "-t", config.MockDriverImage(), "-f", mockDriverDockerfilePath(), "--load"}
	if gv := config.GolangVersion(); gv != "" {
		args = append(args, "--build-arg", "GOLANG_VERSION="+gv)
	}
	if config.BuildxGHACache() {
		args = append(args, "--cache-to", "type=gha,mode=max,scope=mock-driver", "--cache-from", "type=gha,scope=mock-driver")
	}
	args = append(args, repoRoot())
	By("building mock-driver image " + config.MockDriverImage())
	_, err := runner.Run(ctx, "docker", args...)
	Expect(err).NotTo(HaveOccurred(), "docker buildx build mock-driver")
}
