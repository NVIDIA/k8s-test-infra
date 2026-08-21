// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package nvidiasmi

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func hardwareCaptures(t *testing.T) []string {
	t.Helper()
	files, err := filepath.Glob("testdata/hardware/*.xml")
	require.NoError(t, err)
	require.NotEmpty(t, files)
	return files
}

// The other fixtures in this package come from the mock, so on their own they
// only prove the schema agrees with what we emit. These come from real nodes on
// a newer driver than the mock targets, which is where an element rename would
// show up first: a reading the schema can no longer find decodes as absent
// rather than as an error, so the assertions here are on the readings, not on
// ParseSnapshot succeeding.
//
// The coverage is the set of elements the checks in checks.go and temperature.go
// compare, since those are the readings a rename would silently turn into "the
// mock is wrong" for every profile at once. present() is the right test for
// them: an unsupported query is "N/A", which passes, while a renamed element
// leaves an empty body, which fails.
func TestHardwareCapturesDecode(t *testing.T) {
	for _, file := range hardwareCaptures(t) {
		t.Run(filepath.Base(file), func(t *testing.T) {
			raw, err := os.ReadFile(file)
			require.NoError(t, err)

			snap, err := ParseSnapshot(string(raw))
			require.NoError(t, err)

			attached, ok := snap.AttachedGPUs()
			require.True(t, ok, "attached_gpus")
			require.Equal(t, attached, snap.Count(), "every <gpu> block decoded")
			require.False(t, snap.HasFailedGPU(), "captures are of healthy nodes")

			for i := range snap.Count() {
				gpu, err := snap.GPU(i)
				require.NoError(t, err)
				element := snap.doc.GPUs[i]
				label := gpu.Label()

				_, ok := gpu.TemperatureC()
				require.True(t, ok, "%s: gpu_temp", label)
				_, ok = gpu.SMClockMHz()
				require.True(t, ok, "%s: sm_clock", label)
				_, ok = gpu.MemoryTotalMiB()
				require.True(t, ok, "%s: fb_memory_usage/total", label)
				_, ok = gpu.PowerLimitW()
				require.True(t, ok, "%s: current_power_limit", label)
				require.NotEmpty(t, gpu.UUID(), "%s: uuid", label)
				require.NotEmpty(t, gpu.PerformanceState(), "%s: performance_state", label)

				for _, r := range comparedReadings(element) {
					require.True(t, r.raw.present(),
						"%s: %s is absent; the driver may have renamed it", label, r.element)
				}
				requireOneThresholdPresentation(t, label, element.Temperature)
				requireOneSramRendering(t, label, element.ECCErrors)
				requireWholeRemapBlock(t, label, element.RemappedRows)
			}
		})
	}
}

