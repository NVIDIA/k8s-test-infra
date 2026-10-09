// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package cpulocality decides which host CPUs are local to a PCI root
// complex. Both the driver's local_cpulist and NVML's CPU affinity report that
// fact to different consumers, so every stager that publishes it asks here.
package cpulocality

import (
	"os"
	"slices"

	"go.uber.org/zap"

	"github.com/NVIDIA/k8s-test-infra/internal/agent"
	"github.com/NVIDIA/k8s-test-infra/internal/agent/host"
	"github.com/NVIDIA/k8s-test-infra/internal/cpulist"
	"github.com/NVIDIA/k8s-test-infra/pkg/gpu/mocknvml/engine"
)

// kernelCPUOnlineRelPath lists the host's online CPUs, relative to /sys.
const kernelCPUOnlineRelPath = "devices/system/cpu/online"

// Local is the set of CPUs local to rc on a host whose online CPUs are online.
// A nil online leaves the profile's set untouched.
func Local(rc agent.RootComplex, coresPerNUMA int, online []int) []int {
	return hostLocal(profileCPUs(rc, coresPerNUMA), online)
}

// profileCPUs is the set of CPUs the profile places local to rc, synthesized
// the way the NVML engine synthesizes a GPU's CPU affinity so that both start
// from the same set. A root with no NUMA node has none.
func profileCPUs(rc agent.RootComplex, coresPerNUMA int) []int {
	if rc.CPUAffinity != "" {
		cpus, err := cpulist.Parse(rc.CPUAffinity)
		if err != nil {
			zap.L().Warn("ignoring unparseable cpu_affinity",
				zap.String("rootComplex", rc.ID), zap.Error(err))
			return nil
		}
		slices.Sort(cpus)
		return slices.Compact(cpus)
	}
	if rc.NUMANode < 0 {
		return nil
	}
	if coresPerNUMA <= 0 {
		coresPerNUMA = engine.DefaultCoresPerNUMA
	}

	cpus := make([]int, coresPerNUMA)
	for i := range cpus {
		cpus[i] = rc.NUMANode*coresPerNUMA + i
	}
	return cpus
}

// hostLocal keeps the profile's CPUs that the host has. A profile describes
// the machine it models, not the one Mokka runs on: a kernel never lists a CPU
// the machine lacks, and Slurm will not allocate a GPU whose local CPUs are all
// absent. When none of them exist every online CPU is local, as the kernel
// reports for a device with no locality.
func hostLocal(cpus, online []int) []int {
	if len(cpus) == 0 || online == nil {
		return cpus
	}

	kept := slices.DeleteFunc(slices.Clone(cpus), func(c int) bool { return !slices.Contains(online, c) })
	if len(kept) == 0 {
		return online
	}
	return kept
}

// Online reads the host's online CPUs, ascending, or nil when they cannot be
// read: callers then publish the profile's set, as they did before host CPUs
// were considered.
func Online(h *host.Host) []int {
	path := h.SysPath(kernelCPUOnlineRelPath)
	data, err := os.ReadFile(path)
	if err != nil {
		zap.L().Debug("host online CPUs unknown; publishing the profile's CPU locality",
			zap.String("path", path), zap.Error(err))
		return nil
	}

	cpus, err := cpulist.Parse(string(data))
	if err != nil || len(cpus) == 0 {
		zap.L().Warn("unreadable host online CPUs; publishing the profile's CPU locality",
			zap.String("path", path), zap.Error(err))
		return nil
	}
	slices.Sort(cpus)
	return slices.Compact(cpus)
}
