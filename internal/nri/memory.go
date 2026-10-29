// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package nri

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/containerd/nri/pkg/api"
	"go.uber.org/zap"
	"google.golang.org/protobuf/proto"

	"github.com/NVIDIA/k8s-test-infra/internal/cpulist"
)

// KernelMemoryNodes reads the process's kernel-enforced memory-node allowance,
// independent of the simulated sysfs view seen by kubelet.
func KernelMemoryNodes() (string, error) {
	status, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return "", fmt.Errorf("read kernel memory nodes: %w", err)
	}
	for line := range strings.SplitSeq(string(status), "\n") {
		if value, ok := strings.CutPrefix(line, "Mems_allowed_list:"); ok {
			ids, err := cpulist.Parse(strings.TrimSpace(value))
			if err != nil || len(ids) == 0 {
				return "", fmt.Errorf("invalid kernel memory-node allowance %q", value)
			}
			return cpulist.Format(ids), nil
		}
	}
	return "", errors.New("process status has no memory-node allowance")
}

func (p *Plugin) memoryNodes(resources *api.LinuxResources) (string, bool, error) {
	requested := resources.GetCpu().GetMems()
	if p.cfg.MemoryNodes == "" || requested == "" {
		return "", false, nil
	}
	want, err := cpulist.Parse(requested)
	if err != nil {
		return "", false, fmt.Errorf("invalid container memory nodes %q: %w", requested, err)
	}
	allowed, err := cpulist.Parse(p.cfg.MemoryNodes)
	if err != nil || len(allowed) == 0 {
		return "", false, fmt.Errorf("invalid host memory nodes %q", p.cfg.MemoryNodes)
	}
	var mapped []int
	for _, node := range want {
		if slices.Contains(allowed, node) {
			mapped = append(mapped, node)
		}
	}
	if len(mapped) == 0 {
		mapped = allowed
	}
	result := cpulist.Format(mapped)
	changed := result != cpulist.Format(want)
	if changed {
		zap.L().Info("mapping simulated memory affinity to host nodes", zap.String("simulated", requested), zap.String("host", result))
	}
	return result, changed, nil
}

// UpdateContainer keeps kubelet resource reconciliation from reapplying a
// simulated memory-node ID that the Linux kernel cannot use.
func (p *Plugin) UpdateContainer(_ context.Context, _ *api.PodSandbox, container *api.Container, resources *api.LinuxResources) ([]*api.ContainerUpdate, error) {
	defer p.health.begin()()
	mems, changed, err := p.memoryNodes(resources)
	if err != nil || !changed {
		return nil, err
	}
	updated := proto.Clone(resources).(*api.LinuxResources)
	updated.Cpu.Mems = mems
	return []*api.ContainerUpdate{{ContainerId: container.GetId(), Linux: &api.LinuxContainerUpdate{Resources: updated}}}, nil
}
