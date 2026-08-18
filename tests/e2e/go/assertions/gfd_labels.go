// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package assertions

import (
	"fmt"
	"sort"
	"strconv"
)

// This file carries no build tag on purpose. The rest of the package is
// //go:build e2e because it imports the Kubernetes client, which would drag a
// cluster dependency into the normal unit-test run. The derivation and
// comparison below need nothing but the standard library, so keeping them
// untagged is what makes their tests run in the regular `go test ./...` job
// rather than only under -tags e2e. The cluster-facing poll that consumes them
// lives in gfd_labels_wait.go.

// GFD label keys asserted against GPU Feature Discovery.
const (
	GFDLabelProduct = "nvidia.com/gpu.product"
	GFDLabelMemory  = "nvidia.com/gpu.memory"
	GFDLabelCount   = "nvidia.com/gpu.count"
	GFDLabelMachine = "nvidia.com/gpu.machine"
)

// ExpectedMachineTypeLabel is the gpu.machine label GFD publishes from the
// mock DMI identity. It is separate from ExpectedGFDLabels because those come
// from NVML and were already correct while this one read "unknown": it
// requires the rendered sysfs tree to reach the GFD container (#673).
//
// gpu.mode is deliberately NOT asserted here. It also needs the tree, but GFD
// derives the BDF it looks up from NVML's busId, which the mock reports in the
// wrong domain width (#671) — so mode stays "unknown" until that lands, and
// asserting it would couple this expectation to an unrelated fix.
func ExpectedMachineTypeLabel(machineType string) map[string]string {
	return map[string]string{GFDLabelMachine: machineType}
}

// ExpectedGFDLabels derives the GFD labels a node must carry from the profile,
// rather than from the node itself. Deriving them independently is what makes
// the assertion discriminating: if GFD stopped reading the mock NVML, the count
// would go absent or wrong and the comparison would catch it.
func ExpectedGFDLabels(product string, memoryMiB, gpuCount int) map[string]string {
	return map[string]string{
		GFDLabelProduct: product,
		GFDLabelMemory:  strconv.Itoa(memoryMiB),
		GFDLabelCount:   strconv.Itoa(gpuCount),
	}
}

// DiffGFDLabels returns one human-readable problem per expected label that is
// missing or carries the wrong value. An empty result means the node matches.
// It reports every problem rather than stopping at the first, so one run shows
// the whole picture.
func DiffGFDLabels(want, got map[string]string) []string {
	keys := make([]string, 0, len(want))
	for key := range want {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	var problems []string
	for _, key := range keys {
		actual, present := got[key]
		switch {
		case !present || actual == "":
			problems = append(problems, fmt.Sprintf("label %s missing (want %q)", key, want[key]))
		case actual != want[key]:
			problems = append(problems, fmt.Sprintf("label %s = %q, want %q", key, actual, want[key]))
		}
	}

	return problems
}
