//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package e2e is the Go/Ginkgo end-to-end suite for nvml-mock. Cluster
// provisioning and the mock/consumer rollout are owned externally — Tilt in
// both local dev (`make cluster-create` + `tilt up -- <flags>`) and CI (via
// `tilt ci`) — and the suite attaches to the resulting Kind cluster via
// E2E_KUBE_CONTEXT / E2E_CLUSTER_NAME to observe and assert. Both env vars
// default to what `make cluster-create` produces (mokka / kind-mokka), so the
// local `make e2e-<scenario>` targets work without extra env once the cluster
// is up; CI overrides them when the workflow provisions a different name.
// Scenarios that reshape the mock mid-run (NRI's NRI/IMEX/topology variants,
// GPU Operator's injectXid path) do so via `helm upgrade --install`; the rest
// just assert.
//
// The suite is gated by the `e2e` build tag so it never affects the fast
// `go test ./...` / `go build ./...` paths; run it with
// `ginkgo --tags e2e ./tests/e2e/go/...` (see the Makefile `e2e` target).
package e2e

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/config"
)

func TestE2E(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "nvml-mock E2E Suite")
}

// SynchronizedBeforeSuite propagates E2E_IMAGE from process #1 to every
// parallel process so builtImage is consistent even across Ginkgo's process
// pool.
var _ = SynchronizedBeforeSuite(func() []byte {
	return []byte(config.Image())
}, func(data []byte) {
	builtImage = string(data)
})
