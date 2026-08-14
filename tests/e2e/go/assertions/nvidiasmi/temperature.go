// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package nvidiasmi

import "fmt"

// The thresholds are read from the XML rather than the human
// `-q -d TEMPERATURE` table because the XML encodes the same defect signal
// structurally: nvidia-smi emits the absolute threshold elements on pre-Ada and
// replaces them with *_tlimit_threshold on Ada and later. The unused set is
// absent from the document, so "which rows appear" becomes "which elements are
// present" — and -d cannot be combined with -x anyway.
//
// T.Limit readings are margins below the limit (-5 C, 0 C, 5 C), not
// temperatures, so the Ada+ branch asserts presence only. The absolute branch
// asserts the profile's configured values.

// DiffTemperature checks that a `nvidia-smi -q -x` document uses the
// architecture-correct threshold presentation for every GPU:
//
//   - pre-Ada (reportsTLimit=false): absolute gpu_temp_max_threshold,
//     gpu_temp_slow_threshold and gpu_temp_max_gpu_threshold equal to the
//     profile thresholds, and no *_tlimit_threshold element carrying a value.
//   - Ada+ (reportsTLimit=true): the three *_tlimit_threshold elements carry
//     values and no absolute element does.
//
// Only elements with a numeric reading count as present. nvidia-smi still emits
// gpu_temp_tlimit as N/A on pre-Ada, and an N/A body is exactly what a real
// unsupported query looks like; a T.Limit element with a NUMBER on pre-Ada is
// the defect (#635). Absolute thresholds must also never be negative or order
// shutdown below slowdown — the impossible rendering the gate fixes.
func DiffTemperature(out string, reportsTLimit bool, shutdownC, slowdownC, maxOperatingC int) []string {
	doc, err := parse(out)
	if err != nil {
		return []string{err.Error()}
	}

	var problems []string
	for i, gpu := range doc.GPUs {
		name := gpu.label(i)
		if reportsTLimit {
			problems = append(problems, diffTLimitTemperature(name, gpu.Temperature)...)
			continue
		}
		problems = append(problems, diffAbsoluteTemperature(name, gpu.Temperature,
			shutdownC, slowdownC, maxOperatingC)...)
	}
	return problems
}

// namedReading pairs an element body with its DTD name, so a problem message
// names the element rather than a human row label.
type namedReading struct {
	element string
	raw     reading
}

func tlimitThresholds(t temperature) []namedReading {
	return []namedReading{
		{"gpu_temp_max_tlimit_threshold", t.MaxTLimitThreshold},
		{"gpu_temp_slow_tlimit_threshold", t.SlowTLimitThreshold},
		{"gpu_temp_max_gpu_tlimit_threshold", t.MaxGPUTLimitThreshold},
	}
}

func absoluteThresholds(t temperature) []namedReading {
	return []namedReading{
		{"gpu_temp_max_threshold", t.MaxThreshold},
		{"gpu_temp_slow_threshold", t.SlowThreshold},
		{"gpu_temp_max_gpu_threshold", t.MaxGPUThreshold},
	}
}

func diffTLimitTemperature(name string, t temperature) []string {
	var problems []string
	for _, r := range tlimitThresholds(t) {
		if _, ok := r.raw.intValue(); !ok {
			problems = append(problems, fmt.Sprintf("%s: missing %s (body %q)",
				name, r.element, string(r.raw)))
		}
	}
	for _, r := range absoluteThresholds(t) {
		if _, ok := r.raw.intValue(); ok {
			problems = append(problems, fmt.Sprintf(
				"%s: unexpected absolute %s on an Ada+ profile", name, r.element))
		}
	}
	return problems
}

func diffAbsoluteTemperature(name string, t temperature, shutdownC, slowdownC, maxOperatingC int) []string {
	want := []struct {
		element string
		raw     reading
		wantC   int
	}{
		{"gpu_temp_max_threshold", t.MaxThreshold, shutdownC},
		{"gpu_temp_slow_threshold", t.SlowThreshold, slowdownC},
		{"gpu_temp_max_gpu_threshold", t.MaxGPUThreshold, maxOperatingC},
	}

	var problems []string
	for _, w := range want {
		got, ok := w.raw.intValue()
		switch {
		case !ok:
			problems = append(problems, fmt.Sprintf("%s: missing absolute %s (body %q)",
				name, w.element, string(w.raw)))
		case got != w.wantC:
			problems = append(problems, fmt.Sprintf("%s: %s = %d C, want %d C",
				name, w.element, got, w.wantC))
		}
	}
	for _, r := range tlimitThresholds(t) {
		if _, ok := r.raw.intValue(); ok {
			problems = append(problems, fmt.Sprintf(
				"%s: unexpected %s carrying a value on a pre-Ada profile", name, r.element))
		}
	}
	if _, ok := t.TLimit.intValue(); ok {
		problems = append(problems, name+": unexpected gpu_temp_tlimit carrying a value on a pre-Ada profile")
	}
	return append(problems, diffAbsoluteOrdering(name, t)...)
}

func diffAbsoluteOrdering(name string, t temperature) []string {
	var problems []string
	shutdown, hasShutdown := t.MaxThreshold.intValue()
	slowdown, hasSlowdown := t.SlowThreshold.intValue()
	if hasShutdown && shutdown < 0 {
		problems = append(problems, fmt.Sprintf(
			"%s: gpu_temp_max_threshold is negative (%d C)", name, shutdown))
	}
	if hasSlowdown && slowdown < 0 {
		problems = append(problems, fmt.Sprintf(
			"%s: gpu_temp_slow_threshold is negative (%d C)", name, slowdown))
	}
	if hasShutdown && hasSlowdown && shutdown < slowdown {
		problems = append(problems, fmt.Sprintf(
			"%s: gpu_temp_max_threshold (%d C) is below gpu_temp_slow_threshold (%d C)",
			name, shutdown, slowdown))
	}
	return problems
}
