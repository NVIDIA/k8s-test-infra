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

package engine

import (
	"math"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
)

// NVML field IDs (nvmlFieldValue_t.fieldId) for the NVLink surface, mirrored
// from vendor/github.com/NVIDIA/go-nvml/pkg/nvml/nvml.h (NVML_FI_DEV_NVLINK_*).
// nvidia-smi resolves the NVLink subcommands (`nvlink -s/-c/-e`) by first
// reading LINK_COUNT and then iterating per-link fields/getters; without
// LINK_COUNT it concludes the GPU has zero NVLinks and prints nothing.
const (
	fiNvlinkSpeedMbpsL0      = 84
	fiNvlinkSpeedMbpsL5      = 89
	fiNvlinkSpeedMbpsCommon  = 90
	fiNvlinkLinkCount        = 91
	fiNvlinkSpeedMbpsL6      = 132
	fiNvlinkSpeedMbpsL11     = 137
	fiNvlinkThroughputDataTx = 138
	fiNvlinkThroughputDataRx = 139
	fiNvlinkThroughputRawTx  = 140
	fiNvlinkThroughputRawRx  = 141
	fiNvlinkErrorDlReplay    = 161
	fiNvlinkErrorDlRecovery  = 162
	fiNvlinkErrorDlCrc       = 163
	fiNvlinkGetSpeed         = 164
	fiNvlinkGetState         = 165
	fiNvlinkGetVersion       = 166

	// Remote NVLink id (NVML_FI_DEV_NVLINK_REMOTE_NVLINK_ID): the link number
	// on the far end. `nvidia-smi nvlink -R` reads this per link in addition to
	// the remote PCI info; without it the line falls back to "Not Supported".
	// On an NVSwitch fabric every GPU link lands on switch port/link 0.
	fiNvlinkRemoteNvlinkID = 146

	// NVLinks connected to an NVSwitch (NVML_FI_DEV_NVSWITCH_CONNECTED_LINK_COUNT).
	// The 580 nvidia-smi reads this per GPU in `topo -m` to detect the switch
	// fabric and render NV<count> between every switch-connected GPU pair (it no
	// longer calls nvmlDeviceGetP2PStatus the way the 560 binary did).
	fiNvswitchConnectedLinkCount = 147

	// Low-power (single-lane) state and threshold, surfaced by
	// `nvidia-smi nvlink -gLowPwrInfo` (NVML_FI_DEV_NVLINK_GET_POWER_*).
	fiNvlinkGetPowerState           = 167
	fiNvlinkGetPowerThreshold       = 168
	fiNvlinkPowerThresholdMax       = 200
	fiNvlinkPowerThresholdMin       = 223
	fiNvlinkPowerThresholdUnits     = 224
	fiNvlinkPowerThresholdSupported = 225

	// NVLink5 per-link counters surfaced by `nvidia-smi nvlink -e`
	// (NVML_FI_DEV_NVLINK_COUNT_*). scopeId carries the linkId. Field ids 205
	// (VL15_DROPPED) and 216-218 (RAW_BER_*) are deprecated and not modeled.
	fiNvlinkCountXmitPackets = 201
	fiNvlinkCountXmitBytes   = 202
	fiNvlinkCountRcvPackets  = 203
	fiNvlinkCountRcvBytes    = 204

	fiNvlinkCountMalformedPacketErrors    = 206
	fiNvlinkCountBufferOverrunErrors      = 207
	fiNvlinkCountRcvErrors                = 208
	fiNvlinkCountRcvRemoteErrors          = 209
	fiNvlinkCountRcvGeneralErrors         = 210
	fiNvlinkCountLocalLinkIntegrityErrors = 211
	fiNvlinkCountXmitDiscards             = 212

	fiNvlinkCountLinkRecoverySuccessfulEvents = 213
	fiNvlinkCountLinkRecoveryFailedEvents     = 214
	fiNvlinkCountLinkRecoveryEvents           = 215

	fiNvlinkCountEffectiveErrors = 219
	fiNvlinkCountEffectiveBer    = 220
	fiNvlinkCountSymbolErrors    = 221
	fiNvlinkCountSymbolBer       = 222

	fiNvlinkCountFecHistory0  = 235
	fiNvlinkCountFecHistory15 = 250

	// avgBytesPerNvlinkPacket scales the packet counter into a byte counter so
	// the two track realistically (real GB200 reports ~86 bytes/packet).
	avgBytesPerNvlinkPacket = 86

	// Low-power threshold model (matches a real GB200 idle box):
	//   `nvlink -gLowPwrInfo` -> units of 50 us, range 1..1023,
	//   per link: High Speed State + threshold 50000 us (= 1000 * 50 us).
	// NVML_NVLINK_LOW_POWER_THRESHOLD_UNIT_50US = 0x1.
	lowPowerThresholdUnit50us   = 1
	lowPowerThresholdMin        = 1
	lowPowerThresholdMax        = 1023
	lowPowerThresholdDefault    = 1000
	nvlinkPowerStateHighSpeed   = 0
	nvlinkPowerThresholdEnabled = 1
)

