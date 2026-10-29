// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package numa

import (
	"fmt"
	"os"
	"slices"

	"github.com/NVIDIA/k8s-test-infra/internal/cpulist"
	"github.com/NVIDIA/k8s-test-infra/internal/fsutil"
	"sigs.k8s.io/yaml"
)

func (s *Simulator) stageKubeletConfig(plan *Plan) error {
	original, err := os.ReadFile(s.host.RootPath("numa/original/kubelet-config.yaml"))
	if os.IsNotExist(err) {
		original, err = os.ReadFile(s.host.HostPath("/var/lib/kubelet/config.yaml"))
	}
	if err != nil {
		return fmt.Errorf("read original kubelet config: %w", err)
	}
	config := map[string]any{}
	if err := yaml.Unmarshal(original, &config); err != nil {
		return fmt.Errorf("decode kubelet config: %w", err)
	}
	config["topologyManagerPolicy"] = s.opts.Policy
	config["topologyManagerScope"] = "container"
	config["cpuManagerPolicy"] = s.opts.CPUManagerPolicy
	config["memoryManagerPolicy"] = "None"
	if s.opts.CPUManagerPolicy == "static" {
		reserved := s.opts.ReservedCPUs
		if reserved == "" {
			reserved = fmt.Sprint(plan.Zones[0].CPUs[0])
		}
		ids, err := cpulist.Parse(reserved)
		if err != nil || len(ids) == 0 {
			return fmt.Errorf("reserved CPUs must be a non-empty CPU list: %q", reserved)
		}
		var online []int
		for _, zone := range plan.Zones {
			online = append(online, zone.CPUs...)
		}
		for _, id := range ids {
			if !slices.Contains(online, id) {
				return fmt.Errorf("reserved CPU %d is not online", id)
			}
		}
		if len(ids) >= len(online) {
			return fmt.Errorf("reserved CPUs leave no exclusive CPUs available")
		}
		config["reservedSystemCPUs"] = cpulist.Format(ids)
	}
	body, err := yaml.Marshal(config)
	if err != nil {
		return fmt.Errorf("encode kubelet config: %w", err)
	}
	return fsutil.Write(s.host.RootPath("numa/kubelet-config.yaml"), body, 0600)
}