// comparedReadings are the elements every board in the captures emits, whatever
// its architecture. The architecture-dependent families are asserted separately,
// because for those absence is the driver's way of saying "not this rendering"
// rather than a rename.
func comparedReadings(g gpuElement) []namedReading {
	return []namedReading{
		{"product_name", g.ProductName},
		{"product_architecture", g.ProductArchitecture},
		{"uuid", g.UUID},
		{"board_id", g.BoardID},
		{"c2c_mode", g.C2CMode},
		{"fan_speed", g.FanSpeed},
		{"performance_state", g.PerformanceState},
		{"accounting_mode_buffer_size", g.AccountingModeBufferSize},

		{"platformInfo/slot_number", g.PlatformInfo.SlotNumber},
		{"platformInfo/tray_index", g.PlatformInfo.TrayIndex},
		{"platformInfo/host_id", g.PlatformInfo.HostID},
		{"platformInfo/peer_type", g.PlatformInfo.PeerType},
		{"platformInfo/module_id", g.PlatformInfo.ModuleID},

		{"pcie_gen/max_link_gen", g.PCI.GPULinkInfo.PCIeGen.Max},
		{"pcie_gen/max_device_link_gen", g.PCI.GPULinkInfo.PCIeGen.DeviceMax},
		{"pcie_gen/max_host_link_gen", g.PCI.GPULinkInfo.PCIeGen.HostMax},

		{"fb_memory_usage/total", g.FBMemoryUsage.Total},
		{"fb_memory_usage/used", g.FBMemoryUsage.Used},

		{"utilization/gpu_util", g.Utilization.GPU},
		{"utilization/memory_util", g.Utilization.Memory},
		{"utilization/encoder_util", g.Utilization.Encoder},
		{"utilization/decoder_util", g.Utilization.Decoder},
		{"utilization/jpeg_util", g.Utilization.JPEG},
		{"utilization/ofa_util", g.Utilization.OFA},

		{"gpu_virtualization_mode/virtualization_mode", g.Virtualization.Mode},
		{"gpu_virtualization_mode/host_vgpu_mode", g.Virtualization.HostVGPUMode},
		{"gpu_virtualization_mode/vgpu_heterogeneous_mode", g.Virtualization.HeterogeneousMode},

		{"temperature/gpu_temp", g.Temperature.GPUTemp},
		{"temperature/gpu_temp_tlimit", g.Temperature.TLimit},

		{"gpu_power_readings/current_power_limit", g.PowerReadings.CurrentPowerLimit},
		{"gpu_power_readings/min_power_limit", g.PowerReadings.MinPowerLimit},
		{"gpu_power_readings/max_power_limit", g.PowerReadings.MaxPowerLimit},
		{"gpu_power_readings/average_power_draw", g.PowerReadings.AveragePowerDraw},
		{"gpu_power_readings/instant_power_draw", g.PowerReadings.InstantPowerDraw},

		{"clocks/sm_clock", g.Clocks.SMClock},
		{"ecc_mode/current_ecc", g.ECCMode.Current},

		{"encoder_stats/session_count", g.EncoderStats.SessionCount},
		{"encoder_stats/average_fps", g.EncoderStats.AverageFPS},
		{"encoder_stats/average_latency", g.EncoderStats.AverageLatency},
		{"fbc_stats/session_count", g.FBCStats.SessionCount},
		{"fbc_stats/average_fps", g.FBCStats.AverageFPS},
		{"fbc_stats/average_latency", g.FBCStats.AverageLatency},

		{"ecc_errors/volatile/sram_correctable", g.ECCErrors.Volatile.SRAMCorrectable},
		{"ecc_errors/volatile/dram_correctable", g.ECCErrors.Volatile.DRAMCorrectable},
		{"ecc_errors/volatile/dram_uncorrectable", g.ECCErrors.Volatile.DRAMUncorrectable},
		{"ecc_errors/aggregate/sram_correctable", g.ECCErrors.Aggregate.SRAMCorrectable},
		{"ecc_errors/aggregate/dram_correctable", g.ECCErrors.Aggregate.DRAMCorrectable},
		{"ecc_errors/aggregate/dram_uncorrectable", g.ECCErrors.Aggregate.DRAMUncorrectable},
	}
}

// requireOneThresholdPresentation is the hardware side of the gate in
// temperature.go: nvidia-smi emits the absolute threshold trio on pre-Ada and
// the *_tlimit_threshold trio on Ada and later, never both and never neither.
// Asserting the split holds on real boards is what makes the gate's premise
// evidence rather than an assumption, and a rename of either trio shows up here
// as a board reporting no presentation at all.
func requireOneThresholdPresentation(t *testing.T, label string, temp temperature) {
	t.Helper()
	absolute := presentCount(absoluteThresholds(temp))
	tlimit := presentCount(tlimitThresholds(temp))
	require.Contains(t, []int{0, 3}, absolute, "%s: partial absolute threshold trio", label)
	require.Contains(t, []int{0, 3}, tlimit, "%s: partial T.Limit threshold trio", label)
	require.Equal(t, 3, absolute+tlimit,
		"%s: want exactly one threshold presentation, got %d absolute and %d T.Limit",
		label, absolute, tlimit)
}

