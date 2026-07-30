// Copyright (c) 2026, NVIDIA CORPORATION.  All rights reserved.
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package allocwatch

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/NVIDIA/k8s-test-infra/pkg/gpu/mockctl"
	"github.com/NVIDIA/k8s-test-infra/pkg/gpu/mocknvml/engine"
)

// DefaultInterval is how often the node's allocation is re-read. The engine
// re-reads the override file at most once per its own TTL (1s by default), so
// polling much faster only burns kubelet CPU without being observable.
const DefaultInterval = 2 * time.Second

// DevicesFromConfig resolves the GPU set the mock exposes from the loaded
// profile, keyed by the UUID the device plugin advertises — which is what
// pod-resources reports claims against.
func DevicesFromConfig(cfg *engine.Config) []Device {
	devices := make([]Device, 0, cfg.NumDevices)
	for i := 0; i < cfg.NumDevices; i++ {
		dev := Device{Index: i, UUID: cfg.GetDeviceUUID(i)}
		if dc := cfg.GetDeviceConfig(i); dc != nil && dc.Memory != nil {
			dev.TotalBytes = dc.Memory.TotalBytes
			dev.ReservedBytes = dc.Memory.ReservedBytes
		}
		devices = append(devices, dev)
	}
	return devices
}

// Publish merges readings into the override document at path.
//
// Read-modify-write under the same lock nvml-mock-ctl takes, so a human running
// `nvml-mock-ctl temp` while the watcher runs does not lose their edit and the
// watcher does not lose its reading. Only the memory block of each device is
// touched; everything else in the document is carried through untouched.
func Publish(path string, readings []Reading) error {
	unlock, err := mockctl.LockOverride(path)
	if err != nil {
		return fmt.Errorf("lock %s: %w", path, err)
	}
	defer unlock()

	doc, err := mockctl.Load(path)
	if err != nil {
		return fmt.Errorf("load %s: %w", path, err)
	}

	for _, r := range readings {
		// SetFields deep-merges, so used_bytes and free_bytes are replaced
		// while any other key under the device survives.
		doc.SetFields(mockctl.Target{Index: r.Index}, map[string]any{
			"memory": map[string]any{
				"used_bytes": r.UsedBytes,
				"free_bytes": r.FreeBytes,
			},
		})
	}

	if err := mockctl.WriteAtomic(path, doc); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// Watcher polls the node's GPU allocation and republishes the memory readings.
type Watcher struct {
	Lister       Lister
	Devices      []Device
	Policy       Policy
	OverridePath string
	Interval     time.Duration
}

// Run polls until ctx is cancelled. Cancellation is a clean shutdown, not an
// error.
//
// A failed poll is logged and skipped rather than fatal, and publishes nothing.
// Both halves of that matter: exiting would freeze every GPU at its last
// reading for the life of the pod, and publishing zeros on a failed read would
// report the whole node idle every time the kubelet is briefly unreachable.
func (w *Watcher) Run(ctx context.Context) error {
	interval := w.Interval
	if interval <= 0 {
		interval = DefaultInterval
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	w.pollOnce(ctx) // publish immediately rather than after one full interval

	for {
		select {
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.Canceled) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return nil
			}
			return ctx.Err()
		case <-ticker.C:
			w.pollOnce(ctx)
		}
	}
}

func (w *Watcher) pollOnce(ctx context.Context) {
	claims, err := w.Lister.List(ctx)
	if err != nil {
		if ctx.Err() == nil {
			log.Printf("[allocwatch] list allocation: %v (keeping the previous reading)", err)
		}
		return
	}

	readings := Reconcile(w.Devices, claims, w.Policy)
	if err := Publish(w.OverridePath, readings); err != nil {
		log.Printf("[allocwatch] publish: %v", err)
	}
}
