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

// GPM (GPU Performance Monitoring) sample engine. DCGM's profiling module
// (DCGM_FI_PROF_* / dcgmi dmon -e 1001..) reads Hopper+ GPUs exclusively
// through the NVML GPM API: it allocates two opaque sample buffers, snapshots
// them ~100ms apart with nvmlGpmSampleGet, and derives activity ratios and
// throughput rates with nvmlGpmMetricsGet. The pre-Hopper path (perfworks/DCP)
// is driver-internal and cannot be mocked, so GPM is the only route to
// profiling metrics for the mock.
//
// A sample handle is an 8-byte C allocation holding a registry key (cgo
// forbids storing Go pointers in C memory); the snapshot itself lives here.
// Activity metrics are derived from the same utilization source as
// nvmlDeviceGetUtilizationRates — including the dynamic-metrics simulator, so
// PROF metrics vary over time exactly when DEV utilization does — and NVLink
// rates come from the boot-anchored NodeFabric counters, keeping GPM numbers
// consistent with the NVLink field values DCGM reads elsewhere.

package engine

import (
	"sync"
	"time"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
)

// nvmlGpmMetricId_t values, mirrored from
// vendor/github.com/NVIDIA/go-nvml/pkg/nvml/nvml.h (NVML_GPM_METRIC_*).
const (
	gpmMetricGraphicsUtil        = 1
	gpmMetricSMUtil              = 2
	gpmMetricSMOccupancy         = 3
	gpmMetricIntegerUtil         = 4
	gpmMetricAnyTensorUtil       = 5
	gpmMetricDFMATensorUtil      = 6
	gpmMetricHMMATensorUtil      = 7
	gpmMetricDMMATensorUtil      = 8
	gpmMetricIMMATensorUtil      = 9
	gpmMetricDramBwUtil          = 10
	gpmMetricFP64Util            = 11
	gpmMetricFP32Util            = 12
	gpmMetricFP16Util            = 13
	gpmMetricPcieTxPerSec        = 20
	gpmMetricPcieRxPerSec        = 21
	gpmMetricNvlinkTotalRxPerSec = 60
	gpmMetricNvlinkTotalTxPerSec = 61
	gpmMetricNvlinkL0RxPerSec    = 62
	gpmMetricNvlinkL17TxPerSec   = 97
)

// Scaling ratios applied to the GPU utilization percentage to derive the
// per-pipe activity metrics. The absolute values are arbitrary but chosen so
// a busy mock GPU reports the rough shape of a Hopper training workload
// (tensor-dominated, minor FP64/integer); what matters for consumers is that
// each metric is stable, distinguishable, and tracks utilization.
const (
	gpmRatioSMOccupancy = 0.75
	gpmRatioInteger     = 0.10
	gpmRatioAnyTensor   = 0.50
	gpmRatioHMMA        = 0.40
	gpmRatioDMMA        = 0.05
	gpmRatioIMMA        = 0.05
	gpmRatioDFMA        = 0.02
	gpmRatioFP64        = 0.05
	gpmRatioFP32        = 0.25
	gpmRatioFP16        = 0.35

	// PCIe throughput reported at full utilization, in MiB/s. Overridable
	// per device via gpm.pcie_tx_mib_per_sec / gpm.pcie_rx_mib_per_sec.
	gpmDefaultPcieMiBPerSec = 2048
)

// gpmSnapshot is the engine-side content of one GPM sample buffer.
type gpmSnapshot struct {
	taken bool
	ts    time.Time
	// Instantaneous utilization percentages at sample time (dynamic-metrics
	// aware). Activity ratios are computed from the newer of the two samples.
	utilGPU    float64
	utilMemory float64
	// Cumulative per-link NVLink byte counters at sample time; MetricsGet
	// derives bytes/sec from the delta between the two samples.
	nvlinkRx [nvLinkMaxLinks]uint64
	nvlinkTx [nvLinkMaxLinks]uint64
	// PCIe MiB/s at sample time (utilization-scaled config value).
	pcieTxMiBPerSec float64
	pcieRxMiBPerSec float64
}

