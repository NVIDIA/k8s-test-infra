// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"

	mokkav1 "github.com/NVIDIA/k8s-test-infra/api/v1alpha1"
)

// Publish only a complete inventory. Scrapes do not query the API server.
type metricsSnapshot struct {
	text    string
	updated time.Time
}

type metricsEndpoint struct {
	snapshot atomic.Pointer[metricsSnapshot]
}

func serveMetrics(address string) (*metricsEndpoint, func(), error) {
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return nil, nil, err
	}
	endpoint := &metricsEndpoint{}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /metrics", func(w http.ResponseWriter, _ *http.Request) {
		snapshot := endpoint.snapshot.Load()
		if snapshot == nil || time.Since(snapshot.updated) > time.Minute {
			http.Error(w, "waiting for rack inventory", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		_, _ = fmt.Fprint(w, snapshot.text)
	})
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second, WriteTimeout: 15 * time.Second}
	go func() {
		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
			zap.L().Error("metrics server stopped", zap.Error(err))
		}
	}()
	return endpoint, func() { _ = server.Close() }, nil
}

type gpuMetrics struct{ strings.Builder }

func (e *metricsEndpoint) publish(metrics *gpuMetrics) {
	e.snapshot.Store(&metricsSnapshot{text: metrics.String(), updated: time.Now()})
}

func percent(pod *corev1.Pod, key string, fallback float64) float64 {
	v, err := strconv.ParseFloat(pod.Annotations["mokka.nvidia.com/"+key], 64)
	if err != nil || math.IsNaN(v) || v < 0 || v > 100 {
		return fallback
	}
	return v
}

type gpuOwner struct {
	pod       *corev1.Pod
	container string
}

func (m *gpuMetrics) addBoundNode(node string, slot *mokkav1.SGPURackNode, ref mokkav1.SGPURackProfileReference, profiles map[string]*mokkav1.SGPURackProfile, pods []corev1.Pod, allocations assignments) error {
	profile := profiles[ref.Name]
	if profile == nil || profile.UID != ref.UID || profile.Generation != ref.Generation {
		return fmt.Errorf("rack profile %s revision unavailable", ref.Name)
	}
	m.addNode(node, slot, profile, pods, allocations)
	return nil
}

func (m *gpuMetrics) addNode(node string, slot *mokkav1.SGPURackNode, profile *mokkav1.SGPURackProfile, pods []corev1.Pod, allocations assignments) {
	owners := map[string]gpuOwner{}
	for i := range pods {
		p := &pods[i]
		for container, ids := range allocations[p.UID] {
			for _, id := range ids {
				owners[id] = gpuOwner{p, container}
			}
		}
	}
	gpuSpec := profile.Spec.Node.GPUs
	bytes := gpuSpec.Memory.Capacity.Value()
	if gpuSpec.Memory.Reserved != nil {
		bytes -= gpuSpec.Memory.Reserved.Value()
	}
	totalMiB := float64(bytes) / (1024 * 1024)
	for _, gpu := range slot.GPUs {
		labels := fmt.Sprintf("Hostname=%q,UUID=%q,gpu=%q,device=%q,pci_bus_id=%q,modelName=%q", node, gpu.UUID, strconv.Itoa(int(gpu.Index)), fmt.Sprintf("nvidia%d", gpu.MinorNumber), gpu.PCIAddress, gpuSpec.Model.ProductName)
		var utilization, usedMiB float64
		if owner, ok := owners[gpu.UUID]; ok {
			p := owner.pod
			labels += fmt.Sprintf(",pod=%q,namespace=%q,container=%q", p.Name, p.Namespace, owner.container)
			utilization = percent(p, "gpu-utilization", 0)
			usedMiB = totalMiB * percent(p, "gpu-memory-used", 50) / 100
		}
		fmt.Fprintf(&m.Builder, "DCGM_FI_DEV_GPU_UTIL{%s} %g\nDCGM_FI_DEV_FB_USED{%s} %g\nDCGM_FI_DEV_FB_FREE{%s} %g\n", labels, utilization, labels, usedMiB, labels, totalMiB-usedMiB)
	}
}
