// Copyright (c) 2025, NVIDIA CORPORATION.  All rights reserved.
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

// Package main provides a stub generator for the mock NVML bridge.
//
// This generator scans the NVML Go wrapper (nvml.go) for all exported functions,
// then scans the bridge directory for existing implementations, and generates
// stubs only for functions that are not yet implemented. It parses nvml.h to
// extract correct C function prototypes so that generated stubs have
// ABI-compatible signatures.
//
// Usage:
//
//	go run ./cmd/generate-bridge \
//	    -input vendor/github.com/NVIDIA/go-nvml/pkg/nvml/nvml.go \
//	    -header vendor/github.com/NVIDIA/go-nvml/pkg/nvml/nvml.h \
//	    -bridge pkg/gpu/mocknvml/bridge \
//	    -output pkg/gpu/mocknvml/bridge/stubs_generated.go
package main

import (
	"flag"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func main() {
	input := flag.String("input", "vendor/github.com/NVIDIA/go-nvml/pkg/nvml/nvml.go", "NVML Go wrapper file")
	header := flag.String("header", "vendor/github.com/NVIDIA/go-nvml/pkg/nvml/nvml.h", "NVML C header file for prototype extraction")
	bridge := flag.String("bridge", "pkg/gpu/mocknvml/bridge", "Bridge directory to scan for existing implementations")
	output := flag.String("output", "pkg/gpu/mocknvml/bridge/stubs_generated.go", "Output file for generated stubs")
	stats := flag.Bool("stats", false, "Print coverage statistics and exit")
	validate := flag.Bool("validate", false, "Validate hand-written export signatures against nvml.h prototypes")
	flag.Parse()

	if *stats {
		allFunctions, err := parseNVMLFunctions(*input)
		if err != nil {
			log.Fatalf("Failed to parse input: %v", err)
		}
		printStats(os.Stdout, allFunctions, *bridge)
		return
	}

	if *validate {
		headerFile, err := os.Open(*header)
		if err != nil {
			log.Fatalf("Failed to open header: %v", err)
		}
		defer func() { _ = headerFile.Close() }()

		prototypes, err := parseNVMLPrototypes(headerFile)
		if err != nil {
			log.Fatalf("Failed to parse header: %v", err)
		}

		mismatches := validateSignatures(*bridge, prototypes)
		if len(mismatches) == 0 {
			fmt.Println("All hand-written exports match nvml.h prototypes.")
			return
		}
		for _, m := range mismatches {
			fmt.Printf("WARNING: %s\n", m)
		}
		os.Exit(1)
	}

	// Step 1: Parse input file to get all NVML function names
	allFunctions, err := parseNVMLFunctions(*input)
	if err != nil {
		log.Fatalf("Failed to parse input: %v", err)
	}

	// Step 2: Parse nvml.h to get C function prototypes
	headerFile, err := os.Open(*header)
	if err != nil {
		log.Fatalf("Failed to open header: %v", err)
	}
	defer func() { _ = headerFile.Close() }()

	prototypes, err := parseNVMLPrototypes(headerFile)
	if err != nil {
		log.Fatalf("Failed to parse header: %v", err)
	}
	log.Printf("Parsed %d C prototypes from %s", len(prototypes), *header)

	// Step 3: Scan bridge directory for existing //export functions
	existingFunctions, err := scanBridgeExports(*bridge)
	if err != nil {
		log.Fatalf("Failed to scan bridge: %v", err)
	}

	// Step 4: Find missing functions (need stubs)
	missingFunctions := findMissing(allFunctions, existingFunctions)

	log.Printf("Found %d total NVML functions", len(allFunctions))
	log.Printf("Found %d existing implementations", len(existingFunctions))
	log.Printf("Generating %d stub functions", len(missingFunctions))

	// Step 5: Generate stub file with correct ABI signatures
	code := generateStubsFileWithPrototypes(missingFunctions, prototypes)

	formatted, err := format.Source([]byte(code))
	if err != nil {
		log.Printf("Warning: formatting failed: %v", err)
		formatted = []byte(code)
	}

	if err := os.WriteFile(*output, formatted, 0644); err != nil {
		log.Fatalf("Failed to write: %v", err)
	}

	log.Printf("Successfully generated %s", *output)
}

// parseNVMLFunctions extracts all function names starting with "nvml" from the input file.
func parseNVMLFunctions(filename string) ([]string, error) {
	fset := token.NewFileSet()
	node, err := parser.ParseFile(fset, filename, nil, 0)
	if err != nil {
		return nil, err
	}

	var functions []string
	ast.Inspect(node, func(n ast.Node) bool {
		funcDecl, ok := n.(*ast.FuncDecl)
		if !ok || !strings.HasPrefix(funcDecl.Name.Name, "nvml") {
			return true
		}
		functions = append(functions, funcDecl.Name.Name)
		return true
	})

	return functions, nil
}

// scanBridgeExports scans all .go files in the bridge directory for //export comments.
// Returns a map of function names that are already exported.
func scanBridgeExports(bridgeDir string) (map[string]bool, error) {
	exports := make(map[string]bool)

	err := filepath.Walk(bridgeDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		// Skip the stubs_generated.go file to avoid circular dependency
		// but include bridge_generated.go since it contains existing implementations
		if strings.HasSuffix(path, "stubs_generated.go") {
			return nil
		}

		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}

		lines := strings.Split(string(content), "\n")
		for i, line := range lines {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "//export ") {
				// Extract function name from the //export comment
				funcName := strings.TrimPrefix(trimmed, "//export ")
				funcName = strings.TrimSpace(funcName)
				if funcName != "" {
					exports[funcName] = true
				}
			} else if strings.HasPrefix(trimmed, "//export") && i+1 < len(lines) {
				// Handle case where function name is on the next line
				nextLine := strings.TrimSpace(lines[i+1])
				if strings.HasPrefix(nextLine, "func ") {
					// Extract function name: "func nvmlInit_v2(..."
					parts := strings.Fields(nextLine)
					if len(parts) > 1 {
						funcName := strings.Split(parts[1], "(")[0]
						if funcName != "" {
							exports[funcName] = true
						}
					}
				}
			}
		}
		return nil
	})

	return exports, err
}

