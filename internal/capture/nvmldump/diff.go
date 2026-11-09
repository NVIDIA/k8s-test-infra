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

package nvmldump

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
)

// loadOrIdentity lists the getters whose value is not a property of the board:
// readings that move with load or time, the identifiers a capture is scrubbed
// of, and what the host decides (which CPUs a Kind node has). Their return codes
// are still compared: a real and a mock GPU must agree on whether such a getter
// is supported, not on what it reads. Names are matched whole, so GetClockInfo
// is here and GetMaxClockInfo, a board value a profile should reproduce, is not.
var loadOrIdentity = []string{
	"AccountingPids", "BBXTimeData", "ClockInfo", "ComputeRunningProcesses", "CpuAffinity",
	"CpuAffinityWithinScope", "CurrentClockFreqs", "CurrentClocksEventReasons",
	"CurrentClocksThrottleReasons", "DecoderUtilization", "DetailedEccErrors", "DynamicPstatesInfo",
	"EncoderUtilization", "FanSpeed", "FanSpeedRPM", "GpuFabricInfo", "GpuFabricInfoV",
	"GraphicsRunningProcesses", "JpgUtilization", "LastBBXFlushTime", "MPSComputeRunningProcesses",
	"MarginTemperature", "NvLinkErrorCounter", "NvLinkUtilizationCounter", "OfaUtilization",
	"PcieReplayCounter", "PcieThroughput", "Pdi", "PerformanceState", "PlatformInfo", "PowerState",
	"PowerUsage", "ProcessUtilization", "ProcessesUtilizationInfo", "RemappedRows", "RetiredPages",
	"RetiredPagesPendingStatus", "Samples", "Serial", "TargetFanSpeed", "Temperature", "TemperatureV",
	"TotalEccErrors", "TotalEnergyConsumption", "UUID", "UtilizationRates",
	"VgpuInstancesUtilizationInfo", "VgpuProcessUtilization", "VgpuProcessesUtilizationInfo",
	"VgpuUtilization", "ViolationStatus",
}

// readingParts matches the parts of an answer that are readings, where the rest
// of the answer describes the board. A struct field is named Call.Field and an
// array element Call[i], so GetMemoryInfo_v2.Free is the free memory and
// GetThermalSettings(0).Sensor[0].CurrentTemp the first sensor's reading.
var readingParts = []string{
	// The clocks in use: GetClock(type, NVML_CLOCK_ID_CURRENT).
	`GetClock\([0-9]+,0\)`,
	// Memory in use and free; the total and the driver's reservation are compared.
	`Get(?:MemoryInfo(?:_v[0-9]+)?|ConfComputeProtectedMemoryUsage)\.(?:Free|Used)`,
	`GetBAR1MemoryInfo\.Bar1(?:Free|Used)`,
	`GetThermalSettings\([0-9]+\)\.Sensor\[[0-9]+\]\.CurrentTemp`,
	// The struct version a versioned getter is called with, which the answer
	// carries back. It belongs to the API, not to the GPU.
	`.+\.Version`,
}

