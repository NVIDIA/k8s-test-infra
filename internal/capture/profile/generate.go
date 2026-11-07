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

package profile

import (
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// generator holds the profile while the rules build it.
type generator struct {
	c       *capture
	base    *baseProfile // nil without -base
	doc     *mapping
	devices []*mapping // devices[], one per GPU, index first
	mig     *mapping   // the MIG profile table, nil when the board has none
	traces  []trace
	errs    []error
}

// trace is one line of -explain: a key, where it came from, what each GPU
// answered, and what was written.
type trace struct {
	key    string
	from   string
	perGPU []string
	value  string
	rule   string
}

// Rules, as -explain and the comments name them.
const (
	ruleSame      = "same on every GPU"
	rulePerDevice = "differs per GPU: set under devices"
	ruleMedian    = "lower median of the GPUs"
	ruleReading   = "idle reading"
	ruleBase      = "from -base"
	ruleLeftOut   = "left out"
	rulePerGPU    = "per device"
)

// answer is one GPU's value for a key.
type answer struct {
	raw     string // as the capture recorded it
	yaml    string // as the profile takes it; "" when missing
	missing string // why there is no value
}

func (g *generator) answers(src source) []answer {
	out := make([]answer, g.c.gpuCount())
	for i := range out {
		raw, why := src.read(g.c, i)
		if why != "" {
			out[i] = answer{raw: why, missing: why}
			continue
		}
		y, ok := src.format(raw)
		if !ok {
			out[i] = answer{raw: show(raw), missing: "no profile value for " + show(raw)}
			continue
		}
		out[i] = answer{raw: show(raw), yaml: y}
	}
	return out
}

func raws(a []answer) []string {
	out := make([]string, len(a))
	for i, x := range a {
		out[i] = x.raw
	}
	return out
}

// sameText reports whether a value reads the same in the capture and the
// profile, so the comment need not repeat it.
func sameText(raw, yaml string) bool { return raw == yaml || strconv.Quote(raw) == yaml }

func withRaw(from, raw, yaml string) string {
	if sameText(raw, yaml) {
		return from
	}
	return from + " = " + raw
}

// section is one mapping of the profile under construction.
type section struct {
	g   *generator
	m   *mapping
	key string // dotted path, as -explain and -base name it
	// rel is the path below device_defaults, where a per-device value goes
	// under devices[]; nil outside device_defaults.
	rel    []string
	device bool
}

func (s *section) sub(key string) *section {
	return &section{g: s.g, m: s.m.child(key), key: s.key + "." + key, rel: append(slices.Clone(s.rel), key), device: s.device}
}

func (s *section) path(key string) string {
	if s.key == "" {
		return key
	}
	return s.key + "." + key
}

func (s *section) trace(key, from string, perGPU []string, value, rule string) {
	s.g.traces = append(s.g.traces, trace{key: s.path(key), from: from, perGPU: perGPU, value: value, rule: rule})
}

func (s *section) leaveOut(key, from string, a []answer) {
	s.m.note(fmt.Sprintf("%s: left out, %s = %s", key, from, a[0].missing))
	s.trace(key, from, raws(a), "", ruleLeftOut)
}

// prop sets a property of the board: one value when every GPU agrees, one
// per device when they differ, and nothing when the capture has no answer.
func (s *section) prop(key string, src source) {
	a := s.g.answers(src)
	s.place(key, src.from, a)
}

// propOrNA is prop for keys whose profile spelling of "not supported" is
// "N/A".
func (s *section) propOrNA(key string, src source) {
	a := s.g.answers(src)
	for i := range a {
		// Only an answer from the driver means "not supported"; a call the
		// dump did not record says nothing.
		if strings.HasPrefix(a[i].missing, "ERROR_") || a[i].missing == notSupportedNA {
			a[i] = answer{raw: a[i].raw, yaml: strconv.Quote(notSupportedNA)}
		}
	}
	s.place(key, src.from, a)
}

func (s *section) place(key, from string, a []answer) {
	present := 0
	for _, x := range a {
		if x.missing == "" {
			present++
		}
	}
	if present == 0 {
		s.leaveOut(key, from, a)
		return
	}
	if present == len(a) && allSame(a) {
		s.m.set(key, scalar{a[0].yaml}, withRaw(from, a[0].raw, a[0].yaml))
		s.trace(key, from, raws(a), a[0].yaml, ruleSame)
		return
	}
	if !s.device {
		s.g.errs = append(s.g.errs, fmt.Errorf("%s: the GPUs disagree (%s) and the key has no per-device form",
			s.path(key), strings.Join(raws(a), ", ")))
		return
	}
	for i, x := range a {
		if x.missing != "" {
			continue
		}
		s.g.override(i, s.rel).set(key, scalar{x.yaml}, withRaw(from, x.raw, x.yaml))
	}
	s.m.note(key + ": differs per GPU, set under devices: " + from)
	s.trace(key, from, raws(a), "per device", rulePerDevice)
}

func allSame(a []answer) bool {
	for _, x := range a[1:] {
		if x.yaml != a[0].yaml {
			return false
		}
	}
	return true
}

// reading sets a value that moves with load or time. -base wins when it has
// the key; otherwise the capture's idle reading is used, the lower median when
// the GPUs differ.
func (s *section) reading(key string, src source) {
	if v, ok := s.g.base.scalar(s.path(key)); ok {
		s.m.set(key, v, "from -base; load reading, not in the capture")
		s.trace(key, src.from, nil, v.text, ruleBase)
		return
	}
	a := s.g.answers(src)
	var got []answer
	for _, x := range a {
		if x.missing == "" {
			got = append(got, x)
		}
	}
	if len(got) == 0 {
		s.leaveOut(key, src.from, a)
		return
	}
	if len(got) == len(a) && allSame(a) {
		s.m.set(key, scalar{a[0].yaml}, withRaw(src.from, a[0].raw, a[0].yaml)+", "+ruleReading)
		s.trace(key, src.from, raws(a), a[0].yaml, ruleReading)
		return
	}
	pick := lowerMedian(got)
	s.m.set(key, scalar{pick.yaml}, fmt.Sprintf("%s = %s, %s: %s", src.from, strings.Join(raws(a), " "), ruleReading, ruleMedian))
	s.trace(key, src.from, raws(a), pick.yaml, ruleReading+": "+ruleMedian)
}

// lowerMedian picks the middle value, the lower of the two middle ones for
// an even count, so the result is always a value some GPU reported.
func lowerMedian(a []answer) answer {
	sorted := slices.Clone(a)
	sort.SliceStable(sorted, func(i, j int) bool {
		x, errX := strconv.ParseFloat(sorted[i].yaml, 64)
		y, errY := strconv.ParseFloat(sorted[j].yaml, 64)
		if errX != nil || errY != nil {
			return sorted[i].yaml < sorted[j].yaml
		}
		return x < y
	})
	return sorted[(len(sorted)-1)/2]
}

// derive sets a value computed per GPU from several capture values. per
// returns the GPU's value and its working ("34+53"), or a reason it has none.
// A property measured through rounded readings can differ by one between
// GPUs, so the lower median stands for all of them.
func (s *section) derive(key, from string, per func(gpu int) (value int64, working, missing string)) {
	a := make([]answer, s.g.c.gpuCount())
	workings := make([]string, len(a))
	var got []answer
	for i := range a {
		v, w, why := per(i)
		if why != "" {
			a[i], workings[i] = answer{raw: why, missing: why}, why
			continue
		}
		a[i] = answer{raw: w, yaml: strconv.FormatInt(v, 10)}
		workings[i] = w
		if w != a[i].yaml {
			workings[i] = w + "=" + a[i].yaml
		}
		got = append(got, a[i])
	}
	if len(got) == 0 {
		s.leaveOut(key, from, a)
		return
	}
	pick := lowerMedian(got)
	s.m.set(key, scalar{pick.yaml}, fmt.Sprintf("%s per GPU: %s; %s", from, strings.Join(workings, " "), ruleMedian))
	s.trace(key, from, workings, pick.yaml, ruleMedian)
}

// value sets a key computed by a rule from the whole capture.
func (s *section) value(key string, v node, from, rule string) {
	s.m.set(key, v, from)
	text := ""
	if sc, ok := v.(scalar); ok {
		text = sc.text
	}
	s.trace(key, from, nil, text, rule)
}

// fromBase copies a block the capture cannot give from -base, or notes that
// it is missing.
func (s *section) fromBase(key, why string) {
	if n, ok := s.g.base.node(s.path(key)); ok {
		s.m.set(key, n, "from -base: "+why)
		s.trace(key, "-base", nil, "(block)", ruleBase)
		return
	}
	s.m.note(key + ": left out, " + why + "; pass -base to copy it")
	s.trace(key, "-base", nil, "", ruleLeftOut)
}

// override returns GPU i's mapping at path below its devices[] entry.
func (g *generator) override(gpu int, path []string) *mapping {
	m := g.devices[gpu]
	for _, k := range path {
		m = m.child(k)
	}
	return m
}

// ident sets a per-device identifier: -base's value for that device, else the
// placeholder the scrubbed capture holds.
func (g *generator) ident(key string, path []string, src source) {
	a := g.answers(src)
	dotted := strings.Join(append(slices.Clone(path), key), ".")
	for i, x := range a {
		m := g.override(i, path)
		if v, ok := g.base.deviceScalar(i, dotted); ok {
			m.set(key, v, "from -base; scrubbed in the capture")
			g.traces = append(g.traces, trace{key: fmt.Sprintf("devices[%d].%s", i, dotted), from: src.from, perGPU: []string{x.raw}, value: v.text, rule: ruleBase})
			continue
		}
		if x.missing != "" {
			m.note(key + ": left out, " + src.from + " = " + x.missing)
			continue
		}
		m.set(key, scalar{x.yaml}, "placeholder: "+withRaw(src.from, x.raw, x.yaml))
		g.traces = append(g.traces, trace{key: fmt.Sprintf("devices[%d].%s", i, dotted), from: src.from, perGPU: []string{x.raw}, value: x.yaml, rule: rulePerGPU})
	}
}

// perDevice sets a key that names the device itself (its bus ID, minor
// number), so it always sits under devices[].
func (g *generator) perDevice(key string, path []string, src source) {
	a := g.answers(src)
	dotted := strings.Join(append(slices.Clone(path), key), ".")
	for i, x := range a {
		m := g.override(i, path)
		if x.missing != "" {
			m.note(key + ": left out, " + src.from + " = " + x.missing)
			continue
		}
		m.set(key, scalar{x.yaml}, withRaw(src.from, x.raw, x.yaml))
		g.traces = append(g.traces, trace{key: fmt.Sprintf("devices[%d].%s", i, dotted), from: src.from, perGPU: []string{x.raw}, value: x.yaml, rule: rulePerGPU})
	}
}

// prune drops mappings the rules left empty, keeping their notes as comments
// in the parent so the profile still says why the block is missing.
func prune(m *mapping) {
	out := m.entries[:0]
	for _, e := range m.entries {
		child, ok := e.value.(*mapping)
		if !ok {
			out = append(out, e)
			continue
		}
		prune(child)
		if hasValues(child) {
			out = append(out, e)
			continue
		}
		var notes []string
		for _, ce := range child.entries {
			for _, h := range ce.head {
				sep := "."
				if strings.HasPrefix(h, "left out") {
					sep = ": "
				}
				notes = append(notes, e.key+sep+h)
			}
		}
		if len(notes) > 0 {
			out = append(out, &entry{head: notes})
		}
	}
	m.entries = out
}

func hasValues(m *mapping) bool {
	if m.flow {
		return len(m.entries) > 0
	}
	for _, e := range m.entries {
		if e.value != nil {
			return true
		}
	}
	return false
}
