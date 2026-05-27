// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package daemon

import (
	"context"
	"io"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/config"
	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/counters"
	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/render"
)

// renderTree drops a sysfs tree with HCAs configured per HCAsPerGPU.
func renderTreeForTest(t *testing.T, hcas int) string {
	t.Helper()
	dir := t.TempDir()
	err := render.Render(render.Options{
		IB:       config.Infiniband{Enabled: true, RateGbps: 400, HCAsPerGPU: hcas},
		GPUCount: 1,
		NodeName: "writer-test",
		Output:   dir,
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	return dir
}

func readUint(t *testing.T, p string) uint64 {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read %s: %v", p, err)
	}
	n, err := strconv.ParseUint(strings.TrimSpace(string(b)), 10, 64)
	if err != nil {
		t.Fatalf("parse %s: %v", p, err)
	}
	return n
}

func startCountersWriter(t *testing.T, w *CountersWriter) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("Run did not return after cancel")
		}
	})
}

func TestCountersWriter_GrowsBetweenTicks(t *testing.T) {
	root := renderTreeForTest(t, 1)
	p := filepath.Join(root, "sys/class/infiniband/mlx5_0/ports/1/counters/port_xmit_data_64")

	w, err := NewCountersWriter(CountersWriterOptions{
		Root:         root,
		Gen:          counters.Generator{NodeID: 0xab, RateGbps: 400},
		Epochs:       counters.NewEpochs(time.Now()),
		TickInterval: 50 * time.Millisecond,
		Log:          log.New(io.Discard, "", 0),
	})
	if err != nil {
		t.Fatalf("NewCountersWriter: %v", err)
	}
	startCountersWriter(t, w)

	time.Sleep(120 * time.Millisecond)
	v1 := readUint(t, p)
	time.Sleep(150 * time.Millisecond)
	v2 := readUint(t, p)
	if v2 <= v1 {
		t.Fatalf("port_xmit_data_64 did not grow: v1=%d v2=%d", v1, v2)
	}
}

func TestCountersWriter_HonorsResetEpoch(t *testing.T) {
	root := renderTreeForTest(t, 1)
	p := filepath.Join(root, "sys/class/infiniband/mlx5_0/ports/1/counters/port_xmit_data_64")
	epochs := counters.NewEpochs(time.Now().Add(-1 * time.Hour))

	w, _ := NewCountersWriter(CountersWriterOptions{
		Root:         root,
		Gen:          counters.Generator{NodeID: 0xab, RateGbps: 400},
		Epochs:       epochs,
		TickInterval: 50 * time.Millisecond,
		Log:          log.New(io.Discard, "", 0),
	})
	startCountersWriter(t, w)

	time.Sleep(120 * time.Millisecond)
	before := readUint(t, p)

	epochs.Reset(0, time.Now())
	deadline := time.Now().Add(500 * time.Millisecond)
	var after uint64
	for time.Now().Before(deadline) {
		after = readUint(t, p)
		if after < before {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("reset should drop counter: before=%d after=%d", before, after)
}

func TestCountersWriter_AtomicNoPartialFile(t *testing.T) {
	// Render, then race a reader against the writer for ~300ms.
	// Every read must parse to a uint64 (no torn writes / empty files).
	root := renderTreeForTest(t, 1)
	p := filepath.Join(root, "sys/class/infiniband/mlx5_0/ports/1/counters/port_xmit_data_64")

	w, _ := NewCountersWriter(CountersWriterOptions{
		Root:         root,
		Gen:          counters.Generator{NodeID: 0xab, RateGbps: 400},
		Epochs:       counters.NewEpochs(time.Now()),
		TickInterval: 5 * time.Millisecond,
		Log:          log.New(io.Discard, "", 0),
	})
	startCountersWriter(t, w)

	deadline := time.Now().Add(300 * time.Millisecond)
	for time.Now().Before(deadline) {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("read race: %v", err)
		}
		s := strings.TrimSpace(string(b))
		if s == "" {
			t.Fatalf("torn write (empty file)")
		}
		if _, err := strconv.ParseUint(s, 10, 64); err != nil {
			t.Fatalf("torn write %q: %v", s, err)
		}
	}
}

func TestCountersWriter_StopsOnCancel(t *testing.T) {
	root := renderTreeForTest(t, 1)
	w, _ := NewCountersWriter(CountersWriterOptions{
		Root:         root,
		Gen:          counters.Generator{NodeID: 0xab, RateGbps: 400},
		Epochs:       counters.NewEpochs(time.Now()),
		TickInterval: 50 * time.Millisecond,
		Log:          log.New(io.Discard, "", 0),
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	time.Sleep(60 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err != nil && err != context.Canceled {
			t.Fatalf("Run returned %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run did not return after cancel")
	}
}
