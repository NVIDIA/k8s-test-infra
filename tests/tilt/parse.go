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

package tilt

import (
	"sort"
	"strings"
)

// Build is a docker_build() call with its arguments resolved to literals.
type Build struct {
	Dockerfile string
	Only       []string
}

// ContextCopySources returns the build-context paths a Dockerfile COPYs, in
// sorted order. COPY --from=<stage> lines are skipped: they read from an earlier
// stage's filesystem, not from the context, so the context restriction cannot
// affect them.
func ContextCopySources(dockerfile string) []string {
	seen := map[string]bool{}
	for _, line := range logicalLines(dockerfile) {
		for _, path := range copySources(line) {
			seen[path] = true
		}
	}
	return sortedKeys(seen)
}

// copySources returns the context paths a single COPY instruction reads, or nil
// for any other line.
func copySources(line string) []string {
	fields := strings.Fields(line)
	if len(fields) == 0 || !strings.EqualFold(fields[0], "COPY") {
		return nil
	}
	args := fields[1:]

	// Flags precede the operands. --from= redirects the source away from the
	// build context entirely.
	for len(args) > 0 && strings.HasPrefix(args[0], "--") {
		if strings.HasPrefix(args[0], "--from=") {
			return nil
		}
		args = args[1:]
	}
	// The final operand is the destination; anything before it is a source.
	// Fewer than two operands is not a COPY we can read.
	if len(args) < 2 {
		return nil
	}

	var out []string
	for _, src := range args[:len(args)-1] {
		if path := strings.Trim(src, `"'`); path != "" {
			out = append(out, path)
		}
	}
	return out
}

// Constants extracts a Tiltfile's module-level string and string-list
// assignments, so a docker_build argument written as a named constant can be
// resolved to its value.
func Constants(src string) (strs map[string]string, lists map[string][]string) {
	strs, lists = map[string]string{}, map[string][]string{}
	code := stripComments(src)

	for i := 0; i < len(code); i++ {
		// Module level only: an assignment indented under a def is local to it.
		if i > 0 && code[i-1] != '\n' {
			continue
		}
		name, rest, ok := splitAssignment(code[i:])
		if !ok {
			continue
		}
		switch {
		case strings.HasPrefix(rest, "["):
			end := matchDelimiter(rest, '[', ']')
			if end < 0 {
				continue
			}
			lists[name] = stringLiterals(rest[:end])
			i += len(code[i:]) - len(rest) + end
		case strings.HasPrefix(rest, `'`), strings.HasPrefix(rest, `"`):
			if got := stringLiterals(firstLine(rest)); len(got) == 1 {
				strs[name] = got[0]
			}
		}
	}

	return strs, lists
}

// DockerBuilds returns the docker_build() calls in src, resolving the
// dockerfile= and only= arguments through the supplied constant tables. A call
// whose dockerfile= cannot be resolved is skipped; an unresolved only= yields an
// empty Only, which callers are expected to treat as a failure rather than as
// "no restriction".
func DockerBuilds(src string, strs map[string]string, lists map[string][]string) []Build {
	const call = "docker_build("

	var builds []Build
	code := stripComments(src)

	for idx := 0; ; {
		at := strings.Index(code[idx:], call)
		if at < 0 {
			break
		}
		open := idx + at + len(call) - 1
		end := matchDelimiter(code[open:], '(', ')')
		if end < 0 {
			break
		}
		args := code[open+1 : open+end]
		idx = open + end

		dockerfile, ok := resolveString(args, "dockerfile", strs)
		if !ok {
			continue
		}
		builds = append(builds, Build{
			Dockerfile: dockerfile,
			Only:       resolveList(args, "only", lists),
		})
	}

	return builds
}

// resolveString reads a keyword argument that is either a string literal or the
// name of a string constant.
func resolveString(args, keyword string, strs map[string]string) (string, bool) {
	value, ok := keywordValue(args, keyword)
	if !ok {
		return "", false
	}
	if strings.HasPrefix(value, `'`) || strings.HasPrefix(value, `"`) {
		if got := stringLiterals(firstLine(value)); len(got) == 1 {
			return got[0], true
		}
		return "", false
	}
	got, ok := strs[identifier(value)]
	return got, ok
}

