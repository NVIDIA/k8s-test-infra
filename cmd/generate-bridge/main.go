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
// stubs only for functions that are not yet implemented.
//
// Usage:
//
//	go run ./cmd/generate-bridge \
//	    -input vendor/github.com/NVIDIA/go-nvml/pkg/nvml/nvml.go \
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
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func main() {
	input := flag.String("input", "vendor/github.com/NVIDIA/go-nvml/pkg/nvml/nvml.go", "NVML Go wrapper file")
	bridge := flag.String("bridge", "pkg/gpu/mocknvml/bridge", "Bridge directory to scan for existing implementations")
	output := flag.String("output", "pkg/gpu/mocknvml/bridge/stubs_generated.go", "Output file for generated stubs")
	flag.Parse()

	// Step 1: Parse input file to get all NVML function names
	allFunctions, err := parseNVMLFunctions(*input)
	if err != nil {
		log.Fatalf("Failed to parse input: %v", err)
	}

	// Step 2: Scan bridge directory for existing //export functions
	existingFunctions, err := scanBridgeExports(*bridge)
	if err != nil {
		log.Fatalf("Failed to scan bridge: %v", err)
	}

	// Step 3: Find missing functions (need stubs)
	missingFunctions := findMissing(allFunctions, existingFunctions)

	log.Printf("Found %d total NVML functions", len(allFunctions))
	log.Printf("Found %d existing implementations", len(existingFunctions))
	log.Printf("Generating %d stub functions", len(missingFunctions))

	// Step 4: Generate stub file
	code := generateStubsFile(missingFunctions)

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

// generateStubsFile generates the stub file content.
func generateStubsFile(functions []string) string {
	var buf strings.Builder

	buf.WriteString(`// Code generated by cmd/generate-bridge. DO NOT EDIT.
// Copyright (c) 2025, NVIDIA CORPORATION.  All rights reserved.
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

// Use include guards to prevent redefinition errors
#ifndef NVML_TYPES_DEFINED
#define NVML_TYPES_DEFINED

typedef int nvmlReturn_t;
typedef void* nvmlDevice_t;

typedef struct nvmlPciInfo_st {
    char busIdLegacy[16];
    unsigned int domain;
    unsigned int bus;
    unsigned int device;
    unsigned int pciDeviceId;
    unsigned int pciSubSystemId;
    char busId[32];
} nvmlPciInfo_t;

typedef struct nvmlMemory_st {
    unsigned long long total;
    unsigned long long free;
    unsigned long long used;
} nvmlMemory_t;

typedef struct nvmlProcessInfo_st {
    unsigned int pid;
    unsigned long long usedGpuMemory;
} nvmlProcessInfo_t;

#define NVML_SUCCESS                    0
#define NVML_ERROR_UNINITIALIZED        1
#define NVML_ERROR_INVALID_ARGUMENT     2
#define NVML_ERROR_NOT_SUPPORTED        3
#define NVML_ERROR_INSUFFICIENT_SIZE    7
#define NVML_ERROR_TIMEOUT              14
#define NVML_ERROR_UNKNOWN              999

#endif // NVML_TYPES_DEFINED
*/
import "C"

`)

	if len(functions) == 0 {
		buf.WriteString("// All NVML functions are implemented - no stubs needed.\n")
		return buf.String()
	}

	buf.WriteString(fmt.Sprintf("// %d stub functions for unimplemented NVML functions.\n", len(functions)))
	buf.WriteString("// These return NVML_ERROR_NOT_SUPPORTED (3).\n\n")

	// Generate stub for each missing function
	for _, fn := range functions {
		buf.WriteString(fmt.Sprintf("//export %s\n", fn))
		buf.WriteString(fmt.Sprintf("func %s() C.nvmlReturn_t {\n", fn))
		buf.WriteString(fmt.Sprintf("\treturn stubReturn(\"%s\")\n", fn))
		buf.WriteString("}\n\n")
	}

	return buf.String()
}
