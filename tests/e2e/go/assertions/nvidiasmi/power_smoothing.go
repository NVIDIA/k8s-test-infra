// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package nvidiasmi

import (
	"fmt"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// The Power Smoothing block of `nvidia-smi -q` reads the power smoothing field
// ids, and the `nvidia-smi power-smoothing` subcommand writes them through the
// three NVML power smoothing setters.

// PowerSmoothingPresets is how many preset profiles NVML defines.
const PowerSmoothingPresets = 5

// adminOverrideNotSet is how nvidia-smi renders an admin override nobody set.
const adminOverrideNotSet = "Not Set"

// wattTolerance absorbs nvidia-smi rendering watts to two decimals.
const wattTolerance = 0.01

// PowerSmoothingPreset is one preset profile in the units nvidia-smi renders:
// percent of the power ceiling, watts per second and milliseconds.
type PowerSmoothingPreset struct {
	PercentTMPFloor      float64
	RampUpWPerS          float64
	RampDownWPerS        float64
	RampDownHysteresisMS int
}

// PowerSmoothingState is what the Power Smoothing block of `nvidia-smi -q -x`
// reports for one GPU.
type PowerSmoothingState struct {
	Enabled       bool
	ActiveProfile int
	// Current is the active preset's parameters.
	Current            PowerSmoothingPreset
	TMPCeilW           float64
	TMPFloorW          float64
	MinPercentTMPFloor float64
	MaxPercentTMPFloor float64
	NumPresetProfiles  int
	AdminOverridesSet  bool
}

// PowerSmoothing decodes the block. It reports false when the block carries no
// readings: a board without the feature, a failed GPU, or children this schema
// no longer matches.
func (g GPU) PowerSmoothing() (PowerSmoothingState, bool) {
	ps := g.element.PowerSmoothing
	var enabled bool
	switch strings.TrimSpace(string(ps.Enabled)) {
	case "Yes":
		enabled = true
	case "No":
	default:
		return PowerSmoothingState{}, false
	}

	d := numericDecoder{ok: true}
	state := PowerSmoothingState{
		Enabled:       enabled,
		ActiveProfile: d.int(ps.ActiveProfile),
		Current: PowerSmoothingPreset{
			PercentTMPFloor:      d.float(ps.CurrentProfile.PercentTMPFloor),
			RampUpWPerS:          d.float(ps.CurrentProfile.RampUpRate),
			RampDownWPerS:        d.float(ps.CurrentProfile.RampDownRate),
			RampDownHysteresisMS: d.int(ps.CurrentProfile.RampDownHysteresis),
		},
		TMPCeilW:           d.float(ps.CurrentTMP),
		TMPFloorW:          d.float(ps.CurrentTMPFloor),
		MinPercentTMPFloor: d.float(ps.MinPercentTMPFloor),
		MaxPercentTMPFloor: d.float(ps.MaxPercentTMPFloor),
		NumPresetProfiles:  d.int(ps.NumPresetProfiles),
		AdminOverridesSet: ps.AdminOverrides.PercentTMPFloor.anySet(ps.AdminOverrides.RampUpRate,
			ps.AdminOverrides.RampDownRate, ps.AdminOverrides.RampDownHysteresis),
	}
	if !d.ok {
		return PowerSmoothingState{}, false
	}
	return state, true
}

// numericDecoder decodes a run of readings and remembers whether any of them
// was not a number, so a block decodes all-or-nothing without a check per row.
type numericDecoder struct{ ok bool }

func (d *numericDecoder) float(r reading) float64 {
	v, ok := r.unitFloatValue()
	d.ok = d.ok && ok
	return v
}

func (d *numericDecoder) int(r reading) int {
	v, ok := r.intValue()
	d.ok = d.ok && ok
	return v
}

// anySet reports whether any of the admin override readings carries a value.
func (r reading) anySet(others ...reading) bool {
	for _, o := range append([]reading{r}, others...) {
		if strings.TrimSpace(string(o)) != adminOverrideNotSet {
			return true
		}
	}
	return false
}

// unitFloatValue is floatValue for a body whose unit is glued to the number,
// such as "65.50%". It accepts the spaced form ("1000.00 W") too.
func (r reading) unitFloatValue() (float64, bool) {
	fields := strings.Fields(string(r))
	if len(fields) == 0 {
		return 0, false
	}
	value, err := strconv.ParseFloat(strings.TrimSuffix(fields[0], "%"), 64)
	if err != nil {
		return 0, false
	}
	return value, true
}

// PowerSmoothingExpectation is what the block should report.
type PowerSmoothingExpectation struct {
	// Supported is whether the board models the feature. Without it the whole
	// block renders as N/A.
	Supported     bool
	Enabled       bool
	ActiveProfile int
	// Current, when set, is the active preset's expected parameters.
	Current *PowerSmoothingPreset
}

// PowerSmoothingProblems checks the block of every GPU in a `nvidia-smi -q -x`
// document. A failed GPU is skipped, for the reason C2CModeProblems documents;
// skipping every GPU is itself reported.
func PowerSmoothingProblems(out string, want PowerSmoothingExpectation) []string {
	snap, err := ParseSnapshot(out)
	if err != nil {
		return []string{err.Error()}
	}
	var problems []string
	compared := 0
	for i := range snap.doc.GPUs {
		gpu := snap.gpu(i)
		if gpu.Failed() {
			continue
		}
		compared++
		problems = append(problems, PowerSmoothingGPUProblems(gpu, want)...)
	}
	if compared == 0 {
		problems = append(problems, "every GPU failed, so no power_smoothing block was compared")
	}
	return problems
}

// PowerSmoothingGPUProblems checks one GPU's block.
//
// Beyond the configured state it pins the invariants a consumer relies on: the
// ceiling is the enforced power limit, and the floor is that ceiling scaled by
// the active preset's percentage while smoothing is enabled and 0 W while it is
// not. A mock that stored a write but never applied it would pass a check of
// the active profile alone.
func PowerSmoothingGPUProblems(gpu GPU, want PowerSmoothingExpectation) []string {
	name := gpu.Label() + " power_smoothing"
	body := strings.Join(strings.Fields(gpu.element.PowerSmoothing.Body), " ")
	if !want.Supported {
		if body != unsupportedReading {
			return []string{fmt.Sprintf("%s = %q, want %q on a board without power smoothing",
				name, body, unsupportedReading)}
		}
		return nil
	}
	got, ok := gpu.PowerSmoothing()
	if !ok {
		return []string{fmt.Sprintf("%s = %q, want a populated block", name, body)}
	}

	var problems []string
	for _, p := range slices.Concat(
		smoothingStateProblems(got, want), smoothingFixedProblems(got), smoothingEnvelopeProblems(gpu, got)) {
		problems = append(problems, name+": "+p)
	}
	return problems
}

// smoothingStateProblems compares what a setter or the profile decides.
func smoothingStateProblems(got PowerSmoothingState, want PowerSmoothingExpectation) []string {
	var problems []string
	if got.Enabled != want.Enabled {
		problems = append(problems, fmt.Sprintf("enabled = %v, want %v", got.Enabled, want.Enabled))
	}
	if got.ActiveProfile != want.ActiveProfile {
		problems = append(problems, fmt.Sprintf("active profile = %d, want %d", got.ActiveProfile, want.ActiveProfile))
	}
	if want.Current != nil && got.Current != *want.Current {
		problems = append(problems, fmt.Sprintf("current profile = %+v, want %+v", got.Current, *want.Current))
	}
	return problems
}

// smoothingFixedProblems compares the readings no profile or setter moves.
func smoothingFixedProblems(got PowerSmoothingState) []string {
	var problems []string
	if got.NumPresetProfiles != PowerSmoothingPresets {
		problems = append(problems, fmt.Sprintf("%d preset profiles, want %d",
			got.NumPresetProfiles, PowerSmoothingPresets))
	}
	if got.MinPercentTMPFloor != 0 || got.MaxPercentTMPFloor != 100 {
		problems = append(problems, fmt.Sprintf("TMP floor range = %v-%v %%, want 0-100 %%",
			got.MinPercentTMPFloor, got.MaxPercentTMPFloor))
	}
	if got.AdminOverridesSet {
		problems = append(problems, fmt.Sprintf("an admin override is set, want every one %q: nothing writes them",
			adminOverrideNotSet))
	}
	return problems
}

// smoothingEnvelopeProblems checks the ceiling against the power limit and the
// floor against the ceiling and the active preset.
func smoothingEnvelopeProblems(gpu GPU, got PowerSmoothingState) []string {
	var problems []string
	if limit, ok := gpu.PowerLimitW(); !ok {
		problems = append(problems, "current_power_limit is not numeric, so the TMP ceiling cannot be checked")
	} else if math.Abs(got.TMPCeilW-limit) > wattTolerance {
		problems = append(problems, fmt.Sprintf("TMP ceiling = %.2f W, want the %.2f W power limit",
			got.TMPCeilW, limit))
	}
	wantFloor := 0.0
	if got.Enabled {
		wantFloor = got.TMPCeilW * got.Current.PercentTMPFloor / 100
	}
	if math.Abs(got.TMPFloorW-wantFloor) > wattTolerance {
		problems = append(problems, fmt.Sprintf("TMP floor = %.2f W, want %.2f W (%.2f W x %.2f %%, enabled %v)",
			got.TMPFloorW, wantFloor, got.TMPCeilW, got.Current.PercentTMPFloor, got.Enabled))
	}
	return problems
}

var (
	presetHeader = regexp.MustCompile(`^\s*Preset Profile (\d+) Definition\s*$`)
	presetRow    = regexp.MustCompile(`^\s*(% Tmp Floor|Ramp Up Rate|Ramp Down Rate|Ramp Down Hysteresis)\s*:\s*(.+?)\s*$`)
)

// ParsePresetDefinitions decodes `nvidia-smi power-smoothing -ppd` output.
//
// nvidia-smi prints each GPU's presets back to back with no header between
// them, so the caller must scope the command to one GPU with -i; a repeated
// preset id is reported as an error rather than silently overwriting the first.
func ParsePresetDefinitions(out string) (map[int]PowerSmoothingPreset, error) {
	presets := map[int]PowerSmoothingPreset{}
	current := -1
	for line := range strings.SplitSeq(out, "\n") {
		line = strings.TrimRight(line, "\r")
		if m := presetHeader.FindStringSubmatch(line); m != nil {
			id, _ := strconv.Atoi(m[1]) // the pattern only admits digits
			if _, seen := presets[id]; seen {
				return nil, fmt.Errorf("preset %d defined twice: run -ppd against a single GPU", id)
			}
			presets[id] = PowerSmoothingPreset{}
			current = id
			continue
		}
		m := presetRow.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		if current < 0 {
			return nil, fmt.Errorf("%q precedes every preset header", strings.TrimSpace(line))
		}
		p := presets[current]
		if !p.set(m[1], reading(m[2])) {
			return nil, fmt.Errorf("preset %d %s = %q is not numeric", current, m[1], m[2])
		}
		presets[current] = p
	}
	if len(presets) == 0 {
		return nil, fmt.Errorf("no preset definitions in %q", strings.TrimSpace(out))
	}
	return presets, nil
}

// set records one -ppd row under its label, reporting whether it was numeric.
func (p *PowerSmoothingPreset) set(label string, value reading) bool {
	d := numericDecoder{ok: true}
	switch label {
	case "% Tmp Floor":
		p.PercentTMPFloor = d.float(value)
	case "Ramp Up Rate":
		p.RampUpWPerS = d.float(value)
	case "Ramp Down Rate":
		p.RampDownWPerS = d.float(value)
	default:
		p.RampDownHysteresisMS = d.int(value)
	}
	return d.ok
}
