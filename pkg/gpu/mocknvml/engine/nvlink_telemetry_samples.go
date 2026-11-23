// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package engine

import (
	"time"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/NVIDIA/k8s-test-infra/internal/gpuarch"
)

const (
	// nvlinkTelemetrySampleInterval is the cadence the header documents for
	// nvmlDeviceGetNvLinkTelemetrySamples_v1: "approximately 100 ms".
	nvlinkTelemetrySampleInterval = 100 * time.Millisecond

	// nvlinkTelemetryMaxSamples is the per-slot history depth reported for a
	// sampleCount of 0. The header gives no figure, so this is a mock choice:
	// the documented ~2 s poll at 100 ms needs 20, and the headroom lets a
	// slightly late poller still read a gap-free history.
	nvlinkTelemetryMaxSamples = 32
)

// NvLinkTelemetryRequest is one slot of an
// nvmlDeviceGetNvLinkTelemetrySamples_v1 batch, as the caller filled it in.
// HasBuffer reports whether the slot's samples pointer is non-NULL, which the
// engine needs to validate the slot without touching C memory.
type NvLinkTelemetryRequest struct {
	Link       uint32
	SampleType nvml.NvlinkTelemetrySampleType
	Count      uint32
	HasBuffer  bool
}

// NvLinkTelemetryResult is the answer for one slot. Count is the value to
// write back into sampleCount: the supported depth for a count query,
// otherwise the number of Samples. Samples are oldest first.
type NvLinkTelemetryResult struct {
	Return  nvml.Return
	Count   uint32
	Samples []uint64
}

// supportsNvlinkTelemetry gates the telemetry batch on the generation the
// header names ("Rubin or newer") and on the board having NVLink to sample.
func (d *ConfigurableDevice) supportsNvlinkTelemetry() bool {
	return gpuarch.Arch(d.Config.Architecture).AtLeast(gpuarch.Rubin) && d.fabric.HasNvlink(d.index)
}

// GetMockNvLinkTelemetrySamples backs nvmlDeviceGetNvLinkTelemetrySamples_v1.
//
// Each sample is the link's raw flit counter at a point on a 100 ms grid
// ending at the current interval, read from the same epoch-anchored counters
// NVML_FI_DEV_NVLINK_THROUGHPUT_RAW_TX/RX report, so the newest sample and
// the field value agree and the history is monotonic across processes.
//
// A slot that breaks one of the header's argument rules fails the whole batch
// with INVALID_ARGUMENT; only the offending slots carry a non-SUCCESS Return,
// so the caller can tell which. A well-formed slot for a link that is not up
// reports NOT_SUPPORTED in its own Return without failing the batch.
func (d *ConfigurableDevice) GetMockNvLinkTelemetrySamples(reqs []NvLinkTelemetryRequest) ([]NvLinkTelemetryResult, nvml.Return) {
	if ret := d.handleLookupReturn(); ret != nvml.SUCCESS {
		return nil, ret
	}
	if !d.supportsNvlinkTelemetry() {
		debugLog("[NVML] nvmlDeviceGetNvLinkTelemetrySamples_v1 unsupported (arch=%s)\n",
			gpuarch.Arch(d.Config.Architecture))
		return nil, nvml.ERROR_NOT_SUPPORTED
	}

	results := make([]NvLinkTelemetryResult, len(reqs))
	invalid := false
	for i, r := range reqs {
		if !d.validNvLinkTelemetryRequest(r) {
			debugLog("[NVML] nvmlDeviceGetNvLinkTelemetrySamples_v1 slot %d invalid: link=%d type=%d count=%d buffer=%t\n",
				i, r.Link, r.SampleType, r.Count, r.HasBuffer)
			results[i].Return = nvml.ERROR_INVALID_ARGUMENT
			invalid = true
		}
	}
	if invalid {
		return results, nvml.ERROR_INVALID_ARGUMENT
	}

	newest := d.fabric.now().Truncate(nvlinkTelemetrySampleInterval)
	for i, r := range reqs {
		results[i] = d.nvLinkTelemetrySlot(r, newest)
	}
	debugLog("[NVML] nvmlDeviceGetNvLinkTelemetrySamples_v1 -> %d slots\n", len(reqs))
	return results, nvml.SUCCESS
}

func (d *ConfigurableDevice) validNvLinkTelemetryRequest(r NvLinkTelemetryRequest) bool {
	switch {
	case r.SampleType >= nvml.NVLINK_TELEMETRY_SAMPLE_TYPE_COUNT:
		return false
	case !d.nvlinkLinkInRange(int(r.Link)):
		return false
	case r.Count > 0 && !r.HasBuffer:
		return false
	default:
		return r.Count <= nvlinkTelemetryMaxSamples
	}
}

func (d *ConfigurableDevice) nvLinkTelemetrySlot(r NvLinkTelemetryRequest, newest time.Time) NvLinkTelemetryResult {
	if r.Count == 0 {
		return NvLinkTelemetryResult{Return: nvml.SUCCESS, Count: nvlinkTelemetryMaxSamples}
	}
	if l, ok := d.fabric.Link(d.index, int(r.Link)); !ok || !l.Active {
		return NvLinkTelemetryResult{Return: nvml.ERROR_NOT_SUPPORTED}
	}
	samples := make([]uint64, r.Count)
	for i := range samples {
		at := newest.Add(-time.Duration(len(samples)-1-i) * nvlinkTelemetrySampleInterval)
		rx, tx := d.fabric.NvLinkCounters(d.index, int(r.Link), at)
		samples[i] = tx
		if r.SampleType == nvml.NVLINK_TELEMETRY_SAMPLE_TYPE_THROUGHPUT_RAW_RX {
			samples[i] = rx
		}
	}
	return NvLinkTelemetryResult{Return: nvml.SUCCESS, Count: r.Count, Samples: samples}
}