// speedFieldLink maps a per-link SPEED_MBPS_Lx field id to its link index, or
// returns false if the field id is not one of the per-link speed fields.
func speedFieldLink(fieldID uint32) (int, bool) {
	switch {
	case fieldID >= fiNvlinkSpeedMbpsL0 && fieldID <= fiNvlinkSpeedMbpsL5:
		return int(fieldID - fiNvlinkSpeedMbpsL0), true
	case fieldID >= fiNvlinkSpeedMbpsL6 && fieldID <= fiNvlinkSpeedMbpsL11:
		return int(fieldID-fiNvlinkSpeedMbpsL6) + 6, true
	default:
		return 0, false
	}
}

// GetNvLinkFieldValue resolves a single nvmlDeviceGetFieldValues entry for the
// NVLink field set off the immutable NodeFabric. It returns the union member
// the value occupies, the value (as raw bits in a uint64), and the per-field
// NVML return. Unmodeled field ids yield (FieldValueUnsupported, 0,
// ERROR_NOT_SUPPORTED) so the bridge can mark just that entry unsupported
// while still succeeding the overall call (matching real NVML semantics).
func (d *ConfigurableDevice) GetNvLinkFieldValue(fieldID, scopeID uint32) (FieldValueType, uint64, nvml.Return) {
	f := d.fabric
	if f == nil {
		return FieldValueUnsupported, 0, nvml.ERROR_NOT_SUPPORTED
	}
	link := int(scopeID)

	if l, ok := speedFieldLink(fieldID); ok {
		if mbps, active := f.NvLinkSpeedMbps(d.index, l); active {
			return FieldValueUint, mbps, nvml.SUCCESS
		}
		return FieldValueUint, 0, nvml.SUCCESS
	}

	// FEC corrected-symbol histogram (`nvlink -e`): bin 0 dominates, a small
	// trickle in bins 1-2, the rest zero — the shape a healthy NVLink5 reports.
	if fieldID >= fiNvlinkCountFecHistory0 && fieldID <= fiNvlinkCountFecHistory15 {
		v, ok := d.nvLinkActiveCounter(link)
		if !ok {
			return FieldValueUnsupported, 0, nvml.ERROR_NOT_SUPPORTED
		}
		switch fieldID - fiNvlinkCountFecHistory0 {
		case 0:
			return FieldValueUint64, v * 107, nvml.SUCCESS
		case 1:
			return FieldValueUint64, v / 600_000, nvml.SUCCESS
		case 2:
			return FieldValueUint64, v / 2_500_000, nvml.SUCCESS
		default:
			return FieldValueUint64, 0, nvml.SUCCESS
		}
	}

	switch fieldID {
	case fiNvlinkLinkCount:
		return FieldValueUint, uint64(f.ActiveLinkCount(d.index)), nvml.SUCCESS

	case fiNvlinkGetState:
		l, ok := f.Link(d.index, link)
		if !ok {
			return FieldValueUnsupported, 0, nvml.ERROR_NOT_SUPPORTED
		}
		if l.Active {
			return FieldValueUint, 1, nvml.SUCCESS
		}
		return FieldValueUint, 0, nvml.SUCCESS

	case fiNvlinkGetVersion:
		if _, ok := f.Link(d.index, link); !ok {
			return FieldValueUnsupported, 0, nvml.ERROR_NOT_SUPPORTED
		}
		return FieldValueUint, uint64(f.LinkVersion(d.index, link)), nvml.SUCCESS

	case fiNvlinkGetSpeed:
		if mbps, active := f.NvLinkSpeedMbps(d.index, link); active {
			return FieldValueUint, mbps, nvml.SUCCESS
		}
		return FieldValueUnsupported, 0, nvml.ERROR_NOT_SUPPORTED

	// `topo -m` (580): number of this GPU's NVLinks landing on an NVSwitch.
	// >0 makes nvidia-smi draw NV<count> across the switch fabric; report
	// NOT_SUPPORTED when there are none so non-switch profiles show no NV#
	// (negative control) exactly as before.
	case fiNvswitchConnectedLinkCount:
		if n := f.NVSwitchConnectedLinkCount(d.index); n > 0 {
			return FieldValueUint, uint64(n), nvml.SUCCESS
		}
		return FieldValueUnsupported, 0, nvml.ERROR_NOT_SUPPORTED

	// `nvlink -R`: remote NVLink id. Every switch-attached GPU link reaches
	// the far end at link 0 (mirrors the FFFFFFFF:FF:FF.0 remote PCI sentinel).
	case fiNvlinkRemoteNvlinkID:
		if l, ok := f.Link(d.index, link); ok && l.Active {
			return FieldValueUint, 0, nvml.SUCCESS
		}
		return FieldValueUnsupported, 0, nvml.ERROR_NOT_SUPPORTED

	// `nvlink -gLowPwrInfo`: per-link single-lane power state + threshold.
	case fiNvlinkGetPowerState:
		if l, ok := f.Link(d.index, link); ok && l.Active {
			return FieldValueUint, nvlinkPowerStateHighSpeed, nvml.SUCCESS
		}
		return FieldValueUnsupported, 0, nvml.ERROR_NOT_SUPPORTED
	case fiNvlinkGetPowerThreshold:
		if l, ok := f.Link(d.index, link); ok && l.Active {
			return FieldValueUint, lowPowerThresholdDefault, nvml.SUCCESS
		}
		return FieldValueUnsupported, 0, nvml.ERROR_NOT_SUPPORTED

	// `nvlink -gLowPwrInfo` device-level header (units + valid range +
	// supported flag). Reported only when the GPU actually has NVLinks.
	case fiNvlinkPowerThresholdUnits:
		if f.ActiveLinkCount(d.index) == 0 {
			return FieldValueUnsupported, 0, nvml.ERROR_NOT_SUPPORTED
		}
		return FieldValueUint, lowPowerThresholdUnit50us, nvml.SUCCESS
	case fiNvlinkPowerThresholdMin:
		if f.ActiveLinkCount(d.index) == 0 {
			return FieldValueUnsupported, 0, nvml.ERROR_NOT_SUPPORTED
		}
		return FieldValueUint, lowPowerThresholdMin, nvml.SUCCESS
	case fiNvlinkPowerThresholdMax:
		if f.ActiveLinkCount(d.index) == 0 {
			return FieldValueUnsupported, 0, nvml.ERROR_NOT_SUPPORTED
		}
		return FieldValueUint, lowPowerThresholdMax, nvml.SUCCESS
	case fiNvlinkPowerThresholdSupported:
		if f.ActiveLinkCount(d.index) == 0 {
			return FieldValueUnsupported, 0, nvml.ERROR_NOT_SUPPORTED
		}
		return FieldValueUint, nvlinkPowerThresholdEnabled, nvml.SUCCESS

	case fiNvlinkSpeedMbpsCommon:
		l, ok := f.FirstActiveLink(d.index)
		if !ok {
			return FieldValueUint, 0, nvml.SUCCESS
		}
		mbps, _ := f.NvLinkSpeedMbps(d.index, l)
		return FieldValueUint, mbps, nvml.SUCCESS

	case fiNvlinkThroughputDataTx, fiNvlinkThroughputRawTx:
		_, tx := f.NvLinkCounters(d.index, link, f.now())
		return FieldValueUint64, tx, nvml.SUCCESS
	case fiNvlinkThroughputDataRx, fiNvlinkThroughputRawRx:
		rx, _ := f.NvLinkCounters(d.index, link, f.now())
		return FieldValueUint64, rx, nvml.SUCCESS

	case fiNvlinkErrorDlReplay, fiNvlinkErrorDlRecovery, fiNvlinkErrorDlCrc:
		return FieldValueUint64, f.NvLinkErrorCount(d.index, link, f.now()), nvml.SUCCESS

	case fiNvlinkCountXmitPackets, fiNvlinkCountRcvPackets:
		v, ok := d.nvLinkActiveCounter(link)
		if !ok {
			return FieldValueUnsupported, 0, nvml.ERROR_NOT_SUPPORTED
		}
		return FieldValueUint64, v, nvml.SUCCESS

	case fiNvlinkCountXmitBytes, fiNvlinkCountRcvBytes:
		v, ok := d.nvLinkActiveCounter(link)
		if !ok {
			return FieldValueUnsupported, 0, nvml.ERROR_NOT_SUPPORTED
		}
		return FieldValueUint64, v * avgBytesPerNvlinkPacket, nvml.SUCCESS

	// Healthy-link error/recovery counters: present but zero (matches a clean
	// real GB200). The link must exist, else nvidia-smi should stop enumerating.
	case fiNvlinkCountMalformedPacketErrors, fiNvlinkCountBufferOverrunErrors,
		fiNvlinkCountRcvErrors, fiNvlinkCountRcvRemoteErrors,
		fiNvlinkCountRcvGeneralErrors, fiNvlinkCountLocalLinkIntegrityErrors,
		fiNvlinkCountXmitDiscards, fiNvlinkCountLinkRecoverySuccessfulEvents,
		fiNvlinkCountLinkRecoveryFailedEvents, fiNvlinkCountLinkRecoveryEvents,
		fiNvlinkCountEffectiveErrors, fiNvlinkCountSymbolErrors:
		if _, ok := f.Link(d.index, link); !ok {
			return FieldValueUnsupported, 0, nvml.ERROR_NOT_SUPPORTED
		}
		return FieldValueUint64, 0, nvml.SUCCESS

	case fiNvlinkCountEffectiveBer, fiNvlinkCountSymbolBer:
		if _, ok := f.Link(d.index, link); !ok {
			return FieldValueUnsupported, 0, nvml.ERROR_NOT_SUPPORTED
		}
		return FieldValueDouble, math.Float64bits(0), nvml.SUCCESS

	default:
		return FieldValueUnsupported, 0, nvml.ERROR_NOT_SUPPORTED
	}
}

// nvLinkActiveCounter returns the deterministic per-link base counter (used
// for packet/byte/FEC accrual) and whether the link is active. Inactive or
// nonexistent links yield false so the bridge can mark the field unsupported.
func (d *ConfigurableDevice) nvLinkActiveCounter(link int) (uint64, bool) {
	l, ok := d.fabric.Link(d.index, link)
	if !ok || !l.Active {
		return 0, false
	}
	rx, _ := d.fabric.NvLinkCounters(d.index, link, d.fabric.now())
	return rx, true
}
