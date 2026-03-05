// Copyright (c) 2025-2026, NVIDIA CORPORATION.  All rights reserved.
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

package main

import (
	"bufio"
	"fmt"
	"io"
	"regexp"
	"strings"
)

// CParam represents a C function parameter with its type and name.
type CParam struct {
	CType string
	Name  string
}

// FuncProto represents a parsed C function prototype.
type FuncProto struct {
	Name   string
	Params []CParam
}

// parseNVMLPrototypes parses nvml.h content and extracts function prototypes.
// It handles multi-line declarations and DEPRECATED() prefixes.
func parseNVMLPrototypes(r io.Reader) (map[string]FuncProto, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)

	protos := make(map[string]FuncProto)
	var accumulating bool
	var accum strings.Builder

	for scanner.Scan() {
		line := scanner.Text()

		if accumulating {
			accum.WriteString(" ")
			accum.WriteString(strings.TrimSpace(line))
			if strings.Contains(line, ");") {
				proto, err := parsePrototypeLine(accum.String())
				if err == nil {
					protos[proto.Name] = proto
				}
				accumulating = false
				accum.Reset()
			}
			continue
		}

		if !strings.Contains(line, "nvmlReturn_t") || !strings.Contains(line, "DECLDIR") {
			continue
		}

		trimmed := strings.TrimSpace(line)
		if strings.Contains(trimmed, ");") {
			proto, err := parsePrototypeLine(trimmed)
			if err == nil {
				protos[proto.Name] = proto
			}
		} else {
			accumulating = true
			accum.Reset()
			accum.WriteString(trimmed)
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return protos, nil
}

// declRe matches: [DEPRECATED(...)] nvmlReturn_t DECLDIR funcname(params);
var declRe = regexp.MustCompile(`nvmlReturn_t\s+DECLDIR\s+(\w+)\s*\(([^)]*)\)\s*;`)

func parsePrototypeLine(line string) (FuncProto, error) {
	m := declRe.FindStringSubmatch(line)
	if m == nil {
		return FuncProto{}, fmt.Errorf("no match: %s", line)
	}

	name := m[1]
	paramStr := strings.TrimSpace(m[2])

	var params []CParam
	if paramStr != "" && paramStr != "void" {
		params = parseParams(paramStr)
	}

	return FuncProto{Name: name, Params: params}, nil
}

// parseParams splits a C parameter list and extracts type+name for each.
func parseParams(paramStr string) []CParam {
	parts := strings.Split(paramStr, ",")
	var params []CParam

	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		param := parseOneParam(p)
		params = append(params, param)
	}
	return params
}

// parseOneParam parses a single C parameter declaration like "unsigned int *temp"
// or "nvmlDevice_t device" or "const char *serial".
func parseOneParam(decl string) CParam {
	decl = strings.TrimSpace(decl)

	// Handle "char* name" (pointer attached to type)
	if idx := strings.Index(decl, "*"); idx >= 0 {
		// Check if * is attached to the type name (like "char*")
		// or separate (like "char *name" or "unsigned int *temp")
		beforeStar := strings.TrimSpace(decl[:idx])
		afterStar := strings.TrimSpace(decl[idx+1:])

		// afterStar is the parameter name
		if afterStar != "" && !strings.Contains(afterStar, " ") {
			return CParam{
				CType: beforeStar + " *",
				Name:  afterStar,
			}
		}
	}

	// No pointer - split on last space to separate type from name
	// Handle multi-word types like "unsigned int"
	lastSpace := strings.LastIndex(decl, " ")
	if lastSpace < 0 {
		return CParam{CType: decl, Name: ""}
	}

	return CParam{
		CType: strings.TrimSpace(decl[:lastSpace]),
		Name:  strings.TrimSpace(decl[lastSpace+1:]),
	}
}

// cTypeToGo converts a C type string to its CGo equivalent.
func cTypeToGo(cType string) string {
	cType = strings.TrimSpace(cType)

	// Strip const qualifier
	isConst := strings.HasPrefix(cType, "const ")
	if isConst {
		cType = strings.TrimPrefix(cType, "const ")
	}

	// Check for pointer
	isPointer := strings.HasSuffix(cType, " *") || strings.HasSuffix(cType, "*")
	if isPointer {
		cType = strings.TrimSuffix(cType, " *")
		cType = strings.TrimSuffix(cType, "*")
		cType = strings.TrimSpace(cType)
	}

	// Map base type
	var goBase string
	switch cType {
	case "unsigned int":
		goBase = "C.uint"
	case "unsigned long":
		goBase = "C.ulong"
	case "unsigned long long":
		goBase = "C.ulonglong"
	case "int":
		goBase = "C.int"
	case "char":
		goBase = "C.char"
	case "void":
		goBase = "unsafe.Pointer"
	default:
		// nvml types: nvmlDevice_t, nvmlBrandType_t, etc.
		goBase = "C." + cType
	}

	if isPointer {
		return "*" + goBase
	}
	return goBase
}

// lookupProto finds a prototype for the given function name. If not found directly,
// it tries stripping version suffixes (e.g., _v1) and using the unversioned or
// latest versioned prototype, since _v1 functions typically share the same signature
// as their unversioned counterparts in nvml.h.
func lookupProto(name string, prototypes map[string]FuncProto) (FuncProto, bool) {
	if proto, ok := prototypes[name]; ok {
		return proto, true
	}

	// Try stripping _vN suffix and looking up unversioned form
	if idx := strings.LastIndex(name, "_v"); idx > 0 {
		base := name[:idx]
		if proto, ok := prototypes[base]; ok {
			return proto, true
		}
	}

	return FuncProto{}, false
}

// goReservedWords contains Go language keywords that cannot be used as parameter names.
var goReservedWords = map[string]bool{
	"break": true, "case": true, "chan": true, "const": true, "continue": true,
	"default": true, "defer": true, "else": true, "fallthrough": true, "for": true,
	"func": true, "go": true, "goto": true, "if": true, "import": true,
	"interface": true, "map": true, "package": true, "range": true, "return": true,
	"select": true, "struct": true, "switch": true, "type": true, "var": true,
}

// safeParamName returns a parameter name that is safe to use in Go code.
// If the name is a Go keyword, it prefixes with underscore.
func safeParamName(name string) string {
	if goReservedWords[name] {
		return "_" + name
	}
	return name
}

// generateStubWithSignature generates a single stub function with the correct
// C-compatible Go signature based on the parsed prototype.
func generateStubWithSignature(proto FuncProto) string {
	var buf strings.Builder

	buf.WriteString(fmt.Sprintf("//export %s\n", proto.Name))

	// Build parameter list
	var params []string
	for _, p := range proto.Params {
		goType := cTypeToGo(p.CType)
		name := safeParamName(p.Name)
		params = append(params, fmt.Sprintf("%s %s", name, goType))
	}

	paramStr := strings.Join(params, ", ")
	buf.WriteString(fmt.Sprintf("func %s(%s) C.nvmlReturn_t {\n", proto.Name, paramStr))
	buf.WriteString(fmt.Sprintf("\treturn stubReturn(\"%s\")\n", proto.Name))
	buf.WriteString("}\n")

	return buf.String()
}
