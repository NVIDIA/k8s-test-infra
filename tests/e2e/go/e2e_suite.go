//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package e2e

const (
	// ClusterName is the single shared cluster the whole suite runs against.
	ClusterName = "nvml-mock-e2e"

	// nvmlMockNamespace isolates the chart under test from the default namespace.
	nvmlMockNamespace = "mokka"

	nvmlMockSelector = "app.kubernetes.io/name=nvml-mock"
)

// builtImage is the nvml-mock image ref shared across parallel processes
// (built once on process #1 in SynchronizedBeforeSuite, then kind-loaded into
// the shared cluster). Declared here rather than in e2e_suite_test.go so
// non-test files in the same package (suite_cluster.go, suite_helm.go, ...)
// can reference it — identifiers declared in _test.go files are only visible
// to other _test.go files.
var builtImage string
