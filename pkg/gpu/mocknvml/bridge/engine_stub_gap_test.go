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

package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// stubReturnRE matches generated stub bodies: return stubReturn("nvmlDeviceGetFoo")
var stubReturnRE = regexp.MustCompile(`return stubReturn\("(nvmlDevice[^"]+)"\)`)

// TestEngineMethodsAreNotGeneratedStubs guards against the silent-drop class of
// bug in issue #636: ConfigurableDevice implements an NVML method, profiles
// may configure it, but the C export is still a generated stubReturn that
// surfaces N/A / NOT_SUPPORTED to nvidia-smi.
func TestEngineMethodsAreNotGeneratedStubs(t *testing.T) {
	engineMethods, err := configurableDeviceMethodNames(filepath.Join("..", "engine", "device.go"))
	require.NoError(t, err)

	stubsSrc, err := os.ReadFile("stubs_generated.go")
	require.NoError(t, err)

	stubExports := map[string]struct{}{}
	for _, m := range stubReturnRE.FindAllStringSubmatch(string(stubsSrc), -1) {
		stubExports[m[1]] = struct{}{}
	}

	var gaps []string
	for _, method := range engineMethods {
		export := "nvmlDevice" + method
		if _, ok := stubExports[export]; ok {
			gaps = append(gaps, export+" (engine has ConfigurableDevice."+method+")")
		}
	}
	require.Empty(t, gaps,
		"engine methods still exported as generated stubs — wire them in a hand-written bridge file and regenerate stubs:\n  %s",
		strings.Join(gaps, "\n  "))
}

func configurableDeviceMethodNames(path string) ([]string, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Recv == nil || len(fn.Recv.List) != 1 || fn.Name == nil {
			continue
		}
		if !recvIsConfigurableDevice(fn.Recv.List[0].Type) {
			continue
		}
		name := fn.Name.Name
		// Only NVML-shaped getters/setters map 1:1 onto nvmlDevice* exports.
		if strings.HasPrefix(name, "Get") || strings.HasPrefix(name, "Set") {
			names = append(names, name)
		}
	}
	return names, nil
}

func recvIsConfigurableDevice(expr ast.Expr) bool {
	switch t := expr.(type) {
	case *ast.StarExpr:
		return recvIsConfigurableDevice(t.X)
	case *ast.Ident:
		return t.Name == "ConfigurableDevice"
	default:
		return false
	}
}