// findMissing returns functions that exist in allFunctions but not in existingFunctions.
func findMissing(allFunctions []string, existingFunctions map[string]bool) []string {
	var missing []string
	for _, fn := range allFunctions {
		if !existingFunctions[fn] {
			missing = append(missing, fn)
		}
	}
	// Sort for consistent output
	sort.Strings(missing)
	return missing
}

// generateStubsFileWithPrototypes generates the stub file content with correct
// ABI-compatible function signatures parsed from nvml.h.
func generateStubsFileWithPrototypes(functions []string, prototypes map[string]FuncProto) string {
	var buf strings.Builder

	buf.WriteString(`// Code generated by cmd/generate-bridge. DO NOT EDIT.
// Copyright (c) 2025-2026, NVIDIA CORPORATION.  All rights reserved.
//
// This file contains stub implementations for NVML functions that are
// not yet implemented in hand-written files.
//
// To implement a function, add it to one of the implementation files
// (init.go, device.go, device_info.go, system.go) and regenerate this file.
// The generator will automatically detect the new implementation and
// remove the stub.

package main

/*
#include <stdlib.h>
#include <string.h>
#include <stdint.h>

// Include NVML type definitions for strict ABI compatibility.
#include "nvml_types.h"
*/
import "C"

`)

	if len(functions) == 0 {
		buf.WriteString("// All NVML functions are implemented - no stubs needed.\n")
		return buf.String()
	}

	buf.WriteString(fmt.Sprintf("// %d stub functions for unimplemented NVML functions.\n", len(functions)))
	buf.WriteString("// These return NVML_ERROR_NOT_SUPPORTED (3).\n\n")

	var withProto, withoutProto int
	for _, fn := range functions {
		proto, ok := lookupProto(fn, prototypes)
		if ok {
			// Use the actual function name (not the fallback name)
			proto.Name = fn
			buf.WriteString(generateStubWithSignature(proto))
			buf.WriteString("\n")
			withProto++
		} else {
			// Fallback: generate zero-arg stub if no prototype found
			buf.WriteString(fmt.Sprintf("//export %s\n", fn))
			buf.WriteString(fmt.Sprintf("func %s() C.nvmlReturn_t {\n", fn))
			buf.WriteString(fmt.Sprintf("\treturn stubReturn(\"%s\")\n", fn))
			buf.WriteString("}\n\n")
			withoutProto++
		}
	}

	if withoutProto > 0 {
		log.Printf("Warning: %d functions had no prototype in nvml.h (generated zero-arg stubs)", withoutProto)
	}
	log.Printf("Generated %d stubs with correct signatures, %d without", withProto, withoutProto)

	return buf.String()
}