// liveFieldIDs are the field values that are readings, as inclusive ranges of
// field IDs: the same counters, energy, power draw and temperatures the getters
// in loadOrIdentity read. Every other field (ECC mode, link counts, speeds and
// versions, power limits, T.Limit offsets, capabilities) describes the board and
// is compared. The IDs are go-nvml's, which drivers from 580.95 on use; older
// drivers number the fields from 251 to 273 differently.
var liveFieldIDs = [][2]int{
	{nvml.FI_DEV_ECC_SBE_VOL_TOTAL, nvml.FI_DEV_TOTAL_ENERGY_CONSUMPTION},              // ECC and retired-page counts, NVLink errors and bandwidth, time throttled, memory temperature, energy
	{nvml.FI_DEV_RETIRED_PENDING_SBE, nvml.FI_DEV_PCIE_REPLAY_ROLLOVER_COUNTER},        // pending retirements, PCIe replays
	{nvml.FI_DEV_NVLINK_CRC_FLIT_ERROR_COUNT_L6, nvml.FI_DEV_NVLINK_BANDWIDTH_C1_L11},  // NVLink errors and bandwidth, links 6-11
	{nvml.FI_DEV_NVLINK_THROUGHPUT_DATA_TX, nvml.FI_DEV_REMAPPED_FAILURE},              // NVLink throughput, remapped rows
	{nvml.FI_DEV_NVLINK_ECC_DATA_ERROR_COUNT_L0, nvml.FI_DEV_NVLINK_ERROR_DL_CRC},      // NVLink errors
	{nvml.FI_DEV_PCIE_L0_TO_RECOVERY_COUNTER, nvml.FI_DEV_PCIE_L0_TO_RECOVERY_COUNTER}, // PCIe recoveries
	{nvml.FI_DEV_PCIE_COUNT_CORRECTABLE_ERRORS, nvml.FI_DEV_PCIE_COUNT_LANE_ERROR},     // PCIe errors
	{nvml.FI_DEV_POWER_AVERAGE, nvml.FI_DEV_POWER_INSTANT},                             // power draw
	{nvml.FI_DEV_ENERGY, nvml.FI_DEV_ENERGY},                                           // energy
	{nvml.FI_DEV_PCIE_COUNT_TX_BYTES, nvml.FI_DEV_PCIE_COUNT_RX_BYTES},                 // PCIe traffic
	{nvml.FI_DEV_NVLINK_COUNT_XMIT_PACKETS, nvml.FI_DEV_NVLINK_COUNT_SYMBOL_BER},       // NVLink traffic, errors and bit error rates
	{nvml.FI_DEV_C2C_LINK_ERROR_INTR, nvml.FI_DEV_C2C_LINK_ERROR_REPLAY_B2B},           // C2C errors
	{nvml.FI_DEV_NVLINK_COUNT_FEC_HISTORY_0, nvml.FI_DEV_NVLINK_COUNT_FEC_HISTORY_15},  // NVLink corrected blocks
	{nvml.FI_PWR_SMOOTHING_HW_CIRCUITRY_PERCENT_LIFETIME_REMAINING,
		nvml.FI_PWR_SMOOTHING_HW_CIRCUITRY_PERCENT_LIFETIME_REMAINING}, // power smoothing wear
	{nvml.FI_DEV_CLOCKS_EVENT_REASON_SW_THERM_SLOWDOWN, nvml.FI_DEV_POWER_SYNC_BALANCING_AF}, // time throttled, power sync balancing
	{nvml.FI_DEV_NVLINK_COUNT_RAW_ERRORS_LANE0, nvml.FI_DEV_NVLINK_PLR_XMIT_RETRY_BLOCKS},    // NVLink lane errors and retries
	{nvml.FI_DEV_MMA_STALL_PERCENT, nvml.FI_DEV_MMA_STALL_PERCENT},                           // utilisation
	{nvml.FI_DEV_REMAPPED_ROWS_COR_INACTIVE, nvml.FI_DEV_INACTIVE_BANK_REMAPPINGS},           // remapped rows and banks
}

// defaultIgnore matches a call name (Get<name>, optionally with a version suffix
// or arguments, as in GetClockInfo(1) or GetTemperatureV_v2) against
// loadOrIdentity, a live field value as GetFieldValues(id=N), and the reading
// parts of an answer.
var defaultIgnore = `^(?:` + strings.Join([]string{
	`(?:Get|Is)(?:` + strings.Join(loadOrIdentity, "|") + `)(?:_v[0-9]+)?(?:\(.*\))?`,
	`GetFieldValues\(id=(?:` + strings.Join(fieldIDs(liveFieldIDs), "|") + `)\)`,
	strings.Join(readingParts, "|"),
}, "|") + `)$`

func fieldIDs(ranges [][2]int) []string {
	var out []string
	for _, r := range ranges {
		for id := r[0]; id <= r[1]; id++ {
			out = append(out, strconv.Itoa(id))
		}
	}
	return out
}

// verdict is the outcome of comparing one call across the two dumps.
type verdict int

const (
	match verdict = iota
	valueDiffers
	retDiffers
	onlyReal
	onlyMock
	ignoredValue
	// sameError: both sides answered the same error, so there is no value to
	// compare. It counts as a return-code match and nothing more.
	sameError
)

type finding struct {
	where   string
	name    string
	verdict verdict
	hw      call
	mock    call
}

