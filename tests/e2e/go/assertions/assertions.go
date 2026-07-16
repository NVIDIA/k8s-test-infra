//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package assertions ports the bash validate-*.sh checks and the inline
// workflow verification steps to typed Go helpers, one per concern (not one
// blurred AssertIB). Checks run through kubectl pod exec and typed Kubernetes
// reads. Each helper is a Ginkgo helper (GinkgoHelper) so failures point at the
// calling spec line, and every exec result is attached to the Gomega failure
// message.
package assertions

import (
	"context"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// NVMLLink is the versioned NVML symlink the chart installs.
const NVMLLink = "/var/lib/nvml-mock/driver/usr/lib64/libnvidia-ml.so.1"

var intRE = regexp.MustCompile(`[0-9]+`)

// sleepCtx waits d or until ctx is cancelled (a bounded sample gap, not a
// readiness poll).
func sleepCtx(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}

func countLinesWithPrefix(s, prefix string) int {
	c := 0
	for _, l := range strings.Split(s, "\n") {
		if strings.HasPrefix(l, prefix) {
			c++
		}
	}
	return c
}

func countMatches(s, pattern string) int {
	re := regexp.MustCompile(pattern)
	return len(re.FindAllString(s, -1))
}

func sumInts(s string) int {
	sum := 0
	for _, m := range intRE.FindAllString(s, -1) {
		v, err := strconv.Atoi(m)
		if err == nil {
			sum += v
		}
	}
	return sum
}