// gpmRegistry maps sample-handle keys to snapshots. Package-level because
// sample buffers are process-scoped opaque allocations, not per-engine state;
// the embedded nv-hostengine polls from multiple threads, hence the mutex.
var (
	gpmMu      sync.Mutex
	gpmSamples = map[uint64]*gpmSnapshot{}
	gpmNextKey uint64
)

// GpmSampleAlloc registers a new sample buffer and returns its registry key.
func GpmSampleAlloc() uint64 {
	gpmMu.Lock()
	defer gpmMu.Unlock()
	gpmNextKey++
	key := gpmNextKey
	gpmSamples[key] = &gpmSnapshot{}
	debugLog("[NVML] nvmlGpmSampleAlloc -> key=%d\n", key)
	return key
}

// GpmSampleFree releases a sample buffer. Unknown keys report false so the
// bridge can return INVALID_ARGUMENT (double free / never allocated).
func GpmSampleFree(key uint64) bool {
	gpmMu.Lock()
	defer gpmMu.Unlock()
	if _, ok := gpmSamples[key]; !ok {
		return false
	}
	delete(gpmSamples, key)
	debugLog("[NVML] nvmlGpmSampleFree(key=%d)\n", key)
	return true
}

// GpmSnapshotInto snapshots the device's current activity into the sample buffer.
func (d *ConfigurableDevice) GpmSnapshotInto(key uint64) nvml.Return {
	if supported, _ := d.GetGpmSupport(); supported == 0 {
		return nvml.ERROR_NOT_SUPPORTED
	}
	util, ret := d.GetUtilizationRates()
	if ret != nvml.SUCCESS {
		return ret
	}

	// The sample clock must match the clock the NVLink counters accrue on
	// (fabric.now), or MetricsGet would divide one clock's counter delta by
	// the other clock's elapsed time. fabric.now is wall clock in production
	// and overridable in tests.
	ts := time.Now()
	if d.fabric != nil {
		ts = d.fabric.now()
	}

	snap := &gpmSnapshot{
		taken:      true,
		ts:         ts,
		utilGPU:    float64(util.Gpu),
		utilMemory: float64(util.Memory),
	}

	utilFrac := snap.utilGPU / 100.0
	snap.pcieTxMiBPerSec = utilFrac * float64(d.gpmPcieMiBPerSec(true))
	snap.pcieRxMiBPerSec = utilFrac * float64(d.gpmPcieMiBPerSec(false))

	if f := d.fabric; f != nil {
		for link := 0; link < nvLinkMaxLinks; link++ {
			if l, ok := f.Link(d.index, link); ok && l.Active {
				rx, tx := f.NvLinkCounters(d.index, link, ts)
				snap.nvlinkRx[link] = rx
				snap.nvlinkTx[link] = tx
			}
		}
	}

	gpmMu.Lock()
	defer gpmMu.Unlock()
	if _, ok := gpmSamples[key]; !ok {
		return nvml.ERROR_INVALID_ARGUMENT
	}
	gpmSamples[key] = snap
	debugLog("[NVML] nvmlGpmSampleGet(key=%d) -> gpu=%.0f%% mem=%.0f%%\n", key, snap.utilGPU, snap.utilMemory)
	return nvml.SUCCESS
}

// gpmPcieMiBPerSec returns the full-utilization PCIe rate for one direction.
func (d *ConfigurableDevice) gpmPcieMiBPerSec(tx bool) uint64 {
	if d.config != nil && d.config.GPM != nil {
		if tx && d.config.GPM.PcieTxMiBPerSec > 0 {
			return d.config.GPM.PcieTxMiBPerSec
		}
		if !tx && d.config.GPM.PcieRxMiBPerSec > 0 {
			return d.config.GPM.PcieRxMiBPerSec
		}
	}
	return gpmDefaultPcieMiBPerSec
}

