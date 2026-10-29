// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package numa

import (
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"

	"k8s.io/apimachinery/pkg/api/resource"

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
	config["memoryManagerPolicy"] = s.opts.MemoryManagerPolicy
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
	if s.opts.MemoryManagerPolicy == "Static" {
		if err := s.configureMemory(config, plan); err != nil {
			return err
		}
	}
	body, err := yaml.Marshal(config)
	if err != nil {
		return fmt.Errorf("encode kubelet config: %w", err)
	}
	return fsutil.Write(s.host.RootPath("numa/kubelet-config.yaml"), body, 0600)
}

func (s *Simulator) configureMemory(config map[string]any, plan *Plan) error {
	reserved, err := resource.ParseQuantity(s.opts.SystemReservedMemory)
	if err != nil || reserved.Sign() <= 0 {
		return fmt.Errorf("system reserved memory must be a positive quantity: %q", s.opts.SystemReservedMemory)
	}
	system, _ := config["systemReserved"].(map[string]any)
	if system == nil {
		system = map[string]any{}
	}
	system["memory"] = reserved.String()
	config["systemReserved"] = system
	var capacity int64
	for _, zone := range plan.Zones {
		capacity += int64(zone.MemoryKB) * 1024
	}
	total := reserved.Value()
	kube, _ := config["kubeReserved"].(map[string]any)
	if value, ok := kube["memory"]; ok {
		quantity, err := resource.ParseQuantity(fmt.Sprint(value))
		if err != nil || quantity.Sign() < 0 {
			return fmt.Errorf("invalid kubeReserved.memory: %v", value)
		}
		total += quantity.Value()
	}
	eviction, _ := config["evictionHard"].(map[string]any)
	threshold, ok := eviction["memory.available"]
	if !ok && (len(eviction) == 0 || config["mergeDefaultEvictionSettings"] == true) {
		threshold = "100Mi"
	}
	if threshold != nil {
		value := fmt.Sprint(threshold)
		if strings.HasSuffix(value, "%") {
			percent, err := strconv.ParseFloat(strings.TrimSuffix(value, "%"), 64)
			if err != nil || percent < 0 || percent >= 100 {
				return fmt.Errorf("invalid memory eviction threshold %q", value)
			}
			total += int64(float64(capacity) * percent / 100)
		} else {
			quantity, err := resource.ParseQuantity(value)
			if err != nil || quantity.Sign() < 0 {
				return fmt.Errorf("invalid memory eviction threshold %q", value)
			}
			total += quantity.Value()
		}
	}
	if total >= capacity {
		return fmt.Errorf("memory reservations leave no memory for workloads")
	}
	reservations := make([]map[string]any, 0, len(plan.Zones))
	for i, zone := range plan.Zones {
		amount := total / int64(len(plan.Zones))
		if int64(i) < total%int64(len(plan.Zones)) {
			amount++
		}
		if amount >= int64(zone.MemoryKB)*1024 {
			return fmt.Errorf("memory reservation exceeds zone %d capacity", zone.ID)
		}
		reservations = append(reservations, map[string]any{
			"numaNode": zone.ID,
			"limits":   map[string]string{"memory": resource.NewQuantity(amount, resource.BinarySI).String()},
		})
	}
	config["reservedMemory"] = reservations
	return nil
}
