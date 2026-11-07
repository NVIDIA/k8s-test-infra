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
	"strings"
)

// loadOrIdentity lists the getters whose value is not a property of the board:
// readings that move with load or time, and the identifiers a capture is
// scrubbed of. Their return codes are still compared: a real and a mock GPU must
// agree on whether such a getter is supported, not on what it reads. Names are
// matched whole, so GetClockInfo is here and GetMaxClockInfo, a board value a
// profile should reproduce, is not.
var loadOrIdentity = []string{
	"AccountingPids", "ClockInfo", "ComputeRunningProcesses", "CurrentClocksEventReasons",
	"CurrentClocksThrottleReasons", "DecoderUtilization", "DetailedEccErrors", "EncoderUtilization",
	"FanSpeed", "FanSpeedRPM", "FieldValues", "GpuFabricInfo", "GpuFabricInfoV",
	"GraphicsRunningProcesses", "JpgUtilization", "MPSComputeRunningProcesses", "MarginTemperature",
	"MemoryInfo", "NvLinkErrorCounter", "NvLinkUtilizationCounter", "OfaUtilization",
	"PcieReplayCounter", "PcieThroughput", "Pdi", "PerformanceState", "PlatformInfo", "PowerState",
	"PowerUsage", "ProcessUtilization", "ProcessesUtilizationInfo", "RemappedRows", "RetiredPages",
	"RetiredPagesPendingStatus", "Samples", "Serial", "TargetFanSpeed", "Temperature", "TemperatureV",
	"TotalEccErrors", "TotalEnergyConsumption", "UUID", "UtilizationRates",
	"VgpuInstancesUtilizationInfo", "VgpuProcessUtilization", "VgpuProcessesUtilizationInfo",
	"VgpuUtilization", "ViolationStatus",
}

// defaultIgnore matches a call name (Get<name>, optionally with a version suffix
// or arguments, as in GetClockInfo(1) or GetTemperatureV_v2) against that list.
var defaultIgnore = `^(?:Get|Is)(?:` + strings.Join(loadOrIdentity, "|") + `)(?:_v[0-9]+)?(?:\(.*\))?$`

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
	// Keep numbers as written: as float64, 64-bit values such as CPU affinity
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
		out = append(out, finding{
			where: where, name: name, hw: r, mock: m,
			verdict: classify(name, r, inReal, m, inMock, ignore),
		})
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
	case !sameValue(r.Value, m.Value):
		return valueDiffers
	}
	return match
}

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
	_, _ = fmt.Fprintf(w, "value matches:         %s (of %d calls answered SUCCESS on both sides, excluding %d whose value is load or identity)\n",
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
