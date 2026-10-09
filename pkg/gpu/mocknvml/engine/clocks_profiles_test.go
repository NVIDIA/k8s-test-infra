// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package engine

import (
	"encoding/xml"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// readCapturedSupportedClocks returns the supported-clock table of the first GPU
// in a hardware capture, in the order nvidia-smi printed it — which is the order
// NVML returned it.
func readCapturedSupportedClocks(t *testing.T, sku string) []MemoryClockConfig {
	t.Helper()

	var doc struct {
		GPUs []struct {
			MemClocks []struct {
				Value    string   `xml:"value"`
				Graphics []string `xml:"supported_graphics_clock"`
			} `xml:"supported_clocks>supported_mem_clock"`
		} `xml:"gpu"`
	}
	raw, err := os.ReadFile(filepath.Join(hardwareCaptureDir(), "qx-"+sku+".xml"))
	require.NoErrorf(t, err, "no hardware capture for profile %q", sku)
	require.NoError(t, xml.Unmarshal(raw, &doc), "parse hardware capture for %s", sku)
	require.NotEmpty(t, doc.GPUs, "capture for %s declares no GPU", sku)

	mhz := func(s string) uint32 {
		t.Helper()
		v, err := strconv.ParseUint(strings.TrimSuffix(strings.TrimSpace(s), " MHz"), 10, 32)
		require.NoErrorf(t, err, "capture %s clock %q", sku, s)
		return uint32(v)
	}
	table := make([]MemoryClockConfig, 0, len(doc.GPUs[0].MemClocks))
	for _, mc := range doc.GPUs[0].MemClocks {
		entry := MemoryClockConfig{FreqMHz: mhz(mc.Value)}
		for _, g := range mc.Graphics {
			entry.GraphicsClocks = append(entry.GraphicsClocks, mhz(g))
		}
		table = append(table, entry)
	}
	require.NotEmpty(t, table, "capture for %s has no supported clocks", sku)
	return table
}

// TestProfileSupportedClocksMatchHardwareCapture holds every shipped profile's
// supported-clock table, in both copies, to the board it models.
//
// The table used to be a hand-picked handful of points that no real board
// reports, and several profiles listed a memory clock their own capture does
// not have. Now that the application clock setter validates against the
// table, a wrong entry is not just a wrong row in `nvidia-smi -q`: it makes
// `nvidia-smi -ac` refuse a pair the real board accepts. The clock block is
// checked against the table too, because a max or default the table does not
// contain is a pair that a reset would restore and a set would then refuse.
func TestProfileSupportedClocksMatchHardwareCapture(t *testing.T) {
	t.Parallel()

	for _, src := range profileSources() {
		profiles := src.profiles(t)
		for _, sku := range sortedKeys(profiles) {
			path := profiles[sku]
			t.Run(src.label+"/"+sku, func(t *testing.T) {
				t.Parallel()

				cfg, err := LoadYAMLConfig(path)
				require.NoError(t, err, "load profile %s", path)
				dd := cfg.DeviceDefaults
				require.NotNil(t, dd.SupportedClocks, "%s declares no supported_clocks", path)
				require.Equal(t, readCapturedSupportedClocks(t, sku), dd.SupportedClocks.MemoryClocks,
					"%s supported_clocks must be the captured board's table, highest first", path)

				require.NotNil(t, dd.Clocks, "%s declares no clocks block", path)
				top := dd.SupportedClocks.MemoryClocks[0]
				require.Equal(t, top.FreqMHz, dd.Clocks.MemoryMax,
					"%s clocks.memory_max must be the highest supported memory clock", path)
				require.Equal(t, top.GraphicsClocks[0], dd.Clocks.GraphicsMax,
					"%s clocks.graphics_max must be the highest graphics clock at that memory clock", path)

				gfx := supportedGraphicsAt(dd.SupportedClocks, dd.Clocks.MemoryAppDefault)
				require.Containsf(t, gfx, dd.Clocks.GraphicsAppDefault,
					"%s default application clocks %d/%d MHz must be a supported pair",
					path, dd.Clocks.MemoryAppDefault, dd.Clocks.GraphicsAppDefault)
			})
		}
	}
}

func supportedGraphicsAt(sc *SupportedClocksConfig, memMHz uint32) []uint32 {
	i := slices.IndexFunc(sc.MemoryClocks, func(m MemoryClockConfig) bool { return m.FreqMHz == memMHz })
	if i < 0 {
		return nil
	}
	return sc.MemoryClocks[i].GraphicsClocks
}
