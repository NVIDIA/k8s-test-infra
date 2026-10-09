// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package features declares Mokka's feature gates on top of the Kubernetes
// component-base feature gate, so operators use the same syntax and lifecycle
// they know from Kubernetes components.
package features

import (
	"testing"

	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/component-base/featuregate"
	featuregatetesting "k8s.io/component-base/featuregate/testing"
)

// Declare each gate as a featuregate.Feature constant here and add its
// lifecycle to versionedSpecs below. node-agent, nri-plugin and control-plane
// apply every gate, so the chart can pass one list to all of them. No other
// process applies gates: nvml-mock-ctl and the mock NVML and CUDA libraries
// always see the defaults, so code they share with the services must receive
// the decision as a value rather than call Enabled.
//
// Example:
//
//	// owner: @github-handle
//	// issue: https://github.com/NVIDIA/k8s-test-infra/issues/1234
//	//
//	// Selects the alternate placement planner.
//	AlternatePlanner featuregate.Feature = "AlternatePlanner"

// versionedSpecs records each gate's stage per Mokka minor release. Entries
// are separated by blank lines to keep gofmt from realigning the whole map
// when one is added or removed.
var versionedSpecs = map[featuregate.Feature]featuregate.VersionedSpecs{}

// gate is private to Mokka rather than the shared k8s.io/apiserver default, so
// Mokka's gates never mix with those of the Kubernetes libraries it imports.
//
// Its emulation version is the Kubernetes version of component-base, which is
// always newer than any Mokka release, so the newest spec of every gate
// applies. The Mokka versions in versionedSpecs record history; they do not
// depend on the version string a binary was built with.
var gate = featuregate.NewFeatureGate()

func init() {
	runtime.Must(gate.AddVersioned(versionedSpecs))
}

// Enabled reports whether a feature gate is enabled in this process.
func Enabled(f featuregate.Feature) bool {
	return gate.Enabled(f)
}

// SetFeatureGateDuringTest overrides a gate for the duration of a test and
// restores it on cleanup. Tests that call it must not run in parallel with
// other tests that read the same gate.
func SetFeatureGateDuringTest(tb testing.TB, f featuregate.Feature, value bool) {
	tb.Helper()
	featuregatetesting.SetFeatureGateDuringTest(tb, gate, f, value)
}
