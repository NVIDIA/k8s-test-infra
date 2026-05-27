// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package daemon

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/counters"
	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/sysfs"
)

// CountersWriterOptions configures a CountersWriter. Root is the
// MOCK_IB_ROOT sysfs prefix (the same value the renderer used). Gen and
// Epochs are shared with the PMA path so sysfs and perfquery agree.
type CountersWriterOptions struct {
	Root         string
	Gen          counters.Generator
	Epochs       *counters.Epochs
	TickInterval time.Duration
	Log          *log.Logger
}

// CountersWriter periodically rewrites sysfs counter files with values
// computed from Gen + Epochs. One instance handles all local CAs.
type CountersWriter struct {
	opts CountersWriterOptions
	cas  []caTarget
}

// caTarget caches the per-CA directory paths so we don't recompute them
// each tick.
type caTarget struct {
	idx           int
	countersDir   string
	hwCountersDir string
}

// NewCountersWriter scans Root for CAs and prepares the writer. Returns
// an error if Root has no CAs (writer would be a no-op).
func NewCountersWriter(opts CountersWriterOptions) (*CountersWriter, error) {
	if opts.Root == "" {
		return nil, fmt.Errorf("counters writer: Root required")
	}
	if opts.Epochs == nil {
		return nil, fmt.Errorf("counters writer: Epochs required")
	}
	if opts.TickInterval <= 0 {
		return nil, fmt.Errorf("counters writer: TickInterval must be > 0")
	}
	if opts.Log == nil {
		opts.Log = log.Default()
	}
	ports, err := sysfs.Scan(opts.Root)
	if err != nil {
		return nil, fmt.Errorf("counters writer: scan %s: %w", opts.Root, err)
	}
	var cas []caTarget
	for _, p := range ports {
		idx, ok := caIndex(p.CAName)
		if !ok {
			opts.Log.Printf("counters writer: skip non-mlx5 CA %q", p.CAName)
			continue
		}
		portDir := filepath.Join(opts.Root, "sys/class/infiniband", p.CAName, "ports", strconv.Itoa(p.Port))
		cas = append(cas, caTarget{
			idx:           idx,
			countersDir:   filepath.Join(portDir, "counters"),
			hwCountersDir: filepath.Join(portDir, "hw_counters"),
		})
	}
	return &CountersWriter{opts: opts, cas: cas}, nil
}

// Run rewrites counter files on every tick until ctx is canceled.
func (w *CountersWriter) Run(ctx context.Context) error {
	t := time.NewTicker(w.opts.TickInterval)
	defer t.Stop()
	w.tick(ctx, time.Now()) // write once immediately so consumers don't wait a full tick.
	for {
		select {
		case <-ctx.Done():
			return nil
		case now := <-t.C:
			w.tick(ctx, now)
		}
	}
}

func (w *CountersWriter) tick(ctx context.Context, now time.Time) {
	for _, ca := range w.cas {
		elapsed := w.opts.Epochs.Elapsed(ca.idx, now)
		for _, e := range counters.Catalog {
			select {
			case <-ctx.Done():
				return
			default:
			}
			v := w.opts.Gen.Value(ca.idx, e, elapsed)
			var dir string
			switch e.Surface {
			case counters.SurfaceCounters:
				dir = ca.countersDir
			case counters.SurfaceHWCounters:
				dir = ca.hwCountersDir
			default:
				continue
			}
			if err := writeAtomic(filepath.Join(dir, e.Name), v); err != nil {
				w.opts.Log.Printf("counters writer: %s: %v", e.Name, err)
			}
		}
	}
}

// writeAtomic writes "<v>\n" via tempfile + rename so concurrent
// readers (Prometheus exporters, perfquery) never see a torn write.
func writeAtomic(path string, v uint64) error {
	dir := filepath.Dir(path)
	name := filepath.Base(path)
	tmp, err := os.CreateTemp(dir, "."+name+".tmp.*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if _, err := fmt.Fprintf(tmp, "%d\n", v); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return nil
}

// caIndex parses "mlx5_<N>" -> N. Returns false on names that don't fit
// (the writer skips those CAs).
//
// The parsed value is bounded to 16 bits because the generator FNV-seed
// packs it into a uint32 alongside NodeID (see counters.Generator.seed).
// Real profiles cap at 16 CAs; we accept anything up to 0xffff so CodeQL's
// taint analysis cannot flag a silent strconv.Atoi -> uint16 truncation
// downstream, and pathological "mlx5_99999999" names are rejected here
// rather than wrapping around inside the generator.
func caIndex(caName string) (int, bool) {
	s, ok := strings.CutPrefix(caName, "mlx5_")
	if !ok {
		return 0, false
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, false
	}
	if n < 0 || n > 0xffff {
		return 0, false
	}
	return n, true
}
