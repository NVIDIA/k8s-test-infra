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
	"strconv"
	"strings"
)

// The profile is written by hand-rolled code rather than a YAML encoder: every
// value carries a comment naming its source, comments line up in one column,
// and long clock lists wrap the way the hand-written profiles do. Keys keep
// the order the rules add them in, so the same capture always gives the same
// bytes.

type node interface{ isNode() }

// scalar is a value already in YAML syntax: a number, a bool or a quoted string.
type scalar struct{ text string }

type mapping struct {
	entries []*entry
	flow    bool // {start: 0, size: 1} on one line
}

// entry is one key of a mapping. An entry without a value is a comment line.
type entry struct {
	key     string
	value   node
	comment string
	head    []string
}

type sequence struct {
	items []node
}

func (scalar) isNode()    {}
func (*mapping) isNode()  {}
func (*sequence) isNode() {}

func quote(s string) scalar { return scalar{strconv.Quote(s)} }
func integer[T ~int | ~int64 | ~uint8 | ~uint32 | ~uint64](v T) scalar {
	return scalar{strconv.FormatInt(int64(v), 10)}
}

func boolean(b bool) scalar { return scalar{strconv.FormatBool(b)} }

func (m *mapping) find(key string) *entry {
	for _, e := range m.entries {
		if e.key == key && e.value != nil {
			return e
		}
	}
	return nil
}

// child returns the mapping under key, adding an empty one when there is none.
func (m *mapping) child(key string) *mapping {
	if e := m.find(key); e != nil {
		if c, ok := e.value.(*mapping); ok {
			return c
		}
	}
	c := &mapping{}
	m.entries = append(m.entries, &entry{key: key, value: c})
	return c
}

func (m *mapping) set(key string, v node, comment string) {
	if e := m.find(key); e != nil {
		e.value, e.comment = v, comment
		return
	}
	m.entries = append(m.entries, &entry{key: key, value: v, comment: comment})
}

// note adds a comment line, for a key the rules leave out and why.
func (m *mapping) note(text string) {
	m.entries = append(m.entries, &entry{head: []string{text}})
}

// commentColumn is where line comments start, as in the hand-written profiles.
const commentColumn = 36

// flowWidth is how many scalars a wrapped flow list puts on one line.
const flowWidth = 12

func render(header []string, doc *mapping) string {
	var b strings.Builder
	for _, h := range header {
		writeComment(&b, 0, h)
	}
	for i, e := range doc.entries {
		if i > 0 || len(header) > 0 {
			b.WriteByte('\n')
		}
		writeEntry(&b, 0, e)
	}
	return b.String()
}

func writeComment(b *strings.Builder, indent int, text string) {
	b.WriteString(strings.TrimRight(strings.Repeat(" ", indent)+"# "+text, " "))
	b.WriteByte('\n')
}

func writeLine(b *strings.Builder, line, comment string) {
	b.WriteString(line)
	if comment != "" {
		pad := commentColumn - len(line)
		if pad < 1 {
			pad = 1
		}
		b.WriteString(strings.Repeat(" ", pad))
		b.WriteString("# ")
		b.WriteString(comment)
	}
	b.WriteByte('\n')
}

func writeMapping(b *strings.Builder, indent int, m *mapping) {
	for _, e := range m.entries {
		writeEntry(b, indent, e)
	}
}

func writeEntry(b *strings.Builder, indent int, e *entry) {
	for _, h := range e.head {
		writeComment(b, indent, h)
	}
	if e.value == nil {
		return
	}
	pad := strings.Repeat(" ", indent)
	switch v := e.value.(type) {
	case scalar:
		writeLine(b, pad+e.key+": "+v.text, e.comment)
	case *mapping:
		switch {
		case len(v.entries) == 0:
			writeLine(b, pad+e.key+": {}", e.comment)
		case v.flow:
			writeLine(b, pad+e.key+": "+flowMapping(v), e.comment)
		default:
			writeLine(b, pad+e.key+":", e.comment)
			writeMapping(b, indent+2, v)
		}
	case *sequence:
		writeSequence(b, indent, e, v)
	}
}

func writeSequence(b *strings.Builder, indent int, e *entry, s *sequence) {
	pad := strings.Repeat(" ", indent)
	if len(s.items) == 0 {
		writeLine(b, pad+e.key+": []", e.comment)
		return
	}
	if texts, ok := scalars(s); ok {
		writeFlowList(b, pad, e, texts)
		return
	}
	writeLine(b, pad+e.key+":", e.comment)
	for _, item := range s.items {
		m, ok := item.(*mapping)
		if !ok || len(m.entries) == 0 {
			continue
		}
		if m.flow {
			b.WriteString(pad + "  - " + flowMapping(m) + "\n")
			continue
		}
		// The first key shares the dash's line; the rest line up under it.
		var first strings.Builder
		writeEntry(&first, indent+4, m.entries[0])
		b.WriteString(pad + "  - " + strings.TrimPrefix(first.String(), strings.Repeat(" ", indent+4)))
		for _, rest := range m.entries[1:] {
			writeEntry(b, indent+4, rest)
		}
	}
}

// writeFlowList writes scalars as [a, b], wrapped flowWidth to a line when
// they do not fit on one, the way the hand-written profiles list clocks.
func writeFlowList(b *strings.Builder, pad string, e *entry, texts []string) {
	if len(texts) <= flowWidth {
		writeLine(b, pad+e.key+": ["+strings.Join(texts, ", ")+"]", e.comment)
		return
	}
	writeLine(b, pad+e.key+": [", e.comment)
	for i := 0; i < len(texts); i += flowWidth {
		row := strings.Join(texts[i:min(i+flowWidth, len(texts))], ", ")
		if i+flowWidth < len(texts) {
			row += ","
		}
		b.WriteString(pad + "  " + row + "\n")
	}
	b.WriteString(pad + "]\n")
}

func scalars(s *sequence) ([]string, bool) {
	out := make([]string, len(s.items))
	for i, item := range s.items {
		v, ok := item.(scalar)
		if !ok {
			return nil, false
		}
		out[i] = v.text
	}
	return out, true
}

func flowMapping(m *mapping) string {
	parts := make([]string, 0, len(m.entries))
	for _, e := range m.entries {
		if v, ok := e.value.(scalar); ok {
			parts = append(parts, e.key+": "+v.text)
		}
	}
	return "{" + strings.Join(parts, ", ") + "}"
}
