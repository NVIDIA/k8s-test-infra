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