// printStats writes NVML function coverage statistics to w.
func printStats(w io.Writer, allFunctions []string, bridgeDir string) {
	exports, err := scanBridgeExports(bridgeDir)
	if err != nil {
		fmt.Fprintf(w, "Error scanning bridge: %v\n", err)
		return
	}

	total := len(allFunctions)
	implemented := len(exports)
	stubs := total - implemented
	pctImpl := 0.0
	pctStub := 0.0
	if total > 0 {
		pctImpl = float64(implemented) / float64(total) * 100
		pctStub = float64(stubs) / float64(total) * 100
	}

	fmt.Fprintf(w, "NVML Function Coverage:\n")
	fmt.Fprintf(w, "  Total functions:               %d\n", total)
	fmt.Fprintf(w, "  Hand-written implementations:  %d (%.1f%%)\n", implemented, pctImpl)
	fmt.Fprintf(w, "  Generated stubs:               %d (%.1f%%)\n", stubs, pctStub)

	// Per-file breakdown
	fileCounts := make(map[string]int)
	err = filepath.Walk(bridgeDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "stubs_generated.go") {
			return err
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		count := 0
		for _, line := range strings.Split(string(content), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "//export ") {
				count++
			}
		}
		if count > 0 {
			fileCounts[filepath.Base(path)] = count
		}
		return nil
	})
	if err != nil {
		fmt.Fprintf(w, "  Error scanning files: %v\n", err)
		return
	}

	if len(fileCounts) > 0 {
		fmt.Fprintf(w, "\n  By file:\n")
		var files []string
		for f := range fileCounts {
			files = append(files, f)
		}
		sort.Strings(files)
		for _, f := range files {
			fmt.Fprintf(w, "    %-20s %d functions\n", f+":", fileCounts[f])
		}
	}
}

// validateSignatures checks that hand-written //export functions have the
// correct number of parameters compared to their C prototypes in nvml.h.
// Returns a list of mismatch descriptions.
func validateSignatures(bridgeDir string, prototypes map[string]FuncProto) []string {
	var mismatches []string

	_ = filepath.Walk(bridgeDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "stubs_generated.go") {
			return err
		}

		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}

		lines := strings.Split(string(content), "\n")
		for i, line := range lines {
			trimmed := strings.TrimSpace(line)
			if !strings.HasPrefix(trimmed, "//export ") {
				continue
			}
			funcName := strings.TrimSpace(strings.TrimPrefix(trimmed, "//export "))

			// Find the func line (should be next non-empty line)
			goParamCount := 0
			for j := i + 1; j < len(lines) && j <= i+3; j++ {
				funcLine := strings.TrimSpace(lines[j])
				if strings.HasPrefix(funcLine, "func ") {
					parenOpen := strings.Index(funcLine, "(")
					parenClose := strings.Index(funcLine, ")")
					if parenOpen >= 0 && parenClose > parenOpen {
						paramStr := strings.TrimSpace(funcLine[parenOpen+1 : parenClose])
						if paramStr == "" {
							goParamCount = 0
						} else {
							goParamCount = strings.Count(paramStr, ",") + 1
						}
					}
					break
				}
			}

			// Look up C prototype
			proto, ok := lookupProto(funcName, prototypes)
			if !ok {
				continue
			}

			if goParamCount != len(proto.Params) {
				mismatches = append(mismatches, fmt.Sprintf(
					"%s:%d: %s has %d Go params but nvml.h prototype has %d C params",
					filepath.Base(path), i+1, funcName, goParamCount, len(proto.Params),
				))
			}
		}
		return nil
	})

	return mismatches
}
