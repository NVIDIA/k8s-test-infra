//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package e2e is the Go/Ginkgo end-to-end suite for nvml-mock. It is the Go
// port of docs/demo/standalone/demo.sh: ONE shared multi-node Kind cluster is
// created once, the nvml-mock chart is (re)installed per GPU profile via
// `helm upgrade --install` (a chart upgrade, NOT a cluster rebuild), and every
// profile's checks run against that same cluster. The suite owns the full
// lifecycle (cluster create/teardown, image build/load, helm, validation,
// diagnostics) behind one entrypoint that runs identically locally and in CI.
//
// It is gated by the `e2e` build tag so it never affects the fast
// `go test ./...` / `go build ./...` paths; run it with
// `ginkgo --tags e2e ./tests/e2e/go/...` (see the Makefile `e2e` target).
package e2e

import (
	"context"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/config"
)

const (
	// ClusterName is the single shared cluster the whole suite runs against.
	ClusterName = "nvml-mock-e2e"

	// nvmlMockNamespace isolates the chart under test from the default namespace.
	nvmlMockNamespace = "nvml-mock-system"

	nvmlMockSelector = "app.kubernetes.io/name=nvml-mock"
)

// builtImage is the nvml-mock image ref shared across parallel processes
// (built once on process #1 in SynchronizedBeforeSuite, then kind-loaded into
// the shared cluster).
var builtImage string

func TestE2E(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "nvml-mock E2E Suite")
}

// SynchronizedBeforeSuite builds the nvml-mock image ONCE per runner (process
// #1) and shares the ref to every parallel process.
var _ = SynchronizedBeforeSuite(func() []byte {
	if config.SkipBuild() {
		return []byte(config.Image())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	buildImage(ctx)
	return []byte(config.Image())
}, func(data []byte) {
	builtImage = string(data)
})