// GpmMetricsGet computes the requested metrics from two samples. It returns
// one value and one per-metric status per requested id; the overall call
// succeeds as long as the sample handles are valid (per-metric errors mirror
// real NVML, which reports unknown ids in metrics[i].nvmlReturn).
func GpmMetricsGet(key1, key2 uint64, ids []uint32) ([]float64, []nvml.Return, nvml.Return) {
	gpmMu.Lock()
	s1, ok1 := gpmSamples[key1]
	s2, ok2 := gpmSamples[key2]
	gpmMu.Unlock()
	if !ok1 || !ok2 || !s1.taken || !s2.taken {
		return nil, nil, nvml.ERROR_INVALID_ARGUMENT
	}
	// Order-independent: DCGM passes (older, newer) but nothing guarantees it.
	if s2.ts.Before(s1.ts) {
		s1, s2 = s2, s1
	}
	dt := s2.ts.Sub(s1.ts).Seconds()

	values := make([]float64, len(ids))
	rets := make([]nvml.Return, len(ids))
	for i, id := range ids {
		values[i], rets[i] = gpmMetricValue(id, s1, s2, dt)
	}
	return values, rets, nvml.SUCCESS
}

// gpmMetricValue resolves a single metric id. Activity metrics are 0..100
// percentages (per the NVML header); throughput metrics are MiB/s.
func gpmMetricValue(id uint32, s1, s2 *gpmSnapshot, dt float64) (float64, nvml.Return) {
	gpu := s2.utilGPU
	switch id {
	case gpmMetricGraphicsUtil, gpmMetricSMUtil:
		return gpu, nvml.SUCCESS
	case gpmMetricSMOccupancy:
		return gpu * gpmRatioSMOccupancy, nvml.SUCCESS
	case gpmMetricIntegerUtil:
		return gpu * gpmRatioInteger, nvml.SUCCESS
	case gpmMetricAnyTensorUtil:
		return gpu * gpmRatioAnyTensor, nvml.SUCCESS
	case gpmMetricDFMATensorUtil:
		return gpu * gpmRatioDFMA, nvml.SUCCESS
	case gpmMetricHMMATensorUtil:
		return gpu * gpmRatioHMMA, nvml.SUCCESS
	case gpmMetricDMMATensorUtil:
		return gpu * gpmRatioDMMA, nvml.SUCCESS
	case gpmMetricIMMATensorUtil:
		return gpu * gpmRatioIMMA, nvml.SUCCESS
	case gpmMetricDramBwUtil:
		return s2.utilMemory, nvml.SUCCESS
	case gpmMetricFP64Util:
		return gpu * gpmRatioFP64, nvml.SUCCESS
	case gpmMetricFP32Util:
		return gpu * gpmRatioFP32, nvml.SUCCESS
	case gpmMetricFP16Util:
		return gpu * gpmRatioFP16, nvml.SUCCESS
	case gpmMetricPcieTxPerSec:
		return s2.pcieTxMiBPerSec, nvml.SUCCESS
	case gpmMetricPcieRxPerSec:
		return s2.pcieRxMiBPerSec, nvml.SUCCESS
	case gpmMetricNvlinkTotalRxPerSec:
		return nvlinkRateMiBPerSec(s1, s2, dt, -1, false), nvml.SUCCESS
	case gpmMetricNvlinkTotalTxPerSec:
		return nvlinkRateMiBPerSec(s1, s2, dt, -1, true), nvml.SUCCESS
	}
	// Per-link NVLink rates: L{n} RX/TX pairs, ids 62..97.
	if id >= gpmMetricNvlinkL0RxPerSec && id <= gpmMetricNvlinkL17TxPerSec {
		link := int(id-gpmMetricNvlinkL0RxPerSec) / 2
		tx := (id-gpmMetricNvlinkL0RxPerSec)%2 == 1
		return nvlinkRateMiBPerSec(s1, s2, dt, link, tx), nvml.SUCCESS
	}
	return 0, nvml.ERROR_NOT_SUPPORTED
}

// nvlinkRateMiBPerSec derives MiB/s from the byte-counter delta between two
// samples, for one link or (link == -1) all links.
func nvlinkRateMiBPerSec(s1, s2 *gpmSnapshot, dt float64, link int, tx bool) float64 {
	if dt <= 0 {
		return 0
	}
	var delta uint64
	for l := 0; l < nvLinkMaxLinks; l++ {
		if link >= 0 && l != link {
			continue
		}
		c1, c2 := s1.nvlinkRx[l], s2.nvlinkRx[l]
		if tx {
			c1, c2 = s1.nvlinkTx[l], s2.nvlinkTx[l]
		}
		if c2 > c1 {
			delta += c2 - c1
		}
	}
	return float64(delta) / dt / (1024 * 1024)
}
