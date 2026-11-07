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
	ruleRemainder = "the total less the other parts"
	ruleFitted    = "lowered to the room this GPU has"
	ruleBase      = "from -base"
	ruleLeftOut   = "left out"
	rulePerGPU    = "per device"
	ruleGenerated = "generated from a capture"
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
	if !s.device || !s.perDeviceForm(key) {
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
	s.m.noteFor(key, key+": differs per GPU, set under devices: "+from)
	s.trace(key, from, raws(a), "per device", rulePerDevice)
}

// How the mock engine applies a devices[] entry (mergeDeviceOverride in
// pkg/gpu/mocknvml/engine): the top-level keys and the blocks merged key by
// key take one key alone; a block replaced whole takes the block, so its
// devices[] copy repeats the values every GPU shares (completeDeviceBlocks).
// The engine applies nothing else per device.
var (
	perDeviceKeys = map[string]bool{
		"name": true, "serial": true, "brand": true, "board_part_number": true, "vbios_version": true,
		"hostname": true, "architecture": true, "addressing_mode": true,
	}
	blocksMergedByKey   = map[string]bool{"pci": true, "platform": true, "mig": true}
	blocksReplacedWhole = map[string]bool{
		"memory": true, "bar1_memory": true, "power": true, "thermal": true, "fan": true, "clocks": true,
		"clocks_throttle_reasons": true, "utilization": true, "ecc": true, "dynamic_metrics": true,
		"fabric": true, "accounting": true,
	}
)

// perDeviceForm reports whether the engine applies key of this section when a
// devices[] entry sets it.
func (s *section) perDeviceForm(key string) bool {
	if len(s.rel) == 0 {
		return perDeviceKeys[key]
	}
	return blocksMergedByKey[s.rel[0]] || blocksReplacedWhole[s.rel[0]]
}

func allSame(a []answer) bool {
	for _, x := range a[1:] {
		if x.yaml != a[0].yaml {
			return false
		}
	}
	return true
}

