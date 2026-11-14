// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package nvidiasmi

import (
	"fmt"
	"regexp"
	"strconv"
)

// bannerVersion matches the version nvidia-smi prints in the banner of its
// default output: "NVIDIA-SMI 580.65.06".
var bannerVersion = regexp.MustCompile(`NVIDIA-SMI (\d+)\.\d+`)

// BranchProblems checks that the nvidia-smi whose default output is out comes
// from driver branch wantBranch. A node's nvidia-smi comes with its driver, and
// a binary from another branch misreads the mock's answers: NVML field ids and
// the NVLink count it addresses changed between branches.
func BranchProblems(out string, wantBranch int) []string {
	m := bannerVersion.FindStringSubmatch(out)
	if m == nil {
		return []string{"no NVIDIA-SMI version in the nvidia-smi banner"}
	}
	branch, err := strconv.Atoi(m[1])
	if err != nil {
		return []string{fmt.Sprintf("nvidia-smi banner branch %q: %v", m[1], err)}
	}
	if branch != wantBranch {
		return []string{fmt.Sprintf("nvidia-smi is from driver branch %d, the profile's driver is branch %d; "+
			"the image needs an nvidia-smi of that branch (deployments/nvml-mock/Dockerfile)", branch, wantBranch)}
	}
	return nil
}
