// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package engine

import (
	"encoding/binary"
	"time"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
)

const (
	// sampleInterval is how far apart the driver's samples sit. Placing every
	// sample on a fixed grid lets a poller that hands back its last timestamp
	// receive only the samples taken since.
	sampleInterval = time.Second
	// sampleBufferLen is how many samples the driver keeps per type, enough
	// for the 20-second window nvidia-smi summarizes.
	sampleBufferLen = 20
)

// GetSamples returns the samples of samplingType taken after lastSeen, oldest
// first, as the value type of the reading. The mock keeps no history, so every
// sample carries the current reading of the getter the type mirrors; under
// dynamic metrics that reading moves between calls, not within one.
func (d *ConfigurableDevice) GetSamples(samplingType nvml.SamplingType, lastSeen uint64) (nvml.ValueType, []nvml.Sample, nvml.Return) {
	if ret := d.tickFailure(); ret != nvml.SUCCESS {
		return 0, nil, ret
	}
	value, ret := d.sampleReading(samplingType)
	if ret != nvml.SUCCESS {
		debugLog("[NVML] nvmlDeviceGetSamples(type=%d) -> ret=%d\n", samplingType, ret)
		return 0, nil, ret
	}
	samples := sampleWindow(time.Now(), lastSeen, value)
	debugLog("[NVML] nvmlDeviceGetSamples(type=%d, lastSeen=%d) -> %d samples of %d\n",
		samplingType, lastSeen, len(samples), value)
	if len(samples) == 0 {
		return nvml.VALUE_TYPE_UNSIGNED_INT, nil, nvml.ERROR_NOT_FOUND
	}
	return nvml.VALUE_TYPE_UNSIGNED_INT, samples, nvml.SUCCESS
}

// sampleReading is the current value behind samplingType, in the unit of the
// getter it mirrors.
func (d *ConfigurableDevice) sampleReading(samplingType nvml.SamplingType) (uint32, nvml.Return) {
	switch samplingType {
	case nvml.TOTAL_POWER_SAMPLES:
		return d.powerDraw()
	case nvml.GPU_UTILIZATION_SAMPLES, nvml.MEMORY_UTILIZATION_SAMPLES,
		nvml.ENC_UTILIZATION_SAMPLES, nvml.DEC_UTILIZATION_SAMPLES:
		// These describe the whole GPU, which a MIG-partitioned GPU no longer
		// reports as one.
		if d.migPartitioned() {
			return 0, nvml.ERROR_NOT_SUPPORTED
		}
		return d.wholeGPUUtilization(samplingType)
	case nvml.PROCESSOR_CLK_SAMPLES:
		return d.currentClock(nvml.CLOCK_GRAPHICS)
	case nvml.MEMORY_CLK_SAMPLES:
		return d.currentClock(nvml.CLOCK_MEM)
	case nvml.JPG_UTILIZATION_SAMPLES:
		util, _, ret := d.GetJpgUtilization()
		return util, ret
	case nvml.OFA_UTILIZATION_SAMPLES:
		util, _, ret := d.GetOfaUtilization()
		return util, ret
	case nvml.MODULE_POWER_SAMPLES:
		// No profile models module (GPU plus CPU) power separately.
		return 0, nvml.ERROR_NOT_SUPPORTED
	default:
		return 0, nvml.ERROR_INVALID_ARGUMENT
	}
}

func (d *ConfigurableDevice) wholeGPUUtilization(samplingType nvml.SamplingType) (uint32, nvml.Return) {
	switch samplingType {
	case nvml.GPU_UTILIZATION_SAMPLES:
		return d.utilizationRates().Gpu, nvml.SUCCESS
	case nvml.MEMORY_UTILIZATION_SAMPLES:
		return d.utilizationRates().Memory, nvml.SUCCESS
	case nvml.ENC_UTILIZATION_SAMPLES:
		util, _, ret := d.GetEncoderUtilization()
		return util, ret
	default:
		util, _, ret := d.GetDecoderUtilization()
		return util, ret
	}
}

// sampleWindow lays value over the buffer's grid points up to now, keeping the
// ones newer than lastSeen. Timestamps are microseconds since the epoch.
func sampleWindow(now time.Time, lastSeen uint64, value uint32) []nvml.Sample {
	step := uint64(sampleInterval.Microseconds())
	oldest := uint64(now.UnixMicro())/step*step - (sampleBufferLen-1)*step
	samples := make([]nvml.Sample, 0, sampleBufferLen)
	for i := range uint64(sampleBufferLen) {
		if ts := oldest + i*step; ts > lastSeen {
			s := nvml.Sample{TimeStamp: ts}
			binary.NativeEndian.PutUint32(s.SampleValue[:4], value)
			samples = append(samples, s)
		}
	}
	return samples
}