func runDiff(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("diff", flag.ContinueOnError)
	fs.SetOutput(stderr)
	ignore := fs.String("ignore", defaultIgnore, "regexp of whole call names whose value is not compared (return codes still are)")
	limit := fs.Int("max", 40, "maximum mismatches to list per kind")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 2 {
		_, _ = fmt.Fprintln(stderr, "usage: mokka-capture diff nvml [-ignore regexp] [-max n] real.json mock.json")
		return 2
	}
	re, err := regexp.Compile(*ignore)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "-ignore: %v\n", err)
		return 2
	}
	hw, err := load(fs.Arg(0))
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	mock, err := load(fs.Arg(1))
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}

	findings := compare(hw, mock, re)
	report(stdout, findings, *limit)
	return 0
}

func load(path string) (dump, error) {
	var d dump
	raw, err := os.ReadFile(path)
	if err != nil {
		return d, err
	}
	// A committed capture is gzipped to keep the repository small.
	if strings.HasSuffix(path, ".gz") {
		zr, err := gzip.NewReader(bytes.NewReader(raw))
		if err != nil {
			return d, fmt.Errorf("%s: %w", path, err)
		}
		if raw, err = io.ReadAll(zr); err != nil {
			return d, fmt.Errorf("%s: %w", path, err)
		}
	}
	// Keep numbers as written: as float64, 64-bit values such as clock event
	// masks would round, and two different masks would compare equal.
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&d); err != nil {
		return d, fmt.Errorf("%s: %w", path, err)
	}
	return d, nil
}

// compare lines the two dumps up by call name. Devices pair by index, which
// holds when both nodes expose the same GPUs in the same order. A device only
// one side has is compared with an empty one, so all its calls count as missing
// from the other side instead of dropping out of the rates.
func compare(hw, mock dump, ignore *regexp.Regexp) []finding {
	var out []finding
	out = append(out, compareCalls("system", hw.System, mock.System, ignore)...)

	n := max(len(hw.Devices), len(mock.Devices))
	for i := 0; i < n; i++ {
		var h, m deviceDump
		index := i
		if i < len(hw.Devices) {
			h = hw.Devices[i]
			index = h.Index
		}
		if i < len(mock.Devices) {
			m = mock.Devices[i]
		}
		out = append(out, compareCalls(fmt.Sprintf("device %d", index), h.Calls, m.Calls, ignore)...)
	}
	return out
}

func compareCalls(where string, hw, mock map[string]call, ignore *regexp.Regexp) []finding {
	names := map[string]bool{}
	for name := range hw {
		names[name] = true
	}
	for name := range mock {
		names[name] = true
	}
	sorted := make([]string, 0, len(names))
	for name := range names {
		sorted = append(sorted, name)
	}
	sort.Strings(sorted)

	out := make([]finding, 0, len(sorted))
	for _, name := range sorted {
		r, inReal := hw[name]
		m, inMock := mock[name]
		v := classify(name, r, inReal, m, inMock, ignore)
		if v == valueDiffers {
			// List what was compared, not the readings left out of it.
			r.Value, _ = comparedParts(name, r.Value, ignore)
			m.Value, _ = comparedParts(name, m.Value, ignore)
		}
		out = append(out, finding{where: where, name: name, hw: r, mock: m, verdict: v})
	}
	return out
}

func classify(name string, r call, inReal bool, m call, inMock bool, ignore *regexp.Regexp) verdict {
	switch {
	case !inMock:
		return onlyReal
	case !inReal:
		return onlyMock
	case r.Ret != m.Ret:
		return retDiffers
	case r.Ret != "SUCCESS":
		// Both sides answered the same error: the return code matches, and
		// there is no value on either side to compare.
		return sameError
	case ignore.MatchString(name):
		return ignoredValue
	}
	rv, rok := comparedParts(name, r.Value, ignore)
	mv, mok := comparedParts(name, m.Value, ignore)
	switch {
	case !rok && !mok:
		// Every part of the answer is a reading.
		return ignoredValue
	case !sameValue(rv, mv):
		return valueDiffers
	}
	return match
}