// requireOneSramRendering asserts the same either-or for the two SRAM
// uncorrectable renderings SramECCProblems switches on: the parity and SEC-DED
// pair on Ampere and later, the single combined element before it.
func requireOneSramRendering(t *testing.T, label string, ecc eccErrors) {
	t.Helper()
	for _, scope := range []struct {
		name     string
		counters eccCounters
	}{
		{"volatile", ecc.Volatile},
		{"aggregate", ecc.Aggregate},
	} {
		detailed := presentCount([]namedReading{
			{"sram_uncorrectable_parity", scope.counters.SRAMUncorrectableParity},
			{"sram_uncorrectable_secded", scope.counters.SRAMUncorrectableSECDED},
		})
		combined := presentCount([]namedReading{
			{"sram_uncorrectable", scope.counters.SRAMUncorrectable},
		})
		require.Contains(t, []int{0, 2}, detailed,
			"%s: %s has half of the parity/SEC-DED pair", label, scope.name)
		require.Equal(t, 2, detailed+2*combined,
			"%s: %s wants exactly one SRAM uncorrectable rendering", label, scope.name)
	}
}

// requireWholeRemapBlock asserts <remapped_rows> is either fully populated or
// fully absent. Pre-Ampere boards have no row remapping and omit the block, so
// its absence is not a defect; a block missing only some of its children is,
// and that is what a rename looks like.
func requireWholeRemapBlock(t *testing.T, label string, rows remappedRows) {
	t.Helper()
	present := presentCount([]namedReading{
		{"remapped_row_corr", rows.Correctable},
		{"remapped_row_unc", rows.Uncorrectable},
		{"remapped_row_pending", rows.Pending},
		{"remapped_row_failure", rows.Failure},
	})
	require.Contains(t, []int{0, 4}, present,
		"%s: <remapped_rows> is partially present, want all four counters or none", label)
	if present == 4 {
		require.NotEmpty(t, strings.TrimSpace(rows.Histogram.Body),
			"%s: row_remapper_histogram", label)
	}
}

func presentCount(readings []namedReading) int {
	count := 0
	for _, r := range readings {
		if r.raw.present() {
			count++
		}
	}
	return count
}

// scrubbedIdentifiers are the elements a capture must carry as a placeholder
// rather than as the value the node reported, with the shape hardware/README.md
// documents for each. Without this the scrub rests on whoever adds the next
// capture remembering to run it, and the cost of forgetting is a public commit
// naming real hardware.
//
// The patterns are deliberately narrow enough that a real reading cannot satisfy
// them: a genuine serial or PDI would have to be the placeholder to pass.
var scrubbedIdentifiers = []struct {
	element string
	pattern *regexp.Regexp
}{
	{"serial", regexp.MustCompile(`^1650000000\d{3}$`)},
	{"chassis_serial_number", regexp.MustCompile(`^1820000000\d{3}$`)},
	{"uuid", regexp.MustCompile(`^GPU-000000[0-9a-f]{2}-0000-0000-0000-0000000000[0-9a-f]{2}$`)},
	{"clusterUuid", regexp.MustCompile(`^0{7}[0-9a-f]-0000-0000-0000-0{11}[0-9a-f]$`)},
	// pdi and gpu_fabric_guid are per-die identities that sit beside <uuid>, and
	// on Grace-Blackwell they carry the same value as each other.
	{"pdi", regexp.MustCompile(`^0x0{14}[0-9a-f]{2}$`)},
	{"gpu_fabric_guid", regexp.MustCompile(`^0x0{14}[0-9a-f]{2}$`)},
}

func TestHardwareCapturesAreScrubbed(t *testing.T) {
	for _, file := range hardwareCaptures(t) {
		t.Run(filepath.Base(file), func(t *testing.T) {
			raw, err := os.ReadFile(file)
			require.NoError(t, err)

			for _, id := range scrubbedIdentifiers {
				bodies := regexp.MustCompile(
					`<`+id.element+`>([^<]*)</`+id.element+`>`).FindAllStringSubmatch(string(raw), -1)
				for _, body := range bodies {
					value := strings.TrimSpace(body[1])
					// An element the board does not answer carries no identity
					// to scrub.
					if value == "" || value == "N/A" {
						continue
					}
					require.Regexp(t, id.pattern, value,
						"<%s> is not a scrubbed placeholder: this capture still names real hardware",
						id.element)
				}
			}
		})
	}
}