// resolveList reads a keyword argument that is either a list literal or the name
// of a list constant. An unresolvable argument returns nil.
func resolveList(args, keyword string, lists map[string][]string) []string {
	value, ok := keywordValue(args, keyword)
	if !ok {
		return nil
	}
	if strings.HasPrefix(value, "[") {
		if end := matchDelimiter(value, '[', ']'); end > 0 {
			return stringLiterals(value[:end])
		}
		return nil
	}
	return lists[identifier(value)]
}

// keywordValue returns the text following `<keyword>=` in an argument list,
// ignoring occurrences where the name is merely a suffix of a longer keyword.
func keywordValue(args, keyword string) (string, bool) {
	for idx := 0; ; {
		at := strings.Index(args[idx:], keyword+"=")
		if at < 0 {
			return "", false
		}
		start := idx + at
		idx = start + len(keyword) + 1
		if start > 0 && (isIdentRune(args[start-1]) || args[start-1] == '=') {
			continue
		}
		return strings.TrimLeft(args[idx:], " \t\n"), true
	}
}

// splitAssignment recognises a leading `NAME =` and returns the name with the
// text after the operator.
func splitAssignment(s string) (name, rest string, ok bool) {
	line := firstLine(s)
	eq := strings.Index(line, "=")
	if eq < 0 {
		return "", "", false
	}
	name = strings.TrimSpace(line[:eq])
	if name == "" || eq+1 >= len(line) || line[eq+1] == '=' {
		return "", "", false
	}
	for i := 0; i < len(name); i++ {
		if !isIdentRune(name[i]) {
			return "", "", false
		}
	}
	return name, strings.TrimLeft(s[eq+1:], " \t"), true
}

// stringLiterals returns the single- or double-quoted strings in s, in order.
func stringLiterals(s string) []string {
	var out []string
	for i := 0; i < len(s); i++ {
		if s[i] != '\'' && s[i] != '"' {
			continue
		}
		quote := s[i]
		if end := strings.IndexByte(s[i+1:], quote); end >= 0 {
			out = append(out, s[i+1:i+1+end])
			i += end + 1
		}
	}
	return out
}

// matchDelimiter returns the index of the delimiter closing the opening one at
// s[0], skipping over quoted text, or -1 when it is unbalanced.
func matchDelimiter(s string, opening, closing byte) int {
	if len(s) == 0 || s[0] != opening {
		return -1
	}
	depth := 0
	for i := 0; i < len(s); i++ {
		switch c := s[i]; c {
		case '\'', '"':
			end := strings.IndexByte(s[i+1:], c)
			if end < 0 {
				return -1
			}
			i += end + 1
		case opening:
			depth++
		case closing:
			if depth--; depth == 0 {
				return i
			}
		}
	}
	return -1
}

// stripComments blanks out `#` comments while preserving offsets and line
// structure, so `#` inside a string literal is left alone.
func stripComments(src string) string {
	out := []byte(src)
	for i := 0; i < len(out); i++ {
		switch c := out[i]; c {
		case '\'', '"':
			end := strings.IndexByte(string(out[i+1:]), c)
			if end < 0 {
				return string(out)
			}
			i += end + 1
		case '#':
			for ; i < len(out) && out[i] != '\n'; i++ {
				out[i] = ' '
			}
		}
	}
	return string(out)
}

// logicalLines joins backslash continuations so a wrapped instruction reads as
// one line.
func logicalLines(src string) []string {
	var out []string
	var buf strings.Builder
	for _, line := range strings.Split(src, "\n") {
		trimmed := strings.TrimRight(line, " \t")
		if strings.HasSuffix(trimmed, `\`) {
			buf.WriteString(strings.TrimSuffix(trimmed, `\`))
			buf.WriteByte(' ')
			continue
		}
		buf.WriteString(trimmed)
		out = append(out, buf.String())
		buf.Reset()
	}
	if buf.Len() > 0 {
		out = append(out, buf.String())
	}
	return out
}

func firstLine(s string) string {
	if at := strings.IndexByte(s, '\n'); at >= 0 {
		return s[:at]
	}
	return s
}

// identifier returns the leading identifier of s, dropping any trailing
// argument-list punctuation.
func identifier(s string) string {
	for i := 0; i < len(s); i++ {
		if !isIdentRune(s[i]) {
			return s[:i]
		}
	}
	return s
}

func isIdentRune(c byte) bool {
	return c == '_' ||
		(c >= 'a' && c <= 'z') ||
		(c >= 'A' && c <= 'Z') ||
		(c >= '0' && c <= '9')
}

func sortedKeys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