// comparedParts returns the parts of an answer that ignore does not match, and
// whether any are left. The answer is first put in the shape load gives it, so
// a value built in Go and one read from a file prune alike.
func comparedParts(name string, v any, ignore *regexp.Regexp) (any, bool) {
	// A value that does not survive the round trip did not come from a dump
	// (which is JSON); it is then compared as it is.
	raw, err := json.Marshal(v)
	if err != nil {
		return v, true
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var generic any
	if err := dec.Decode(&generic); err != nil {
		return v, true
	}
	return prune(name, generic, ignore)
}

func prune(path string, v any, ignore *regexp.Regexp) (any, bool) {
	switch t := v.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, x := range t {
			p := path + "." + k
			if ignore.MatchString(p) {
				continue
			}
			if y, ok := prune(p, x, ignore); ok {
				out[k] = y
			}
		}
		return out, len(out) > 0 || len(t) == 0
	case []any:
		// An element left empty keeps its place, so the elements after it still
		// pair by index.
		out := make([]any, len(t))
		kept := len(t) == 0
		for i, x := range t {
			p := fmt.Sprintf("%s[%d]", path, i)
			if ignore.MatchString(p) {
				continue
			}
			y, ok := prune(p, x, ignore)
			out[i] = y
			kept = kept || ok
		}
		return out, kept
	}
	return v, true
}

// sameValue compares two answers by their JSON. Both come from a dump, which is
// JSON, so marshalling them cannot fail.
func sameValue(a, b any) bool {
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	return bytes.Equal(ja, jb)
}

func report(w io.Writer, findings []finding, limit int) {
	count := map[verdict]int{}
	for _, f := range findings {
		count[f.verdict]++
	}
	paired := count[match] + count[valueDiffers] + count[retDiffers] + count[ignoredValue] + count[sameError]

	_, _ = fmt.Fprintf(w, "calls compared:        %d\n", paired)
	_, _ = fmt.Fprintf(w, "return code matches:   %s\n", ratio(paired-count[retDiffers], paired))
	// A value is compared only where the real device answered SUCCESS, and only
	// for getters that describe the board.
	valued := count[match] + count[valueDiffers]
	_, _ = fmt.Fprintf(w, "value matches:         %s (of %d calls answered SUCCESS on both sides, excluding %d whose value is a reading or an identifier)\n",
		ratio(count[match], valued), valued+count[ignoredValue], count[ignoredValue])
	_, _ = fmt.Fprintf(w, "only on real device:   %d\n", count[onlyReal])
	_, _ = fmt.Fprintf(w, "only on mock device:   %d\n", count[onlyMock])

	list(w, findings, retDiffers, "return code differs", limit)
	list(w, findings, valueDiffers, "value differs", limit)
	list(w, findings, onlyReal, "only on the real device", limit)
	list(w, findings, onlyMock, "only on the mock device", limit)
}

func ratio(n, d int) string {
	if d == 0 {
		return "n/a"
	}
	return fmt.Sprintf("%d of %d (%.1f%%)", n, d, 100*float64(n)/float64(d))
}

func list(w io.Writer, findings []finding, v verdict, title string, limit int) {
	shown := 0
	total := 0
	for _, f := range findings {
		if f.verdict == v {
			total++
		}
	}
	if total == 0 {
		return
	}
	_, _ = fmt.Fprintf(w, "\n%s (%d):\n", title, total)
	for _, f := range findings {
		if f.verdict != v {
			continue
		}
		if shown == limit {
			_, _ = fmt.Fprintf(w, "  ... %d more\n", total-shown)
			return
		}
		shown++
		_, _ = fmt.Fprintf(w, "  %-9s %-40s %s\n", f.where, f.name, detail(f, v))
	}
}

// detail is what a listing says about one finding.
func detail(f finding, v verdict) string {
	switch v {
	case retDiffers:
		return fmt.Sprintf("real=%s mock=%s", f.hw.Ret, f.mock.Ret)
	case valueDiffers:
		return fmt.Sprintf("real=%s mock=%s", short(f.hw.Value), short(f.mock.Value))
	case onlyReal:
		return "real=" + f.hw.Ret
	case onlyMock:
		return "mock=" + f.mock.Ret
	case match, ignoredValue, sameError:
		return ""
	}
	return ""
}

func short(v any) string {
	b, _ := json.Marshal(v)
	if len(b) > 60 {
		return string(b[:57]) + "..."
	}
	return string(b)
}
