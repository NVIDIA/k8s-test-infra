// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package numa

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"

	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/NVIDIA/k8s-test-infra/internal/cpulist"
	"github.com/NVIDIA/k8s-test-infra/internal/fsutil"
)

func (s *Simulator) stageKubeletConfig(plan *Plan) error {
	if !s.opts.ConfigureKubelet {
		return nil
	}
	original, err := os.ReadFile(s.host.RootPath("numa/original/kubelet-config.yaml"))
	if os.IsNotExist(err) {
		original, err = os.ReadFile(s.host.HostPath("/var/lib/kubelet/config.yaml"))
	}
	if err != nil {
		return fmt.Errorf("read original kubelet config: %w", err)
	}
	document, err := decodeKubeletConfig(original)
	if err != nil {
		return fmt.Errorf("decode kubelet config: %w", err)
	}
	config := &document.Settings
	config.TopologyManagerPolicy = s.opts.Policy
	config.TopologyManagerScope = "container"
	config.CPUManagerPolicy = s.opts.CPUManagerPolicy
	config.MemoryManagerPolicy = kubeletMemoryPolicy(s.opts.MemoryManagerPolicy)
	if err := s.configureManagers(config, plan); err != nil {
		return err
	}
	body, err := document.Bytes()
	if err != nil {
		return fmt.Errorf("encode kubelet config: %w", err)
	}
	return fsutil.Write(s.host.RootPath("numa/kubelet-config.yaml"), body, 0600)
}

// Kubelet requires "None"/"Static"; Mokka settings use lowercase.
func kubeletMemoryPolicy(policy string) string {
	if policy == "static" {
		return "Static"
	}
	return "None"
}

func (s *Simulator) configureManagers(config *kubeletSettings, plan *Plan) error {
	if err := s.configureCPU(config, plan); err != nil {
		return err
	}
	if s.opts.MemoryManagerPolicy == "static" {
		return s.configureMemory(config, plan)
	}
	return nil
}

func (s *Simulator) reservedCPUs(plan *Plan) (string, error) {
	reserved := s.opts.ReservedCPUs
	if reserved == "" {
		reserved = strconv.Itoa(plan.Zones[0].CPUs[0])
	}
	ids, err := cpulist.Parse(reserved)
	if err != nil || len(ids) == 0 {
		return "", fmt.Errorf("reserved CPUs must be a non-empty CPU list: %q", reserved)
	}
	var online []int
	for _, zone := range plan.Zones {
		online = append(online, zone.CPUs...)
	}
	for _, id := range ids {
		if !slices.Contains(online, id) {
			return "", fmt.Errorf("reserved CPU %d is not online", id)
		}
	}
	if len(ids) >= len(online) {
		return "", errors.New("reserved CPUs leave no exclusive CPUs available")
	}
	return cpulist.Format(ids), nil
}

func (s *Simulator) configureMemory(config *kubeletSettings, plan *Plan) error {
	reserved, err := resource.ParseQuantity(s.opts.SystemReservedMemory)
	if err != nil || reserved.Sign() <= 0 {
		return fmt.Errorf("system reserved memory must be a positive quantity: %q", s.opts.SystemReservedMemory)
	}
	config.SystemReserved.Memory = reserved.String()
	var capacity int64
	for _, zone := range plan.Zones {
		capacity += int64(zone.MemoryKB) * 1024
	}
	kubeAmount, err := memoryQuantity(config.KubeReserved.Memory)
	if err != nil {
		return err
	}
	evictionAmount, err := memoryEviction(config, capacity)
	if err != nil {
		return err
	}
	total := reserved.Value() + kubeAmount + evictionAmount
	if total >= capacity {
		return errors.New("memory reservations leave no memory for workloads")
	}
	reservations, err := memoryReservations(plan, total)
	if err != nil {
		return err
	}
	config.ReservedMemory = reservations
	return nil
}

func memoryQuantity(value string) (int64, error) {
	if value == "" {
		return 0, nil
	}
	quantity, err := resource.ParseQuantity(value)
	if err != nil || quantity.Sign() < 0 {
		return 0, fmt.Errorf("invalid memory quantity %v", value)
	}
	return quantity.Value(), nil
}

func memoryEviction(config *kubeletSettings, capacity int64) (int64, error) {
	threshold, ok := config.EvictionHard["memory.available"]
	if !ok && (len(config.EvictionHard) == 0 || config.MergeDefaultEvictionSettings) {
		threshold = "100Mi"
	}
	if threshold == "" {
		return 0, nil
	}
	value := threshold
	if !strings.HasSuffix(value, "%") {
		return memoryQuantity(value)
	}
	percent, err := strconv.ParseFloat(strings.TrimSuffix(value, "%"), 64)
	if err != nil || percent < 0 || percent >= 100 {
		return 0, fmt.Errorf("invalid memory eviction threshold %q", value)
	}
	return int64(float64(capacity) * percent / 100), nil
}

func memoryReservations(plan *Plan, total int64) ([]memoryReservation, error) {
	reservations := make([]memoryReservation, 0, len(plan.Zones))
	for i, zone := range plan.Zones {
		amount := total / int64(len(plan.Zones))
		if int64(i) < total%int64(len(plan.Zones)) {
			amount++
		}
		if amount >= int64(zone.MemoryKB)*1024 {
			return nil, fmt.Errorf("memory reservation exceeds zone %d capacity", zone.ID)
		}
		reservations = append(reservations, memoryReservation{
			NUMANode: zone.ID,
			Limits:   map[string]string{"memory": resource.NewQuantity(amount, resource.BinarySI).String()},
		})
	}
	return reservations, nil
}

func (s *Simulator) configureCPU(config *kubeletSettings, plan *Plan) error {
	if s.opts.CPUManagerPolicy != "static" {
		return nil
	}
	if s.opts.SystemReservedCPU != "" {
		return s.configureSharedCPU(config, plan)
	}
	reserved, err := s.reservedCPUs(plan)
	if err != nil {
		return err
	}
	config.ReservedSystemCPUs = reserved
	return nil
}

func (s *Simulator) configureSharedCPU(config *kubeletSettings, plan *Plan) error {
	quantity, err := resource.ParseQuantity(s.opts.SystemReservedCPU)
	if err != nil || quantity.Sign() <= 0 {
		return fmt.Errorf("system reserved CPU must be positive: %q", s.opts.SystemReservedCPU)
	}
	var cpus int
	for _, zone := range plan.Zones {
		cpus += len(zone.CPUs)
	}
	total := quantity.DeepCopy()
	if value := config.KubeReserved.CPU; value != "" {
		kubeCPU, err := resource.ParseQuantity(value)
		if err != nil || kubeCPU.Sign() < 0 {
			return fmt.Errorf("invalid kubeReserved.cpu: %v", value)
		}
		total.Add(kubeCPU)
	}
	if total.Value() >= int64(cpus) {
		return errors.New("CPU reservations leave no exclusive CPUs")
	}
	config.SystemReserved.CPU = quantity.String()
	config.ReservedSystemCPUs = ""
	return nil
}