// reading sets a value that moves with load or time: the capture's idle
// reading, the lower median when the GPUs differ. -base is not read, so every
// reading traces to the board.
func (s *section) reading(key string, src source) {
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

// gpu0 sets a value from GPU 0's answer, for the thermal values that must add
// up with one another. The comment lists the GPUs when they differ, and marks
// a load reading. It returns the value written, "" when there is none.
func (s *section) gpu0(key string, src source, loadReading bool) string {
	a := s.g.answers(src)
	if a[0].missing != "" {
		s.leaveOut(key, src.from+" on GPU 0", a)
		return ""
	}
	from := withRaw(src.from+" on GPU 0", a[0].raw, a[0].yaml)
	if loadReading {
		from += ", " + ruleReading
	}
	if !allSame(a) {
		from += "; GPUs read " + strings.Join(raws(a), " ")
	}
	s.m.set(key, scalar{a[0].yaml}, from)
	s.trace(key, src.from, raws(a), a[0].yaml, ruleGPU0)
	return a[0].yaml
}

// remainder sets a reading to what is left of total once the parts already
// written are taken from it, so the profile's values add up the way the
// driver's do: free memory is the total less what is reserved and used. It is
// worked out per GPU, from each GPU's own parts, and set per device when the
// GPUs come out different. The comment gives the capture's own reading of the
// key for comparison. The last part is a reading; see remainderOf.
func (s *section) remainder(key string, src source, total string, parts ...string) {
	names := append([]string{total}, parts...)
	lefts := make([]int64, s.g.c.gpuCount())
	workings := make([]string, len(lefts))
	for gpu := range lefts {
		left, working, err := s.remainderOf(gpu, names)
		if err != nil {
			s.m.note(fmt.Sprintf("%s: left out, %v", key, err))
			s.trace(key, src.from, nil, "", ruleLeftOut)
			return
		}
		lefts[gpu], workings[gpu] = left, working
	}
	formula := strings.Join(names, " - ")
	captured := raws(s.g.answers(src))
	if !slices.ContainsFunc(lefts, func(n int64) bool { return n != lefts[0] }) {
		s.m.set(key, integer(lefts[0]), fmt.Sprintf("%s = %s; %s reads %s", formula, workings[0], src.from, onEveryGPU(captured)))
		s.trace(key, src.from, captured, strconv.FormatInt(lefts[0], 10), ruleRemainder)
		return
	}
	for gpu, left := range lefts {
		s.g.override(gpu, s.rel).set(key, integer(left), fmt.Sprintf("%s = %s; %s reads %s", formula, workings[gpu], src.from, captured[gpu]))
	}
	s.m.noteFor(key, key+": differs per GPU, set under devices: "+formula)
	s.trace(key, src.from, captured, "per device", rulePerDevice)
}

// remainderOf is one GPU's remainder of names[0] and its working. The last
// part is a reading, written once for every GPU as the lower median of theirs.
// On a GPU whose other parts differ from its siblings', such as a larger
// reservation, that reading can exceed what the GPU has room for; it is then
// lowered to the room under the GPU's devices[] entry, so the parts still add
// up and nothing comes out negative.
func (s *section) remainderOf(gpu int, names []string) (int64, string, error) {
	nums := make([]int64, len(names))
	for i, name := range names {
		n, err := s.partOf(gpu, name)
		if err != nil {
			return 0, "", err
		}
		nums[i] = n
	}
	last := len(nums) - 1
	room := nums[0]
	for _, n := range nums[1:last] {
		room -= n
	}
	if room < 0 {
		return 0, "", fmt.Errorf("%s is negative on GPU %d", strings.Join(names[:last], " - "), gpu)
	}
	if nums[last] > room {
		reading := names[last]
		s.g.override(gpu, s.rel).set(reading, integer(room), fmt.Sprintf("%s = %d: the reading written for every GPU, %d, does not fit this one",
			strings.Join(names[:last], " - "), room, nums[last]))
		dotted := strings.Join(append(slices.Clone(s.rel), reading), ".")
		s.g.traces = append(s.g.traces, trace{key: fmt.Sprintf("devices[%d].%s", gpu, dotted), from: s.path(reading),
			perGPU: []string{strconv.FormatInt(nums[last], 10)}, value: strconv.FormatInt(room, 10), rule: ruleFitted})
		nums[last] = room
	}
	texts := make([]string, len(nums))
	for i, n := range nums {
		texts[i] = strconv.FormatInt(n, 10)
	}
	return room - nums[last], strings.Join(texts, " - "), nil
}

// partOf is one GPU's value of a key of this section: the one written under
// its devices[] entry when the GPUs differ, else the one for every GPU.
func (s *section) partOf(gpu int, name string) (int64, error) {
	e := s.m.find(name)
	if m := s.g.deviceMapping(gpu, s.rel); m != nil {
		if own := m.find(name); own != nil {
			e = own
		}
	}
	if e != nil {
		if v, ok := e.value.(scalar); ok {
			if n, err := strconv.ParseInt(v.text, 10, 64); err == nil {
				return n, nil
			}
		}
	}
	return 0, fmt.Errorf("%s has no value for GPU %d to take it from", name, gpu)
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

// deviceMapping is override without adding anything: nil when GPU i's
// devices[] entry has no mapping at path.
func (g *generator) deviceMapping(gpu int, path []string) *mapping {
	m := g.devices[gpu]
	for _, k := range path {
		e := m.find(k)
		if e == nil {
			return nil
		}
		next, ok := e.value.(*mapping)
		if !ok {
			return nil
		}
		m = next
	}
	return m
}

// completeDeviceBlocks repeats the values every GPU shares in each devices[]
// block the engine replaces whole, so a GPU that differs in one key keeps the
// rest of the block. The repeated values keep their source comments.
func (g *generator) completeDeviceBlocks(defaults *mapping) {
	for _, d := range g.devices {
		for _, e := range d.entries {
			own, ok := e.value.(*mapping)
			if !ok || !blocksReplacedWhole[e.key] {
				continue
			}
			shared := defaults.find(e.key)
			if shared == nil {
				continue
			}
			if sm, ok := shared.value.(*mapping); ok {
				e.value = withShared(sm, own)
				e.head = append(e.head, e.key+": the engine replaces a device's whole block, so the values every GPU shares are repeated")
			}
		}
	}
}

// withShared is own with every value of shared it does not set itself, in
// shared's order, followed by own's other keys.
func withShared(shared, own *mapping) *mapping {
	out := &mapping{flow: shared.flow}
	placed := map[string]bool{}
	for _, se := range shared.entries {
		oe := own.find(se.key)
		if oe != nil {
			placed[se.key] = true
		}
		if e := sharedEntry(se, oe); e != nil {
			out.entries = append(out.entries, e)
		}
	}
	for _, oe := range own.entries {
		if oe.value == nil || !placed[oe.key] {
			out.entries = append(out.entries, oe)
		}
	}
	return out
}

// sharedEntry is what a devices[] block holds in the place of se, an entry of
// the shared block: the block's own entry oe when it has one, merged with se
// when both are blocks, else se's values. A note holds the place of a key each
// GPU sets on its own, and is not repeated itself.
func sharedEntry(se, oe *entry) *entry {
	sm, sharedBlock := se.value.(*mapping)
	switch {
	case se.value == nil:
		return oe
	case oe == nil && sharedBlock:
		if v := valuesOf(sm); v != nil {
			return &entry{key: se.key, value: v, comment: se.comment}
		}
		return nil
	case oe == nil:
		return &entry{key: se.key, value: se.value, comment: se.comment}
	}
	if om, ok := oe.value.(*mapping); ok && sharedBlock {
		return &entry{key: se.key, value: withShared(sm, om), comment: oe.comment}
	}
	return oe
}

// valuesOf copies the values of m, without its notes; nil when it has none.
func valuesOf(m *mapping) *mapping {
	out := &mapping{flow: m.flow}
	for _, e := range m.entries {
		switch v := e.value.(type) {
		case nil:
			continue
		case *mapping:
			if c := valuesOf(v); c != nil {
				out.entries = append(out.entries, &entry{key: e.key, value: c, comment: e.comment})
			}
		default:
			out.entries = append(out.entries, &entry{key: e.key, value: v, comment: e.comment})
		}
	}
	if len(out.entries) == 0 {
		return nil
	}
	return out
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
