// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package nvidiasmi

import (
	"fmt"
	"regexp"
	"strings"
)

// ExcludedGPU is one line of `nvidia-smi -B`: a GPU the kernel module was told
// not to manage, which NVML lists but never enumerates.
type ExcludedGPU struct {
	BusID string
	UUID  string
}

var excludedGPULine = regexp.MustCompile(`^Excluded GPU \d+: (\S+) \(UUID: (\S+)\)$`)

// ParseExcludedGPUs decodes `nvidia-smi -B` output, in the order nvidia-smi
// lists the GPUs. Any line it does not recognise is an error, so an NVML
// failure is not mistaken for an empty list.
func ParseExcludedGPUs(out string) ([]ExcludedGPU, error) {
	gpus := []ExcludedGPU{}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "No excluded devices found." {
			continue
		}
		m := excludedGPULine.FindStringSubmatch(line)
		if m == nil {
			return nil, fmt.Errorf("unexpected nvidia-smi -B line %q", line)
		}
		gpus = append(gpus, ExcludedGPU{BusID: m[1], UUID: m[2]})
	}
	return gpus, nil
}
